import base64
import sys
import traceback
from pathlib import Path

from openai import OpenAI

BASE_URL = "http://localhost:3000/v1"
UFREETOKENS_API_KEY = "sk-uft-ShOBK6LhTUez0ImPaxfhX02ozlUVDey91PrhihtPzWx"

CHAT_MODEL = "dots-studio/dots-3-note-preview:free"
EMBEDDING_MODEL = "BAAI/bge-m3"
VLM_MODEL = "Qwen/Qwen3-VL-8B-Instruct"
TTS_MODEL = "FunAudioLLM/CosyVoice2-0.5B"
ASR_MODEL = "FunAudioLLM/SenseVoiceSmall"
IMAGE_MODEL = "Kwai-Kolors/Kolors"

OUT_DIR = Path(__file__).parent / "tmp" / "test_out"
OUT_DIR.mkdir(parents=True, exist_ok=True)

client = OpenAI(base_url=BASE_URL, api_key=UFREETOKENS_API_KEY)


def stream_chat(model, messages):
    stream = client.chat.completions.create(
        model=model,
        messages=messages,
        stream=True,
        stream_options={"include_usage": True},
    )
    usage = None
    for chunk in stream:
        if chunk.choices and chunk.choices[0].delta.content:
            print(chunk.choices[0].delta.content, end="", flush=True)
        # 部分上游每个 chunk 都带 usage，只保留最后一次
        if chunk.usage:
            usage = chunk.usage
    print(f"\n[usage] {usage}")


def test_chat_stream():
    stream_chat(CHAT_MODEL, [{"role": "user", "content": "用一句话介绍你自己"}])


def test_embedding():
    resp = client.embeddings.create(model=EMBEDDING_MODEL, input=["你好，世界", "hello world"])
    for item in resp.data:
        print(f"index={item.index} dim={len(item.embedding)} head={item.embedding[:4]}")
    print(f"[usage] {resp.usage}")


def test_vlm_stream():
    stream_chat(VLM_MODEL, [{
        "role": "user",
        "content": [
            {"type": "image_url", "image_url": {"url": "https://sf-maas-uat-prod.oss-cn-shanghai.aliyuncs.com/dog.png"}},
            {"type": "text", "text": "用一句话描述这张图片"},
        ],
    }])


def test_tts():
    path = OUT_DIR / "speech.mp3"
    with client.audio.speech.with_streaming_response.create(
        model=TTS_MODEL,
        voice="alex",
        input="你好，欢迎使用 uFreeTokens。",
        response_format="mp3",
    ) as resp:
        resp.stream_to_file(path)
    print(f"saved {path} ({path.stat().st_size} bytes)")


def test_asr():
    path = OUT_DIR / "speech.mp3"
    if not path.exists():
        raise RuntimeError("speech.mp3 不存在，需要先跑 TTS")
    with open(path, "rb") as f:
        resp = client.audio.transcriptions.create(model=ASR_MODEL, file=f)
    print(f"text: {resp.text}")


def test_image():
    resp = client.images.generate(
        model=IMAGE_MODEL,
        prompt="一只坐在窗台上的橘猫，水彩风格",
        size="1024x1024",
        n=1,
    )
    item = resp.data[0]
    if item.b64_json:
        path = OUT_DIR / "image.png"
        path.write_bytes(base64.b64decode(item.b64_json))
        print(f"saved {path}")
    else:
        print(f"url: {item.url}")


TESTS = [
    ("chat (stream)", test_chat_stream),
    ("embedding", test_embedding),
    ("vlm (stream)", test_vlm_stream),
    ("tts", test_tts),
    ("asr", test_asr),
    ("image", test_image),
]

if __name__ == "__main__":
    # 可选参数过滤，例如: python test.py tts asr
    selected = [t for t in TESTS if len(sys.argv) == 1 or any(a in t[0] for a in sys.argv[1:])]
    results = []
    for name, fn in selected:
        print(f"\n===== {name} =====")
        try:
            fn()
            results.append((name, "OK"))
        except Exception as e:
            traceback.print_exc(limit=1)
            results.append((name, f"FAIL: {e}"))
    print("\n===== summary =====")
    for name, status in results:
        print(f"{name:15} {status}")
