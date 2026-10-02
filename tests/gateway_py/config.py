"""测试配置：读取 .env，按 UFT_PROVIDER 选择上游供应商档案（见 providers.py）。

网关里的虚拟模型名与上游模型 ID 保持一致（onboard_siliconflow.py 导入时 name 留空 =
upstream_model），所以 L0（直连）和 L1（网关）用同一个模型 ID。
"""
import os
from pathlib import Path

from dotenv import load_dotenv

from providers import PROFILES

HERE = Path(__file__).resolve().parent
ASSETS = HERE / "assets"
load_dotenv(HERE / ".env", override=True)  # .env 优先于系统环境变量（避免旧 Key 残留在环境里）

PROVIDER = os.getenv("UFT_PROVIDER", "siliconflow")
if PROVIDER not in PROFILES:
    raise SystemExit("未知的 UFT_PROVIDER=%s，可选：%s" % (PROVIDER, ", ".join(PROFILES)))
PROFILE = PROFILES[PROVIDER]

DIRECT_BASE_URL = os.getenv("UFT_DIRECT_BASE_URL", PROFILE["base_url"]).rstrip("/")
DIRECT_KEY = os.getenv(PROFILE["key_env"], "")

GATEWAY_URL = os.getenv("UFT_GATEWAY_URL", "http://localhost:8080/v1").rstrip("/")
GATEWAY_KEY = os.getenv("UFT_API_KEY", "")

ADMIN_URL = os.getenv("UFT_ADMIN_URL", "http://localhost:8081").rstrip("/")
ADMIN_TOKEN = os.getenv("UFT_ADMIN_TOKEN", "dev-admin-token-change-me")

TIMEOUT = float(os.getenv("UFT_TEST_TIMEOUT", "120"))

# 能力 → 上游模型 ID；值为 None 的能力在该供应商上不测（用例自动跳过）。
MODELS = PROFILE["models"]
EMBEDDING_DIM = PROFILE.get("embedding_dim")
TTS_VOICE = PROFILE.get("tts_voice_direct")
TTS_VOICE_GATEWAY = PROFILE.get("tts_voice_gateway", TTS_VOICE)
IMAGE_EXTRA = PROFILE.get("image_extra", {"size": "1024x1024"})
IMAGE_SMALL_SIZE = PROFILE.get("image_small_size", "1024x1024")
ASR_EXTRA = PROFILE.get("asr_extra", {})
# 供应商差异：不支持 base64 向量编码 / 图像接口不认 n（一次只出一张）
EMBEDDING_NO_BASE64 = PROFILE.get("embedding_no_base64", False)
EMBEDDING_NO_BASE64_DIRECT = PROFILE.get("embedding_no_base64_direct", False)
# 直连时不是 OpenAI 形状的能力（由网关方言 / codec 适配），L0 直连用例跳过
DIRECT_UNSUPPORTED = set(PROFILE.get("direct_unsupported", []))
IMAGE_NO_N = PROFILE.get("image_no_n", False)

# SiliconFlow 文档示例图（国内可直接访问）；V1 用例用它测公网 URL 输入。
PUBLIC_IMAGE_URL = "https://sf-maas-uat-prod.oss-cn-shanghai.aliyuncs.com/dog.png"


def has(*kinds):
    return all(MODELS.get(k) for k in kinds)


def direct_ok(*kinds):
    return has(*kinds) and not (DIRECT_UNSUPPORTED & set(kinds))


def mask(key):
    return key[:7] + "..." + key[-4:] if len(key) > 12 else "***"
