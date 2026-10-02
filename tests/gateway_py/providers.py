"""上游供应商测试档案。用环境变量 UFT_PROVIDER 选择（默认 siliconflow）。

每个档案描述：
  base_url / key_env      L0 直连用的地址与密钥环境变量（密钥写在 .env）
  models                  每种能力用哪个上游模型；None = 该供应商不支持（或网关尚未适配），
                          相关用例自动跳过
  items                   onboard_siliconflow.py（通用接入脚本）导入网关时的类型 / 能力 / 定价
  其余键                  供应商特有的请求参数（音色、图像参数、向量维度等）

网关里的虚拟模型名 = 上游模型 ID，所以 L0 和 L1 用同一个模型名。
"""

CAPS_CHAT = ["stream", "tools"]

PROFILES = {
    "siliconflow": dict(
        name="SiliconFlow",
        base_url="https://api.siliconflow.cn/v1",
        key_env="SILICONFLOW_API_KEY",
        models={
            "chat": "Qwen/Qwen2.5-7B-Instruct",
            "vlm": "Qwen/Qwen3-VL-8B-Instruct",
            "embedding": "BAAI/bge-m3",
            "rerank": "BAAI/bge-reranker-v2-m3",
            "image": "Kwai-Kolors/Kolors",
            "tts": "FunAudioLLM/CosyVoice2-0.5B",
            "asr": "FunAudioLLM/SenseVoiceSmall",
        },
        embedding_dim=1024,
        # SiliconFlow 的音色要带上游模型名前缀；经网关可以用短名（渠道指令补前缀）
        tts_voice_direct="FunAudioLLM/CosyVoice2-0.5B:alex",
        tts_voice_gateway="alex",
        image_extra={"image_size": "1024x1024", "batch_size": 1, "num_inference_steps": 20},
        image_small_size="512x512",
        # 定价：对话模型用 cost=(输入, 输出)，CNY/百万 token；其他类型用 components：
        # (meter, unit, 成本价, 售价或 None=按加价自动算)。免费模型成本 0，售价手工给低价。
        # CosyVoice 官网按 UTF-8 字节计价（约 ¥50/百万字节），网关按字符计价，中文 ≈ ¥150/百万字符。
        items=[
            dict(kind="chat", family="qwen", type="chat", ctx=32768, out=4096, caps=CAPS_CHAT,
                 cost=("0", "0"), sell=("0.1", "0.1")),
            dict(kind="vlm", family="qwen", type="chat", ctx=262144, out=8192, caps=CAPS_CHAT + ["vision"],
                 cost=("0.5", "2"), sell=None),
            dict(kind="embedding", family="bge", type="embedding", ctx=8192, out=1, caps=[],
                 cost=("0", "0"), sell=("0.1", "0.1")),
            dict(kind="rerank", family="bge", type="rerank", ctx=8192, out=1, caps=[],
                 cost=("0", "0"), sell=("0.1", "0.1")),
            dict(kind="image", family="kolors", type="image", ctx=1, out=1, caps=[],
                 components=[("image", "per_image", "0", "0.05")]),
            dict(kind="tts", family="cosyvoice", type="audio", ctx=1, out=1, caps=["tts"],
                 components=[("input_char", "per_1m_chars", "150", None)],
                 overrides={"$voice_prefix_upstream_model": True}),
            dict(kind="asr", family="sensevoice", type="audio", ctx=1, out=1, caps=["asr"],
                 components=[("audio_second", "per_second", "0", "0.0002")]),
        ],
    ),

    # ---- 以下三个档案的模型 ID 来自官方文档（2026-10-02），执行前 onboard.py 会核对上游模型列表 ----
    # 只填当前网关「OpenAI 兼容透传」就能工作的能力；需要适配的能力填 None（见《多供应商接口兼容性评估》）。
    "openrouter": dict(
        name="OpenRouter",
        base_url="https://openrouter.ai/api/v1",
        key_env="OPENROUTER_API_KEY",
        # 实测（2026-10-02）：Google、OpenAI 的模型对本账号所在地区返回 403 "not available in your region"，
        # 免费模型经常 429 限流，所以都选了实测可用的低价模型。
        # rerank / tts / asr 模型只在 GET /models?output_modalities=all 中列出。
        models={
            # deepseek-v4-flash 是推理模型：max_tokens 较小时 token 全花在推理上，content 为空；
            # 改用非推理的 qwen3-vl-8b-instruct（同时支持工具调用与图片输入）
            "chat": "qwen/qwen3-vl-8b-instruct",
            "vlm": "qwen/qwen3-vl-8b-instruct",
            "embedding": "baai/bge-m3",
            "rerank": "qwen/qwen3-reranker-8b",
            # 经网关方言适配：/images/generations → OpenRouter 的 /images，只返回 b64_json
            "image": "qwen/qwen-image-3",
            "tts": "x-ai/grok-voice-tts-1.0",
            "asr": "mistralai/voxtral-small-24b-2507-stt",
        },
        embedding_dim=1024,
        image_extra={"size": "1024x1024"},
        image_small_size="1024x1024",
        # 直连时这些能力不是 OpenAI 形状（图像路径不同），只测网关层
        direct_unsupported=["image"],
        tts_voice_direct="eve",
        tts_voice_gateway="eve",
        # Voxtral 偶尔会把中文语音直接翻译成英文，显式指定语言
        asr_extra={"language": "zh"},
        # OpenRouter 价格是 USD；这里按 7.2 折成 CNY 录入成本
        items=[
            # chat 与 vlm 是同一个模型，只导入一次（带 vision 能力）
            dict(kind="vlm", family="qwen", type="chat", ctx=131072, out=8192, caps=CAPS_CHAT + ["vision"],
                 cost=("1", "4"), sell=None),
            dict(kind="embedding", family="bge", type="embedding", ctx=8192, out=1, caps=[],
                 components=[("input", "per_1m_tokens", "0.072", None)]),
            dict(kind="rerank", family="qwen", type="rerank", ctx=8192, out=1, caps=[],
                 components=[("input", "per_1m_tokens", "0", "0.1")]),
            dict(kind="image", family="qwen", type="image", ctx=1, out=1, caps=[],
                 components=[("image", "per_image", "0.15", None)]),
            dict(kind="tts", family="grok", type="audio", ctx=1, out=1, caps=["tts"],
                 components=[("input_char", "per_1m_chars", "108", None)]),
            dict(kind="asr", family="voxtral", type="audio", ctx=1, out=1, caps=["asr"],
                 components=[("audio_second", "per_second", "0.00036", None)]),
        ],
    ),
    "dashscope": dict(
        name="阿里云百炼",
        base_url="https://dashscope.aliyuncs.com/compatible-mode/v1",
        key_env="DASHSCOPE_API_KEY",
        models={
            "chat": "qwen-flash",
            "vlm": "qwen3-vl-flash",
            "embedding": "text-embedding-v4",
            # 以下 4 项经网关方言 / codec 适配（dashscope 预设）：rerank 走 /compatible-api/v1/reranks，
            # 图像走原生同步接口，TTS 走原生接口并下载 wav，ASR 走 chat + input_audio
            "rerank": "qwen3-rerank",
            "image": "qwen-image-3.0",
            "tts": "qwen3-tts-flash",
            "asr": "qwen3-asr-flash",
        },
        embedding_dim=1024,
        # 直连只支持 float；经网关由方言 transform embedding_base64_encode 在网关内编码
        embedding_no_base64_direct=True,
        # 直连时这些能力不是 OpenAI 形状，只测网关层
        direct_unsupported=["rerank", "image", "tts", "asr"],
        tts_voice_direct="Cherry",
        tts_voice_gateway="alloy",  # 方言 voice_map：alloy -> Cherry
        image_extra={"size": "1024x1024", "n": 1},
        image_small_size="1024x1024",
        items=[
            dict(kind="chat", family="qwen", type="chat", ctx=131072, out=8192, caps=CAPS_CHAT,
                 cost=("0.15", "1.5"), sell=None),
            dict(kind="vlm", family="qwen", type="chat", ctx=262144, out=8192, caps=CAPS_CHAT + ["vision"],
                 cost=("0.15", "1.5"), sell=None),
            dict(kind="embedding", family="qwen", type="embedding", ctx=8192, out=1, caps=[],
                 components=[("input", "per_1m_tokens", "0.5", None)]),
            dict(kind="rerank", family="qwen", type="rerank", ctx=4096, out=1, caps=[],
                 components=[("input", "per_1m_tokens", "0.5", None)]),
            dict(kind="image", family="qwen", type="image", ctx=1, out=1, caps=[],
                 components=[("image", "per_image", "0.2", None)]),
            dict(kind="tts", family="qwen", type="audio", ctx=1, out=1, caps=["tts"],
                 components=[("input_char", "per_1m_chars", "100", None)]),
            dict(kind="asr", family="qwen", type="audio", ctx=1, out=1, caps=["asr"],
                 components=[("audio_second", "per_second", "0.00022", None)]),
        ],
    ),
    "volcengine": dict(
        name="火山方舟",
        base_url="https://ark.cn-beijing.volces.com/api/v3",
        key_env="ARK_API_KEY",
        models={
            "chat": "doubao-seed-2-0-mini-260428",
            "vlm": "doubao-seed-2-0-mini-260428",
            "embedding": "doubao-embedding-text-240715",  # 文本向量（即将下线）；新的 vision 向量走 /embeddings/multimodal，需要适配
            "rerank": None,  # 方舟没有通用 rerank 接口
            "image": "doubao-seedream-5-0-lite-260128",
            "tts": None,  # 豆包语音：独立域名、独立鉴权，需要专用适配器
            "asr": None,
        },
        embedding_dim=2560,
        # Seedream 不认 n；尺寸有最小像素要求
        image_extra={"size": "2K", "watermark": False},
        image_small_size="2K",
        image_no_n=True,
        items=[
            dict(kind="chat", family="doubao", type="chat", ctx=262144, out=16384, caps=CAPS_CHAT + ["vision"],
                 cost=("0.2", "2"), sell=None),
            dict(kind="embedding", family="doubao", type="embedding", ctx=4096, out=1, caps=[],
                 components=[("input", "per_1m_tokens", "0.5", None)]),
            dict(kind="image", family="seedream", type="image", ctx=1, out=1, caps=[],
                 components=[("image", "per_image", "0.22", None)]),
        ],
    ),
}
