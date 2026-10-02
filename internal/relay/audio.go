package relay

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/WALLE-AI/uFreeTokens/internal/adapter"
	"github.com/WALLE-AI/uFreeTokens/internal/auth"
	"github.com/WALLE-AI/uFreeTokens/internal/httpx"
	"github.com/WALLE-AI/uFreeTokens/internal/router"
	"github.com/WALLE-AI/uFreeTokens/internal/schema"
)

// AudioSpeech 是 POST /v1/audio/speech（语音合成）。经渠道方言选择的 codec 转发
// （百炼走原生接口并下载音频），响应是二进制音频：
// 非流式整体返回，stream=true 时按块转发（不是 SSE）。按输入字符数计费（input_char
// 计量项）——字符数在请求时就确定，流式中途断开也按全额计费。
func (s *Service) AudioSpeech(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	start := time.Now()
	log := s.Logger.With("request_id", httpx.RequestIDFromContext(ctx))

	principal, ok := auth.FromContext(ctx)
	if !ok {
		httpx.WriteError(w, r, http.StatusUnauthorized, "invalid_api_key", "Invalid API key.")
		return
	}
	_, reqMap, ok := s.readJSON(w, r)
	if !ok {
		return
	}
	modelName, _ := reqMap["model"].(string)
	adm, ok := s.admit(w, r, log, principal, specSpeech, modelName)
	if !ok {
		return
	}
	defer adm.release()

	input, _ := reqMap["input"].(string)
	chars := utf8.RuneCountInString(input)
	if strings.TrimSpace(input) == "" {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "\"input\" is required.")
		return
	}
	if chars > s.Cfg.MaxSpeechChars {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request",
			fmt.Sprintf("\"input\" must be at most %d characters.", s.Cfg.MaxSpeechChars))
		return
	}
	usage := schema.Usage{InputChars: int64(chars), Source: schema.UsageSourceUpstream}
	if !s.reserve(w, r, log, adm, usage) {
		return
	}

	meta := newMeta(r, adm, specSpeech, start)
	meta.isStream, _ = reqMap["stream"].(bool)
	call := &adapter.Call{Endpoint: specSpeech.path, JSON: reqMap, Stream: meta.isStream}
	resp, _, picked, trace, ok := s.dispatch(w, r, log, adm, specSpeech, meta, router.Features{}, codecRequest(call))
	if !ok {
		return
	}
	defer resp.Body.Close()
	// 字符数在请求时就已确定：始终按它计费，只保留 codec 报告的上游成本用于对账。
	s.finishCodec(w, r, log, adm, meta, resp, picked, trace, call, func(u schema.Usage) schema.Usage {
		usage.UpstreamCost = u.UpstreamCost
		return usage
	})
}

// copyFlushing 按块把上游二进制流转发给客户端，每块 Flush 一次；任一端出错即停止。
func copyFlushing(w http.ResponseWriter, src io.Reader) {
	flusher, _ := w.(http.Flusher)
	buf := make([]byte, 32*1024)
	for {
		n, err := src.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
		if err != nil {
			return
		}
	}
}

// 语音识别接受的音频格式（与 OpenAI 一致）。
var transcriptionExts = map[string]bool{
	".flac": true, ".m4a": true, ".mp3": true, ".mp4": true, ".mpeg": true, ".mpga": true,
	".oga": true, ".ogg": true, ".wav": true, ".webm": true,
}

// AudioTranscriptions 是 POST /v1/audio/transcriptions（语音识别，multipart/form-data）。
// 文件缓存在内存里（受 MaxUpstreamBody 限制），每次重试都由 codec 重新组装上游请求
// （OpenAI 兼容上游是 multipart，百炼是 chat + input_audio）。按音频时长计费（audio_second），另计一次 request（模型
// 配置了按次价时生效）。时长优先取上游返回的 usage.seconds / duration，其次解析
// WAV 头，都拿不到时按文件大小估算（usage_source=estimated）。
func (s *Service) AudioTranscriptions(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	start := time.Now()
	log := s.Logger.With("request_id", httpx.RequestIDFromContext(ctx))

	principal, ok := auth.FromContext(ctx)
	if !ok {
		httpx.WriteError(w, r, http.StatusUnauthorized, "invalid_api_key", "Invalid API key.")
		return
	}
	mediaType, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "multipart/form-data" || params["boundary"] == "" {
		httpx.WriteError(w, r, http.StatusUnsupportedMediaType, "unsupported_media_type", "Content-Type must be multipart/form-data.")
		return
	}
	body, err := readBody(r, s.Cfg.MaxUpstreamBody)
	if err != nil {
		s.writeBodyReadError(w, r, err)
		return
	}
	form, err := parseTranscriptionForm(body, params["boundary"])
	if err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "Malformed multipart body.")
		return
	}
	if len(form.File) == 0 {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_request", "\"file\" is required.")
		return
	}
	if !transcriptionExts[strings.ToLower(filepath.Ext(form.Filename))] {
		httpx.WriteError(w, r, http.StatusUnsupportedMediaType, "unsupported_media_type",
			"Unsupported audio format; use flac, m4a, mp3, mp4, mpeg, mpga, oga, ogg, wav or webm.")
		return
	}
	adm, ok := s.admit(w, r, log, principal, specTranscriptions, form.Model)
	if !ok {
		return
	}
	defer adm.release()

	estimate := schema.Usage{AudioMillis: estimateAudioMillis(form.File), Requests: 1}
	if !s.reserve(w, r, log, adm, estimate) {
		return
	}

	meta := newMeta(r, adm, specTranscriptions, start)
	call := &adapter.Call{Endpoint: specTranscriptions.path, Form: form}
	resp, _, picked, trace, ok := s.dispatch(w, r, log, adm, specTranscriptions, meta, router.Features{}, codecRequest(call))
	if !ok {
		return
	}
	defer resp.Body.Close()
	s.finishCodec(w, r, log, adm, meta, resp, picked, trace, call, func(u schema.Usage) schema.Usage {
		u.Requests = 1
		if u.AudioMillis == 0 {
			// response_format=text/srt/vtt 等情况下上游不返回时长；按文件估算。
			u.AudioMillis = estimate.AudioMillis
			u.Source = schema.UsageSourceUpstream
			if _, exact := wavDurationMillis(form.File); !exact {
				u.Source = schema.UsageSourceEstimated
			}
		}
		return u
	})
}

func parseTranscriptionForm(body []byte, boundary string) (*adapter.Form, error) {
	form := &adapter.Form{}
	mr := multipart.NewReader(bytes.NewReader(body), boundary)
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			return form, nil
		}
		if err != nil {
			return nil, err
		}
		data, err := io.ReadAll(part)
		if err != nil {
			return nil, err
		}
		switch name := part.FormName(); {
		case name == "file":
			form.File, form.Filename, form.FileType = data, part.FileName(), part.Header.Get("Content-Type")
		case name == "model":
			form.Model = strings.TrimSpace(string(data))
		case name != "":
			form.Fields = append(form.Fields, [2]string{name, string(data)})
		}
	}
}

// maxEstimatedAudioMillis 是按文件大小估算时长的上限（2 小时），防止异常大的估算值
// 把预扣抬得过高。
const maxEstimatedAudioMillis = 2 * 3600 * 1000

// estimateAudioMillis 估算音频时长：WAV 精确解析；其他格式按 16kbps（2000 字节/秒）
// 保守估算——低码率假设会高估时长，只用于预扣和上游未返回时长时的兜底。
func estimateAudioMillis(file []byte) int64 {
	if ms, ok := wavDurationMillis(file); ok {
		return ms
	}
	ms := int64(len(file)) * 1000 / 2000
	if ms > maxEstimatedAudioMillis {
		ms = maxEstimatedAudioMillis
	}
	if ms < 1000 {
		ms = 1000
	}
	return ms
}

// wavDurationMillis 解析 RIFF/WAVE 头：data 块字节数 ÷ fmt 块的 byteRate。
func wavDurationMillis(b []byte) (int64, bool) {
	if len(b) < 12 || string(b[0:4]) != "RIFF" || string(b[8:12]) != "WAVE" {
		return 0, false
	}
	var byteRate uint32
	for off := 12; off+8 <= len(b); {
		id := string(b[off : off+4])
		size := binary.LittleEndian.Uint32(b[off+4 : off+8])
		body := off + 8
		switch id {
		case "fmt ":
			if body+12 <= len(b) {
				byteRate = binary.LittleEndian.Uint32(b[body+8 : body+12])
			}
		case "data":
			if byteRate == 0 {
				return 0, false
			}
			// 流式写出的 WAV 常把 data 块大小写成 0 或 0xFFFFFFFF，按实际剩余字节计。
			if size == 0 || int(size) > len(b)-body {
				size = uint32(len(b) - body)
			}
			return int64(size) * 1000 / int64(byteRate), true
		}
		off = body + int(size) + int(size&1)
	}
	return 0, false
}
