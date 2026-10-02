# Gateway 多模态接口补全与缺陷修复技术实施方案

> 输入：`tests/gateway_py/REPORT.md`（2026-10-02，SiliconFlow 端到端测试）
> 目标：修复测试发现的网关缺陷，补全 rerank / 图像生成 / 语音合成 / 语音识别四个接口，打通**运营上架 → 用户在模型库看到正确的类型与价格 → 按文档调用 → 正确计费**的完整链路，并同步更新 frontend/web 文档。
> 验收：`tests/gateway_py` 中所有 `xfail` 用例翻转为通过（去掉 xfail 标记），新增用例全部通过。

---

## 1. 问题清单

### 1.1 测试报告中的缺陷 / 缺口

| ID | 问题 | 根因（代码位置） |
|---|---|---|
| D1 | `/v1/chat/completions`、`/v1/messages` 不校验模型 type，embedding 模型被转发到上游后才失败 | `relay.go` ChatCompletions 只校验存在性与可见性；`embeddings.go:88` 有对称校验 |
| D2 | base64 图片按 `len(body)/4` 计 token，约 1MB 以上的图片就超出上下文窗口 → 503；预扣金额被严重高估 | `relay.go:200` `estimateTokens(len(body))` |
| D3 | 路由失败（no_available_channel）、上游失败等错误统一提示 "Upstream request failed."，看不出原因 | `relay.go` `classifyRelayError` 只返回 status/code，message 写死 |
| D4 | 超过 20MB 的请求体返回 400 "not valid JSON" | `io.LimitReader(r.Body, MaxUpstreamBody)` 静默截断，截断后的 JSON 解析失败 |
| D5 | 路由器支持 `NeedVision` 过滤，但 relay 从不设置 | `relay.go:211` 构造 `router.Features` 时漏掉了 |
| L1 | `/v1/messages` 丢弃 Anthropic image block | `messages.go` `extractAnthropicText` 只保留 text |
| G1 | `/v1/rerank` 无路由 | `gateway.go` |
| G2 | `/v1/images/generations` 503 | `gateway.go:120` notImplementedHandler |
| G3 | `/v1/audio/speech`、`/v1/audio/transcriptions` 503 | `gateway.go:121-122` |

### 1.2 本次梳理新发现（不改就无法上架）

| ID | 问题 | 位置 |
|---|---|---|
| F1 | 计费只有 token 类计量项（input/output/cache/reasoning/request），没有「张 / 字符 / 秒」的计量项。`per_image`/`per_second` 单位已存在，但没有计量项能用到 | `internal/pricing/pricing.go`、`internal/admin/catalog.go` `validMeters` |
| F2 | `schema.Usage` / `request_logs` 只有 token 字段，图片张数、字符数、音频时长无处记录 | `internal/schema/usage.go`、`migrations/00007` |
| F3 | 模型 type `audio` 无法区分 TTS 和 ASR | `migrations/00003` type CHECK |
| F4 | 批量导入、定价预览只支持 `cost_input/cost_output`，无法给图像 / 语音模型定价 | `internal/admin/importmodels.go`、`preview.go` |
| F5 | 用户端模型库 `modelFromCatalog` 忽略 catalog 的 `type`，只读 `per_1m_tokens` 的 input/output 价格 → 图像 / 语音 / rerank 模型显示 ¥0，也无法按分类筛选 | `frontend/web/src/data/models.ts:819` |
| F6 | 文档 TryIt 只支持 JSON 请求和文本响应，无法上传音频文件、播放音频、预览图片 | `frontend/web/src/docs/api/TryIt.tsx` |
| F7 | `Adapter` 接口只有 JSON → JSON / SSE 一种形态；anthropic、gemini 适配器会忽略 endpoint，把非对话请求打到错误的上游路径 | `internal/adapter/adapter.go`、`embeddings.go` 注释 |

---

## 2. 总体设计

### 2.1 端点 × 模型类型矩阵（统一校验，修 D1）

新增 `relay.endpointSpec`，每个端点声明允许的模型类型 / 能力和上游路径。所有 handler 在 catalog 查到 vm 之后统一调用 `checkModelFor(spec, vm)`：

| 端点 | 日志名 | 允许 type | 必需能力 | 上游路径 |
|---|---|---|---|---|
| `/v1/chat/completions` | chat.completions | chat | — | `/chat/completions` |
| `/v1/messages` | messages | chat | — | `/chat/completions` |
| `/v1/embeddings` | embeddings | embedding | — | `/embeddings` |
| `/v1/rerank` | rerank | rerank | — | `/rerank` |
| `/v1/images/generations` | images.generations | image | — | `/images/generations` |
| `/v1/audio/speech` | audio.speech | audio | `tts` | `/audio/speech` |
| `/v1/audio/transcriptions` | audio.transcriptions | audio | `asr` | `/audio/transcriptions` |

- 类型不匹配统一返回 `404 model_not_found`，与现有 embeddings 行为一致，不泄露模型是否存在。message 改为 "The requested model does not exist or does not support this endpoint."
- **F3 的决策**：不新增 type，而是在 `audio` 下用能力 `tts` / `asr` 区分。能力是 `TEXT[]`，不需要改 CHECK 约束。`internal/admin/enums.go` 的 Capabilities 增加 `tts`、`asr`；后台创建 audio 模型时这两个能力必须二选一。

### 2.2 公共前置管线抽取

把 ChatCompletions / Embeddings 中重复的前置逻辑抽成 `func (s *Service) prepare(w, r, spec, opts) (*prepared, bool)`，包括：读 body（含 413 检查）→ 解析 model → Key 模型白名单 → RPM / 并发限流 → catalog 快照 → type / 能力校验。之后各 handler 只负责：

1. 估算用量 → 计算预扣金额 → `Wallet.Reserve`
2. `callUpstreamWithRetry`（复用，不改）
3. 读响应 → 提取用量 → 结算 → 写响应 → 写 request_logs

transcriptions 是 multipart 请求，`prepare` 要支持两种 body 读取方式：JSON 和 multipart（见 3.4）。

### 2.3 计量项与计费扩展（F1、F2）

**新增计量项（meter）**。`price_components.meter` 没有 CHECK 约束，只需要改 Go 白名单：

| meter | unit | 用于 | 数量来源 |
|---|---|---|---|
| `input` | `per_1m_tokens` | rerank（复用已有） | 上游 `meta.tokens.input_tokens` / `meta.billed_units.input_tokens` / `usage.total_tokens` |
| `image` | `per_image` | 图像生成 | 响应中的图片张数 |
| `input_char` | `per_1m_chars`（新单位） | TTS | `input` 字段的 Unicode 字符数 |
| `audio_second` | `per_second` | ASR | 上游返回的时长；拿不到时按文件解析（见 3.4） |
| `request` | `per_request` | 各接口的按次兜底（已有） | 1 |

**改动点**：

- `pricing.Usage` / `schema.Usage` 增加 `Images`、`InputChars`、`AudioMillis` 字段；`meterQuantities` 增加对应项。`per_second` 单位用 `AudioMillis/1000` 的小数计算，避免按整秒向上取整导致多收。
- `pricing.Unit` 增加 `UnitPer1MChars`，`Charge` 中与 `per_1m_tokens` 一样除以一百万。
- **迁移 `00031_media_metering.sql`**：
  - `price_components.unit` 的 CHECK 增加 `'per_1m_chars'`
  - `request_logs` 增加 `image_count INT`、`input_chars INT`、`audio_ms INT`。分区父表加列，现有分区会自动继承。
  - 小时汇总表（`reqlog/rollup.go` 写入的表）同步增加三个求和列
- `reqlog.Record`、`/v1/usage`（`usageTotals`）、后台调用日志 / 用量分析里的这三个维度一起透出。
- `schema.Usage.IsZero()` 把新字段计入判断，避免把「只有图片张数」的用量误判为没有用量而走兜底。

**TTS 计量口径（需确认，见第 10 节 Q1）**：用户侧按 Unicode 字符计价（与 OpenAI TTS 一致，用户容易理解）。SiliconFlow 的成本按 UTF-8 字节计，1 个中文字符 = 3 字节。运营录入成本价时要换算（中文场景下成本 ≈ 字节单价 × 3）。在导入向导里加一个提示，并允许渠道级的 `cost_multiplier` 兜底。

### 2.4 适配器扩展（F7）

在 `Adapter` 接口中新增两个方法：

```go
// SupportsEndpoint 声明适配器能否处理某个逻辑端点；路由时据此过滤渠道。
SupportsEndpoint(endpoint string) bool
// BuildRawRequest 用于非 JSON 请求体（multipart），body/contentType 由 relay 组装。
BuildRawRequest(ctx context.Context, target Target, endpoint, contentType string, body io.Reader) (*http.Request, error)
```

- OpenAIAdapter：支持全部 7 个端点。JSON 类端点继续用 `BuildRequest`，它会改写 `model` 并叠加 `param_overrides`；对非对话端点，`stream_options` 的注入逻辑只在 `endpoint == /chat/completions` 时生效。
- AnthropicAdapter / GeminiAdapter：`SupportsEndpoint` 只对 chat 返回 true。
- `router.Pick` 增加 `Features.Endpoint`，过滤掉适配器不支持该端点的渠道（需要把 Registry 传入 router，或由 relay 预先算好 `ExcludeChannels`，推荐后者，router 不依赖 adapter 包）。这样即使误把 image 模型挂在 anthropic 渠道上，也只会得到 `503 no_available_channel`，不会打到错误的路径。

### 2.5 错误码与提示（修 D3、D4）

| 变更 | 内容 |
|---|---|
| 新增 `request_too_large` | `413`。读取 body 时用 `LimitReader(limit+1)`，读满即判超限；同时识别 `*http.MaxBytesError`。不再出现 "not valid JSON" 误导 |
| `classifyRelayError` 返回 message | `no_available_channel` → "No upstream channel can serve this request (capability, context length or health)."；`upstream_error` → "Upstream provider failed after retries."；`invalid_request`（上游 400）→ "Upstream rejected the request: <上游 error.message 截断 200 字>"（透传上游原因，便于用户自查参数；去掉上游 Key、URL 等敏感信息） |
| `unsupported_media_type` | `415`。transcriptions 的 Content-Type 不是 multipart，或文件类型不在白名单内 |

所有新增错误码都登记到 `gatewayErrorCodes`，`TestGatewayErrorCodes_Registered` 会检查是否遗漏。

---

## 3. 新接口详细设计

### 3.1 `POST /v1/rerank`（G1）

- **请求**：`{model, query, documents: string[] | {text}[], top_n?, return_documents?}`。除 `model` 外原样透传。
- **校验**：`documents` 非空，且不超过 1000 条（可配置）；`query` 非空。否则返回 `400 invalid_request`。
- **预扣**：`estInput = estimateTokens(len(query)+Σlen(documents))`，按 `input` 计量项计价。TPM 计入 estInput。
- **响应**：原样返回上游 JSON，改写 `id = request_id`，并补上 `model = vmName`（SiliconFlow 不返回 model）。
- **用量提取**（新函数 `extractRerankUsage`，兼容三种格式）：
  1. `meta.billed_units.input_tokens`（SiliconFlow / Cohere，实测返回了这个字段）
  2. `meta.tokens.input_tokens`
  3. `usage.total_tokens` / `usage.prompt_tokens`（Jina 等）
  都没有时按 estInput 计费，`usage_source=estimated`。

### 3.2 `POST /v1/images/generations`（G2）

- **请求**：透传。同时兼容 OpenAI 字段 `n`、`size` 和 SiliconFlow 原生字段 `batch_size`、`image_size`：若只给了 OpenAI 字段，由渠道的 `param_overrides` 之外新增的**字段映射** `channel.param_renames`（JSONB，如 `{"n":"batch_size","size":"image_size"}`）完成改名。这个映射放在 OpenAIAdapter 的 `BuildRequest` 里统一处理，以后其他端点也能用。
  - 迁移：`channels` 增加 `param_renames JSONB NOT NULL DEFAULT '{}'`；后台渠道编辑页增加这个字段。SiliconFlow 的导入预设自动填上。
- **校验**：张数 `n`/`batch_size` 默认 1，上限 4（可配置）；`prompt` 必填。
- **预扣**：`张数 × image 单价`。
- **响应归一化**：上游返回 `images[]`（SiliconFlow）而没有 `data[]` 时，补一个 `data[]`（OpenAI 形状）并保留原字段；改写 `id`、`model`，补 `created`。
- **计费**：`Images = len(data)`，以实际生成张数为准；一张都没有时按失败处理，不扣费。
- **超时**：图像生成耗时通常 5–60 秒。渠道级读超时沿用 `Retry.TotalDeadline`（90 秒），只在**拿到响应头之前**重试。
- **URL 有效期**：SiliconFlow 返回的签名 URL 1 小时后过期（实测 `X-Amz-Expires=3600`）。一期透传并在文档中明确说明；二期可选转存到自有 OSS（见 Q3）。

### 3.3 `POST /v1/audio/speech`（G3-TTS）

- **请求**：`{model, input, voice, response_format?, speed?, stream?}`，透传。`input` 长度上限 4096 字符（可配置）。
- **voice**：SiliconFlow 的格式是 `模型名:音色`（如 `FunAudioLLM/CosyVoice2-0.5B:alex`），其中带的是上游模型名。网关不改写 voice。但虚拟模型名和上游模型名不同时，用户无法知道上游模型名，所以：
  - 支持短音色名：voice 不含 `:` 时，适配器拼成 `<upstream_model>:<voice>`。只对 `provider.code=siliconflow` 生效，用渠道 `param_overrides` 中的 `voice_prefix_upstream_model: true` 开关控制，不写死供应商。
  - 文档只公开短音色名（alex、anna、bella、benjamin、charles、claire、david、diana）。
- **预扣与计费**：数量在请求时就能确定，`InputChars = utf8.RuneCountInString(input)`，`usage_source=upstream`。
- **响应**：二进制。非流式：完整读取（上限 `MaxUpstreamBody`），用上游的 `Content-Type` 原样写回，加上 `X-Request-Id`。`stream=true`：按 32KB 分块 `io.Copy` + Flush 转发，不做 SSE 解析；中途断开也按全额字符数计费（字符数在请求时已确定，与流式对话不同）。
- **重试**：实测 SiliconFlow TTS 偶发 500，现有分类器把 5xx 归为可换渠道重试，不需要改。
- 需要给 `handleNonStream` 增加一个 `rawPassthrough` 分支，绕开 `DecodeResponse` 的 JSON 解析。

### 3.4 `POST /v1/audio/transcriptions`（G3-ASR）

- **请求**：`multipart/form-data`，字段 `file`（必填）、`model`（必填）、`language`、`prompt`、`response_format`、`temperature`。
- **处理**：
  1. `prepare` 用 `r.MultipartReader()` 流式解析，`model` 字段必须在 `file` 之前（OpenAI SDK 就是这个顺序）。若顺序相反，先把 file 缓冲到内存，上限 20MB，与网关 body 上限一致。
  2. 文件扩展名 / MIME 白名单：mp3、wav、m4a、mp4、mpeg、mpga、webm、ogg、flac。否则返回 `415`。
  3. 重新组装 multipart（`model` 改写为上游模型名），通过 `BuildRawRequest` 发出。重试时需要重放 body，所以文件保存在 `[]byte` 中（≤20MB）。
- **时长计量**（`AudioMillis`），按优先级：
  1. 上游返回的 `usage.seconds`（OpenAI gpt-4o-transcribe）或 `duration`（verbose_json）
  2. 本地解析：wav 读 RIFF 头（零依赖）；mp3 扫描帧头累加（引入 `github.com/tcolgate/mp3`，纯 Go）；其他格式解析不到
  3. 都拿不到时按 `request` 计量项（按次）兜底，`usage_source=estimated`
- **预扣**：按文件大小估算时长（保守取 16kbps → 秒数 = bytes / 2000），上限 2 小时；模型只配了按次价时按 1 次预扣。
- **响应**：上游 JSON 原样返回（`{text}`），改写 / 补充 `id`。`response_format=text/srt/vtt` 时是纯文本，原样透传 Content-Type。

### 3.5 `/v1/completions`

不在本期范围，保持 503 not_implemented。

---

## 4. 缺陷修复设计

### D1 对话接口模型类型校验
通过 2.1 的 `checkModelFor` 统一实现。注意 `/v1/messages` 内部调用 ChatCompletions 的路径也会生效。

### D2 图片 token 估算
- 新增 `estimateChatInputTokens(body []byte, reqMap)`：遍历 `messages[].content[]`，对 `image_url.url` 以 `data:` 开头的部分扣除其字节数，再按每张图 `ImageTokenEstimate`（新配置 `relay.image_token_estimate`，默认 1500）加回；公网 URL 图片同样按每张固定值计算。
- 这个估算用于路由的上下文窗口过滤、TPM 和预扣三处。最终计费仍以上游返回的 usage 为准，不受影响。
- 单测：4MB 的 data URL 图 + 短文本 → 估算值 < 3000 token。

### D3 / D4
见 2.5。

### D5 视觉能力过滤
- `features.NeedVision = countImageInputs(reqMap) > 0`（`attribution.go` 已有这个函数）。
- **上线风险**：已上架的 VLM 如果没有勾选 `vision` 能力，开启后会立刻 503。分两步上线：
  1. 新增配置 `relay.enforce_vision`，默认 false。上线一个只读检查：后台工作台增加待办，「近 7 天有 `image_inputs>0` 的请求、但能力里没有 vision 的模型」，提醒运营补勾。
  2. 运营补齐之后把默认值改为 true。

### L1 `/v1/messages` 图片支持
- `anthropicRequestToOpenAI`：content 数组中出现 `{"type":"image","source":{"type":"base64","media_type","data"}}` 时转为 `{"type":"image_url","image_url":{"url":"data:<media_type>;base64,<data>"}}`；`source.type=url` 转为 `image_url.url`。消息中有图片时，content 保持数组形式（text 块转为 `{"type":"text","text"}`）；没有图片时保持字符串，与现有行为一致。
- 这样 D5 的视觉过滤自动生效。更新 `messages_internal_test.go`。

---

## 5. 运营后台改造（让模型能正确上架）

| 项 | 改动 |
|---|---|
| 枚举 | `enums.go`：Capabilities 加 `tts`、`asr`；Meters 加 `image`、`input_char`、`audio_second`；Units 加 `per_1m_chars` |
| 价格组件校验 | `validMeters` / `validUnits` 同步更新；增加 meter↔unit 合法组合校验（`image` 只能配 `per_image`，`input_char` 只能配 `per_1m_chars`，`audio_second` 只能配 `per_second`） |
| 批量导入 `import-models` | `ImportModelItem` 增加通用的 `cost_components` / `sell_components`（`[{meter,unit,price}]`），与 `cost_input/cost_output` 二选一（后者保留以兼容现有前端）。PlanImport 按 type 校验必需的计量项：chat→input+output，embedding/rerank→input，image→image，audio+tts→input_char，audio+asr→audio_second 或 request |
| 定价预览 `PricingPreview` | 从「input/output 两项」泛化为按组件逐项加价、逐项计算毛利；毛利率取各组件的最小值 |
| 后台前端 | 导入向导第 4 步「定价」按模型 type 渲染不同的价格输入；能力多选增加 tts/asr，audio 类型强制二选一；渠道编辑页增加 `param_renames`；调用日志 / 用量分析增加张数 / 字符 / 时长列 |
| SiliconFlow 预设 | `upstream-models` 列表返回模型时，按上游 `sub_type`（text-to-speech、speech-to-text、image、reranker、embedding）自动推断 type 和能力，减少人工填错 |

---

## 6. 用户端 frontend/web 改造

### 6.1 模型库（F5）
- `api/catalog.ts` 已经透出 `type`。`modelFromCatalog` 改为：
  - `modalities` / `tags` 由 `type + capabilities` 推导：chat（有 vision 时加 image 输入）、embedding → `tags:['embedding']`、rerank → `['rerank']`、image → 输出模态 image、audio+tts → `['voice']`、audio+asr → `['transcription']`。
  - 价格展示改为按计量项格式化：新增 `formatPriceComponents(sellPrice)`，输出类似 `¥0.1 / 张`、`¥65 / 百万字符`、`¥0.003 / 秒`、`¥0.5 / 百万 Token（输入）`；没有 output 计量项的模型不显示输出价格。`inputPricePerM` / `outputPricePerM` 继续保留，供排序和比价使用；非 token 计价的模型在价格排序里单独归组。
- `PRIMARY_TAGS` 增加 `rerank`（`matchesPrimaryTag` 已有这个 case）；分类计数沿用 App.tsx 的实时统计。
- 模型详情页增加「接口」卡片：按 type 显示对应端点和最小调用示例（cURL / Python），并链接到对应文档页。

### 6.2 Playground
- 一期：对话模型支持上传图片（有 vision 能力时出现上传按钮，转成 data URL）。
- 二期：image 模型显示生成表单和结果预览；TTS 显示文本框、音色下拉和 `<audio>` 播放；ASR 支持上传文件后显示文本；embedding / rerank 只给代码示例，不做交互。

### 6.3 个人中心用量
用量明细和汇总增加「张 / 字符 / 秒」列（来自 `/console` 用量接口，需要同步加字段）。

---

## 7. 文档更新（frontend/web/src/docs）

文档的唯一事实来源是 `internal/app/gateway_openapi.go` 生成的 `docs/gateway-openapi.json`，再加上 `content/{zh,en}/*.mdx`。中文是源语言。

### 7.1 OpenAPI（Go 侧）
- 把 `notImpl(...images/audio...)` 替换为完整的 operation：`createImage`、`createSpeech`、`createTranscription`（operationId 不变，保证前端链接稳定），并新增 `createRerank`（`POST /v1/rerank`，tag `Rerank`）。
- 新增 schema：`RerankRequest/Response`、`ImageGenerationRequest/Response`、`SpeechRequest`（响应为 `audio/*` 二进制）、`TranscriptionRequest`（`multipart/form-data`）/ `TranscriptionResponse`。
- 更新说明文字：chat（图片输入、图片 token 估算规则、vision 路由）；messages（支持 image block，删除「仅支持文本」）；embeddings 不变；`model_not_found`（type 不匹配也返回它）；`no_available_channel`（加上 vision 和端点不支持）；新增 `request_too_large`、`unsupported_media_type`。
- 重新生成：`UPDATE_GATEWAY_API_DOC=1 go test ./internal/app -run TestGatewayOpenAPI_UpToDate`。

### 7.2 MDX 页面

| 页面 | 动作 | 要点 |
|---|---|---|
| `vision.mdx`（新，guides） | 新增 | image_url（URL / base64）、多图、大小限制、图片 token 预估、哪些模型支持（catalog `capabilities` 含 vision）、`/v1/messages` 写法 |
| `rerank.mdx`（新） | 新增 | 请求 / 响应、top_n、return_documents、计费（输入 token）、与 embeddings 搭配做 RAG 的示例 |
| `images.mdx`（新） | 新增 | 参数（n/size 与上游原生参数）、返回 `data[].url` 的 **1 小时有效期**、按张计费、超时建议 |
| `audio.mdx`（新） | 新增 | TTS：音色列表、格式、流式（二进制分块，不是 SSE）、按字符计费；ASR：multipart、支持格式、20MB 上限、按秒计费（拿不到时长时按次）；OpenAI SDK 写法 |
| `anthropic-messages.mdx` | 改 | 删除「图片会被静默忽略」，改为支持 base64 / url 图片 |
| `models.mdx` | 改 | 模型类型 × 端点对照表；能力 vision/tts/asr 的含义 |
| `billing.mdx` | 改 | 计量单位表（token / 张 / 百万字符 / 秒 / 次）；各端点的预扣规则；图片 token 估算 |
| `rate-limits.mdx` | 改 | TPM 只统计 token 类端点（chat/messages/embeddings/rerank）；图像和语音只受 RPM 与并发限制 |
| `errors.mdx` | 自动 | 错误码表由 spec 渲染；补充 413 / 415 的处理建议 |
| `streaming.mdx` | 改 | 补一句「TTS 流式是二进制分块，不是 SSE」 |
| `api.mdx` | 改 | 接口总表：移除「未实现」标注，加入 rerank |
| `integrations/openai-sdk.mdx` | 改 | `client.images.generate`、`client.audio.speech.create`、`client.audio.transcriptions.create`、rerank 用 httpx（OpenAI SDK 没有 rerank） |
| `changelog.mdx` | 改 | 新增版本条目 |
| `en/*` | 同步翻译 | 完成后 `npm run docs:check -- --write-hashes` |

### 7.3 文档组件（F6）
- `snippets.ts`：requestBody 为 `multipart/form-data` 时，生成 `curl -F`、Python `files=`、JS `FormData` 示例；响应为 `audio/*` 时，cURL 示例加 `--output speech.mp3`。
- `TryIt.tsx`：multipart 操作渲染文件选择器；响应 Content-Type 为 `audio/*` 时渲染 `<audio controls>`（Blob URL），`image/*` 时渲染 `<img>`；JSON 响应里有 `data[].url` 时附带缩略图预览。计费类操作的 TryIt 默认参数用最低消耗（n=1、短文本）。
- `npm run lint`（含 `docs:check`）+ `npm run build` 必须通过。

---

## 8. 测试计划

| 层 | 内容 |
|---|---|
| Go 单测 | `pricing`：新计量项 / 单位、per_second 小数计算；`relay`：每个新 handler 用 httptest 模拟上游，覆盖成功、上游 4xx/5xx 重试、类型不匹配、413/415、用量提取的三种格式、TTS 二进制透传与流式、ASR multipart 重放；`adapter`：`param_renames`、voice 前缀、`SupportsEndpoint`；`messages`：image block 转换；估算函数 D2；`TestGatewayOpenAPI_UpToDate` / `TestGatewayErrorCodes_Registered` |
| 迁移 | `00031` up/down 在 `uft` 测试库上执行；旧分区继承新列 |
| E2E（`tests/gateway_py`） | 去掉 G1–G3、D1、D2、L1 的 xfail，改为正式断言；新增：413 / 415；type 不匹配（每个端点各一条）；vision 路由；计费精确性（生成 1 张图扣 1×单价、TTS 扣「字符数×单价」、ASR 按秒）；`/v1/usage` 的新维度 |
| 前端 | `npm run lint && npm run build`；模型库：各类型模型的价格和分类筛选手工验收；TryIt：上传、播放、预览 |

---

## 9. 实施排期

| 阶段 | 内容 | 预估 |
|---|---|---|
| P0 | D1–D5、L1（不依赖计费扩展，可以先上线） | 3 天 |
| P1 | 计量计费底座：迁移 00031、pricing / schema / reqlog / usage、`param_renames`、Adapter 扩展、公共前置管线抽取 | 4 天 |
| P2 | rerank + images（JSON 类端点）及单测 | 3 天 |
| P3 | audio speech + transcriptions（二进制 / multipart）及单测 | 4 天 |
| P4 | 运营后台：导入 / 定价泛化、枚举、渠道编辑、日志列 | 3 天 |
| P5 | 用户端：模型库类型与价格、详情页接口卡片、Playground 图片输入、用量列 | 3 天 |
| P6 | 文档：OpenAPI、MDX（中英）、snippets / TryIt | 3 天 |
| P7 | 联调：SiliconFlow 全量 E2E、上架演练、回归 | 2 天 |

合计约 25 人日。P0 与 P1 可以并行；P2/P3 依赖 P1；P5/P6 可以在 P2 完成后开始。

### 上架流程（P7 之后的运营操作）
1. 后台 → 供应商 siliconflow → 获取上游模型 → 勾选模型（type 和能力自动推断，人工复核）
2. 按 type 填写成本价（TTS 注意字节→字符换算），预览毛利后导入
3. 填写展示信息（描述、标签），确认 visible_tiers
4. 跑 `pytest -m gateway` 冒烟，通过后在用户端模型库核对价格和分类

---

## 10. 待决策事项

| # | 问题 | 推荐 |
|---|---|---|
| Q1 | TTS 对用户按「字符」还是「UTF-8 字节」计价 | **字符**（与 OpenAI 一致、用户直观）；成本侧在导入时换算 |
| Q2 | ASR 拿不到时长的格式（m4a/webm/ogg 等）怎么计费 | 一期按次兜底；二期视需要引入 ffprobe 或纯 Go 解析 |
| Q3 | 图像 URL 1 小时过期，是否转存到自有对象存储 | 一期透传并在文档中说明；有用户反馈后再做转存（涉及存储成本与内容合规） |
| Q4 | `enforce_vision` 何时默认开启 | 补齐存量 VLM 的 vision 能力后开启，预计 P0 上线后 1 周 |
| Q5 | 图像 / 语音是否需要内容安全审核 | 依赖上游审核（`content_filtered` 已有分类），平台侧审核不在本期范围 |

---

## 11. 兼容性与回滚
- 所有改动对现有 chat/embeddings 调用方向后兼容。唯一的行为变化是 D1（错误码从 400 变为 404，且不再请求上游），在 changelog 中注明。
- 迁移只增加列、放宽 CHECK 约束，down 迁移可以回滚。
- 新端点可以通过配置开关 `gateway.endpoints.{rerank,images,audio}`（默认 true）单独关闭，关闭后恢复 503 not_implemented。
- `enforce_vision`、`image_token_estimate` 都可以在线调整。

---

## 12. 实施记录（2026-10-02）

方案已按第 10 节的推荐决策全部实施，以下为与原方案不同或细化的地方。

| 项 | 原方案 | 实际实现 | 原因 |
|---|---|---|---|
| OpenAI 字段映射 | 新增 `channels.param_renames` 列，把 `n`/`size` 改名为 `batch_size`/`image_size` | **未做** | 实测 SiliconFlow 图像接口直接接受 `n`、`size`，并且已经返回 `data[]` |
| TTS 音色前缀 | 由 `provider.code` 决定 | 渠道 `param_overrides` 中的指令键 `$voice_prefix_upstream_model: true`（`$` 开头的键不会转发给上游）；导入时由 `import-models` 的 `param_overrides` 写入，后台向导对 SiliconFlow 默认勾选 | 不把供应商名写死在代码里，其他上游也可以复用 |
| ASR 时长 | 引入 mp3 解析库 | 优先取上游的 `usage.seconds`（实测 SiliconFlow 返回这个字段），其次 WAV 头，最后按 16kbps 估算；不引入新依赖 | 主流上游已返回时长 |
| 计量项约束 | 迁移只放宽 unit 的 CHECK | meter 也有 CHECK（`chk_price_components_meter`），一并放宽；同时重建 `v_admin_channel_margin`，毛利计算加入多模态计量项 | 原方案漏看了这两处 |
| 配置开关 | `gateway.endpoints.{...}` | `relay.enforce_vision`、`relay.image_token_estimate`、`relay.disabled_endpoints`（逗号分隔 rerank/images/audio），环境变量为 `UFT_RELAY_*` | 与现有配置分区保持一致 |
| 模型类型纠错 | — | `PATCH /virtual-models/{id}` 支持修改 `type`，后台「编辑基本信息」中增加类型选择 | 发现旧向导把所有模型都按 `chat` 导入，需要有地方纠正 |
| 后台向导 | 定价步骤按类型渲染 | 按模型 ID 自动推断种类（对话 / 嵌入 / 重排序 / 图像 / 语音合成 / 语音识别），可手改；非对话模型使用单一计量项价格，提交 `cost_components`/`sell_components` | 旧向导写死了 `type: 'chat'`，这是一个已有缺陷 |

### 主要改动位置
- 网关：`internal/relay/{endpoints,rerank,images,audio}.go`（新增），`relay.go`/`embeddings.go`/`messages.go`/`helpers.go`；`internal/adapter`（端点支持声明、multipart、音色前缀指令）；`internal/app/gateway.go`（路由与下线开关）
- 计费：`internal/pricing`、`internal/schema`、`internal/reqlog`、`migrations/00031_media_metering.sql`
- 运营后台：`internal/admin/{catalog,importmodels,preview,pricectx,updates,reqlogs,enums}.go`；`frontend/admin`（向导、价格编辑器、调用日志、模型类型编辑）
- 用户端：`frontend/web/src/data/models.ts`（类型、价格、分类）、`ModelApiSection.tsx`（模型详情页的接口调用卡片）、个人中心用量列
- 文档：`internal/app/gateway_openapi.go` → `docs/gateway-openapi.json`；新增 `vision`/`rerank`/`images`/`audio` 中英文页面，更新 8 篇中英文页面；文档调试面板支持文件上传、音频播放、图片预览

### 验证结果
- Go：27 个包全部通过（新增 `relay/multimodal_test.go`、`multimodal_internal_test.go`、`app/admin_media_test.go`、`pricing` 多模态计量项用例）
- 端到端（`tests/gateway_py`，真实 SiliconFlow）：83 passed、0 xfail
- 前端：web `npm run lint`（tsc + docs:check，44 页，0 错误 0 警告）、`npm run build`；admin `tsc` + `build`；Playwright 冒烟（文档页、调试面板的语音合成与识别和图像生成、模型库价格与分类、后台向导多模态定价 dry-run）

### 遗留与上架注意事项
- 开发库中 `Qwen/Qwen3-Embedding-0.6B`、`Tongyi-MAI/Z-Image` 被旧向导误录为 `chat`，需要在后台「虚拟模型 → 编辑基本信息」中改成 `embedding` / `image`，并按新类型重新发布售价（Z-Image 按张）。
- `relay.enforce_vision` 目前默认关闭：补齐存量 VLM 的 `vision` 能力后再开启（Q4）。
- 图像 URL 会过期，目前不转存（Q3）；图像和语音不做平台侧内容审核（Q5）。
