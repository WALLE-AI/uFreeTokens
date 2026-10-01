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
}

// admin.TodoCounts
export interface TodoCounts {
  price_changes_pending: number;
  price_changes_blocked: number;
  listings_pending: number;
  channels_negative_margin: number;
  channels_missing_cost: number;
  models_missing_sell_price: number;
}

export type AdminEnv = 'production' | 'staging' | 'dev';

// ---------- 价格 ----------

export type Meter = 'input' | 'input_cache_read' | 'input_cache_write' | 'output' | 'output_reasoning' | 'request';
export type PriceUnit = 'per_1m_tokens' | 'per_request' | 'per_image' | 'per_second';

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

// admin.VirtualModelMetadata；scores 前端约定 intelligenceIndex/codingIndex/agenticIndex
export interface ModelScores {
  intelligenceIndex?: number;
  codingIndex?: number;
  agenticIndex?: number;
}

export interface VirtualModelMetadata {
  display_name: string | null;
  description: string | null;
  provider_display: string | null;
  tags: string[];
  scores: ModelScores | null;
  updated_at: ISODateTime;
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

// admin.PriceSourceInfo
export interface PriceSource {
  id: number;
  provider_id: number | null;
  provider_code: string | null;
  level: SourceLevel;
  kind: SourceKind;
  fetcher: string;
  url: string | null;
  schedule: string;
  config: Record<string, unknown>;
  enabled: boolean;
  last_success_at: ISODateTime | null;
  observation_count_7d: number;
  created_at: ISODateTime;
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

export type ListingStatus = 'pending' | 'dismissed' | 'published';

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
  suggested: {
    name: string;
    family: string;
    currency: string;
    input_price: DecimalString | null;
    output_price: DecimalString | null;
  };
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
  sell_book_id: number;
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
  | 'admin_user:manage';

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
