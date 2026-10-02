// 多模态相关的未导出函数测试（同 helpers_internal_test.go 的理由放在 package relay 内部）。
package relay

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"
)

func TestAnthropicRequestToOpenAI_TranslatesImageBlocks(t *testing.T) {
	in := map[string]any{"model": "m", "messages": []any{
		map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "image", "source": map[string]any{"type": "base64", "media_type": "image/png", "data": "QUJD"}},
			map[string]any{"type": "image", "source": map[string]any{"type": "url", "url": "https://x/y.jpg"}},
			map[string]any{"type": "text", "text": "describe"},
		}},
		map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": "ok"}}},
	}}
	out, err := anthropicRequestToOpenAI(in)
	if err != nil {
		t.Fatal(err)
	}
	msgs := out["messages"].([]any)
	parts, ok := msgs[0].(map[string]any)["content"].([]any)
	if !ok || len(parts) != 3 {
		t.Fatalf("user content = %#v, want 3 OpenAI parts", msgs[0])
	}
	url0 := parts[0].(map[string]any)["image_url"].(map[string]any)["url"]
	url1 := parts[1].(map[string]any)["image_url"].(map[string]any)["url"]
	if url0 != "data:image/png;base64,QUJD" || url1 != "https://x/y.jpg" {
		t.Errorf("image urls = %v, %v", url0, url1)
	}
	if parts[2].(map[string]any)["text"] != "describe" {
		t.Errorf("text part = %#v", parts[2])
	}
	// 没有图片的消息保持纯文本字符串（与之前的行为一致）
	if msgs[1].(map[string]any)["content"] != "ok" {
		t.Errorf("text-only content = %#v, want string", msgs[1])
	}
	if countImageInputs(out) != 2 {
		t.Errorf("countImageInputs = %d, want 2", countImageInputs(out))
	}
}

func TestEstimateChatInputTokens_ExcludesImageBytes(t *testing.T) {
	dataURL := "data:image/png;base64," + strings.Repeat("A", 4*1024*1024)
	reqMap := map[string]any{"messages": []any{map[string]any{"role": "user", "content": []any{
		map[string]any{"type": "image_url", "image_url": map[string]any{"url": dataURL}},
		map[string]any{"type": "text", "text": "what"},
	}}}}
	body := []byte(`{"messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"` + dataURL + `"}},{"type":"text","text":"what"}]}]}`)
	got := estimateChatInputTokens(body, reqMap, 1500)
	if got < 1500 || got > 1600 {
		t.Errorf("estimate = %d, want ~1500 (one image + a few text tokens)", got)
	}
	if plain := estimateChatInputTokens([]byte(`{"messages":[]}`), map[string]any{}, 1500); plain != estimateTokens(15) {
		t.Errorf("text-only estimate = %d, want %d", plain, estimateTokens(15))
	}
}

func wavBytes(sampleRate, channels, bitsPerSample, seconds int, dataSize uint32) []byte {
	byteRate := sampleRate * channels * bitsPerSample / 8
	var b bytes.Buffer
	b.WriteString("RIFF")
	_ = binary.Write(&b, binary.LittleEndian, uint32(36+byteRate*seconds))
	b.WriteString("WAVEfmt ")
	_ = binary.Write(&b, binary.LittleEndian, uint32(16))
	_ = binary.Write(&b, binary.LittleEndian, uint16(1))
	_ = binary.Write(&b, binary.LittleEndian, uint16(channels))
	_ = binary.Write(&b, binary.LittleEndian, uint32(sampleRate))
	_ = binary.Write(&b, binary.LittleEndian, uint32(byteRate))
	_ = binary.Write(&b, binary.LittleEndian, uint16(channels*bitsPerSample/8))
	_ = binary.Write(&b, binary.LittleEndian, uint16(bitsPerSample))
	b.WriteString("data")
	_ = binary.Write(&b, binary.LittleEndian, dataSize)
	b.Write(make([]byte, byteRate*seconds))
	return b.Bytes()
}

func TestWavDurationMillis(t *testing.T) {
	if ms, ok := wavDurationMillis(wavBytes(16000, 1, 16, 2, 64000)); !ok || ms != 2000 {
		t.Errorf("2s wav = %d %v, want 2000 true", ms, ok)
	}
	// 流式写出的 WAV：data 块大小为 0xFFFFFFFF，按实际剩余字节计
	if ms, ok := wavDurationMillis(wavBytes(8000, 1, 16, 3, 0xFFFFFFFF)); !ok || ms != 3000 {
		t.Errorf("streamed wav = %d %v, want 3000 true", ms, ok)
	}
	if _, ok := wavDurationMillis([]byte("ID3 not a wav")); ok {
		t.Error("mp3 must not parse as wav")
	}
	if ms := estimateAudioMillis(make([]byte, 20000)); ms != 10000 {
		t.Errorf("estimate for 20KB non-wav = %d, want 10000 (16kbps)", ms)
	}
}

func TestUpstreamErrorDetail(t *testing.T) {
	cases := map[string]string{
		`{"error":{"message":"bad  \n model"}}`: "bad model",
		`{"code":20015,"message":"too long"}`:   "too long",
		`{"error":"plain"}`:                     "plain",
		`not json`:                              "",
	}
	for in, want := range cases {
		if got := upstreamErrorDetail([]byte(in)); got != want {
			t.Errorf("upstreamErrorDetail(%s) = %q, want %q", in, got, want)
		}
	}
	long := `{"message":"` + strings.Repeat("长", 300) + `"}`
	if got := upstreamErrorDetail([]byte(long)); len([]rune(got)) != 201 {
		t.Errorf("long message not truncated to 200 runes + ellipsis: %d", len([]rune(got)))
	}
}
