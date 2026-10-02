"""公共 fixture。

两层测试（见方案第 4 节）：
  - target="direct"  L0：直连上游供应商（UFT_PROVIDER 选择，见 providers.py），确认上游 Key / 模型 / 请求格式本身没问题
  - target="gateway" L1：经本地 cmd/gateway
用 `api` fixture 的用例两层都跑；只用 `gw` / `direct` 的用例只跑一层。
"""
import base64
import io
import struct
import time
import wave

import httpx
import pytest

import config


class Target:
    def __init__(self, name, base_url, key):
        self.name = name
        self.base_url = base_url
        self.key = key
        self.http = httpx.Client(base_url=base_url, timeout=config.TIMEOUT,
                                 headers={"Authorization": "Bearer " + key})

    def post(self, path, **kw):
        return self.http.post(path, **kw)

    def get(self, path, **kw):
        return self.http.get(path, **kw)

    def stream(self, path, body):
        """发起流式请求，返回 (response, [每个 data: 帧的原始字符串])。"""
        frames = []
        with self.http.stream("POST", path, json=body) as r:
            if r.status_code != 200:
                r.read()
                return r, frames
            for line in r.iter_lines():
                if line.startswith("data:"):
                    frames.append(line[5:].strip())
        return r, frames

    def openai(self):
        from openai import OpenAI
        return OpenAI(base_url=self.base_url, api_key=self.key, timeout=config.TIMEOUT, max_retries=0)

    def __repr__(self):
        return self.name


def _reachable(url):
    try:
        httpx.get(url, timeout=3)
        return True
    except httpx.HTTPError:
        return False


@pytest.fixture(scope="session")
def direct():
    if not config.DIRECT_KEY:
        pytest.skip("未设置 %s" % config.PROFILE["key_env"])
    return Target("direct", config.DIRECT_BASE_URL, config.DIRECT_KEY)


@pytest.fixture(scope="session")
def gw():
    if not config.GATEWAY_KEY:
        pytest.skip("未设置 UFT_API_KEY")
    if not _reachable(config.GATEWAY_URL.rsplit("/v1", 1)[0] + "/healthz"):
        pytest.skip("网关不可达：" + config.GATEWAY_URL)
    return Target("gateway", config.GATEWAY_URL, config.GATEWAY_KEY)


@pytest.fixture(params=["direct", "gateway"])
def api(request):
    return request.getfixturevalue("direct" if request.param == "direct" else "gw")


def pytest_collection_modifyitems(items):
    # 按参数自动打 direct / gateway 标记，便于 `pytest -m gateway` 只跑网关层。
    for item in items:
        cs = getattr(item, "callspec", None)
        names = set(getattr(item, "fixturenames", ()))
        if cs and cs.params.get("api") in ("direct", "gateway"):
            item.add_marker(cs.params["api"])
        elif "gw" in names:
            item.add_marker("gateway")
        elif "direct" in names:
            item.add_marker("direct")


@pytest.fixture(autouse=True)
def _pace():
    # 上游有 RPM 限制，用例之间稍作间隔。
    yield
    time.sleep(0.3)


# ---------- 测试素材（运行时生成，不入库二进制文件） ----------

@pytest.fixture(scope="session")
def red_circle_png():
    """白底红色实心圆，用来问 VLM 颜色与形状。"""
    from PIL import Image, ImageDraw
    img = Image.new("RGB", (256, 256), "white")
    ImageDraw.Draw(img).ellipse((48, 48, 208, 208), fill=(220, 20, 20))
    buf = io.BytesIO()
    img.save(buf, format="PNG")
    return buf.getvalue()


@pytest.fixture(scope="session")
def red_circle_data_url(red_circle_png):
    return "data:image/png;base64," + base64.b64encode(red_circle_png).decode()


@pytest.fixture(scope="session")
def silence_wav():
    """1 秒 16kHz 静音 WAV，只用来测网关 ASR 接口的现状。"""
    buf = io.BytesIO()
    w = wave.open(buf, "wb")
    w.setnchannels(1)
    w.setsampwidth(2)
    w.setframerate(16000)
    w.writeframes(struct.pack("<h", 0) * 16000)
    w.close()
    return buf.getvalue()
