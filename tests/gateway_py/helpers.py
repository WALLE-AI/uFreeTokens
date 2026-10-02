"""断言辅助。"""
import json

import pytest

import config


def requires(*kinds):
    """当前供应商档案没有配置这些能力的模型时跳过（见 providers.py）。"""
    return pytest.mark.skipif(not config.has(*kinds), reason="%s 未配置能力：%s" % (config.PROVIDER, "、".join(kinds)))


def requires_direct(*kinds):
    """L0 直连用例：上游这些能力不是 OpenAI 形状（由网关适配）时跳过。"""
    return pytest.mark.skipif(not config.direct_ok(*kinds),
                              reason="%s 直连不是 OpenAI 形状或未配置：%s（只测网关层）" % (config.PROVIDER, "、".join(kinds)))


def audio_file(data):
    """按内容判断音频文件名与 MIME（百炼 TTS 返回 wav，其余多为 mp3）。"""
    if data[:4] == b"RIFF":
        return "speech.wav", "audio/wav"
    return "speech.mp3", "audio/mpeg"


def assert_error_shape(r, status=None, code=None):
    """网关错误体：{"error":{"message","type","code","request_id"}}（见 errors.mdx）。"""
    if status is not None:
        assert r.status_code == status, r.text
    body = r.json()
    assert "error" in body, body
    err = body["error"]
    for k in ("message", "type", "code"):
        assert err.get(k), body
    if code is not None:
        assert err["code"] == code, body
    return err


def parse_chunks(frames):
    """把 SSE data 帧解析成 JSON 块列表，返回 (chunks, saw_done)。"""
    chunks, done = [], False
    for f in frames:
        if f == "[DONE]":
            done = True
        elif f:
            chunks.append(json.loads(f))
    return chunks, done


def join_delta(chunks):
    return "".join((c.get("choices") or [{}])[0].get("delta", {}).get("content") or "" for c in chunks
                   if c.get("choices"))


def assert_stream_end(target_name, done):
    """OpenAI 原生协议以 data: [DONE] 结束；网关按文档（streaming.mdx）不转发 [DONE]，以连接关闭为结束。"""
    if target_name == "gateway":
        assert not done, "文档写明网关不发送 data: [DONE]，但收到了"
    else:
        assert done, "缺少 data: [DONE]"
