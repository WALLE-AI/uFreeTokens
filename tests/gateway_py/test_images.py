"""图像生成：POST /v1/images/generations（方案 7.6）。"""
import httpx
import pytest

import config
from helpers import requires, requires_direct, assert_error_shape

pytestmark = requires("image")

MODEL = config.MODELS.get("image")
REQ = dict({"model": MODEL, "prompt": "一只戴着红色围巾的橘猫，水彩风格"}, **config.IMAGE_EXTRA)


def image_urls(body):
    # SiliconFlow 返回 images[].url；OpenAI 规范是 data[].url，两种都认。
    return [x["url"] for x in (body.get("images") or body.get("data") or []) if x.get("url")]


@pytest.mark.paid
@requires_direct("image")
def test_i0_direct(direct):
    r = direct.post("/images/generations", json=REQ)
    assert r.status_code == 200, r.text
    urls = image_urls(r.json())
    assert urls, r.text
    img = httpx.get(urls[0], timeout=60)
    assert img.status_code == 200
    assert img.content[:4] in (b"\x89PNG", b"\xff\xd8\xff\xe0", b"\xff\xd8\xff\xe1", b"\xff\xd8\xff\xdb") \
        or img.headers.get("content-type", "").startswith("image/"), img.headers


@pytest.mark.paid
def test_i1_gateway(gw):
    r = gw.post("/images/generations", json=REQ)
    assert r.status_code == 200, r.text
    body = r.json()
    assert body["model"] == MODEL and body["created"] > 0
    data = body["data"]  # OpenAI 形状：url 或 b64_json（OpenRouter 只返回 b64_json）
    assert data
    if data[0].get("url"):
        assert httpx.get(data[0]["url"], timeout=60).status_code == 200
    else:
        assert len(data[0]["b64_json"]) > 1000


@pytest.mark.paid
def test_i2_openai_fields_and_sdk(gw):
    """OpenAI 的 n/size 字段与 openai SDK 的 images.generate 都能用。"""
    kw = {} if config.IMAGE_NO_N else {"n": 1}
    resp = gw.openai().images.generate(model=MODEL, prompt="a red apple", size=config.IMAGE_SMALL_SIZE, **kw)
    assert resp.data and (resp.data[0].url or resp.data[0].b64_json)


def test_i3_requires_prompt_and_limits_n(gw):
    assert_error_shape(gw.post("/images/generations", json={"model": MODEL}), 400, "invalid_request")
    assert_error_shape(gw.post("/images/generations", json=dict(REQ, n=5)), 400, "invalid_request")
