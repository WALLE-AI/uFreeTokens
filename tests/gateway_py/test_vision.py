"""VLM：/v1/chat/completions 的 image_url 输入；/v1/messages 的 image block（方案 7.2）。"""
import base64
import io
import os

import pytest

import config
from helpers import requires, assert_error_shape, assert_stream_end, join_delta, parse_chunks

pytestmark = requires("vlm")

MODEL = config.MODELS.get("vlm")
COLOR_Q = "图片里是什么颜色的什么形状？只用中文简短回答。"


def vision_msg(url, text=COLOR_Q):
    return [{"role": "user", "content": [
        {"type": "image_url", "image_url": {"url": url}},
        {"type": "text", "text": text},
    ]}]


def answer(r):
    assert r.status_code == 200, r.text
    return r.json()["choices"][0]["message"]["content"]


def test_v1_public_url(api):
    r = api.post("/chat/completions", json={"model": MODEL, "max_tokens": 64,
                                            "messages": vision_msg(config.PUBLIC_IMAGE_URL, "图片里是什么动物？简短回答。")})
    text = answer(r)
    assert any(k in text for k in ("狗", "犬", "dog", "Dog")), text


def test_v2_base64(api, red_circle_data_url):
    text = answer(api.post("/chat/completions", json={"model": MODEL, "max_tokens": 64,
                                                      "messages": vision_msg(red_circle_data_url)}))
    assert "红" in text and "圆" in text, text


@pytest.mark.stream
def test_v3_stream(api, red_circle_data_url):
    r, frames = api.stream("/chat/completions", {"model": MODEL, "max_tokens": 64, "stream": True,
                                                 "messages": vision_msg(red_circle_data_url)})
    assert r.status_code == 200, r.text
    chunks, done = parse_chunks(frames)
    assert_stream_end(api.name, done)
    text = join_delta(chunks)
    assert "红" in text, text


def test_v4_multi_images(api, red_circle_data_url):
    from PIL import Image
    buf = io.BytesIO()
    Image.new("RGB", (128, 128), (20, 60, 220)).save(buf, format="PNG")
    blue = "data:image/png;base64," + base64.b64encode(buf.getvalue()).decode()
    msgs = [{"role": "user", "content": [
        {"type": "image_url", "image_url": {"url": red_circle_data_url}},
        {"type": "image_url", "image_url": {"url": blue}},
        {"type": "text", "text": "这两张图分别主要是什么颜色？按顺序简短回答。"},
    ]}]
    text = answer(api.post("/chat/completions", json={"model": MODEL, "max_tokens": 64, "messages": msgs}))
    assert "红" in text and "蓝" in text, text


def _noise_png_data_url(target_bytes):
    from PIL import Image
    side = int((target_bytes / 3) ** 0.5)
    img = Image.frombytes("RGB", (side, side), os.urandom(side * side * 3))
    buf = io.BytesIO()
    img.save(buf, format="PNG")
    return "data:image/png;base64," + base64.b64encode(buf.getvalue()).decode()


@pytest.mark.paid
def test_v5_large_image_direct(direct):
    """基线：约 4MB 的 base64 图片直连上游可以正常识别。"""
    url = _noise_png_data_url(3 * 1024 * 1024)
    r = direct.post("/chat/completions", json={"model": MODEL, "max_tokens": 16, "messages": vision_msg(url, "这是什么？")})
    assert r.status_code == 200, r.text[:300]


@pytest.mark.paid
def test_v5_large_image_via_gateway(gw):
    """D2：图片按固定 token 估算，大图不再被上下文窗口过滤成 503。"""
    url = _noise_png_data_url(3 * 1024 * 1024)
    r = gw.post("/chat/completions", json={"model": MODEL, "max_tokens": 16, "messages": vision_msg(url, "这是什么？")})
    print("[gateway] 4MB image -> %d %s" % (r.status_code, r.text[:200]))
    assert r.status_code == 200, r.text[:300]


def test_v5_body_over_limit(gw):
    """D4：超过 20MB 的请求体返回 413 request_too_large。"""
    url = "data:image/png;base64," + "A" * (21 * 1024 * 1024)
    r = gw.post("/chat/completions", json={"model": MODEL, "messages": vision_msg(url)})
    assert_error_shape(r, 413, "request_too_large")


def test_v6_messages_image_block(gw):
    """L1：/v1/messages 的 image block 转成 image_url，模型能看到图片。"""
    # 用绿色方块而不是红色圆：模型看不到图时容易"猜中"红色圆形，造成假 XPASS。
    from PIL import Image
    buf = io.BytesIO()
    Image.new("RGB", (200, 200), (30, 180, 60)).save(buf, format="PNG")
    body = {"model": MODEL, "max_tokens": 64, "messages": [{"role": "user", "content": [
        {"type": "image", "source": {"type": "base64", "media_type": "image/png",
                                     "data": base64.b64encode(buf.getvalue()).decode()}},
        {"type": "text", "text": "图片是什么颜色？简短回答。"},
    ]}]}
    r = gw.post("/messages", json=body)
    assert r.status_code == 200, r.text
    text = "".join(b.get("text", "") for b in r.json()["content"])
    assert "绿" in text, text
