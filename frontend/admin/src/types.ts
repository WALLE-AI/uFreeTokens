// 通用类型。字段一律 snake_case，对齐后端 cmd/admin 的 JSON（接口方案 §0.1）；
// 金额字段带 _micro 后缀，int64 微元（1,000,000 = 1 元）；单价/比率是十进制
// 字符串（§0.4）。每个类型都对应 internal/admin 或 internal/app 里的一个 Go
// 结构体，注释里标了出处，改字段时两边一起改。不要用 any。

export type Micro = number;
export type DecimalString = string;
export type ISODateTime = string;

// 页码分页（配置类列表，接口方案 §0.2）——admin.Page[T]
export interface Paginated<T> {
  data: T[];
  total: number;
  page: number;
  page_size: number;
}

// 游标分页（调用日志、流水、审计日志）；没有下一页时 next_cursor 为 ""
export interface Cursor<T> {
  data: T[];
  next_cursor: string;
}

export interface ListData<T> {
  data: T[];
}

// ---------- 审计 / 待办 ----------

export interface AuditLogEntry {
  id: number;
  actor_id: number;
  actor_name: string;
  action: string;
  target_type: string;
  target_id: string;
  before: unknown;
  after: unknown;
  ip: string;
  created_at: ISODateTime;
  // 经由运营智能体执行（审批后以审批人身份调用）时关联的会话与工具调用
  agent_session_id?: number | null;
  agent_tool_call_id?: string | null;
}

// admin.TodoCounts
export interface TodoCounts {
  price_changes_pending: number;
  price_changes_blocked: number;
  listings_pending: number;
  channels_negative_margin: number;
  channels_missing_cost: number;
  models_missing_sell_price: number;
  // 外部数据采集：新发现的优惠情报、待确认的榜单模型映射、连续失败的数据源
  offers_new: number;
  aliases_suggested: number;
  data_sources_failing: number;
  // 当前管理员有权限处理的智能体待审提案数（按请求计算）
  agent_pending_approvals?: number;
}

export type AdminEnv = 'production' | 'staging' | 'dev';

// ---------- 价格 ----------

export type Meter =
  | 'input'
  | 'input_cache_read'
  | 'input_cache_write'
  | 'output'
  | 'output_reasoning'
  | 'request'
  | 'image'
  | 'input_char'
  | 'audio_second';
export type PriceUnit = 'per_1m_tokens' | 'per_request' | 'per_image' | 'per_second' | 'per_1m_chars';

// admin.PriceComponentInput（请求与响应同形）
export interface PriceComponent {
  meter: Meter;
  unit: PriceUnit;
  service_tier: string;
  tier_min_input: number;
  tier_max_input: number | null;
  window_start_min: number | null;
  window_end_min: number | null;
  unit_price: DecimalString;
}

// 写入价格时可省略的字段由后端补默认值
export type PriceComponentInput = Pick<PriceComponent, 'meter' | 'unit' | 'unit_price'> & Partial<PriceComponent>;

// admin.PriceBrief：只有基础 input/output 单价的摘要
export interface PriceBrief {
  price_book_id: number;
  currency: string;
  input: DecimalString | null;
  output: DecimalString | null;
  effective_from: ISODateTime;
}

// admin.CostCNY：折算成人民币（含汇率与 cost_multiplier）后的成本单价
export interface CostCNY {
  input: DecimalString | null;
  output: DecimalString | null;
  fx_rate: DecimalString;
  fx_date: ISODateTime | null;
  fx_missing: boolean;
}

// admin.PriceBookInfo
export interface PriceBook {
  id: number;
  kind: 'sell' | 'cost';
  tier: string | null; // 运行时不参与计价，仅作信息展示（接口方案 §1.3）
  currency: string;
  effective_from: ISODateTime;
  effective_to: ISODateTime | null;
  created_by: number | null;
  note: string | null;
  created_at: ISODateTime;
  is_current: boolean;
  components: PriceComponent[];
}

// admin.FXRateInfo
export interface FXRate {
  base: string;
  quote: string;
  rate: DecimalString;
  source: string;
  effective_date: ISODateTime;
}

// ---------- 供应商 ----------

export type Protocol = 'openai' | 'anthropic' | 'gemini';
export type ActiveStatus = 'active' | 'disabled';

// admin.ProviderSummary
export interface ProviderSummary {
  id: number;
  code: string;
  name: string;
  protocol: Protocol;
  currency: string;
  status: ActiveStatus;
  account_count: number;
  active_key_count: number;
  channel_count: number;
  pending_listing_count: number;
}

// admin.ProviderDetail
export interface ProviderDetail extends ProviderSummary {
  accounts: ProviderAccountSummary[];
  price_sources: PriceSource[];
}

// admin.Provider（创建接口的返回）
export interface Provider {
  id: number;
  code: string;
  name: string;
  protocol: Protocol;
}

// admin.ProviderAccountSummary
export interface ProviderAccountSummary {
  id: number;
  provider_id: number;
  provider_code: string;
  name: string;
  base_url: string;
  region: string | null;
  cost_multiplier: DecimalString;
  status: ActiveStatus;
  key_count: number;
  active_key_count: number;
  channel_count: number;
}

export type ProviderKeyStatus = 'active' | 'disabled' | 'exhausted' | 'revoked';

// admin.ProviderKeyInfo（永远不含明文/密文）
export interface ProviderKey {
  id: number;
  last4: string;
  weight: number;
  status: ProviderKeyStatus;
  disabled_reason: string | null;
  rpm_limit: number | null;
  tpm_limit: number | null;
  concurrency_limit: number | null;
  created_at: ISODateTime;
}

export interface ProviderAccountDetail extends ProviderAccountSummary {
  keys: ProviderKey[];
}

// admin.ProviderAccount（创建接口的返回）
export interface ProviderAccount {
  id: number;
  provider_id: number;
  name: string;
  base_url: string;
  cost_multiplier: DecimalString;
}

// admin.ProviderKeySummary（添加密钥的返回）
export interface ProviderKeyCreated {
  id: number;
  provider_account_id: number;
  last4: string;
  weight: number;
}

// admin.UpstreamModel
export interface UpstreamModel {
  id: string;
  owned_by: string;
}

// ---------- 虚拟模型 ----------

export type ModelType = 'chat' | 'embedding' | 'image' | 'audio' | 'rerank';
export type ModelStatus = 'active' | 'hidden' | 'deprecated';
export type Tier = 'free' | 'pro' | 'enterprise';

// admin.VirtualModel（创建 / ?name= 精确查找的返回）
export interface VirtualModel {
  id: number;
  name: string;
  family: string;
  type: ModelType;
  context_window: number;
  max_output: number;
  capabilities: string[];
  visible_tiers: Tier[];
}

// admin.VirtualModelSummary
export interface VirtualModelSummary {
  id: number;
  name: string;
  family: string;
  type: ModelType;
  status: ModelStatus;
  context_window: number;
  max_output: number;
  capabilities: string[];
  visible_tiers: Tier[];
  aliases: string[];
  display_name: string | null;
  has_metadata: boolean;
  channel_count: number;
  active_channel_count: number;
  sell_price: PriceBrief | null;
  min_margin_ratio: DecimalString | null;
}

// admin.VirtualModelMetadata 的 scores。键名由后端白名单固定（internal/admin/scores.go，
// 技术方案 §3.1）：顶层 intelligence_index / coding_index / agentic_index，外加一层
// 嵌套的 design_arena；值必须是数字，其他键 PUT 时返回 400。
// 外部评测榜单导入时按 benchmarks.score_key 投影进来的单项分数也在这里（arena_text 等）。
export type ScoreKey =
  | 'intelligence_index'
  | 'coding_index'
  | 'agentic_index'
  | 'arena_text'
  | 'arena_chinese'
  | 'arena_coding'
  | 'arena_webdev'
  | 'arena_vision'
  | 'gpqa_diamond'
  | 'swe_bench_verified'
  | 'hle'
  | 'terminal_bench'
  | 'aider_polyglot'
  | 'arc_agi_2'
  | 'livebench'
  | 'epoch_eci'
  | 'opencompass'
  | 'superclue';
export type DesignArenaKey = 'code' | 'ui_component' | 'game_dev' | 'data_viz' | 'three_d' | 'image' | 'video' | 'svg';

export type ModelScores = Partial<Record<ScoreKey, number>> & {
  design_arena?: Partial<Record<DesignArenaKey, number>>;
};

export interface VirtualModelMetadata {
  display_name: string | null;
  description: string | null;
  provider_display: string | null;
  tags: string[];
  scores: ModelScores | null;
  updated_at: ISODateTime;
}

// admin.MetadataSuggestion：GET /virtual-models/{id}/metadata/suggestion，展示元数据的建议值（只读）。
// source：external（外部目录，detail 为抓取器名）/ vendor（内置厂商表）/ derived（由模型名、类型、能力推导）
// / llm（?llm=1 时由 LLM 生成的介绍，detail 为所用模型）
export type MetadataSuggestionSource = 'external' | 'vendor' | 'derived' | 'llm';
export interface SuggestedText {
  value: string; // 空串 = 没有建议
  source?: MetadataSuggestionSource;
  detail?: string;
}
export interface MetadataSuggestion {
  virtual_model_id: number;
  display_name: SuggestedText;
  provider_display: SuggestedText;
  description: SuggestedText;
  tags: { value: string[] | null; source?: MetadataSuggestionSource };
  llm_available: boolean; // 服务端配置了 LLM，可用 ?llm=1 生成介绍
}

// POST /virtual-models/metadata/autofill 的单条结果（app.autofillItemResult）
export interface AutofillItemResult {
  id: number;
  ok: boolean;
  name?: string;
  changes?: Partial<Record<'display_name' | 'provider_display' | 'description', string>> & { tags?: string[] };
  applied: boolean;
  error?: { code: string; message: string };
}

// admin.VirtualModelDetail
export interface VirtualModelDetail extends VirtualModelSummary {
  metadata: VirtualModelMetadata | null;
  sell_price_book: PriceBook | null;
  channels: ChannelSummary[];
}

// ---------- 渠道 ----------

// admin.ChannelSummary
export interface ChannelSummary {
  id: number;
  status: ActiveStatus;
  virtual_model_id: number;
  virtual_model_name: string;
  provider_account_id: number;
  provider_account_name: string;
  provider_id: number;
  provider_code: string;
  upstream_model: string;
  priority: number;
  weight: number;
  allowed_tiers: Tier[] | null;
  allowed_account_ids: number[] | null;
  experiment_key: string | null;
  variant_label: string | null;
  cost_multiplier: DecimalString;
  param_overrides: Record<string, unknown>;
  cost_price: PriceBrief | null;
  cost_price_cny: CostCNY | null;
  sell_price: PriceBrief | null;
  margin_ratio: DecimalString | null;
  pending_change_request_id: number | null;
}

// admin.PriceObservationInfo；spec 是 pricesync.PriceSpec 的原始 JSON（PascalCase 键）
export interface PriceObservation {
  id: number;
  source_id: number;
  source_level: string;
  source_kind: string;
  observed_at: ISODateTime;
  spec: unknown;
}

// admin.ChannelDetail
export interface ChannelDetail extends ChannelSummary {
  cost_price_history: PriceBook[];
  recent_observations: PriceObservation[];
}

// admin.Channel（创建 / 三元组精确查找的返回）
export interface Channel {
  id: number;
  virtual_model_id: number;
  provider_account_id: number;
  upstream_model: string;
  priority: number;
  weight: number;
  experiment_key: string | null;
  variant_label: string | null;
  allowed_account_ids: number[] | null;
}

// ---------- 价格同步 ----------

export type SourceLevel = 'L1' | 'L2' | 'L3' | 'L4' | 'L5';
export type SourceKind = 'api' | 'html' | 'dataset' | 'billing' | 'manual';
// 数据源领域：price 价格 / offer 优惠情报 / benchmark 评测榜单（price_sources 表已泛化为数据源）
export type SourceDomain = 'price' | 'offer' | 'benchmark';

// admin.PriceSourceInfo
export interface PriceSource {
  id: number;
  domain: SourceDomain;
  name: string;
  provider_id: number | null;
  provider_code: string | null;
  level: SourceLevel;
  kind: SourceKind;
  fetcher: string;
  url: string | null;
  schedule: string;
  config: Record<string, unknown>;
  enabled: boolean;
  license: string | null;
  attribution: string | null; // 对外展示时必须带的署名文案
  public_display: boolean; // 许可证是否允许在公开页展示（false = 仅后台可见）
  auto_publish: boolean; // 榜单：映射完整且无异常时自动发布导入的 run
  next_run_at: ISODateTime | null;
  last_run_at: ISODateTime | null;
  last_success_at: ISODateTime | null;
  last_error: string | null;
  consecutive_failures: number;
  observation_count_7d: number;
  created_at: ISODateTime;
}

export type DataSourceRunStatus = 'running' | 'ok' | 'unchanged' | 'failed' | 'rejected';

// admin.DataSourceRun：一次抓取的运行记录（GET /price-sources/{id}/runs）
export interface DataSourceRun {
  id: number;
  source_id: number;
  started_at: ISODateTime;
  finished_at: ISODateTime | null;
  status: DataSourceRunStatus;
  items_fetched: number | null;
  items_changed: number | null;
  error: string | null;
  detail: unknown;
}

// ---------- 优惠雷达 ----------

export type OfferType = 'free_model' | 'discount' | 'off_peak' | 'free_quota' | 'new_user_credit' | 'price_cut';
export type OfferStatus = 'new' | 'confirmed' | 'ignored' | 'expired' | 'adopted';
export type OfferDetection = 'structured' | 'price_diff' | 'llm_extract' | 'manual';

// offers.Offer：市场上观测到的上游优惠情报（只进情报库，采用后才生成 promotions）
export interface Offer {
  id: number;
  source_id: number | null;
  source_name?: string | null;
  provider_code: string;
  upstream_model: string | null; // null = 账号级 / 全场优惠
  offer_type: OfferType;
  discount_ratio: DecimalString | null; // 价格乘数："0" = 免费，"0.5" = 五折
  quota: unknown;
  limits: unknown;
  starts_at: ISODateTime | null;
  ends_at: ISODateTime | null;
  conditions: string | null;
  evidence_url: string | null;
  evidence_excerpt: string | null;
  detection: OfferDetection;
  status: OfferStatus;
  adopted_promotion_id: number | null;
  decided_by_name: string | null;
  decided_at: ISODateTime | null;
  first_seen_at: ISODateTime;
  last_seen_at: ISODateTime;
  // 同供应商 + 上游模型的待上架候选（免费模型自动进待上架）
  listing_id: number | null;
  listing_status: ListingStatus | null;
}

// ---------- 比价看板 ----------

// admin.ChannelCost：渠道成本价（每百万 tokens，原币种 + 折人民币）
export interface ChannelCost {
  channel_id: number;
  provider_code: string;
  upstream_model: string;
  currency: string | null;
  input: DecimalString | null;
  output: DecimalString | null;
  input_cny: DecimalString | null;
  output_cny: DecimalString | null;
}

// admin.MarketPrice：各价格源对同一模型的最新观测价
export interface MarketPrice {
  source_id: number;
  source_name: string;
  level: string;
  upstream_model: string;
  currency: string;
  input: DecimalString | null;
  output: DecimalString | null;
  input_cny: DecimalString | null;
  output_cny: DecimalString | null;
  observed_at: ISODateTime;
}

// admin.PriceComparisonRow。margin_ratio：售价对最低成本的毛利率（输入:输出 = 3:1 混合），
// 负数 = 亏损；vs_market_lowest：售价 / 市场最低价，> 1 表示我们更贵。
export interface PriceComparisonRow {
  virtual_model_id: number;
  virtual_model: string;
  sell_currency: string | null;
  sell_input: DecimalString | null;
  sell_output: DecimalString | null;
  channels: ChannelCost[] | null;
  market: MarketPrice[] | null;
  margin_ratio: DecimalString | null;
  vs_market_lowest: DecimalString | null;
}

// ---------- 榜单模型映射 ----------

export type ModelAliasStatus = 'auto' | 'suggested' | 'confirmed' | 'ignored' | 'unmatched';

// admin.ModelAlias。status=suggested 时 virtual_model_id 只是模糊匹配给出的候选，尚未生效。
export interface ModelAlias {
  namespace: string;
  external_label: string;
  virtual_model_id: number | null;
  virtual_model: string | null;
  status: ModelAliasStatus;
  method: string;
  confidence: number | null;
  variant: string | null;
  seen_count: number;
  first_seen_at: ISODateTime;
  last_seen_at: ISODateTime;
  decided_by_name: string | null;
  decided_at: ISODateTime | null;
}

// admin.SetModelAliasResult
export interface SetModelAliasResult {
  alias: ModelAlias | null;
  relinked_results: number; // 重新关联的已导入榜单成绩条数
  reprojected_runs: number; // 重新投影进 scores 的已发布 run 数
}

export type ChangeStatus = 'pending' | 'auto_approved' | 'approved' | 'rejected' | 'applied' | 'superseded' | 'blocked';
export type ChangeDirection = 'up' | 'down' | 'mixed' | 'new' | 'removed';

// admin.ChangeRequestSummary
export interface ChangeRequestSummary {
  id: number;
  status: ChangeStatus;
  direction: ChangeDirection;
  max_change_ratio: DecimalString;
  effective_from: ISODateTime;
  created_at: ISODateTime;
  channel_id: number;
  virtual_model_id: number;
  virtual_model_name: string;
  provider_id: number;
  provider_code: string;
  provider_account_name: string;
  upstream_model: string;
  issue_count: number;
  blocked_reason: string | null;
  decided_by: number | null;
  decided_by_name: string | null;
  decided_at: ISODateTime | null;
  decision_reason: string | null;
  applied_book_id: number | null;
}

// admin.ComponentChange
export interface ComponentChange {
  meter: Meter;
  service_tier: string;
  tier_min_input: number;
  old_price: DecimalString | null; // null = 新增的计量项
  new_price: DecimalString | null; // null = 提案里移除了该计量项
  change_ratio: DecimalString | null;
}

export interface ValidationIssue {
  rule: string;
  severity: 'blocking' | 'force_review' | 'warning' | string;
  message: string;
}

export interface Evidence {
  observation_id: number;
  source_id: number;
  source_level: SourceLevel;
  source_kind: SourceKind;
  source_url: string | null;
  observed_at: ISODateTime;
  raw_excerpt: string;
}

// admin.ChangeImpact：按近 7 天实际用量估算的成本变化与毛利影响（审批参考值）
export interface ChangeImpact {
  window_days: number;
  cost_before_micro: Micro;
  cost_after_micro: Micro;
  cost_delta_micro: Micro;
  sell_price: PriceBrief | null;
  margin_before: DecimalString | null;
  margin_after: DecimalString | null;
  fx_missing: boolean;
}

// admin.SpecJSON
export interface PriceSpec {
  currency: string;
  components: PriceComponent[];
  effective_from: ISODateTime | null;
  expires_at: ISODateTime | null;
}

// admin.ChangeRequestDetail
export interface ChangeRequestDetail extends ChangeRequestSummary {
  currency: string;
  components: ComponentChange[];
  issues: ValidationIssue[];
  proposed_spec: PriceSpec;
  current_book: PriceBook | null;
  evidence: Evidence[];
  impact: ChangeImpact | null;
}

export interface BatchApproveResult {
  id: number;
  ok: boolean;
  applied_book_id?: number;
  error?: { code: string; message: string };
}

export type ListingStatus = 'pending' | 'dismissed' | 'published' | 'expired';
export type ListingOrigin = 'price_source' | 'free_offer';

// admin.ListingMeta：来源接口随价格给出的模型参数（预填上架表单）
export interface ListingMeta {
  name?: string;
  type?: ModelType;
  context_window?: number;
  max_output?: number;
  capabilities?: string[] | null;
  input_modalities?: string[] | null;
  output_modalities?: string[] | null;
  source?: string;
}

// admin.PendingListing
export interface PendingListing {
  id: number;
  status: ListingStatus;
  provider_id: number;
  provider_code: string;
  provider_name: string;
  upstream_model: string;
  source_id: number;
  source_level: SourceLevel;
  observed_spec: PriceSpec;
  observed_meta: ListingMeta | null;
  suggested: {
    name: string;
    family: string;
    currency: string;
    input_price: DecimalString | null;
    output_price: DecimalString | null;
    type: ModelType | ''; // 以下来自 observed_meta，来源没给时为空 / 0
    context_window: number;
    max_output: number;
    capabilities: string[] | null;
  };
  origin: ListingOrigin;
  offer_id: number | null;
  free: boolean; // 观测单价全为 0
  attached: boolean; // 上架时复用了已有同名虚拟模型（只挂渠道，售价不变）
  retired_at: ISODateTime | null; // 上游免费结束、系统自动停用渠道的时间
  // 与上游模型同名的已有虚拟模型（虚拟模型名 = 上游原始模型名）；在用时上架只挂渠道
  existing_virtual_model_id: number | null;
  existing_virtual_model_status: 'active' | 'hidden' | 'deprecated' | null;
  published_virtual_model_id: number | null;
  published_channel_id: number | null;
  first_observed_at: ISODateTime;
  last_observed_at: ISODateTime;
  decided_at: ISODateTime | null;
}

// app.publishListingResultDTO
export interface PublishListingResult {
  virtual_model_id: number;
  channel_id: number;
  cost_book_id: number;
  sell_book_id: number; // 挂到已有虚拟模型时为 0（售价不变）
  metadata_created: boolean; // 虚拟模型原本没有展示元数据，这次按外部目录 / 模型名自动生成了一条
}

// POST /pricesync/reference-price-lookup 的返回（USD / 百万 token）
export interface ReferencePrice {
  matched: boolean;
  source?: string;
  input?: DecimalString;
  output?: DecimalString;
}

export interface ReferencePriceLookupResult {
  data: Record<string, ReferencePrice>;
  currency: string;
  openrouter_error?: string;
  litellm_error?: string;
}

// ---------- 账户 ----------

export type AccountStatus = 'active' | 'suspended' | 'closed';
export type ApiKeyStatus = 'active' | 'disabled' | 'revoked';

// admin.Account
export interface Account {
  id: number;
  type: 'personal' | 'organization';
  name: string;
  status: AccountStatus;
  tier: Tier;
  credit_limit_micro: Micro;
  // 内部测试 / 压测 / 评测账户：流量不计入公开排行榜（技术方案 §3.2）
  exclude_from_public_stats: boolean;
  created_at: ISODateTime;
}

// admin.WalletSummary
export interface WalletSummary {
  cash_balance_micro: Micro;
  bonus_balance_micro: Micro;
  frozen_micro: Micro;
}

// admin.APIKey
export interface ApiKey {
  id: number;
  account_id: number;
  name: string;
  display_prefix: string;
  status: ApiKeyStatus;
  allowed_models: string[] | null;
  rpm_limit: number | null;
  tpm_limit: number | null;
  concurrency_limit: number | null;
  created_at: ISODateTime;
}

export interface ApiKeyCreated extends ApiKey {
  raw_key: string; // 只在创建响应里出现一次
}

// app.walletAdjustDTO
export interface WalletAdjustReceipt {
  ref_id: string;
  account_id: number;
  amount_micro: Micro;
  cash_after_micro: Micro;
  bonus_after_micro: Micro;
}

// ---------- 账户检索 / 流水 / 赠送（B3，接口方案 §4） ----------

// admin.AccountSummary
export interface AccountSummary extends Account {
  owner_email: string | null;
  cash_balance_micro: Micro;
  bonus_balance_micro: Micro;
  frozen_micro: Micro;
  active_key_count: number;
  last_active_at: ISODateTime | null;
}

export interface AccountMember {
  user_id: number;
  email: string | null;
  email_verified: boolean;
  role: 'owner' | 'admin' | 'developer' | 'billing' | 'viewer';
  created_at: ISODateTime;
}

export interface GrantsSummary {
  count: number;
  remaining_micro: Micro;
  nearest_expires_at: ISODateTime | null;
}

export type LedgerType = 'recharge' | 'consume' | 'refund' | 'grant' | 'grant_expire' | 'adjust';

// admin.LedgerEntry
export interface LedgerEntry {
  id: number;
  type: LedgerType;
  amount_micro: Micro;
  balance_kind: 'cash' | 'bonus';
  cash_after_micro: Micro;
  bonus_after_micro: Micro;
  ref_type: 'request' | 'payment_order' | 'promotion' | 'admin';
  ref_id: string;
  grant_id: number | null;
  created_at: ISODateTime;
}

export type GrantSource = 'signup' | 'promotion' | 'compensation' | 'invite';

// admin.CreditGrantInfo
export interface CreditGrant {
  id: number;
  source: GrantSource;
  promotion_id: number | null;
  amount_micro: Micro;
  remaining_micro: Micro;
  model_scope: string[] | null;
  expires_at: ISODateTime | null;
  created_at: ISODateTime;
}

// admin.APIKeyListItem
export interface ApiKeyListItem extends ApiKey {
  account_name: string;
  last_used_at: ISODateTime | null;
  expires_at: ISODateTime | null;
  budget_limit_micro: Micro | null;
  budget_period: 'none' | 'daily' | 'monthly' | null;
}

// ---------- 用量统计 / 调用日志（B4，接口方案 §3、§6） ----------

// admin.Metrics：比率/百分位没有样本时为 null
export interface Metrics {
  requests: number;
  success: number;
  error_rate: DecimalString | null;
  input_tokens: number;
  output_tokens: number;
  cache_read_tokens: number;
  reasoning_tokens: number;
  revenue_micro: Micro;
  list_amount_micro: Micro;
  cost_micro: Micro;
  gross_profit_micro: Micro;
  gross_margin: DecimalString | null;
  p50_latency_ms: number | null;
  p95_latency_ms: number | null;
  p95_ttft_ms: number | null;
  estimated_ratio: DecimalString | null;
  active_accounts: number;
}

// source：raw = 直接统计请求日志；rollup = 读小时汇总表（时间窗 > 48 小时，边界按整点对齐，
// 最近约 5 分钟可能尚未汇总，P50/P95 为直方图近似）
export type StatsSource = 'raw' | 'rollup';

export interface StatsOverview {
  source: StatsSource;
  from: ISODateTime;
  to: ISODateTime;
  current: Metrics;
  previous: Metrics;
}

export type UsageInterval = 'hour' | 'day' | 'none';
export type UsageGroupBy = 'none' | 'virtual_model' | 'channel' | 'provider' | 'account' | 'api_key';
export type UsageOrderBy = 'requests' | 'revenue_micro' | 'cost_micro' | 'gross_profit' | 'input_tokens' | 'output_tokens' | 'errors';

export interface UsageGroup {
  key: string; // "__other__" = Top N 之外的合并组
  label: string;
  totals: Metrics;
}

export interface UsagePoint extends Metrics {
  bucket: string; // day: YYYY-MM-DD；hour: YYYY-MM-DDTHH:00:00Z
  group: string;
}

export interface UsageResult {
  source: StatsSource;
  interval: UsageInterval;
  group_by: UsageGroupBy;
  totals: Metrics;
  groups: UsageGroup[];
  series: UsagePoint[];
}

// admin.RequestLogItem
export interface RequestLogItem {
  request_id: string;
  created_at: ISODateTime;
  account_id: number;
  api_key_id: number;
  virtual_model: string;
  channel_id: number | null;
  provider_key_id: number | null;
  endpoint: string;
  is_stream: boolean;
  status: string; // success / upstream_error / ...
  http_status: number | null;
  error_code: string | null;
  attempts: number;
  ttft_ms: number | null;
  latency_ms: number | null;
  input_tokens: number | null;
  output_tokens: number | null;
  // 多模态用量（迁移 00031）：生成图片张数、语音合成字符数、语音识别时长（毫秒）
  image_count: number | null;
  input_chars: number | null;
  audio_ms: number | null;
  usage_source: 'upstream' | 'estimated' | 'mixed';
  charged_amount_micro: Micro | null;
  list_amount_micro: Micro | null;
  cost_micro: Micro | null;
}

// admin.RequestLogDetail；attempt_trace 为网关写入的原始 JSON（重试轨迹）
export interface RequestLogDetail extends RequestLogItem {
  cache_read_tokens: number | null;
  cache_write_tokens: number | null;
  reasoning_tokens: number | null;
  attempt_trace: unknown;
  sell_price_book_id: number | null;
  cost_price_book_id: number | null;
  promotion_ids: number[] | null;
  upstream_cost: DecimalString | null;
  fx_rate: DecimalString | null;
  client_ip: string | null;
  user_agent: string | null;
  experiment_key: string | null;
  variant_label: string | null;
  account_name: string | null;
  api_key_name: string | null;
  channel_label: string | null;
  provider_code: string | null;
}

// reqlog.AttemptTraceEntry：一次请求内按顺序的每次上游尝试（重试 / 故障转移）
export interface AttemptTraceEntry {
  channel_id: number;
  key_id: number;
  // success / rate_limited / key_exhausted / key_invalid / upstream_unavailable / bad_request / content_filtered / connection_error
  status: string;
  latency_ms: number;
  codec?: string; // 方言指定的 codec（空 = 协议默认透传）
}

// ---------- 管理员身份与权限（B5，internal/adminauth） ----------

export type Permission =
  | '*'
  | 'account:read'
  | 'account:write'
  | 'wallet:adjust'
  | 'catalog:read'
  | 'catalog:write'
  | 'provider_key:write'
  | 'pricing:read'
  | 'pricing:write'
  | 'price_change:approve'
  | 'observe:read'
  | 'audit:read'
  | 'admin_user:manage'
  | 'agent:use'
  | 'agent:admin';

// GET /me（app.meResponse）
export interface AdminMe {
  id: number;
  name: string;
  email: string;
  roles: string[];
  permissions: Permission[];
  break_glass: boolean;
  totp_enabled: boolean;
}

// adminauth.AdminUser
export interface AdminUser {
  id: number;
  email: string;
  name: string;
  status: 'active' | 'disabled';
  roles: string[];
  permissions: Permission[];
  totp_enabled: boolean;
  last_login_at: ISODateTime | null;
  created_at: ISODateTime;
}

// adminauth.Role
export interface AdminRole {
  code: string;
  name: string;
  permissions: Permission[];
}

// ---------- 字典 / 计数 / 价格预览 / 批量导入（B7） ----------

// GET /meta/enums（admin.Enums + permissions）
export interface MetaEnums {
  tiers: string[];
  protocols: string[];
  model_types: string[];
  model_statuses: string[];
  capabilities: string[];
  meters: string[];
  units: string[];
  currencies: string[];
  account_types: string[];
  account_statuses: string[];
  api_key_statuses: string[];
  provider_statuses: string[];
  provider_key_statuses: string[];
  channel_statuses: string[];
  change_statuses: string[];
  change_directions: string[];
  ledger_types: string[];
  ledger_balance_kinds: string[];
  grant_sources: string[];
  source_levels: string[];
  source_kinds: string[];
  fetchers: string[];
  listing_statuses: string[];
  member_roles: string[];
  source_domains: string[];
  offer_types: string[];
  offer_statuses: string[];
  model_alias_statuses: string[];
  data_source_run_statuses: string[];
  benchmark_categories: string[];
  benchmark_statuses: string[];
  benchmark_origins: string[];
  score_keys: string[];
  design_arena_keys: string[];
  permissions: Permission[];
}

// GET /catalog/counts（admin.CatalogCounts）
export interface CatalogCounts {
  models: { total: number; missing_sell_price: number; missing_metadata: number; no_active_channel: number; negative_margin: number };
  channels: { total: number; active: number; negative_margin: number; missing_cost: number; dedicated: number };
}

// POST /pricing/preview（admin.PricingPreviewResult）
export interface PricingPreviewItem {
  key: string;
  cost_input_cny: DecimalString | null;
  cost_output_cny: DecimalString | null;
  sell_input: DecimalString | null;
  sell_output: DecimalString | null;
  margin_ratio: DecimalString | null;
  negative_margin: boolean;
}

export interface PricingPreviewResult {
  currency: string;
  fx_rate: DecimalString | null;
  fx_date: ISODateTime | null;
  fx_missing: boolean;
  items: PricingPreviewItem[];
}

// POST /provider-accounts/{id}/import-models（admin.ImportModelPlan / ImportModelResult）
export interface ImportModelItemInput {
  upstream_model: string;
  name?: string;
  family?: string;
  type?: string;
  context_window?: number;
  max_output?: number;
  capabilities?: string[];
  visible_tiers?: string[];
  cost_input?: DecimalString;
  cost_output?: DecimalString;
  markup_percent?: DecimalString;
  sell_input?: DecimalString;
  sell_output?: DecimalString;
  keep_existing_sell?: boolean;
  // 按计量项定价（图像、语音等非 token 计价的模型），与 cost_input/cost_output 二选一
  cost_components?: ImportPriceInput[];
  sell_components?: ImportPriceInput[];
  // 写入新建渠道的 param_overrides（如 {"$voice_prefix_upstream_model": true}）
  param_overrides?: Record<string, unknown>;
}

export interface ImportPriceInput {
  meter: Meter;
  unit: PriceUnit;
  price: DecimalString;
}

export interface ImportPreviewComponent {
  meter: Meter;
  unit: PriceUnit;
  cost_cny: DecimalString | null;
  sell: DecimalString;
  margin_ratio: DecimalString | null;
}

export interface ImportModelRow {
  upstream_model: string;
  name: string;
  status: 'new' | 'vm_exists' | 'listed';
  virtual_model_id: number | null;
  channel_id: number | null;
  cost_input_cny: DecimalString | null;
  cost_output_cny: DecimalString | null;
  sell_input: DecimalString | null;
  sell_output: DecimalString | null;
  margin_ratio: DecimalString | null;
  publish_sell_price: boolean;
  components?: ImportPreviewComponent[];
  errors: string[];
  ok: boolean;
  result?: {
    virtual_model_id: number;
    channel_id: number;
    created_vm: boolean;
    created_channel: boolean;
    cost_book_id: number;
    sell_book_id: number | null;
  };
  error?: { code: string; message: string };
}

export interface ImportModelsResult {
  dry_run: boolean;
  currency: string;
  fx_rate: DecimalString | null;
  fx_date: ISODateTime | null;
  fx_missing: boolean;
  items: ImportModelRow[];
}

export interface BatchItemResult {
  id: number;
  ok: boolean;
  error?: { code: string; message: string };
}

// ---------- 基准测试（技术方案 §3.2 / §3.5） ----------

export type BenchmarkCategory =
  | 'general'
  | 'coding'
  | 'agents'
  | 'reasoning'
  | 'chinese'
  | 'search'
  | 'media'
  | 'artifacts'
  | 'embedding';
export type BenchmarkStatus = 'draft' | 'published' | 'archived';
export type BenchmarkOrigin = 'manual' | 'import' | 'self_eval';
export type BenchmarkCostCurrency = 'USD' | 'CNY';

// admin.Benchmark
export interface Benchmark {
  id: number;
  slug: string;
  name: string;
  category: BenchmarkCategory;
  description: string;
  metric_name: string;
  metric_unit: string;
  higher_is_better: boolean;
  source_name: string | null;
  source_url: string | null;
  status: BenchmarkStatus;
  sort_order: number;
  // 外部榜单导入：来源数据源、外部键（tabular boards[].key）、模型名映射命名空间
  data_source_id: number | null;
  data_source_name: string | null;
  external_key: string | null;
  alias_namespace: string | null;
  score_key: string | null; // 发布时投影进 virtual_model_metadata.scores 的键
  public_display: boolean; // false = 来源许可证不允许对外展示，仅后台可见
  version: number;
  created_at: ISODateTime;
  updated_at: ISODateTime;
}

// admin.BenchmarkSummary：列表项，附 run 数与当前已发布 run 的摘要
export interface BenchmarkSummary extends Benchmark {
  run_count: number;
  published_run_id: number | null;
  published_run_at: ISODateTime | null;
  published_result_count: number;
}

// admin.BenchmarkRun。published = 当前对外展示的 run；published_at 非空但
// published=false 表示曾经发布、已被更新的 run 取代（保留为历史）。
export interface BenchmarkRun {
  id: number;
  benchmark_id: number;
  origin: BenchmarkOrigin;
  run_at: ISODateTime;
  notes: string;
  cost_currency: BenchmarkCostCurrency;
  published: boolean;
  published_at: ISODateTime | null;
  created_by: number | null;
  created_at: ISODateTime;
  result_count: number;
}

// admin.BenchmarkDetail：runs 按 run_at 倒序
export interface BenchmarkDetail extends Benchmark {
  runs: BenchmarkRun[];
}

// admin.BenchmarkResult。cost_per_task_micro 是 run.cost_currency 的微单位（1,000,000 = 1 USD/CNY）
export interface BenchmarkResult {
  model_label: string;
  virtual_model_id: number | null;
  virtual_model: string | null; // 关联模型的当前名称（只读）
  score: number;
  cost_per_task_micro: number | null;
  avg_duration_ms: number | null;
  error_rate: number | null; // 0..1
  sample_count: number | null;
  extra: unknown;
}

// admin.BenchmarkRunDetail：results 按成绩从好到差排序
export interface BenchmarkRunDetail extends BenchmarkRun {
  results: BenchmarkResult[];
}

// admin.BenchmarkResultInput（POST /benchmarks/{id}/runs 的 results[]）
export interface BenchmarkResultInput {
  model_label: string;
  virtual_model_id?: number | null;
  virtual_model?: string | null; // 按名称关联；不存在时整个 run 400
  score: number;
  cost_per_task_micro?: number | null;
  avg_duration_ms?: number | null;
  error_rate?: number | null;
  sample_count?: number | null;
  extra?: Record<string, unknown> | null;
}

// ---------- 公开应用榜治理（技术方案 §8.2） ----------

// X-Title / HTTP-Referer 是调用方自报的，运营可以屏蔽冒名应用、合并别名、改展示名
export type PublicAppRuleAction = 'block' | 'merge' | 'rename';

// admin.PublicAppRule
export interface PublicAppRule {
  id: number;
  app_key: string; // url:https://host 或 name:<小写 X-Title>
  action: PublicAppRuleAction;
  merge_into: string | null; // action=merge 时的目标 app_key
  display_name: string | null; // 覆盖展示名（rename 必填，block/merge 可选）
  note: string;
  created_at: ISODateTime;
  updated_at: ISODateTime;
}

// admin.PublicAppCandidate：未经隐私阈值过滤的原始自报应用，按 tokens 倒序，最多 500 个
export interface PublicAppCandidate {
  app_key: string;
  app_name: string;
  app_url: string;
  requests: number;
  tokens: number;
  distinct_accounts: number;
  rule: PublicAppRule | null;
}

// GET /public-apps 的响应：min_distinct_accounts 是网关公开榜单的隐私阈值（rankings_min_accounts）
export interface PublicAppsResponse {
  data: PublicAppCandidate[];
  min_distinct_accounts: number;
}
