"""文本 Chat：POST /v1/chat/completions（方案 7.1）。"""
import json
import time

import pytest

import config
from helpers import requires, assert_error_shape, assert_stream_end, join_delta, parse_chunks

pytestmark = requires("chat")

MODEL = config.MODELS.get("chat")
MSG = [{"role": "user", "content": "用一句话介绍一下你自己。"}]

WEATHER_TOOL = {
    "type": "function",
    "function": {
        "name": "get_weather",
        "description": "查询某个城市的当前天气",
        "parameters": {
            "type": "object",
            "properties": {"city": {"type": "string", "description": "城市名，如 北京"}},
            "required": ["city"],
        },
    },
}
TOOL_MSG = [{"role": "user", "content": "北京现在天气怎么样？请调用工具查询。"}]


def test_c1_non_stream(api):
    r = api.post("/chat/completions", json={"model": MODEL, "messages": MSG, "max_tokens": 64})
    assert r.status_code == 200, r.text
    body = r.json()
    assert body["choices"][0]["message"]["content"].strip()
    assert body["choices"][0]["finish_reason"] in ("stop", "length")
    assert body["usage"]["prompt_tokens"] > 0
    assert body["usage"]["completion_tokens"] > 0
    if api.name == "gateway":
        # 网关应把上游模型名改写回客户端请求的模型 ID
        assert body["model"] == MODEL


@pytest.mark.stream
def test_c2_stream(api):
    t0 = time.time()
    r, frames = api.stream("/chat/completions", {"model": MODEL, "messages": MSG, "stream": True, "max_tokens": 64})
    assert r.status_code == 200, r.text
    assert r.headers["content-type"].startswith("text/event-stream")
    chunks, done = parse_chunks(frames)
    assert_stream_end(api.name, done)
    assert len(chunks) > 1, "流式应返回多个数据块"
    assert join_delta(chunks).strip()
    print("[%s] stream %d chunks in %.2fs" % (api.name, len(chunks), time.time() - t0))


@pytest.mark.stream
def test_c3_stream_include_usage(api):
    r, frames = api.stream("/chat/completions", {"model": MODEL, "messages": MSG, "stream": True, "max_tokens": 32,
                                                 "stream_options": {"include_usage": True}})
    assert r.status_code == 200, r.text
    chunks, done = parse_chunks(frames)
    assert_stream_end(api.name, done)
    usage = [c["usage"] for c in chunks if c.get("usage")]
    assert usage, "include_usage=true 时最后应有 usage 块"
    assert usage[-1]["prompt_tokens"] > 0 and usage[-1]["completion_tokens"] > 0


@pytest.mark.stream
def test_c4_stream_without_include_usage_no_leak(gw):
    """网关为计费会注入 include_usage，但客户端没要时不能把 usage 块透出（relay.go 注释 §7.4）。"""
    r, frames = gw.stream("/chat/completions", {"model": MODEL, "messages": MSG, "stream": True, "max_tokens": 32})
    assert r.status_code == 200, r.text
    chunks, _ = parse_chunks(frames)
    assert not any(c.get("usage") and not c.get("choices") for c in chunks), "泄露了网关注入的 usage 块"


@pytest.mark.stream
def test_c5_ttft(api):
    """首 token 延迟：只记录，不断言。"""
    t0 = time.time()
    ttft = None
    with api.http.stream("POST", "/chat/completions",
                         json={"model": MODEL, "messages": MSG, "stream": True, "max_tokens": 64}) as r:
        assert r.status_code == 200
        for line in r.iter_lines():
            if line.startswith("data:") and '"content"' in line and ttft is None:
                ttft = time.time() - t0
    print("[%s] TTFT=%.3fs total=%.3fs" % (api.name, ttft or -1, time.time() - t0))
    assert ttft is not None


def test_c6_tool_call(api):
    r = api.post("/chat/completions", json={"model": MODEL, "messages": TOOL_MSG, "tools": [WEATHER_TOOL],
                                            "tool_choice": "auto", "max_tokens": 256})
    assert r.status_code == 200, r.text
    msg = r.json()["choices"][0]["message"]
    calls = msg.get("tool_calls") or []
    assert calls, "模型没有发起工具调用：%s" % msg
    assert calls[0]["function"]["name"] == "get_weather"
    args = json.loads(calls[0]["function"]["arguments"])
    assert "北京" in args.get("city", "")


@pytest.mark.stream
def test_c6_tool_call_stream(api):
    r, frames = api.stream("/chat/completions", {"model": MODEL, "messages": TOOL_MSG, "tools": [WEATHER_TOOL],
                                                 "stream": True, "max_tokens": 256})
    assert r.status_code == 200, r.text
    chunks, done = parse_chunks(frames)
    assert_stream_end(api.name, done)
    name, args = "", ""
    for c in chunks:
        for tc in (c.get("choices") or [{}])[0].get("delta", {}).get("tool_calls") or []:
            fn = tc.get("function") or {}
            name += fn.get("name") or ""
            args += fn.get("arguments") or ""
    assert name == "get_weather", chunks[-3:]
    assert "北京" in json.loads(args).get("city", "")


def test_c7_max_tokens_truncates(api):
    r = api.post("/chat/completions", json={"model": MODEL, "max_tokens": 5, "temperature": 0,
                                            "messages": [{"role": "user", "content": "从 1 数到 100，用逗号分隔。"}]})
    assert r.status_code == 200, r.text
    body = r.json()
    assert body["choices"][0]["finish_reason"] == "length"
    assert body["usage"]["completion_tokens"] <= 5


def test_c8_bad_key(gw):
    r = gw.http.post("/chat/completions", json={"model": MODEL, "messages": MSG},
                     headers={"Authorization": "Bearer sk-uft-invalid"})
    err = assert_error_shape(r, 401, "invalid_api_key")
    assert err["type"] == "authentication_error"


def test_c8_missing_key(gw):
    import httpx
    r = httpx.post(gw.base_url + "/chat/completions", json={"model": MODEL, "messages": MSG}, timeout=10)
    assert_error_shape(r, 401)


def test_c9_unknown_model(gw):
    r = gw.post("/chat/completions", json={"model": "no-such/model", "messages": MSG})
    assert_error_shape(r, 404, "model_not_found")


@requires("embedding")
def test_c10_embedding_model_on_chat_rejected_locally(gw):
    """D1：类型不匹配在网关本地 404，不转发上游。"""
    r = gw.post("/chat/completions", json={"model": config.MODELS["embedding"], "messages": MSG})
    assert_error_shape(r, 404, "model_not_found")


def test_c12_upstream_bad_request_reason_echoed(gw):
    """D3：上游 400 的原因透传给用户。"""
    r = gw.post("/chat/completions", json={"model": MODEL, "messages": MSG, "max_tokens": 10_000_000})
    err = assert_error_shape(r, 400, "invalid_request")
    assert err["message"].startswith("Upstream rejected the request"), err


def test_c11_openai_sdk(api):
    client = api.openai()
    resp = client.chat.completions.create(model=MODEL, messages=MSG, max_tokens=32)
    assert resp.choices[0].message.content.strip()
    text = ""
    for chunk in client.chat.completions.create(model=MODEL, messages=MSG, max_tokens=32, stream=True):
        if chunk.choices and chunk.choices[0].delta.content:
            text += chunk.choices[0].delta.content
    assert text.strip()
