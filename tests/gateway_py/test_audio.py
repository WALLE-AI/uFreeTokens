"""语音：TTS POST /v1/audio/speech、ASR POST /v1/audio/transcriptions（方案 7.7）。"""
import pytest

import config
from helpers import assert_error_shape, audio_file, requires, requires_direct

TTS_TEXT = "今天天气很好，我们一起去公园散步吧。"
TTS_REQ = {"model": config.MODELS.get("tts"), "input": TTS_TEXT, "voice": config.TTS_VOICE, "response_format": "mp3"}


def transcribe(target, audio, filename, mime):
    return target.post("/audio/transcriptions", data=dict({"model": config.MODELS.get("asr")}, **config.ASR_EXTRA),
                       files={"file": (filename, audio, mime)})


@pytest.fixture(scope="module")
def tts_mp3(direct):
    if not config.direct_ok("tts"):
        pytest.skip("%s 直连 TTS 不是 OpenAI 形状或未配置" % config.PROVIDER)
    r = direct.post("/audio/speech", json=TTS_REQ)
    assert r.status_code == 200, r.text[:300]
    return r.content


@pytest.mark.paid
@requires_direct("tts")
def test_a0_tts_direct(tts_mp3):
    assert len(tts_mp3) > 1024
    # MP3：ID3 头或帧同步字节
    assert tts_mp3[:3] == b"ID3" or (tts_mp3[0] == 0xFF and tts_mp3[1] & 0xE0 == 0xE0), tts_mp3[:8]


@pytest.mark.paid
@requires_direct("asr", "tts")
def test_a1_asr_roundtrip_direct(direct, tts_mp3):
    """把 TTS 生成的音频回灌给 ASR，识别结果应包含原文关键词。"""
    r = transcribe(direct, tts_mp3, "speech.mp3", "audio/mpeg")
    assert r.status_code == 200, r.text
    text = r.json()["text"]
    hits = [k for k in ("天气", "公园", "散步") if k in text]
    assert len(hits) >= 2, text


@pytest.fixture(scope="module")
def gw_mp3(gw):
    """经网关合成：用网关侧音色（SiliconFlow 为短音色名 alex，由渠道指令补上上游模型前缀）。"""
    if not config.has("tts"):
        pytest.skip("%s 未配置能力：tts" % config.PROVIDER)
    r = gw.post("/audio/speech", json=dict(TTS_REQ, voice=config.TTS_VOICE_GATEWAY))
    assert r.status_code == 200, r.text[:300]
    assert r.headers["content-type"].startswith("audio/")
    return r.content


@pytest.mark.paid
@requires("tts")
def test_a2_tts_gateway_short_voice(gw_mp3):
    assert len(gw_mp3) > 1024


@pytest.mark.paid
@pytest.mark.stream
def test_a2_tts_gateway_stream(gw):
    size = 0
    if not config.has("tts"):
        pytest.skip("%s 未配置能力：tts" % config.PROVIDER)
    with gw.http.stream("POST", "/audio/speech", json=dict(TTS_REQ, voice=config.TTS_VOICE_GATEWAY, stream=True)) as r:
        assert r.status_code == 200
        for chunk in r.iter_bytes():
            size += len(chunk)
    assert size > 1024


@pytest.mark.paid
@requires("asr")
def test_a2_asr_gateway_roundtrip(gw, gw_mp3):
    r = transcribe(gw, gw_mp3, *audio_file(gw_mp3))
    assert r.status_code == 200, r.text
    text = r.json()["text"]
    assert len([k for k in ("天气", "公园", "散步") if k in text]) >= 2, text


@pytest.mark.paid
@requires("asr")
def test_a2_asr_openai_sdk(gw, gw_mp3):
    import io
    f = io.BytesIO(gw_mp3)
    f.name = audio_file(gw_mp3)[0]
    resp = gw.openai().audio.transcriptions.create(model=config.MODELS.get("asr"), file=f, **config.ASR_EXTRA)
    assert "天气" in resp.text


@requires("asr")
def test_a3_asr_rejects_bad_format_and_json(gw):
    assert_error_shape(transcribe(gw, b"x", "a.exe", "application/octet-stream"), 415, "unsupported_media_type")
    assert_error_shape(gw.post("/audio/transcriptions", json={"model": config.MODELS.get("asr")}), 415, "unsupported_media_type")


@requires("tts", "asr")
def test_a4_type_mismatch(gw, silence_wav):
    """tts 模型不能识别、asr 模型不能合成。"""
    assert_error_shape(gw.post("/audio/speech", json=dict(TTS_REQ, model=config.MODELS.get("asr"))), 404, "model_not_found")
    r = gw.post("/audio/transcriptions", data={"model": config.MODELS.get("tts")}, files={"file": ("a.wav", silence_wav, "audio/wav")})
    assert_error_shape(r, 404, "model_not_found")
