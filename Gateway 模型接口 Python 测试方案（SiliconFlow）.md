# Gateway 模型接口 Python 测试方案（SiliconFlow 上游）

## 1. 目标与范围

frontend/web 的接口文档（`docs/gateway-openapi.json`，渲染在 `frontend/web/src/docs`）面向的后端是 `cmd/gateway`。本方案用 Python 搭一套端到端测试，**接真实上游 SiliconFlow**，验证以下几类模型接口：

| 类别 | 网关接口 | 说明 |
|---|---|---|
| 文本对话（非流式 / 流式） | `POST /v1/chat/completions` | 含 `stream=true`、`stream_options.include_usage`、tool calling |
| 视觉语言模型（VLM） | `POST /v1/chat/completions` | `messages[].content` 中带 `image_url`（URL 与 base64 两种） |
| Anthropic 兼容 | `POST /v1/messages` | 非流式 / 流式；带图片的情况单独记录 |
| 向量 Embedding | `POST /v1/embeddings` | 单条 / 批量输入 |
| 重排序 Rerank | `POST /v1/rerank` | **网关暂无此路由** |
| 图像生成 | `POST /v1/images/generations` | 网关返回 503 `not_implemented` |
| 语音合成 TTS | `POST /v1/audio/speech` | 网关返回 503 `not_implemented` |
| 语音识别 ASR | `POST /v1/audio/transcriptions` | 网关返回 503 `not_implemented` |
| 辅助接口 | `GET /v1/models`、`GET /v1/usage`、`GET /v1/catalog` | 用于确认模型可见、验证扣费 |

**不在范围内**：实现网关缺失的接口（images/audio/rerank）。本方案只负责测出现状并把缺口记下来，实现另立方案。

---

## 2. 现状盘点（代码核实结论）

依据 `internal/app/gateway.go`、`internal/relay/*`：

1. **已实现**：`/v1/chat/completions`、`/v1/embeddings`、`/v1/messages` 走完整的「路由 → 预扣 → 转发（重试/换 Key/换渠道）→ 结算」链路；`/v1/models`、`/v1/usage`、`/v1/catalog` 可用。
2. **VLM**：relay 把请求体按 `map[string]any` 透传给 OpenAI 兼容上游，`content` 数组里的 `image_url` 会原样转给上游，理论上可用，需要实测确认。
3. **`/v1/messages` 的图片**：`anthropicRequestToOpenAI` 只用 `extractAnthropicText` 提取文本，Anthropic 格式的 `image` block 会被**丢弃**。测试要把这一点记录为已知限制。
4. **Embeddings**：`internal/relay/embeddings.go:88` 要求虚拟模型 `type == "embedding"`，导入时类型必须填对。
5. **未实现**：`/v1/images/generations`、`/v1/audio/speech`、`/v1/audio/transcriptions`、`/v1/completions` 返回 503 `not_implemented`。
6. **完全没有路由**：`/v1/rerank`，请求会返回 404。后台的模型类型虽然支持 `rerank`（`internal/admin/enums.go`），但数据面没有对应接口。

---

## 3. SiliconFlow 选型

上游 Base URL：`https://api.siliconflow.cn/v1`（OpenAI 兼容，协议填 `openai`）。

| 类别 | 网关模型 type | 候选模型（执行前用上游 `/v1/models` 核实是否还在） |
|---|---|---|
| 文本 | chat | `Qwen/Qwen2.5-7B-Instruct`（免费）、`deepseek-ai/DeepSeek-V3` |
| VLM | chat（capabilities 加 `vision`） | `Qwen/Qwen2.5-VL-32B-Instruct` 或 `Qwen/Qwen2-VL-72B-Instruct` |
| Embedding | embedding | `BAAI/bge-m3`、`BAAI/bge-large-zh-v1.5` |
| Rerank | rerank | `BAAI/bge-reranker-v2-m3` |
| 图像 | image | `Kwai-Kolors/Kolors` |
| TTS | audio | `FunAudioLLM/CosyVoice2-0.5B`（voice 如 `FunAudioLLM/CosyVoice2-0.5B:alex`） |
| ASR | audio | `FunAudioLLM/SenseVoiceSmall` |

优先用免费或低价模型控制费用。模型名统一写在配置文件里，不硬编码在用例中。

---

## 4. 两层测试结构

同一组用例跑两遍，用来区分问题出在上游还是网关：

- **L0 基线层（直连 SiliconFlow）**：`base_url=https://api.siliconflow.cn/v1`，`SILICONFLOW_API_KEY`。用来确认上游 Key、模型名、请求格式本身没问题。包括网关未实现的接口（rerank/images/audio），为以后实现留一份「上游预期响应」样本。
- **L1 网关层（经 cmd/gateway）**：`base_url=http://localhost:8080/v1`，网关 API Key `sk-uft-...`。验证鉴权、路由、模型名改写、流式透传、计费。

判定规则：L0 通过、L1 失败 → 网关问题；L0 也失败 → 上游或配置问题，不算网关缺陷。

---

## 5. 环境准备

### 5.1 启动本地栈

```bash
./scripts/dev-all.sh start      # DB + gateway(:8080) + admin(:8081) + worker + 前端
./scripts/dev-all.sh status
```

### 5.2 接入 SiliconFlow（脚本 `onboard_siliconflow.py`，可重复执行）

用应急令牌 `dev-admin-token-change-me` 调 admin API（`http://localhost:8081`），步骤与 `tools/devseed/main.go` 一致：

1. `POST /providers`：`{"code":"siliconflow","name":"SiliconFlow","protocol":"openai"}`（已存在则查出来复用）
2. `POST /provider-accounts`：`{"provider_id":..,"name":"sf-test","base_url":"https://api.siliconflow.cn/v1","cost_multiplier":"1"}`
3. `POST /provider-accounts/{id}/keys`：`{"secret": $SILICONFLOW_API_KEY, "weight":100}`
4. `GET /provider-accounts/{id}/upstream-models`：核对第 3 节的模型都在上游列表里
5. `POST /provider-accounts/{id}/import-models`：先 `dry_run:true` 看预览，再 `dry_run:false` 正式导入，`type` 按第 3 节填写，成本价按 SiliconFlow 官网价格（CNY；免费模型填 0 时注意是否被负毛利校验拦下）
6. 等 10 秒以上（网关每 10 秒刷新目录快照），再用网关 Key 调 `GET /v1/models` 确认模型可见

网关 API Key 用 seed 账户 demo@uft.local 的 Key（`tmp/dev-stack/seed-output.txt`），或在用户端个人中心新建。

### 5.3 配置（`.env`，不提交 git）

```
SILICONFLOW_API_KEY=sk-...
SF_BASE_URL=https://api.siliconflow.cn/v1
UFT_GATEWAY_URL=http://localhost:8080/v1
UFT_API_KEY=sk-uft-...
UFT_ADMIN_URL=http://localhost:8081
UFT_ADMIN_TOKEN=dev-admin-token-change-me
```

---

## 6. 目录与依赖

```
tests/gateway_py/
├── README.md
├── requirements.txt          # pytest, httpx, openai, anthropic, python-dotenv, pytest-html(可选)
├── .env.example
├── config.py                 # 读取 .env + 模型名映射
├── onboard_siliconflow.py    # 第 5.2 节接入脚本
├── conftest.py               # fixture：target ∈ {sf, gateway} 参数化；httpx/openai 客户端
├── assets/                   # 测试图片 cat.jpg、测试音频 hello.wav
├── test_chat.py              # 文本非流式 / 流式 / tool calling / 错误码
├── test_vision.py            # VLM：image_url(URL) / base64；/v1/messages 图片
├── test_messages.py          # Anthropic 兼容（anthropic SDK）
├── test_embeddings.py
├── test_rerank.py
├── test_images.py
├── test_audio.py             # TTS + ASR
└── test_billing.py           # /v1/usage 余额变化、X-Request-ID
```

同时用两种客户端：**原生 httpx**（精确断言状态码、响应头、SSE 原始帧）和 **openai / anthropic 官方 SDK**（验证文档里写的 SDK 用法真能跑通）。

---

## 7. 用例清单

### 7.1 文本 Chat（`test_chat.py`）

| ID | 用例 | 断言 |
|---|---|---|
| C1 | 非流式基本对话 | 200；`choices[0].message.content` 非空；`usage.prompt_tokens/completion_tokens > 0`；`model` 字段等于请求的模型 ID |
| C2 | 流式 `stream=true` | `Content-Type: text/event-stream`；收到多个 `data:` 块；拼接后的 `delta.content` 非空；最后是 `data: [DONE]` |
| C3 | 流式 + `stream_options.include_usage=true` | 最后一个数据块带 `usage` |
| C4 | 流式、**不带** include_usage | 客户端**收不到** usage 块（网关为计费注入的 usage 不能泄露给客户端，见 relay.go:535） |
| C5 | 首 token 延迟 | 记录 TTFT 和总耗时，只记录不断言 |
| C6 | tool calling | 返回 `tool_calls`，`function.arguments` 是合法 JSON；流式下 arguments 分片拼接后也合法 |
| C7 | 参数透传 | `temperature`、`max_tokens=5` 生效（输出被截断，`finish_reason=length`） |
| C8 | 错误：无 Key / 错 Key | 401，错误体形状符合 `errors.mdx` |
| C9 | 错误：不存在的模型 | 4xx，错误码符合文档 |
| C10 | 错误：拿 embedding 模型调 chat | 4xx（不应转发到上游） |
| C11 | openai SDK 调用 | 非流式和流式都正常 |

### 7.2 VLM（`test_vision.py`）

| ID | 用例 | 断言 |
|---|---|---|
| V1 | `image_url` 为公网 URL | 200；回答里提到图片内容（如图里是猫，回答含"猫/cat"） |
| V2 | `image_url` 为 `data:image/jpeg;base64,...` | 同 V1 |
| V3 | VLM 流式 | 同 C2 |
| V4 | 多张图片 | 200 |
| V5 | 大图（约 4MB base64） | 记录网关有没有请求体大小限制，以及返回什么错误 |
| V6 | `/v1/messages` 带 Anthropic `image` block | **预期是已知限制**：图片被丢弃，模型回答"看不到图片"。用例标 `xfail` 并注明原因 |

### 7.3 Anthropic Messages（`test_messages.py`）

| ID | 用例 | 断言 |
|---|---|---|
| M1 | 非流式 | 响应有 `type=message`、`content[0].type=text`、`stop_reason`、`usage.input_tokens/output_tokens` |
| M2 | 流式 | 事件顺序：`message_start` → `content_block_start` → `content_block_delta`… → `content_block_stop` → `message_delta` → `message_stop` |
| M3 | `system` 字段 | system 提示生效 |
| M4 | anthropic SDK（`base_url` 指向网关） | 正常返回 |

### 7.4 Embeddings（`test_embeddings.py`）

| ID | 用例 | 断言 |
|---|---|---|
| E1 | 单条字符串 | `data[0].embedding` 是 float 列表，维度符合模型（bge-m3 是 1024） |
| E2 | 批量数组输入 | `len(data) == len(input)`，`index` 有序 |
| E3 | 语义检验 | 余弦相似度 cos("猫","小猫") > cos("猫","汽车") |
| E4 | `encoding_format=base64` | 能解码，维度一致 |
| E5 | `usage.prompt_tokens > 0`，没有输出 token | — |
| E6 | 拿 chat 模型调 embeddings | 4xx |

### 7.5 Rerank（`test_rerank.py`）

| ID | 用例 | 断言 |
|---|---|---|
| R0 (L0) | 直连 SF `POST /v1/rerank`：`{model, query, documents, top_n}` | `results[].relevance_score` 降序，相关文档排第一 |
| R1 (L1) | 经网关调用同一请求 | **当前预期 404**（路由不存在）。用例断言现状，并附一条 `xfail(strict=True)` 用例，按 R0 的标准断言；以后接口实现了它会变成 XPASS，提醒去更新用例 |

### 7.6 图像生成（`test_images.py`）

| ID | 用例 | 断言 |
|---|---|---|
| I0 (L0) | 直连 SF `POST /v1/images/generations`（Kolors，`image_size=1024x1024`） | 返回 `images[].url` 或 `data[].url`，能下载到 PNG/JPEG |
| I1 (L1) | 经网关 | 当前预期 503 + `not_implemented`；同样附 xfail 的目标用例 |

### 7.7 语音（`test_audio.py`）

| ID | 用例 | 断言 |
|---|---|---|
| A0 (L0) | 直连 SF TTS `/v1/audio/speech`（`model, input, voice, response_format=mp3`） | 二进制音频，大小 > 1KB |
| A1 (L0) | 直连 SF ASR `/v1/audio/transcriptions`（multipart 上传 `file` + `model`） | `text` 非空；把 A0 生成的音频回灌给 ASR，识别文本和原文相似 |
| A2 (L1) | 经网关 TTS / ASR | 当前预期 503 `not_implemented`；附 xfail 目标用例 |

### 7.8 计费与观测（`test_billing.py`）

| ID | 用例 | 断言 |
|---|---|---|
| B1 | 调用前后对比 `GET /v1/usage` | 成功调用后余额减少（先扣赠送再扣现金） |
| B2 | 失败调用（如不存在的模型） | 余额不变 |
| B3 | 响应头 `X-Request-ID` | 每次请求都有，且彼此不同（见 `request-id.mdx`） |
| B4 | 流式中途断开 | 客户端读到第 2 个块后关闭连接；之后余额只按已产生的用量扣，不重复扣、不卡住预扣（工作台调用日志里能看到这条记录） |

---

## 8. 运行方式

```bash
cd tests/gateway_py
pip install -r requirements.txt
cp .env.example .env            # 填入 Key
python onboard_siliconflow.py   # 一次性接入，可重复执行
pytest -v                       # 全部（L0 + L1）
pytest -v -m gateway            # 只跑网关层
pytest -v -m "not paid"         # 跳过收费模型
pytest -v --html=report.html    # 生成报告
```

pytest marker：`sf`（L0）、`gateway`（L1）、`stream`、`paid`（会产生费用）、`not_implemented`（网关尚未实现的接口）。

---

## 9. 交付物与验收标准

1. `tests/gateway_py/` 测试代码 + README。
2. 一份测试报告（`report.html` 或 Markdown），按接口列出 L0 / L1 结果，以及缺陷和缺口清单。
3. 验收：
   - Chat（含流式、tool calling）、VLM、Embeddings、Messages 的 L1 用例全部通过，或失败项都有定位结论；
   - rerank / images / audio 的 L0 基线通过，L1 现状（404 / 503）断言通过，并形成「待实现接口」清单，附上游请求/响应样本；
   - 计费用例 B1–B3 通过。

---

## 10. 风险与注意事项

| 风险 | 处理 |
|---|---|
| SiliconFlow Key 泄露 | 只放在 `.env` 和后台密钥库（加密存储），`.env` 加进 `.gitignore`；日志里对 Key 做掩码 |
| 费用 | 默认用免费或低价模型，`max_tokens` 设小；图像和语音只跑 1–2 次；收费用例打 `paid` 标记 |
| 上游限流 / 波动 | 用例之间加间隔；网络错误重试 1 次；结果不确定的语义断言（V1、A1）用关键词宽松匹配 |
| 模型下架或改名 | 模型名集中在 `config.py`，执行前跑一次 `upstream-models` 核对 |
| 免费模型成本价为 0 | 导入时可能被负毛利或缺成本价的校验拦下，按后台提示手动设置售价 |
| 目录快照延迟 | 导入后等 10 秒再跑 L1 |

---

## 11. 执行步骤

1. 搭骨架：config / conftest / requirements / .env.example
2. 写 `onboard_siliconflow.py` 并在本地栈跑通，`/v1/models` 能看到 SF 模型
3. 写 Chat + 流式 + Messages 用例（核心链路）
4. 写 VLM + Embeddings 用例
5. 写 Rerank / Images / Audio 的 L0 基线和 L1 现状用例
6. 写计费与观测用例
7. 全量执行，输出报告和缺陷/缺口清单
