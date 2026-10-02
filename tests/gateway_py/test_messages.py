"""Anthropic 兼容接口 POST /v1/messages（方案 7.3）。只测网关层：SiliconFlow 不是 Anthropic 协议上游。"""
import json

import httpx
import pytest

import config
from helpers import requires, assert_error_shape

pytestmark = requires("chat")

MODEL = config.MODELS.get("chat")


def body(**kw):
    b = {"model": MODEL, "max_tokens": 64, "messages": [{"role": "user", "content": "用一句话介绍你自己。"}]}
    b.update(kw)
    return b


def test_m1_non_stream(gw):
    r = gw.post("/messages", json=body())
    assert r.status_code == 200, r.text
    m = r.json()
    assert m["type"] == "message" and m["role"] == "assistant"
    assert m["content"][0]["type"] == "text" and m["content"][0]["text"].strip()
    assert m["stop_reason"] in ("end_turn", "max_tokens", "stop_sequence")
    assert m["usage"]["input_tokens"] > 0 and m["usage"]["output_tokens"] > 0
    assert m["model"] == MODEL


def test_m1_max_tokens_stop_reason(gw):
    r = gw.post("/messages", json=body(max_tokens=3, messages=[{"role": "user", "content": "从 1 数到 100。"}]))
    assert r.status_code == 200, r.text
    assert r.json()["stop_reason"] == "max_tokens"


@pytest.mark.stream
def test_m2_stream_event_order(gw):
    events = []
    with gw.http.stream("POST", "/messages", json=body(stream=True)) as r:
        assert r.status_code == 200, r.read()
        assert r.headers["content-type"].startswith("text/event-stream")
        ev = None
        for line in r.iter_lines():
            if line.startswith("event:"):
                ev = line[6:].strip()
            elif line.startswith("data:"):
                events.append((ev, json.loads(line[5:].strip())))
    names = [e for e, _ in events]
    assert names[0] == "message_start", names
    assert names[-1] == "message_stop", names
    for must in ("content_block_start", "content_block_delta", "content_block_stop", "message_delta"):
        assert must in names, names
    assert names.index("content_block_start") < names.index("content_block_delta") < names.index("content_block_stop") \
        < names.index("message_delta") < names.index("message_stop")
    text = "".join(d.get("delta", {}).get("text", "") for e, d in events if e == "content_block_delta")
    assert text.strip()
    delta = [d for e, d in events if e == "message_delta"][-1]
    assert delta["delta"].get("stop_reason")
    assert delta.get("usage", {}).get("output_tokens", 0) > 0


def test_m3_system_prompt(gw):
    r = gw.post("/messages", json=body(system="无论用户问什么，你只回答两个字：收到", temperature=0,
                                       messages=[{"role": "user", "content": "今天天气如何？"}]))
    assert r.status_code == 200, r.text
    assert "收到" in r.json()["content"][0]["text"]


def test_m3_system_as_blocks(gw):
    r = gw.post("/messages", json=body(system=[{"type": "text", "text": "你的名字叫小星。"}], temperature=0,
                                       messages=[{"role": "user", "content": "你叫什么名字？"}]))
    assert r.status_code == 200, r.text
    assert "小星" in r.json()["content"][0]["text"]


def test_m3_multi_turn(gw):
    msgs = [{"role": "user", "content": "记住数字 42。"},
            {"role": "assistant", "content": "好的，我记住了 42。"},
            {"role": "user", "content": [{"type": "text", "text": "我让你记住的数字是多少？只回答数字。"}]}]
    r = gw.post("/messages", json=body(messages=msgs, temperature=0))
    assert r.status_code == 200, r.text
    assert "42" in r.json()["content"][0]["text"]


def test_m4_x_api_key_rejected(gw):
    """文档：只认 Authorization: Bearer，只带 x-api-key 返回 401 invalid_api_key。"""
    r = httpx.post(gw.base_url + "/messages", json=body(), timeout=30,
                   headers={"x-api-key": gw.key, "anthropic-version": "2023-06-01"})
    assert_error_shape(r, 401, "invalid_api_key")


def test_m4_tools_rejected(gw):
    """文档：带 tools 字段直接 400 invalid_request。"""
    r = gw.post("/messages", json=body(tools=[{"name": "f", "input_schema": {"type": "object"}}]))
    assert_error_shape(r, 400, "invalid_request")


def test_m5_anthropic_sdk(gw):
    anthropic = pytest.importorskip("anthropic")
    # 文档：用 auth_token 让 SDK 发 Authorization: Bearer；base_url 不带 /v1（SDK 自己拼 /v1/messages）
    client = anthropic.Anthropic(base_url=gw.base_url.rsplit("/v1", 1)[0], auth_token=gw.key, api_key=None,
                                 timeout=config.TIMEOUT, max_retries=0)
    msg = client.messages.create(model=MODEL, max_tokens=32, messages=[{"role": "user", "content": "你好"}])
    assert msg.content[0].text.strip()
    with client.messages.stream(model=MODEL, max_tokens=32, messages=[{"role": "user", "content": "你好"}]) as s:
        text = "".join(s.text_stream)
    assert text.strip()
