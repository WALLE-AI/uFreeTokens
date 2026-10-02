"""Rerank：POST /v1/rerank（方案 7.5）。"""
import config
from helpers import requires, requires_direct, assert_error_shape

pytestmark = requires("rerank")

MODEL = config.MODELS.get("rerank")
REQ = {
    "model": MODEL,
    "query": "苹果公司发布了什么新手机？",
    "documents": [
        "今天北京天气晴朗，适合出游。",
        "Apple 在秋季发布会上推出了新款 iPhone。",
        "苹果富含维生素，每天一个苹果有益健康。",
        "特斯拉公布了第三季度交付量。",
    ],
    "top_n": 3,
    "return_documents": True,
}


def check_rerank(body):
    results = body["results"]
    assert 0 < len(results) <= REQ["top_n"]
    scores = [x["relevance_score"] for x in results]
    assert scores == sorted(scores, reverse=True), scores
    assert results[0]["index"] == 1, results  # iPhone 那条最相关


@requires_direct("rerank")
def test_r0_direct(direct):
    r = direct.post("/rerank", json=REQ)
    assert r.status_code == 200, r.text
    check_rerank(r.json())


def test_r1_gateway(gw):
    r = gw.post("/rerank", json=REQ)
    assert r.status_code == 200, r.text
    body = r.json()
    check_rerank(body)
    assert body["model"] == MODEL
    assert body["id"] == r.headers["x-request-id"]


def test_r2_requires_documents(gw):
    r = gw.post("/rerank", json=dict(REQ, documents=[]))
    assert_error_shape(r, 400, "invalid_request")


@requires("chat")
def test_r3_chat_model_rejected(gw):
    r = gw.post("/rerank", json=dict(REQ, model=config.MODELS["chat"]))
    assert_error_shape(r, 404, "model_not_found")
