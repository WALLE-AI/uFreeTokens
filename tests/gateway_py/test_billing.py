"""计费与观测（方案 7.8）：/v1/usage 余额变化、失败不扣费、X-Request-Id、流式中途断开。"""
import time

import pytest

import config
from helpers import requires

pytestmark = requires("chat")

MODEL = config.MODELS.get("chat")
MSG = [{"role": "user", "content": "你好"}]


def wallet(gw):
    r = gw.get("/usage")
    assert r.status_code == 200, r.text
    return r.json()


def total(w):
    return w["wallet"]["cash_balance_micro"] + w["wallet"]["bonus_balance_micro"]


def wait_settled(gw, frozen0, requests=None, timeout=10):
    """结算在响应写完后进行（流式尤其如此）；request_logs 由 reqlog 异步批量写入（每秒 flush），
    所以 usage 统计是最终一致的。轮询直到冻结金额回到基线、请求数达到预期。"""
    deadline = time.time() + timeout
    while True:
        w = wallet(gw)
        ok = w["wallet"]["frozen_micro"] == frozen0 and (requests is None or w["usage"]["total_requests"] >= requests)
        if ok or time.time() > deadline:
            return w
        time.sleep(0.5)


def test_b0_usage_shape(gw):
    w = wallet(gw)
    for k in ("total_requests", "total_input_tokens", "total_output_tokens", "total_charged_amount_micro"):
        assert k in w["usage"], w
    for k in ("cash_balance_micro", "bonus_balance_micro", "frozen_micro"):
        assert k in w["wallet"], w


def test_b1_success_charges(gw):
    w0 = wallet(gw)
    r = gw.post("/chat/completions", json={"model": MODEL, "messages": MSG, "max_tokens": 16})
    assert r.status_code == 200, r.text
    w1 = wait_settled(gw, w0["wallet"]["frozen_micro"], w0["usage"]["total_requests"] + 1)
    assert total(w1) < total(w0), (w0, w1)
    assert w1["usage"]["total_requests"] >= w0["usage"]["total_requests"] + 1
    assert w1["usage"]["total_charged_amount_micro"] - w0["usage"]["total_charged_amount_micro"] == total(w0) - total(w1)


def test_b1_bonus_first(gw):
    """先扣赠送余额，赠送余额够用时现金不变。"""
    w0 = wallet(gw)
    if w0["wallet"]["bonus_balance_micro"] <= 0:
        import pytest
        pytest.skip("赠送余额为 0")
    gw.post("/chat/completions", json={"model": MODEL, "messages": MSG, "max_tokens": 8})
    w1 = wait_settled(gw, w0["wallet"]["frozen_micro"])
    assert w1["wallet"]["cash_balance_micro"] == w0["wallet"]["cash_balance_micro"]
    assert w1["wallet"]["bonus_balance_micro"] < w0["wallet"]["bonus_balance_micro"]


@requires("embedding")
def test_b2_failure_not_charged(gw):
    w0 = wallet(gw)
    # 本地拒绝（模型不存在）
    assert gw.post("/chat/completions", json={"model": "no-such/model", "messages": MSG}).status_code == 404
    # 转发到上游后失败（embedding 模型调 chat，见 test_c10）
    assert gw.post("/chat/completions", json={"model": config.MODELS["embedding"], "messages": MSG}).status_code >= 400
    w1 = wait_settled(gw, w0["wallet"]["frozen_micro"])
    assert total(w1) == total(w0), (w0, w1)
    assert w1["wallet"]["frozen_micro"] == w0["wallet"]["frozen_micro"], "失败请求的预扣没有释放"


def test_b3_request_id(gw):
    ids = set()
    for _ in range(2):
        r = gw.post("/chat/completions", json={"model": MODEL, "messages": MSG, "max_tokens": 4})
        assert r.status_code == 200
        rid = r.headers.get("x-request-id")
        assert rid
        assert r.json()["id"] == rid, "文档：响应 id 与 X-Request-Id 相同"
        ids.add(rid)
    assert len(ids) == 2


def test_b3_request_id_passthrough(gw):
    r = gw.http.post("/chat/completions", json={"model": MODEL, "messages": MSG, "max_tokens": 4},
                     headers={"X-Request-Id": "pytest-req-12345"})
    assert r.headers.get("x-request-id") == "pytest-req-12345"


def test_b3_request_id_on_error(gw):
    r = gw.post("/chat/completions", json={"model": "no-such/model", "messages": MSG})
    assert r.json()["error"]["request_id"] == r.headers.get("x-request-id")


def test_b4_stream_disconnect(gw):
    """读到第 2 个块就断开：之后预扣必须释放，扣费不超过预扣上限，也不能因断开而漏结算卡住冻结金额。"""
    w0 = wallet(gw)
    with gw.http.stream("POST", "/chat/completions",
                        json={"model": MODEL, "stream": True, "max_tokens": 512,
                              "messages": [{"role": "user", "content": "写一篇 500 字的春游作文。"}]}) as r:
        assert r.status_code == 200
        n = 0
        for line in r.iter_lines():
            if line.startswith("data:"):
                n += 1
                if n >= 2:
                    break
    w1 = wait_settled(gw, w0["wallet"]["frozen_micro"], w0["usage"]["total_requests"] + 1, timeout=20)
    assert w1["wallet"]["frozen_micro"] == w0["wallet"]["frozen_micro"], "断开后预扣没有释放"
    assert total(w1) <= total(w0)
    # request_logs 异步写入：前面慢请求（如百炼图像约 60 秒）的日志可能同时落进来，只断言至少 +1
    assert w1["usage"]["total_requests"] >= w0["usage"]["total_requests"] + 1


def sell_price(gw, model, meter):
    """从 GET /v1/catalog 取模型某个计量项的售价（元）。"""
    from decimal import Decimal
    for m in gw.get("/catalog").json()["data"]:
        if m["name"] == model:
            for c in (m.get("sell_price") or {}).get("components", []):
                if c["meter"] == meter:
                    return Decimal(c["unit_price"])
    pytest.fail("目录里没有 %s 的 %s 售价" % (model, meter))


def micro_ceil(amount):
    """元 → 微元，向上取整（与网关 pricing.RoundCeil 一致）。"""
    from decimal import ROUND_CEILING, Decimal
    return int((amount * Decimal(1_000_000)).to_integral_value(rounding=ROUND_CEILING))


def _charge(gw, fn):
    w0 = wallet(gw)
    fn()
    w1 = wait_settled(gw, w0["wallet"]["frozen_micro"], w0["usage"]["total_requests"] + 1)
    return w0, w1, total(w0) - total(w1)


@pytest.mark.paid
@requires("image")
@pytest.mark.skipif(config.IMAGE_NO_N, reason="上游图像接口不认 n，无法一次生成 2 张")
def test_b5_image_charged_per_image(gw):
    """2 张 × 目录里的单张售价。"""
    model = config.MODELS["image"]
    w0, w1, charged = _charge(gw, lambda: gw.post("/images/generations", json={
        "model": model, "prompt": "a blue cup", "n": 2, "size": config.IMAGE_SMALL_SIZE}).raise_for_status())
    assert charged == micro_ceil(2 * sell_price(gw, model, "image")), charged
    assert w1["usage"]["total_images"] - w0["usage"]["total_images"] == 2


@pytest.mark.paid
@requires("tts")
def test_b6_tts_charged_per_char(gw):
    """10 个字符 × 目录里的每百万字符售价。"""
    from decimal import Decimal
    model, text = config.MODELS["tts"], "一二三四五六七八九十"
    w0, w1, charged = _charge(gw, lambda: gw.post("/audio/speech", json={
        "model": model, "input": text, "voice": config.TTS_VOICE_GATEWAY}).raise_for_status())
    assert charged == micro_ceil(10 * sell_price(gw, model, "input_char") / Decimal(1_000_000)), charged
    assert w1["usage"]["total_input_chars"] - w0["usage"]["total_input_chars"] == 10


@pytest.mark.paid
@requires("asr")
def test_b7_asr_charged_per_second(gw, silence_wav):
    """上游返回的时长（毫秒折算成小数秒）× 目录里的每秒售价，向上取整到微元。"""
    from decimal import Decimal
    model = config.MODELS["asr"]
    w0, w1, charged = _charge(gw, lambda: gw.post("/audio/transcriptions", data={"model": model},
                                                  files={"file": ("a.wav", silence_wav, "audio/wav")}).raise_for_status())
    ms = w1["usage"]["total_audio_ms"] - w0["usage"]["total_audio_ms"]
    assert ms > 0
    assert charged == micro_ceil(Decimal(ms) / 1000 * sell_price(gw, model, "audio_second")), (charged, ms)
