"""Embeddings：POST /v1/embeddings（方案 7.4）。"""
import base64
import math
import struct

import pytest

import config
from helpers import requires, assert_error_shape

pytestmark = requires("embedding")

MODEL = config.MODELS.get("embedding")


def cos(a, b):
    dot = sum(x * y for x, y in zip(a, b))
    return dot / (math.sqrt(sum(x * x for x in a)) * math.sqrt(sum(y * y for y in b)))


def embed(api, inp, **kw):
    r = api.post("/embeddings", json=dict({"model": MODEL, "input": inp}, **kw))
    assert r.status_code == 200, r.text
    return r.json()


def test_e1_single(api):
    body = embed(api, "今天天气很好")
    vec = body["data"][0]["embedding"]
    assert isinstance(vec, list) and len(vec) == (config.EMBEDDING_DIM or len(vec)) > 0
    assert all(isinstance(x, float) for x in vec[:10])
    if api.name == "gateway":
        assert body["model"] == MODEL


def test_e2_batch(api):
    inputs = ["苹果", "香蕉", "汽车", "飞机"]
    data = embed(api, inputs)["data"]
    assert len(data) == len(inputs)
    assert [d["index"] for d in data] == list(range(len(inputs)))


def test_e3_semantic(api):
    v = [d["embedding"] for d in embed(api, ["猫", "小猫咪", "汽车发动机"])["data"]]
    assert cos(v[0], v[1]) > cos(v[0], v[2])


@pytest.mark.skipif(config.EMBEDDING_NO_BASE64, reason="上游不支持 encoding_format=base64")
def test_e4_base64(api):
    if api.name == "direct" and config.EMBEDDING_NO_BASE64_DIRECT:
        pytest.skip("上游直连只支持 float；经网关由方言在网关内编码 base64")
    body = embed(api, "hello", encoding_format="base64")
    raw = base64.b64decode(body["data"][0]["embedding"])
    vec = struct.unpack("<%df" % (len(raw) // 4), raw)
    assert len(vec) == (config.EMBEDDING_DIM or len(vec)) > 0


def test_e5_usage(api):
    usage = embed(api, "统计 token 用量")["usage"]
    assert usage["prompt_tokens"] > 0
    assert usage.get("completion_tokens", 0) == 0


@requires("chat")
def test_e6_chat_model_rejected(gw):
    r = gw.post("/embeddings", json={"model": config.MODELS["chat"], "input": "hi"})
    assert_error_shape(r, 404, "model_not_found")


def test_e7_openai_sdk(api):
    resp = api.openai().embeddings.create(model=MODEL, input=["a", "b"])
    assert len(resp.data) == 2 and len(resp.data[0].embedding) == (config.EMBEDDING_DIM or len(resp.data[0].embedding))
