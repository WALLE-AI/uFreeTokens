package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"sort"
	"strings"

	"github.com/WALLE-AI/uFreeTokens/internal/httpx"
)

// 网关数据面（cmd/gateway 的 /v1/*）的 OpenAPI 3.1 文档，产物是
// docs/gateway-openapi.json，由 frontend/web 的双语（zh/en）API 参考页面渲染。
// TestGatewayOpenAPI_UpToDate 保证文件与代码一致，TestGatewayErrorCodes_Registered
// 扫描数据面源码，保证每个可能写出的错误码都登记在 gatewayErrorCodes 里。
//
// 和 AdminOpenAPI 的区别：
//   - 路由不多且稳定，直接在 gatewayOperations 里逐个手写（operationId 是
//     前端依赖的稳定标识，不能自动生成）；
//   - /v1/catalog、/v1/models、/v1/usage 的响应是网关自己的 Go 类型，复用
//     admin_openapi.go 的 schemaGen 反射生成，字段说明来自 gatewayFieldDocs；
//   - chat/embeddings/messages 的请求/响应是 OpenAI/Anthropic 协议形状，基本
//     透传给上游，只手写网关真正读取/校验/改写的字段（additionalProperties: true）；
//   - 每个说明都带英文 + x-i18n.zh 中文两份。

// ---------- 文档用的响应包装类型（与 handler 写出的 map 形状一致） ----------

// catalogListResponse 对应 catalogHandler 写出的 {"object":"list","data":[...]}。
type catalogListResponse struct {
	Object string         `json:"object"`
	Data   []catalogModel `json:"data"`
}

// modelListResponse 对应 listModelsHandler 写出的 {"object":"list","data":[...]}。
type modelListResponse struct {
	Object string        `json:"object"`
	Data   []openAIModel `json:"data"`
}

// usageResponse 对应 usageHandler 写出的 {"wallet":...,"usage":...}。
type usageResponse struct {
	Wallet usageWallet `json:"wallet"`
	Usage  usageTotals `json:"usage"`
}

// l10n 是一段双语文案。
type l10n struct{ en, zh string }

// exampleRequestID 是文档示例里统一使用的请求 ID（ULID 形状）。
const exampleRequestID = "01K6E7T0Q0V5N3M8P2R4S6W9YZ"

// ---------- 错误码登记表 ----------

// gatewayErrorCode 是 /v1/* 可能返回的一个错误码。message 是代码里典型的
// message 文案，只用来生成错误示例。
type gatewayErrorCode struct {
	code      string
	status    int
	retryable bool
	message   string
	desc      l10n
}

// gatewayErrorCodes 是数据面错误码的唯一登记处；TestGatewayErrorCodes_Registered
// 扫描源码保证这里不缺、不多、HTTP 状态一致。
var gatewayErrorCodes = []gatewayErrorCode{
	{"account_suspended", http.StatusForbidden, false, "This account has been suspended.", l10n{
		"The account that owns this API key has been suspended.",
		"该 API Key 所属的账户已被停用。"}},
	{"concurrency_limit_exceeded", http.StatusTooManyRequests, true, "Too many concurrent requests.", l10n{
		"Too many in-flight requests on this API key (the key's concurrency limit). Retry after an earlier request finishes; `Retry-After` is set when a wait time is known.",
		"该 API Key 同时进行中的请求数超过并发上限。等已有请求结束后重试；能算出等待时长时会带 `Retry-After` 头。"}},
	{"content_filtered", http.StatusBadRequest, false, "Upstream request failed.", l10n{
		"The upstream provider rejected the request through its content moderation. Not charged.",
		"上游厂商的内容审核拦截了这次请求。不计费。"}},
	{"insufficient_balance", http.StatusPaymentRequired, false, "Insufficient balance.", l10n{
		"The wallet balance cannot cover the estimated cost that is frozen before the call. Top up, or lower `max_tokens`.",
		"钱包余额不足以覆盖调用前预扣（冻结）的预估费用。请充值，或调小 `max_tokens`。"}},
	{"internal_error", http.StatusInternalServerError, true, "Failed to load model catalog.", l10n{
		"Unexpected gateway error (catalog, database or wallet failure). Safe to retry; report the `request_id` if it persists.",
		"网关内部错误（模型目录、数据库或钱包故障）。可以重试；持续出现请附上 `request_id` 反馈。"}},
	{"invalid_api_key", http.StatusUnauthorized, false, "Invalid API key.", l10n{
		"Missing or malformed `Authorization: Bearer <key>` header, or the key does not exist, has been disabled/revoked, or has expired.",
		"缺少或格式错误的 `Authorization: Bearer <key>` 头，或 Key 不存在、已禁用/吊销、已过期。"}},
	{"invalid_request", http.StatusBadRequest, false, "Request body is not valid JSON.", l10n{
		"The request is invalid: the body cannot be read (e.g. larger than 20 MB) or is not valid JSON, `model` is missing, the `since` query parameter is not RFC 3339, the `/v1/messages` body uses an unsupported feature, or the upstream provider rejected the request as a bad request. Not charged.",
		"请求无效：请求体读取失败（如超过 20 MB）或不是合法 JSON、缺少 `model`、查询参数 `since` 不是 RFC 3339、`/v1/messages` 用了不支持的特性，或上游厂商判定请求参数有误。不计费。"}},
	{"model_not_allowed", http.StatusForbidden, false, "This API key is not allowed to use this model.", l10n{
		"The API key has a model allow-list that does not include the requested model.",
		"该 API Key 配置了可用模型白名单，且不包含所请求的模型。"}},
	{"model_not_found", http.StatusNotFound, false, "The requested model does not exist.", l10n{
		"The model does not exist, is not active, is not visible to your account tier, or (on `/v1/embeddings`) is not an embedding model.",
		"模型不存在、未上线、对你的账户等级不可见，或（在 `/v1/embeddings` 上）不是 embedding 类型的模型。"}},
	{"no_available_channel", http.StatusServiceUnavailable, true, "Upstream request failed.", l10n{
		"No upstream channel can serve the request right now: all channels are unhealthy or cooling down, or none supports the requested capability (streaming, tools, JSON schema) or context length. Not charged.",
		"当前没有可用的上游渠道：所有渠道都不健康或在冷却中，或没有渠道支持所需能力（流式、工具调用、JSON Schema）或上下文长度。不计费。"}},
	{"not_found", http.StatusNotFound, false, "Benchmark not found.", l10n{
		"The requested resource does not exist or is not published (public benchmark endpoints).",
		"请求的资源不存在或未发布（公开基准测试接口）。"}},
	{"not_implemented", http.StatusServiceUnavailable, false, "Not implemented.", l10n{
		"The endpoint exists but is not implemented yet.",
		"该接口已预留路径但尚未实现。"}},
	{"rate_limit_exceeded", http.StatusTooManyRequests, true, "Too many requests.", l10n{
		"Rate limit exceeded: requests-per-minute or tokens-per-minute limit of the API key (relay endpoints), or the per-IP limit of the public endpoints (`/v1/catalog`, `/v1/rankings/*`, `/v1/benchmarks`). Wait for `Retry-After` seconds when present.",
		"触发限流：API Key 的每分钟请求数或每分钟 token 数上限（转发类接口），或公开接口（`/v1/catalog`、`/v1/rankings/*`、`/v1/benchmarks`）的按 IP 限流。有 `Retry-After` 头时按其秒数等待后重试。"}},
	{"service_unavailable", http.StatusServiceUnavailable, true, "Public rankings are temporarily unavailable.", l10n{
		"The public rankings are temporarily switched off by the operator (e.g. while a statistics issue is fixed). Retry later.",
		"公开榜单被运营临时下线（如修正统计口径期间）。请稍后重试。"}},
	{"upstream_error", http.StatusBadGateway, true, "Upstream request failed.", l10n{
		"The upstream provider failed after automatic retries (connection error, 5xx, provider-side rate limit or key problem), or returned a response the gateway could not read. Not charged.",
		"自动重试后上游仍然失败（连接错误、5xx、上游限流或上游 Key 问题），或上游响应无法读取/解析。不计费。"}},
}

func lookupErrorCode(code string) (gatewayErrorCode, bool) {
	for _, c := range gatewayErrorCodes {
		if c.code == code {
			return c, true
		}
	}
	return gatewayErrorCode{}, false
}

// ---------- 反射类型的字段说明 ----------

// gatewayFieldDocs 按 "Schema名.字段名" 给反射生成的 schema 补说明；缺一个
// 字段生成就失败，保证每个属性都有双语说明。
var gatewayFieldDocs = map[string]l10n{
	"CatalogListResponse.object": {"Always `list`.", "固定为 `list`。"},
	"CatalogListResponse.data":   {"Models visible to the `free` tier (`active` and `deprecated`), sorted by `name`.", "对 `free` 等级可见的模型（含 `active` 与 `deprecated`），按 `name` 排序。"},

	"CatalogModel.name":             {"Model ID used in the `model` field of API calls.", "调用接口时 `model` 字段使用的模型 ID。"},
	"CatalogModel.family":           {"Model family, e.g. `deepseek`.", "模型系列，如 `deepseek`。"},
	"CatalogModel.type":             {"Model type: `chat`, `embedding`, `image`, `audio` or `rerank`.", "模型类型：`chat`、`embedding`、`image`、`audio` 或 `rerank`。"},
	"CatalogModel.context_window":   {"Context window in tokens.", "上下文窗口（token 数）。"},
	"CatalogModel.max_output":       {"Maximum output tokens per request.", "单次请求最大输出 token 数。"},
	"CatalogModel.capabilities":     {"Declared capabilities, e.g. `stream`, `tools`, `json_schema`, `vision`.", "声明的能力，如 `stream`、`tools`、`json_schema`、`vision`。"},
	"CatalogModel.sell_price":       {"Public price. Omitted when no price is configured.", "公开售价；未配置售价时省略。"},
	"CatalogModel.display_name":     {"Human-readable name. Omitted if not set.", "展示名称；未录入时省略。"},
	"CatalogModel.description":      {"Model introduction. Omitted if not set.", "模型介绍；未录入时省略。"},
	"CatalogModel.provider_display": {"Display name of the model vendor. Omitted if not set.", "模型厂商展示名；未录入时省略。"},
	"CatalogModel.tags":             {"Display tags. Omitted if not set.", "展示标签；未录入时省略。"},
	"CatalogModel.scores":           {"Free-form benchmark scores (e.g. `intelligenceIndex`, `codingIndex`, `agenticIndex`). Omitted if not set.", "自由格式的评测分数（如 `intelligenceIndex`、`codingIndex`、`agenticIndex`）；未录入时省略。"},
	"CatalogModel.status":           {"`active`, or `deprecated` (listed for reference only; cannot be called).", "`active`，或 `deprecated`（仅供展示，不能调用）。"},

	"CatalogPrice.currency":   {"Price currency, e.g. `CNY`.", "计价币种，如 `CNY`。"},
	"CatalogPrice.components": {"Price components, one per meter.", "价格分项，每个计量项一条。"},

	"CatalogPriceComponent.meter":      {"What is metered: `input`, `input_cache_read`, `input_cache_write`, `output`, `output_reasoning` or `request`.", "计量项：`input`、`input_cache_read`、`input_cache_write`、`output`、`output_reasoning` 或 `request`。"},
	"CatalogPriceComponent.unit":       {"Pricing unit: `per_1m_tokens`, `per_request`, `per_image` or `per_second`.", "计价单位：`per_1m_tokens`、`per_request`、`per_image` 或 `per_second`。"},
	"CatalogPriceComponent.unit_price": {"Price per unit in `currency` (whole currency units, not micro), as a decimal string, e.g. `\"1.5\"` = ¥1.5 per 1M tokens.", "每单位价格，以 `currency` 的元为单位（不是微元），十进制字符串，如 `\"1.5\"` 表示每百万 token ¥1.5。"},

	"ModelListResponse.object": {"Always `list`.", "固定为 `list`。"},
	"ModelListResponse.data":   {"Models callable by this key's account tier, sorted by `id`.", "该 Key 所属账户等级可调用的模型，按 `id` 排序。"},

	"OpenAIModel.id":       {"Model ID to use in the `model` field.", "调用时 `model` 字段使用的模型 ID。"},
	"OpenAIModel.object":   {"Always `model`.", "固定为 `model`。"},
	"OpenAIModel.created":  {"Always `0` (not tracked).", "固定为 `0`（不记录）。"},
	"OpenAIModel.owned_by": {"Always `ufreetokens`.", "固定为 `ufreetokens`。"},

	"UsageResponse.wallet": {"Current wallet balance of the account.", "账户当前钱包余额。"},
	"UsageResponse.usage":  {"Cumulative usage of successful requests (from `since` if given).", "成功请求的累计用量（传了 `since` 时从该时间点起算）。"},

	"UsageWallet.cash_balance_micro":  {"Cash balance in micro-CNY (1,000,000 = ¥1).", "现金余额，微元（1,000,000 = ¥1）。"},
	"UsageWallet.bonus_balance_micro": {"Bonus (gifted credit) balance in micro-CNY.", "赠送余额，微元。"},
	"UsageWallet.frozen_micro":        {"Amount currently frozen for in-flight requests, in micro-CNY.", "进行中请求当前冻结的金额，微元。"},

	"UsageTotals.total_requests":             {"Number of successful requests.", "成功请求数。"},
	"UsageTotals.total_input_tokens":         {"Total input tokens.", "累计输入 token 数。"},
	"UsageTotals.total_output_tokens":        {"Total output tokens.", "累计输出 token 数。"},
	"UsageTotals.total_charged_amount_micro": {"Total amount charged, in micro-CNY.", "累计实扣金额，微元。"},
}

// gatewaySchemaDocs 是反射 schema 本身（而非字段）的说明。
var gatewaySchemaDocs = map[string]l10n{
	"CatalogListResponse":   {"Public model catalog.", "公开模型目录。"},
	"CatalogModel":          {"A model in the public catalog. Internal routing details (channels, upstream accounts, cost prices) are never exposed.", "公开目录中的一个模型。不暴露渠道、上游账号、成本价等内部路由细节。"},
	"CatalogPrice":          {"Public sell price of a model.", "模型的公开售价。"},
	"CatalogPriceComponent": {"One price component.", "一条价格分项。"},
	"ModelListResponse":     {"OpenAI-compatible model list.", "OpenAI 兼容的模型列表。"},
	"OpenAIModel":           {"OpenAI-compatible model object.", "OpenAI 兼容的模型对象。"},
	"UsageResponse":         {"Wallet balance and cumulative usage of the key's account. All amounts are integer micro-CNY.", "Key 所属账户的钱包余额与累计用量。金额均为整数微元。"},
	"UsageWallet":           {"Wallet balance snapshot.", "钱包余额快照。"},
	"UsageTotals":           {"Cumulative usage totals.", "累计用量。"},
}

// ---------- schema 小工具 ----------

func i18nDesc(zh string) map[string]any {
	return map[string]any{"zh": map[string]any{"description": zh}}
}

// described 给 schema 加上英文 description 与 x-i18n.zh.description。
func described(s map[string]any, d l10n) map[string]any {
	s["description"] = d.en
	s["x-i18n"] = i18nDesc(d.zh)
	return s
}

func strProp(d l10n) map[string]any  { return described(map[string]any{"type": "string"}, d) }
func intProp(d l10n) map[string]any  { return described(map[string]any{"type": "integer"}, d) }
func boolProp(d l10n) map[string]any { return described(map[string]any{"type": "boolean"}, d) }

func arrProp(items any, d l10n) map[string]any {
	return described(map[string]any{"type": "array", "items": items}, d)
}

// objSchema 生成对象 schema；required 为 nil 时不输出 required。
func objSchema(d l10n, required []string, additional bool, props map[string]any) map[string]any {
	s := map[string]any{"type": "object", "properties": props, "additionalProperties": additional}
	if len(required) > 0 {
		s["required"] = required
	}
	return described(s, d)
}

func schemaRef(name string) map[string]any {
	return map[string]any{"$ref": "#/components/schemas/" + name}
}

// rawJSON 把手写的 JSON 示例转成 RawMessage（保留书写顺序，MarshalIndent 会统一缩进）。
func rawJSON(s string) (json.RawMessage, error) {
	if !json.Valid([]byte(s)) {
		return nil, fmt.Errorf("invalid JSON example: %s", s)
	}
	return json.RawMessage(s), nil
}

// ---------- 手写的协议 schema ----------

func textBlockSchema() map[string]any {
	return objSchema(l10n{"A text content block.", "文本内容块。"}, []string{"type", "text"}, true, map[string]any{
		"type": described(map[string]any{"type": "string", "const": "text"}, l10n{"Always `text`. Non-text blocks are ignored.", "固定为 `text`；非文本块会被忽略。"}),
		"text": strProp(l10n{"The text.", "文本内容。"}),
	})
}

func anthropicTextContent(d l10n) map[string]any {
	return described(map[string]any{"anyOf": []any{
		map[string]any{"type": "string"},
		map[string]any{"type": "array", "items": textBlockSchema()},
	}}, d)
}

func handwrittenSchemas() map[string]any {
	return map[string]any{
		"ChatCompletionRequest": objSchema(l10n{
			"OpenAI-compatible chat completion request. Only the fields below are read or rewritten by the gateway; all other fields (e.g. `temperature`, `top_p`, `stop`) are passed through to the upstream unchanged.",
			"OpenAI 兼容的对话补全请求。网关只读取或改写下列字段；其余字段（如 `temperature`、`top_p`、`stop`）原样透传给上游。"},
			[]string{"model", "messages"}, true, map[string]any{
				"model": strProp(l10n{"Model ID, e.g. `deepseek-ai/DeepSeek-V4-Flash` (see `GET /v1/models`). Rewritten to the channel's upstream model name before forwarding.",
					"模型 ID，如 `deepseek-ai/DeepSeek-V4-Flash`（见 `GET /v1/models`）。转发前会被改写为渠道的上游模型名。"}),
				"messages": arrProp(objSchema(l10n{"A chat message (passed through unchanged).", "一条对话消息（原样透传）。"}, []string{"role"}, true, map[string]any{
					"role":    strProp(l10n{"`system`, `user`, `assistant` or `tool`.", "`system`、`user`、`assistant` 或 `tool`。"}),
					"content": described(map[string]any{}, l10n{"Message content: a string or an array of content parts.", "消息内容：字符串或内容分段数组。"}),
				}), l10n{"Conversation messages, passed through unchanged. Their size counts toward the input-token estimate used for pre-authorization.",
					"对话消息，原样透传；其大小计入预扣时的输入 token 估算。"}),
				"stream": boolProp(l10n{"If `true`, the response is a `text/event-stream` of `chat.completion.chunk` events. The model's channel must support streaming.",
					"为 `true` 时以 `text/event-stream` 返回 `chat.completion.chunk` 事件流；渠道需支持流式。"}),
				"stream_options": objSchema(l10n{"Streaming options.", "流式选项。"}, nil, true, map[string]any{
					"include_usage": boolProp(l10n{"Set to `true` to receive the final usage-only chunk (empty `choices`). The gateway always requests usage from the upstream for billing, but only forwards this chunk if you set this yourself.",
						"设为 `true` 才会收到最后那个只含 usage 的 chunk（`choices` 为空）。网关为计费总会向上游索取 usage，但只有你自己设置了此项才转发该 chunk。"}),
				}),
				"max_tokens": intProp(l10n{"Maximum output tokens. The pre-authorized amount is based on min(`max_tokens`, `max_completion_tokens`, model max output, 8192).",
					"最大输出 token 数。预扣金额按 min(`max_tokens`、`max_completion_tokens`、模型最大输出、8192) 计算。"}),
				"max_completion_tokens": intProp(l10n{"Alternative to `max_tokens`; the smaller of the two is used for pre-authorization.",
					"`max_tokens` 的替代字段；预扣时取两者中较小值。"}),
				"tools": arrProp(map[string]any{"type": "object"}, l10n{"Tool definitions (passed through). If non-empty, only channels with the `tools` capability are used.",
					"工具定义（原样透传）。非空时只路由到具备 `tools` 能力的渠道。"}),
				"response_format": objSchema(l10n{"Response format (passed through).", "响应格式（原样透传）。"}, nil, true, map[string]any{
					"type": strProp(l10n{"If `json_schema` or `json_object`, only channels with the `json_schema` capability are used.",
						"为 `json_schema` 或 `json_object` 时只路由到具备 `json_schema` 能力的渠道。"}),
				}),
			}),
		"ChatCompletionResponse": objSchema(l10n{
			"OpenAI-compatible chat completion. The upstream response is passed through; only `id` (set to the request ID) and `model` (set to the requested model ID) are rewritten.",
			"OpenAI 兼容的对话补全结果。上游响应原样透传，只改写 `id`（改为请求 ID）和 `model`（改为所请求的模型 ID）。"},
			[]string{"id", "object", "model", "choices"}, true, map[string]any{
				"id":      strProp(l10n{"The request ID (same as the `X-Request-Id` response header).", "请求 ID（与响应头 `X-Request-Id` 相同）。"}),
				"object":  strProp(l10n{"`chat.completion`.", "`chat.completion`。"}),
				"created": intProp(l10n{"Unix timestamp (seconds) from the upstream.", "上游返回的 Unix 时间戳（秒）。"}),
				"model":   strProp(l10n{"The model ID you requested.", "你请求的模型 ID。"}),
				"choices": arrProp(objSchema(l10n{"A completion choice.", "一个候选结果。"}, nil, true, map[string]any{
					"index": intProp(l10n{"Choice index.", "候选序号。"}),
					"message": objSchema(l10n{"The generated message.", "生成的消息。"}, nil, true, map[string]any{
						"role":    strProp(l10n{"`assistant`.", "`assistant`。"}),
						"content": described(map[string]any{"type": []any{"string", "null"}}, l10n{"Generated text.", "生成的文本。"}),
					}),
					"finish_reason": strProp(l10n{"Why generation stopped, e.g. `stop`, `length`, `tool_calls`.", "停止原因，如 `stop`、`length`、`tool_calls`。"}),
				}), l10n{"Completion choices.", "候选结果列表。"}),
				"usage": objSchema(l10n{"Token usage reported by the upstream; billing is based on it.", "上游报告的 token 用量，计费以此为准。"}, nil, true, map[string]any{
					"prompt_tokens":     intProp(l10n{"Input tokens.", "输入 token 数。"}),
					"completion_tokens": intProp(l10n{"Output tokens.", "输出 token 数。"}),
					"total_tokens":      intProp(l10n{"Total tokens.", "总 token 数。"}),
				}),
			}),
		"EmbeddingRequest": objSchema(l10n{
			"OpenAI-compatible embeddings request. Only `model` is read and rewritten by the gateway; all other fields (e.g. `encoding_format`, `dimensions`) are passed through to the upstream unchanged.",
			"OpenAI 兼容的向量嵌入请求。网关只读取并改写 `model`；其余字段（如 `encoding_format`、`dimensions`）原样透传给上游。"},
			[]string{"model", "input"}, true, map[string]any{
				"model": strProp(l10n{"ID of an `embedding`-type model; other model types return `404 model_not_found`.",
					"`embedding` 类型的模型 ID；其他类型的模型返回 `404 model_not_found`。"}),
				"input": described(map[string]any{"anyOf": []any{
					map[string]any{"type": "string"},
					map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				}}, l10n{"Text or array of texts to embed (passed through).", "要向量化的文本或文本数组（原样透传）。"}),
			}),
		"EmbeddingResponse": objSchema(l10n{
			"OpenAI-compatible embeddings result. The upstream response is passed through; only `id` (set to the request ID) and `model` are rewritten.",
			"OpenAI 兼容的向量嵌入结果。上游响应原样透传，只改写 `id`（改为请求 ID）和 `model`。"},
			[]string{"object", "data", "model"}, true, map[string]any{
				"id":     strProp(l10n{"The request ID (added by the gateway).", "请求 ID（网关添加）。"}),
				"object": strProp(l10n{"`list`.", "`list`。"}),
				"data": arrProp(objSchema(l10n{"One embedding.", "一个向量。"}, nil, true, map[string]any{
					"object":    strProp(l10n{"`embedding`.", "`embedding`。"}),
					"index":     intProp(l10n{"Index of the corresponding input.", "对应输入的序号。"}),
					"embedding": arrProp(map[string]any{"type": "number"}, l10n{"The embedding vector.", "向量。"}),
				}), l10n{"Embeddings, one per input.", "向量列表，每个输入一条。"}),
				"model": strProp(l10n{"The model ID you requested.", "你请求的模型 ID。"}),
				"usage": objSchema(l10n{"Token usage; only input tokens are billed.", "token 用量；只按输入 token 计费。"}, nil, true, map[string]any{
					"prompt_tokens": intProp(l10n{"Input tokens.", "输入 token 数。"}),
					"total_tokens":  intProp(l10n{"Total tokens.", "总 token 数。"}),
				}),
			}),
		"MessageRequest": objSchema(l10n{
			"Anthropic Messages API request (text only). The request is translated to an OpenAI chat completion internally: only the fields below (plus `temperature`, `top_p` and `stop_sequences`) are carried over; other fields are ignored.",
			"Anthropic Messages API 请求（仅文本）。网关在内部把它转译为 OpenAI 对话补全：只保留下列字段（以及 `temperature`、`top_p`、`stop_sequences`），其他字段会被忽略。"},
			[]string{"model", "messages"}, true, map[string]any{
				"model": strProp(l10n{"Model ID, e.g. `deepseek-ai/DeepSeek-V4-Flash`. Any chat model works, not only Claude models.",
					"模型 ID，如 `deepseek-ai/DeepSeek-V4-Flash`。任何对话模型都可以，不限于 Claude。"}),
				"max_tokens": intProp(l10n{"Maximum output tokens. Optional for the gateway (unlike Anthropic's API), but recommended: it lowers the pre-authorized amount.",
					"最大输出 token 数。网关不强制（与 Anthropic 官方不同），但建议填写：可降低预扣金额。"}),
				"system": anthropicTextContent(l10n{"System prompt: a string or an array of `text` blocks.", "系统提示词：字符串或 `text` 块数组。"}),
				"messages": arrProp(objSchema(l10n{"A conversation turn.", "一轮对话。"}, []string{"role", "content"}, true, map[string]any{
					"role":    strProp(l10n{"`user` or `assistant`.", "`user` 或 `assistant`。"}),
					"content": anthropicTextContent(l10n{"A string or an array of `text` blocks; non-text blocks are ignored.", "字符串或 `text` 块数组；非文本块会被忽略。"}),
				}), l10n{"Conversation turns.", "对话轮次。"}),
			}),
		"MessageResponse": objSchema(l10n{
			"Anthropic Messages API response with a single text block.",
			"Anthropic Messages API 响应，只含一个文本块。"},
			[]string{"id", "type", "role", "content", "model", "stop_reason", "stop_sequence", "usage"}, false, map[string]any{
				"id":            strProp(l10n{"The request ID (same as the `X-Request-Id` response header).", "请求 ID（与响应头 `X-Request-Id` 相同）。"}),
				"type":          strProp(l10n{"Always `message`.", "固定为 `message`。"}),
				"role":          strProp(l10n{"Always `assistant`.", "固定为 `assistant`。"}),
				"content":       arrProp(textBlockSchema(), l10n{"Exactly one `text` block with the generated text.", "恰好一个 `text` 块，内容为生成的文本。"}),
				"model":         strProp(l10n{"The model ID you requested.", "你请求的模型 ID。"}),
				"stop_reason":   strProp(l10n{"`end_turn` or `max_tokens`.", "`end_turn` 或 `max_tokens`。"}),
				"stop_sequence": described(map[string]any{"type": "null"}, l10n{"Always `null`.", "固定为 `null`。"}),
				"usage": objSchema(l10n{"Token usage.", "token 用量。"}, []string{"input_tokens", "output_tokens"}, false, map[string]any{
					"input_tokens":  intProp(l10n{"Input tokens.", "输入 token 数。"}),
					"output_tokens": intProp(l10n{"Output tokens.", "输出 token 数。"}),
				}),
			}),
	}
}

// ---------- 操作定义 ----------

type gatewayOperation struct {
	method, path, opID, tag string
	summary, desc           l10n
	auth                    string // "none" | "api_key"
	billable                bool
	rateLimit               *l10n
	notImplemented          bool
	params                  []any
	reqSchema               any
	reqExample              string
	respSchema              any
	respExample             any               // Go 值（反射类型，按真实编码输出）或 rawJSON
	respHeaders             map[string]any    // 200 响应额外的头
	streamExample           string            // 非空时 200 额外给出 text/event-stream
	errors                  []string          // 可能返回的错误码
	errMessages             map[string]string // 错误示例里的 message（按错误码覆盖登记表里的默认值）
	extraStreamDesc         *l10n             // text/event-stream 内容的说明
}

var (
	relayErrors = []string{"invalid_request", "content_filtered", "invalid_api_key", "insufficient_balance",
		"account_suspended", "model_not_allowed", "model_not_found", "rate_limit_exceeded",
		"concurrency_limit_exceeded", "internal_error", "upstream_error", "no_available_channel"}
	relayRateLimit = l10n{
		"Per API key, when configured on the key: requests per minute (RPM), tokens per minute (TPM; counts the estimated input tokens plus the reserved output tokens) and concurrent requests. Unset limits are unlimited.",
		"按 API Key 生效（在 Key 上配置时）：每分钟请求数（RPM）、每分钟 token 数（TPM，按预估输入 token 加预留输出 token 计）、并发请求数。未配置即不限制。"}
)

const chatDescEN = "OpenAI-compatible chat completion. The gateway checks `model`, the key's model allow-list, rate limits and balance, then forwards the body to an upstream channel. Only `model` is rewritten (to the channel's upstream model name); all other fields are passed through. In the response, `model` is the model ID you requested and `id` is the request ID (same as `X-Request-Id`).\n\n" +
	"**Billing.** Before forwarding, an estimated cost is frozen in your wallet: input tokens are estimated from the body size (about 4 bytes per token), output tokens = min(`max_tokens`/`max_completion_tokens`, model max output, 8192). After the call the real cost is settled from the upstream `usage` and the rest is released. If the upstream returns no usage, a non-streaming call is billed at the estimate. Failed calls are not charged.\n\n" +
	"**Failover.** If an upstream call fails before anything is sent to you, the gateway retries on another key or channel (up to 3 attempts within 90 s). Once a stream has started there is no retry.\n\n" +
	"**Streaming.** With `stream: true` the response is `text/event-stream` carrying `chat.completion.chunk` events (`data: {...}`). The gateway always asks the upstream for usage (for billing), but forwards the usage-only chunk (empty `choices`) only if you set `stream_options.include_usage: true` yourself. The upstream `data: [DONE]` line is not forwarded: the stream ends when the connection closes. If you disconnect mid-stream before usage arrives, output is billed by estimating tokens from the bytes already forwarded to you (never more than the frozen estimate).\n\n" +
	"**Routing.** Requests with `stream: true`, non-empty `tools`, or `response_format.type` of `json_schema`/`json_object` only go to channels with that capability, and the estimated input plus reserved output must fit the channel's context window; otherwise `503 no_available_channel`."

const chatDescZH = "OpenAI 兼容的对话补全。网关校验 `model`、Key 的模型白名单、限流与余额后，把请求体转发给上游渠道。只改写 `model`（改为渠道的上游模型名），其余字段原样透传。响应中 `model` 是你请求的模型 ID，`id` 是请求 ID（与 `X-Request-Id` 相同）。\n\n" +
	"**计费。** 转发前先在钱包里冻结一笔预估费用：输入 token 按请求体大小估算（约 4 字节 1 个 token），输出 token = min(`max_tokens`/`max_completion_tokens`、模型最大输出、8192)。调用结束后按上游返回的 `usage` 结算实际费用并释放剩余冻结。上游没有返回 usage 时，非流式请求按预估值计费。失败的请求不计费。\n\n" +
	"**故障转移。** 在向你写出任何内容之前上游失败时，网关会换 Key 或换渠道重试（90 秒内最多 3 次）。流式输出一旦开始就不再重试。\n\n" +
	"**流式。** `stream: true` 时以 `text/event-stream` 返回 `chat.completion.chunk` 事件（`data: {...}`）。网关为计费总会向上游索取 usage，但只有你自己设置了 `stream_options.include_usage: true`，才会把那个只含 usage 的 chunk（`choices` 为空）转发给你。上游的 `data: [DONE]` 不会转发：连接关闭即表示流结束。若你在收到 usage 之前中途断开，输出按已转发给你的字节数估算计费（不超过冻结的预估值）。\n\n" +
	"**路由。** `stream: true`、非空 `tools`、或 `response_format.type` 为 `json_schema`/`json_object` 的请求只会路由到具备相应能力的渠道，且预估输入加预留输出不能超过渠道的上下文窗口；否则返回 `503 no_available_channel`。"

func gatewayOperations() ([]gatewayOperation, error) {
	catalogExample := catalogListResponse{Object: "list", Data: []catalogModel{
		{
			Name: "BAAI/bge-m3", Family: "bge", Type: "embedding", ContextWindow: 8192, MaxOutput: 0,
			Capabilities: []string{},
			SellPrice: &catalogPrice{Currency: "CNY", Components: []catalogPriceComponent{
				{Meter: "input", Unit: "per_1m_tokens", UnitPrice: "0.5"},
			}},
			DisplayName: "BGE M3", ProviderDisplay: "BAAI", Tags: []string{"embedding", "multilingual"},
			Status: "active",
		},
		{
			Name: "deepseek-ai/DeepSeek-V4-Flash", Family: "deepseek", Type: "chat", ContextWindow: 128000, MaxOutput: 8192,
			Capabilities: []string{"stream", "tools", "json_schema"},
			SellPrice: &catalogPrice{Currency: "CNY", Components: []catalogPriceComponent{
				{Meter: "input", Unit: "per_1m_tokens", UnitPrice: "1.5"},
				{Meter: "output", Unit: "per_1m_tokens", UnitPrice: "3"},
			}},
			DisplayName: "DeepSeek V4 Flash", Description: "Fast general-purpose chat model.", ProviderDisplay: "DeepSeek",
			Tags:   []string{"reasoning", "coding"},
			Scores: map[string]any{"agenticIndex": 68, "codingIndex": 82, "intelligenceIndex": 39.5},
			Status: "active",
		},
	}}
	modelsExample := modelListResponse{Object: "list", Data: []openAIModel{
		{ID: "BAAI/bge-m3", Object: "model", OwnedBy: "ufreetokens"},
		{ID: "deepseek-ai/DeepSeek-V4-Flash", Object: "model", OwnedBy: "ufreetokens"},
	}}
	usageExample := usageResponse{
		Wallet: usageWallet{CashBalanceMicro: 5_000_000, BonusBalanceMicro: 1_000_000, FrozenMicro: 0},
		Usage:  usageTotals{TotalRequests: 12, TotalInputTokens: 3400, TotalOutputTokens: 1800, TotalChargedAmountMicro: 10_500},
	}
	chatResp, err := rawJSON(`{"id":"` + exampleRequestID + `","object":"chat.completion","created":1759305600,"model":"deepseek-ai/DeepSeek-V4-Flash","choices":[{"index":0,"message":{"role":"assistant","content":"你好！有什么可以帮你的？"},"finish_reason":"stop"}],"usage":{"prompt_tokens":18,"completion_tokens":9,"total_tokens":27}}`)
	if err != nil {
		return nil, err
	}
	embResp, err := rawJSON(`{"id":"` + exampleRequestID + `","object":"list","data":[{"object":"embedding","index":0,"embedding":[0.0123,-0.0456,0.0789]}],"model":"BAAI/bge-m3","usage":{"prompt_tokens":6,"total_tokens":6}}`)
	if err != nil {
		return nil, err
	}
	msgResp, err := rawJSON(`{"id":"` + exampleRequestID + `","type":"message","role":"assistant","content":[{"type":"text","text":"你好！有什么可以帮你的？"}],"model":"deepseek-ai/DeepSeek-V4-Flash","stop_reason":"end_turn","stop_sequence":null,"usage":{"input_tokens":18,"output_tokens":9}}`)
	if err != nil {
		return nil, err
	}
	streamExample := `data: {"id":"` + exampleRequestID + `","object":"chat.completion.chunk","created":1759305600,"model":"deepseek-ai/DeepSeek-V4-Flash","choices":[{"index":0,"delta":{"role":"assistant","content":"你好"},"finish_reason":null}]}` + "\n\n" +
		`data: {"id":"` + exampleRequestID + `","object":"chat.completion.chunk","created":1759305600,"model":"deepseek-ai/DeepSeek-V4-Flash","choices":[{"index":0,"delta":{"content":"！"},"finish_reason":"stop"}]}` + "\n\n" +
		`data: {"id":"` + exampleRequestID + `","object":"chat.completion.chunk","created":1759305600,"model":"deepseek-ai/DeepSeek-V4-Flash","choices":[],"usage":{"prompt_tokens":18,"completion_tokens":2,"total_tokens":20}}` + "\n\n"

	notImpl := func(method, path, opID, endpoint string, summary l10n) gatewayOperation {
		return gatewayOperation{errMessages: map[string]string{
			// 与 notImplementedHandler 的 message 一致
			"not_implemented": "The " + endpoint + " relay pipeline is not yet implemented (Phase1 in progress). See docs roadmap.",
		}, method: method, path: path, opID: opID, tag: "Not implemented", auth: "api_key", notImplemented: true,
			summary: summary,
			desc: l10n{"Reserved path that is not implemented yet. After API key authentication it always returns `503 not_implemented`.",
				"预留路径，尚未实现。通过 API Key 鉴权后固定返回 `503 not_implemented`。"},
			errors: []string{"not_implemented"}}
	}

	ops := []gatewayOperation{
		{
			method: http.MethodGet, path: "/v1/catalog", opID: "getCatalog", tag: "Catalog", auth: "none",
			summary: l10n{"Get the public model catalog", "获取公开模型目录"},
			desc: l10n{
				"Public model catalog; no API key required. Returns every model visible to the `free` tier, including `deprecated` ones (listed for reference only and cannot be called; filter by `status`), sorted by `name`, with the public sell price and display metadata. Responses carry `Cache-Control: public, max-age=60`.\n\nTo see which models *your key* can call, use `GET /v1/models`.",
				"公开模型目录，无需 API Key。返回对 `free` 等级可见的全部模型，包括 `deprecated` 模型（仅供展示、不能调用，可按 `status` 过滤），按 `name` 排序，附带公开售价与展示信息。响应带 `Cache-Control: public, max-age=60`。\n\n要查看*你的 Key* 能调用哪些模型，请用 `GET /v1/models`。"},
			rateLimit:  &l10n{"60 requests per minute per client IP.", "每个客户端 IP 每分钟 60 次。"},
			respSchema: catalogListResponse{}, respExample: catalogExample,
			respHeaders: map[string]any{"Cache-Control": described(map[string]any{"schema": map[string]any{"type": "string"}, "example": "public, max-age=60"},
				l10n{"`public, max-age=60`.", "`public, max-age=60`。"})},
			errors:      []string{"rate_limit_exceeded", "internal_error"},
			errMessages: map[string]string{"internal_error": "Failed to load model catalog."},
		},
		{
			method: http.MethodGet, path: "/v1/models", opID: "listModels", tag: "Models", auth: "api_key",
			summary: l10n{"List models", "列出模型"},
			desc: l10n{
				"OpenAI-compatible model list: the active models visible to the account tier of this API key, sorted by `id`. No prices or context lengths (use `GET /v1/catalog`). The key's own model allow-list is not applied here.",
				"OpenAI 兼容的模型列表：该 API Key 所属账户等级可见的、已上线的模型，按 `id` 排序。不含价格与上下文长度（请用 `GET /v1/catalog`）。这里不会按 Key 自身的模型白名单过滤。"},
			respSchema: modelListResponse{}, respExample: modelsExample,
			errors:      []string{"invalid_api_key", "account_suspended", "internal_error"},
			errMessages: map[string]string{"internal_error": "Failed to list models."},
		},
		{
			method: http.MethodGet, path: "/v1/usage", opID: "getUsage", tag: "Usage", auth: "api_key",
			summary: l10n{"Get balance and usage", "查询余额与用量"},
			desc: l10n{
				"Wallet balance and cumulative usage of the account that owns this API key (account-wide, not per key). Amounts are integer micro-CNY (1,000,000 = ¥1). Usage counts successful requests only; pass `since` to count from a point in time. The wallet is always the current balance.",
				"该 API Key 所属账户的钱包余额与累计用量（按账户统计，不区分 Key）。金额为整数微元（1,000,000 = ¥1）。用量只统计成功的请求；传 `since` 可从某个时间点起算。钱包始终是当前余额。"},
			params: []any{described(map[string]any{"name": "since", "in": "query", "required": false,
				"schema": map[string]any{"type": "string", "format": "date-time"}, "example": "2026-09-01T00:00:00Z"},
				l10n{"Only count requests created at or after this time (RFC 3339). Omit for all history.", "只统计该时间（RFC 3339）及之后创建的请求；不传则统计全部历史。"})},
			respSchema: usageResponse{}, respExample: usageExample,
			errors:      []string{"invalid_request", "invalid_api_key", "account_suspended", "internal_error"},
			errMessages: map[string]string{"invalid_request": "'since' must be RFC3339.", "internal_error": "Failed to load wallet."},
		},
		{
			method: http.MethodPost, path: "/v1/chat/completions", opID: "createChatCompletion", tag: "Chat", auth: "api_key", billable: true,
			summary:    l10n{"Create a chat completion", "创建对话补全"},
			desc:       l10n{chatDescEN, chatDescZH},
			rateLimit:  &relayRateLimit,
			reqSchema:  schemaRef("ChatCompletionRequest"),
			reqExample: `{"model":"deepseek-ai/DeepSeek-V4-Flash","messages":[{"role":"system","content":"You are a helpful assistant."},{"role":"user","content":"你好"}],"max_tokens":1024}`,
			respSchema: schemaRef("ChatCompletionResponse"), respExample: chatResp,
			streamExample: streamExample,
			extraStreamDesc: &l10n{"Returned when `stream: true`. Each event is `data: <chat.completion.chunk JSON>` followed by a blank line; the usage-only chunk (last event above) is sent only if `stream_options.include_usage` is `true`. No `data: [DONE]` line.",
				"`stream: true` 时返回。每个事件是 `data: <chat.completion.chunk JSON>` 加一个空行；只有 `stream_options.include_usage` 为 `true` 时才会发送只含 usage 的 chunk（上例最后一条）。不发送 `data: [DONE]`。"},
			errors: relayErrors,
		},
		{
			method: http.MethodPost, path: "/v1/embeddings", opID: "createEmbedding", tag: "Embeddings", auth: "api_key", billable: true,
			summary: l10n{"Create embeddings", "创建向量嵌入"},
			desc: l10n{
				"OpenAI-compatible embeddings. Uses the same authentication, rate limiting, pre-authorization, failover and settlement as `/v1/chat/completions`, without streaming. `model` must be an `embedding`-type model. Only `model` is rewritten before forwarding; in the response `model` is the model ID you requested and `id` is the request ID. Only input tokens are billed (TPM also counts input tokens only).",
				"OpenAI 兼容的向量嵌入。鉴权、限流、预扣、故障转移和结算与 `/v1/chat/completions` 相同，不支持流式。`model` 必须是 `embedding` 类型的模型。转发前只改写 `model`；响应中 `model` 是你请求的模型 ID，`id` 是请求 ID。只按输入 token 计费（TPM 也只计输入 token）。"},
			rateLimit:  &relayRateLimit,
			reqSchema:  schemaRef("EmbeddingRequest"),
			reqExample: `{"model":"BAAI/bge-m3","input":"uFreeTokens 是一个大模型 API 聚合平台"}`,
			respSchema: schemaRef("EmbeddingResponse"), respExample: embResp,
			errors: relayErrors,
		},
		{
			method: http.MethodPost, path: "/v1/messages", opID: "createMessage", tag: "Anthropic", auth: "api_key", billable: true,
			summary: l10n{"Create a message (Anthropic-compatible)", "创建消息（Anthropic 兼容）"},
			desc: l10n{
				"Anthropic Messages API–compatible entry point. The request is translated to an OpenAI chat completion internally and goes through exactly the same authentication, rate limiting, routing, failover and billing as `/v1/chat/completions`; any chat model can be used.\n\n" +
					"- Authenticate with `Authorization: Bearer <key>`; the `x-api-key` header is not accepted.\n" +
					"- Text only: `system` and each message's `content` may be a string or an array of `text` blocks; other block types are ignored.\n" +
					"- The response is a `message` with a single `text` block; `id` is the request ID.\n" +
					"- Errors use the gateway's OpenAI-style format (see `Error`), not Anthropic's `{\"type\": \"error\"}` format.",
				"兼容 Anthropic Messages API 的入口。请求在内部转译为 OpenAI 对话补全，鉴权、限流、路由、故障转移和计费与 `/v1/chat/completions` 完全一致；可使用任何对话模型。\n\n" +
					"- 用 `Authorization: Bearer <key>` 鉴权，不支持 `x-api-key` 头。\n" +
					"- 仅支持文本：`system` 与每条消息的 `content` 可以是字符串或 `text` 块数组，其他类型的块会被忽略。\n" +
					"- 响应是只含一个 `text` 块的 `message`，`id` 为请求 ID。\n" +
					"- 错误沿用网关的 OpenAI 风格格式（见 `Error`），不是 Anthropic 的 `{\"type\": \"error\"}` 格式。"},
			rateLimit:  &relayRateLimit,
			reqSchema:  schemaRef("MessageRequest"),
			reqExample: `{"model":"deepseek-ai/DeepSeek-V4-Flash","max_tokens":1024,"system":"You are a helpful assistant.","messages":[{"role":"user","content":"你好"}]}`,
			respSchema: schemaRef("MessageResponse"), respExample: msgResp,
			errors: relayErrors,
		},
		notImpl(http.MethodPost, "/v1/completions", "createCompletion", "completions", l10n{"Create a legacy completion (not implemented)", "旧式文本补全（未实现）"}),
		notImpl(http.MethodPost, "/v1/images/generations", "createImage", "images.generations", l10n{"Generate images (not implemented)", "图像生成（未实现）"}),
		notImpl(http.MethodPost, "/v1/audio/transcriptions", "createTranscription", "audio.transcriptions", l10n{"Transcribe audio (not implemented)", "语音转写（未实现）"}),
		notImpl(http.MethodPost, "/v1/audio/speech", "createSpeech", "audio.speech", l10n{"Generate speech (not implemented)", "语音合成（未实现）"}),
	}
	pub, err := publicOperations()
	if err != nil {
		return nil, err
	}
	return append(ops, pub...), nil
}

// ---------- 错误示例 ----------

// bufferWriter 是一个最小的 http.ResponseWriter，用来捕获 httpx.WriteError 的真实输出。
type bufferWriter struct {
	header http.Header
	body   bytes.Buffer
}

func (b *bufferWriter) Header() http.Header         { return b.header }
func (b *bufferWriter) Write(p []byte) (int, error) { return b.body.Write(p) }
func (b *bufferWriter) WriteHeader(int)             {}

// errorExample 让 httpx.WriteError 真正写一次错误，示例与线上格式（含 type 字段的取值）保持一致。
func errorExample(c gatewayErrorCode) (json.RawMessage, error) {
	req, err := http.NewRequest(http.MethodGet, "/", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Request-Id", exampleRequestID)
	w := &bufferWriter{header: http.Header{}}
	httpx.RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		httpx.WriteError(w, r, c.status, c.code, c.message)
	})).ServeHTTP(w, req)
	return rawJSON(strings.TrimSpace(w.body.String()))
}

var statusTextZH = map[int]string{
	http.StatusOK:                  "成功",
	http.StatusBadRequest:          "请求无效",
	http.StatusUnauthorized:        "未认证",
	http.StatusPaymentRequired:     "余额不足",
	http.StatusForbidden:           "无权限",
	http.StatusNotFound:            "未找到",
	http.StatusTooManyRequests:     "请求过多",
	http.StatusInternalServerError: "服务器内部错误",
	http.StatusBadGateway:          "上游错误",
	http.StatusServiceUnavailable:  "服务不可用",
}

func responseDesc(status int) (string, map[string]any, error) {
	zh, ok := statusTextZH[status]
	if !ok {
		return "", nil, fmt.Errorf("no zh text for HTTP status %d", status)
	}
	return http.StatusText(status), i18nDesc(zh), nil
}

// ---------- 组装 ----------

// GatewayOpenAPI 生成网关数据面的 OpenAPI 3.1 文档（JSON，2 空格缩进，键有序、输出稳定）。
func GatewayOpenAPI() ([]byte, error) {
	doc, err := buildGatewayOpenAPI()
	if err != nil {
		return nil, err
	}
	// 不转义 <、>、&：描述里的 Markdown（如 `Bearer <key>`）原样可读。
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(doc); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

func buildGatewayOpenAPI() (map[string]any, error) {
	g := &schemaGen{defs: map[string]any{}, types: map[string]reflect.Type{}}
	ops, err := gatewayOperations()
	if err != nil {
		return nil, err
	}
	reqIDHeader := map[string]any{"$ref": "#/components/headers/X-Request-Id"}

	paths := map[string]map[string]any{}
	for _, o := range ops {
		op := map[string]any{
			"operationId": o.opID,
			"tags":        []string{o.tag},
			"summary":     o.summary.en,
			"description": o.desc.en,
			"x-i18n":      map[string]any{"zh": map[string]any{"summary": o.summary.zh, "description": o.desc.zh}},
			"x-auth":      o.auth,
		}
		if o.auth == "none" {
			op["security"] = []any{}
		} else {
			op["security"] = []any{map[string]any{"apiKey": []any{}}}
		}
		responses := map[string]any{}
		if o.notImplemented {
			op["x-status"] = "not_implemented"
		} else {
			op["x-billable"] = o.billable
			if o.rateLimit != nil {
				op["x-rate-limit"] = map[string]any{"en": o.rateLimit.en, "zh": o.rateLimit.zh}
			}
			if len(o.params) > 0 {
				op["parameters"] = o.params
			}
			if o.reqSchema != nil {
				ex, err := rawJSON(o.reqExample)
				if err != nil {
					return nil, fmt.Errorf("%s request example: %w", o.opID, err)
				}
				op["requestBody"] = map[string]any{"required": true, "content": map[string]any{
					"application/json": map[string]any{"schema": o.reqSchema, "example": ex}}}
			}
			respSchema := o.respSchema
			if _, isMap := respSchema.(map[string]any); !isMap {
				respSchema = g.schema(reflect.TypeOf(respSchema))
			}
			content := map[string]any{"application/json": map[string]any{"schema": respSchema, "example": o.respExample}}
			if o.streamExample != "" {
				content["text/event-stream"] = map[string]any{
					"schema":  described(map[string]any{"type": "string"}, *o.extraStreamDesc),
					"example": o.streamExample,
				}
			}
			headers := map[string]any{"X-Request-Id": reqIDHeader}
			for k, v := range o.respHeaders {
				headers[k] = v
			}
			desc, zh, err := responseDesc(http.StatusOK)
			if err != nil {
				return nil, err
			}
			responses["200"] = map[string]any{"description": desc, "x-i18n": zh, "headers": headers, "content": content}
		}

		// 错误响应：按状态码分组，每组列出可能的错误码，示例取第一个错误码。
		byStatus := map[int][]string{}
		for _, code := range o.errors {
			c, ok := lookupErrorCode(code)
			if !ok {
				return nil, fmt.Errorf("%s: error code %q is not registered", o.opID, code)
			}
			byStatus[c.status] = append(byStatus[c.status], code)
		}
		for status, codes := range byStatus {
			// 示例取 o.errors 里先列出的错误码（更常见的放前面），列表本身按字母排序。
			first, _ := lookupErrorCode(codes[0])
			sort.Strings(codes)
			if msg, ok := o.errMessages[first.code]; ok {
				first.message = msg
			}
			ex, err := errorExample(first)
			if err != nil {
				return nil, err
			}
			headers := map[string]any{"X-Request-Id": reqIDHeader}
			if status == http.StatusTooManyRequests {
				headers["Retry-After"] = map[string]any{"$ref": "#/components/headers/Retry-After"}
			}
			desc, zh, err := responseDesc(status)
			if err != nil {
				return nil, err
			}
			responses[fmt.Sprint(status)] = map[string]any{
				"description": desc, "x-i18n": zh, "headers": headers, "x-error-codes": codes,
				"content": map[string]any{"application/json": map[string]any{"schema": schemaRef("Error"), "example": ex}},
			}
		}
		op["responses"] = responses

		if paths[o.path] == nil {
			paths[o.path] = map[string]any{}
		}
		paths[o.path][strings.ToLower(o.method)] = op
	}

	// 反射生成的 schema 补上双语说明；缺说明直接报错。
	for name, def := range g.defs {
		m, _ := def.(map[string]any)
		d, ok := gatewaySchemaDocs[name]
		if !ok || m == nil {
			return nil, fmt.Errorf("schema %s has no entry in gatewaySchemaDocs", name)
		}
		described(m, d)
		props, _ := m["properties"].(map[string]any)
		for p, ps := range props {
			fd, ok := gatewayFieldDocs[name+"."+p]
			if !ok {
				return nil, fmt.Errorf("field %s.%s has no entry in gatewayFieldDocs", name, p)
			}
			described(ps.(map[string]any), fd)
		}
	}
	for name, s := range handwrittenSchemas() {
		g.defs[name] = s
	}
	g.defs["Error"] = objSchema(l10n{
		"Error response (OpenAI-compatible). Branch on `error.code`, not on `message`.",
		"错误响应（OpenAI 兼容）。程序应按 `error.code` 判断，不要依赖 `message`。"},
		[]string{"error"}, false, map[string]any{
			"error": objSchema(l10n{"Error details.", "错误详情。"}, []string{"message", "type", "code"}, false, map[string]any{
				"message": strProp(l10n{"Human-readable English message; may change.", "给人看的英文说明，可能变化。"}),
				"type": strProp(l10n{"Error category derived from the HTTP status: `invalid_request_error` (400/404), `authentication_error` (401), `insufficient_quota` (402), `permission_error` (403), `rate_limit_error` (429), `api_error` (5xx).",
					"由 HTTP 状态决定的错误类别：`invalid_request_error`（400/404）、`authentication_error`（401）、`insufficient_quota`（402）、`permission_error`（403）、`rate_limit_error`（429）、`api_error`（5xx）。"}),
				"code":       strProp(l10n{"Stable machine-readable error code; see `x-error-codes`.", "稳定的机器可读错误码，见 `x-error-codes`。"}),
				"request_id": strProp(l10n{"Request ID, same as the `X-Request-Id` response header.", "请求 ID，与响应头 `X-Request-Id` 相同。"}),
			}),
		})

	type errCodeDoc struct {
		Code        string            `json:"code"`
		Status      int               `json:"status"`
		Retryable   bool              `json:"retryable"`
		Description map[string]string `json:"description"`
	}
	codes := make([]errCodeDoc, 0, len(gatewayErrorCodes))
	for _, c := range gatewayErrorCodes {
		codes = append(codes, errCodeDoc{c.code, c.status, c.retryable, map[string]string{"en": c.desc.en, "zh": c.desc.zh}})
	}
	sort.Slice(codes, func(i, j int) bool { return codes[i].Code < codes[j].Code })

	tagDocs := []struct {
		name string
		d    l10n
	}{
		{"Catalog", l10n{"Public model catalog.", "公开模型目录。"}},
		{"Models", l10n{"Models callable with your API key.", "API Key 可调用的模型。"}},
		{"Usage", l10n{"Balance and usage of your account.", "账户余额与用量。"}},
		{"Chat", l10n{"OpenAI-compatible chat completions.", "OpenAI 兼容的对话补全。"}},
		{"Embeddings", l10n{"OpenAI-compatible embeddings.", "OpenAI 兼容的向量嵌入。"}},
		{"Anthropic", l10n{"Anthropic Messages API compatibility.", "Anthropic Messages API 兼容接口。"}},
		publicTagDocs[0], publicTagDocs[1],
		{"Not implemented", l10n{"Reserved endpoints that return `503 not_implemented`.", "预留接口，返回 `503 not_implemented`。"}},
	}
	tags := make([]any, 0, len(tagDocs))
	for _, t := range tagDocs {
		tags = append(tags, map[string]any{"name": t.name, "description": t.d.en, "x-i18n": i18nDesc(t.d.zh)})
	}

	info := l10n{
		"Public data-plane API of uFreeTokens (`cmd/gateway`), compatible with the OpenAI API (and the Anthropic Messages API on `/v1/messages`). Authenticate with `Authorization: Bearer sk-uft-...`. All money amounts are integer micro-CNY (1,000,000 = ¥1). Every response carries `X-Request-Id`. Generated by `internal/app.GatewayOpenAPI`; do not edit by hand.",
		"uFreeTokens 公开数据面接口（`cmd/gateway`），兼容 OpenAI API（`/v1/messages` 兼容 Anthropic Messages API）。用 `Authorization: Bearer sk-uft-...` 鉴权。所有金额均为整数微元（1,000,000 = ¥1）。每个响应都带 `X-Request-Id`。由 `internal/app.GatewayOpenAPI` 生成，勿手改。"}

	return map[string]any{
		"openapi": "3.1.0",
		"info": map[string]any{"title": "uFreeTokens Gateway API", "version": "v1",
			"description": info.en, "x-i18n": i18nDesc(info.zh)},
		"servers": []any{map[string]any{"url": "https://ufreetokens.com"}},
		"tags":    tags,
		"paths":   paths,
		"components": map[string]any{
			"schemas": g.defs,
			"securitySchemes": map[string]any{"apiKey": described(map[string]any{"type": "http", "scheme": "bearer"},
				l10n{"API key: `Authorization: Bearer sk-uft-...`.", "API Key：`Authorization: Bearer sk-uft-...`。"})},
			"headers": map[string]any{
				"X-Request-Id": described(map[string]any{"schema": map[string]any{"type": "string"}, "example": exampleRequestID},
					l10n{"Request ID. Echoes the client's `X-Request-Id` if it is 1-64 characters of `[A-Za-z0-9_-]`; otherwise a new ULID is generated. Include it when reporting problems.",
						"请求 ID。客户端传入的 `X-Request-Id` 若为 1-64 位 `[A-Za-z0-9_-]` 则原样返回，否则由网关生成一个 ULID。反馈问题时请附上。"}),
				"Retry-After": described(map[string]any{"schema": map[string]any{"type": "integer"}, "example": 2},
					l10n{"Seconds to wait before retrying; set when the limiter knows the wait time.", "重试前应等待的秒数；限流器能算出等待时长时才会设置。"}),
			},
			"x-error-codes": codes,
		},
	}, nil
}
