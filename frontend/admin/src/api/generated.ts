// 由 internal/app.AdminTypeScript 从 Go 类型生成（与 docs/admin-openapi.json 同源），勿手改。
// 更新：UPDATE_ADMIN_API_DOC=1 go test ./internal/app -run TestAdminOpenAPI_UpToDate
/* eslint-disable */

export interface APIKeyListItem {
  id: number;
  account_id: number;
  name: string;
  display_prefix: string;
  status: string;
  allowed_models: string[] | null;
  rpm_limit: number | null;
  tpm_limit: number | null;
  concurrency_limit: number | null;
  created_at: string;
  account_name: string;
  last_used_at: string | null;
  expires_at: string | null;
  budget_limit_micro: number | null;
  budget_period: string | null;
}

export interface Account {
  id: number;
  type: string;
  name: string;
  status: string;
  tier: string;
  credit_limit_micro: number;
  exclude_from_public_stats: boolean;
  created_at: string;
}

export interface AccountDetailResponse {
  account: Account | null;
  wallet: WalletSummary | null;
  members: AccountMember[] | null;
  active_grants_summary: GrantsSummary;
}

export interface AccountDialect {
  provider_account_id: number;
  provider_code: string;
  protocol: string;
  dialect: unknown;
  effective: Dialect | null;
  endpoints: Record<string, boolean> | null;
  suggested_preset?: string;
  history: DialectVersion[] | null;
}

export interface AccountMember {
  user_id: number;
  email: string | null;
  email_verified: boolean;
  role: string;
  created_at: string;
}

export interface AccountSummary {
  id: number;
  type: string;
  name: string;
  status: string;
  tier: string;
  credit_limit_micro: number;
  exclude_from_public_stats: boolean;
  created_at: string;
  owner_email: string | null;
  cash_balance_micro: number;
  bonus_balance_micro: number;
  frozen_micro: number;
  active_key_count: number;
  last_active_at: string | null;
}

export interface AccountWithWallet {
  account: Account | null;
  wallet: WalletSummary | null;
}

export interface AddMemberRequest {
  email: string;
  role: string;
}

export interface AddProviderKeyRequest {
  secret: string;
  weight: number;
}

export interface AdjustWalletRequest {
  amount_micro: number;
  amount: number;
  ref_id: string;
  reason: string;
  expected_cash_balance_micro: number | null;
}

export interface AdminUser {
  id: number;
  email: string;
  name: string;
  status: string;
  roles: string[] | null;
  permissions: string[] | null;
  totp_enabled: boolean;
  last_login_at: string | null;
  created_at: string;
}

export interface AdoptOfferRequest {
  side: string;
  channel_id: number | null;
  virtual_model: string;
  name: string;
  discount_ratio: string | null;
  starts_at: string | null;
  ends_at: string | null;
  priority: number;
  budget_total: number | null;
}

export interface AdoptOfferResponse {
  promotion_id: number;
}

export interface AgentMessageRequest {
  content: string;
}

export interface AgentMetaResponse {
  enabled: boolean;
  jobs_enabled: boolean;
  model: string;
  missing: string[] | null;
  max_turns: number;
  max_tool_calls: number;
  max_tokens: number;
  tools: AgentToolInfo[] | null;
  playbooks: AgentPlaybookInfo[] | null;
}

export interface AgentPlaybookInfo {
  name: string;
  title: string;
  description: string;
  target_type: string;
  starter: string;
  allowed_tools: string[] | null;
  usable: boolean;
}

export interface AgentProposalsResponse {
  data: Proposal[] | null;
  next_cursor: string;
}

export interface AgentSSEEvent {
  event: string;
  data: unknown;
}

export interface AgentSessionDetail {
  session: Session;
  messages: Message[] | null;
  tool_calls: ToolCallView[] | null;
  read_only: boolean;
}

export interface AgentToolInfo {
  name: string;
  description: string;
  risk: string;
  permission: string;
}

export interface AgentUpdateSessionRequest {
  title: string | null;
  archived: boolean | null;
}

export interface ApiError {
  code: string;
  message: string;
}

export interface AppliedBookResponse {
  applied_book_id: number;
}

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
  created_at: string;
  agent_session_id: number | null;
  agent_tool_call_id: string | null;
}

export interface Auth {
  validation?: Validation;
  alternate_hosts?: string[] | null;
}

export interface AutofillItemResult {
  id: number;
  ok: boolean;
  name?: string;
  changes?: Record<string, unknown> | null;
  applied: boolean;
  error?: ApiError | null;
}

export interface AutofillItemsResponse {
  results: AutofillItemResult[] | null;
}

export interface AutofillMetadataRequest {
  virtual_model_ids: number[] | null;
  dry_run: boolean;
}

export interface BatchApproveRequest {
  ids: number[] | null;
  reason: string;
  max_abs_change_ratio: string;
}

export interface BatchApproveResponse {
  results: BatchApproveResult[] | null;
}

export interface BatchApproveResult {
  id: number;
  ok: boolean;
  applied_book_id?: number | null;
  error?: { code: string; message: string; } | null;
}

export interface BatchDismissRequest {
  ids: number[] | null;
  reason: string;
}

export interface BatchItemResult {
  id: number;
  ok: boolean;
  error?: ApiError | null;
}

export interface BatchItemsResponse {
  results: BatchItemResult[] | null;
}

export interface Benchmark {
  id: number;
  slug: string;
  name: string;
  category: string;
  description: string;
  metric_name: string;
  metric_unit: string;
  higher_is_better: boolean;
  source_name: string | null;
  source_url: string | null;
  status: string;
  sort_order: number;
  data_source_id: number | null;
  data_source_name: string | null;
  external_key: string | null;
  alias_namespace: string | null;
  score_key: string | null;
  public_display: boolean;
  version: number;
  created_at: string;
  updated_at: string;
}

export interface BenchmarkDetail {
  id: number;
  slug: string;
  name: string;
  category: string;
  description: string;
  metric_name: string;
  metric_unit: string;
  higher_is_better: boolean;
  source_name: string | null;
  source_url: string | null;
  status: string;
  sort_order: number;
  data_source_id: number | null;
  data_source_name: string | null;
  external_key: string | null;
  alias_namespace: string | null;
  score_key: string | null;
  public_display: boolean;
  version: number;
  created_at: string;
  updated_at: string;
  runs: BenchmarkRun[] | null;
}

export interface BenchmarkResult {
  model_label: string;
  virtual_model_id: number | null;
  virtual_model: string | null;
  score: number;
  cost_per_task_micro: number | null;
  avg_duration_ms: number | null;
  error_rate: number | null;
  sample_count: number | null;
  extra: unknown;
}

export interface BenchmarkResultInput {
  model_label: string;
  virtual_model_id: number | null;
  virtual_model: string | null;
  score: number | null;
  cost_per_task_micro: number | null;
  avg_duration_ms: number | null;
  error_rate: number | null;
  sample_count: number | null;
  extra: unknown;
}

export interface BenchmarkRun {
  id: number;
  benchmark_id: number;
  origin: string;
  run_at: string;
  notes: string;
  cost_currency: string;
  published: boolean;
  published_at: string | null;
  created_by: number | null;
  created_at: string;
  result_count: number;
}

export interface BenchmarkRunDetail {
  id: number;
  benchmark_id: number;
  origin: string;
  run_at: string;
  notes: string;
  cost_currency: string;
  published: boolean;
  published_at: string | null;
  created_by: number | null;
  created_at: string;
  result_count: number;
  results: BenchmarkResult[] | null;
}

export interface BenchmarkSummary {
  id: number;
  slug: string;
  name: string;
  category: string;
  description: string;
  metric_name: string;
  metric_unit: string;
  higher_is_better: boolean;
  source_name: string | null;
  source_url: string | null;
  status: string;
  sort_order: number;
  data_source_id: number | null;
  data_source_name: string | null;
  external_key: string | null;
  alias_namespace: string | null;
  score_key: string | null;
  public_display: boolean;
  version: number;
  created_at: string;
  updated_at: string;
  run_count: number;
  published_run_id: number | null;
  published_run_at: string | null;
  published_result_count: number;
}

export interface Catalog {
  list_paths?: string[] | null;
}

export interface CatalogCounts {
  models: { total: number; missing_sell_price: number; missing_metadata: number; no_active_channel: number; negative_margin: number; };
  channels: { total: number; active: number; negative_margin: number; missing_cost: number; dedicated: number; };
}

export interface ChangeImpact {
  window_days: number;
  cost_before_micro: number;
  cost_after_micro: number;
  cost_delta_micro: number;
  sell_price: PriceBrief | null;
  margin_before: string | null;
  margin_after: string | null;
  fx_missing: boolean;
}

export interface ChangePasswordRequest {
  old_password: string;
  new_password: string;
}

export interface ChangeRequestDetail {
  id: number;
  status: string;
  direction: string;
  max_change_ratio: string;
  effective_from: string;
  created_at: string;
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
  decided_at: string | null;
  decision_reason: string | null;
  applied_book_id: number | null;
  currency: string;
  components: ComponentChange[] | null;
  issues: ValidationIssue[] | null;
  proposed_spec: SpecJSON;
  current_book: PriceBookInfo | null;
  evidence: EvidenceInfo[] | null;
  impact: ChangeImpact | null;
}

export interface ChangeRequestSummary {
  id: number;
  status: string;
  direction: string;
  max_change_ratio: string;
  effective_from: string;
  created_at: string;
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
  decided_at: string | null;
  decision_reason: string | null;
  applied_book_id: number | null;
}

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

export interface ChannelCost {
  channel_id: number;
  provider_code: string;
  upstream_model: string;
  currency: string | null;
  input: string | null;
  output: string | null;
  input_cny: string | null;
  output_cny: string | null;
}

export interface ChannelDetail {
  id: number;
  status: string;
  virtual_model_id: number;
  virtual_model_name: string;
  provider_account_id: number;
  provider_account_name: string;
  provider_id: number;
  provider_code: string;
  upstream_model: string;
  priority: number;
  weight: number;
  allowed_tiers: string[] | null;
  allowed_account_ids: number[] | null;
  experiment_key: string | null;
  variant_label: string | null;
  cost_multiplier: string;
  param_overrides: unknown;
  cost_price: PriceBrief | null;
  cost_price_cny: CostCNY | null;
  sell_price: PriceBrief | null;
  margin_ratio: string | null;
  pending_change_request_id: number | null;
  cost_price_history: PriceBookInfo[] | null;
  recent_observations: PriceObservationInfo[] | null;
  change_requests: ChangeRequestSummary[] | null;
}

export interface ChannelHealth {
  channel_id: number;
  virtual_model: string;
  provider_account: string;
  upstream_model: string;
  requests: number;
  errors: number;
  error_rate: string | null;
  p95_latency_ms: number | null;
  breaker_state: string;
  breaker_since: string | null;
  keys_total: number;
  keys_on_cooldown: number;
  cooldown_max_seconds: number;
  status: string;
  status_reasons: string[] | null;
}

export interface ChannelHealthReport {
  window_minutes: number;
  thresholds: HealthThresholds;
  runtime_state_known: boolean;
  channels: ChannelHealth[] | null;
  recent_events: HealthEvent[] | null;
}

export interface ChannelSummary {
  id: number;
  status: string;
  virtual_model_id: number;
  virtual_model_name: string;
  provider_account_id: number;
  provider_account_name: string;
  provider_id: number;
  provider_code: string;
  upstream_model: string;
  priority: number;
  weight: number;
  allowed_tiers: string[] | null;
  allowed_account_ids: number[] | null;
  experiment_key: string | null;
  variant_label: string | null;
  cost_multiplier: string;
  param_overrides: unknown;
  cost_price: PriceBrief | null;
  cost_price_cny: CostCNY | null;
  sell_price: PriceBrief | null;
  margin_ratio: string | null;
  pending_change_request_id: number | null;
}

export interface Chat {
  thinking_default?: Record<string, unknown> | null;
  force_single_tool_call?: boolean;
  strict_messages?: boolean;
  single_system_message?: boolean;
  min_max_tokens?: number;
}

export interface ComponentChange {
  meter: string;
  service_tier: string;
  tier_min_input: number;
  old_price: string | null;
  new_price: string | null;
  change_ratio: string | null;
}

export interface CostCNY {
  input: string | null;
  output: string | null;
  media?: MeterPrice[] | null;
  fx_rate: string;
  fx_date: string | null;
  fx_missing: boolean;
}

export interface CreateAPIKeyRequest {
  name: string;
  allowed_models: string[] | null;
  rpm_limit: number | null;
  tpm_limit: number | null;
  concurrency_limit: number | null;
  budget_limit_micro: number | null;
  budget_period: string;
  expires_at: string | null;
}

export interface CreateAccountInput {
  type: string;
  name: string;
  tier: string;
  credit_limit_micro: number;
  exclude_from_public_stats: boolean;
}

export interface CreateAdminInput {
  email: string;
  name: string;
  password: string;
  roles: string[] | null;
}

export interface CreateBenchmarkInput {
  slug: string;
  name: string;
  category: string;
  description: string;
  metric_name: string;
  metric_unit: string;
  higher_is_better: boolean | null;
  source_name: string | null;
  source_url: string | null;
  status: string;
  sort_order: number;
  score_key: string | null;
}

export interface CreateBenchmarkRunInput {
  origin: string;
  run_at: string;
  notes: string;
  cost_currency: string;
  results: BenchmarkResultInput[] | null;
  publish: boolean;
}

export interface CreateChannelInput {
  virtual_model_id: number;
  provider_account_id: number;
  upstream_model: string;
  priority: number;
  weight: number;
  allowed_tiers: string[] | null;
  experiment_key: string;
  variant_label: string;
  allowed_account_ids: number[] | null;
  param_overrides: Record<string, unknown> | null;
}

export interface CreatePriceSourceRequest {
  provider_id: number | null;
  level: string;
  kind: string;
  fetcher: string;
  url: string;
  domain: string;
  name: string;
  schedule: string;
  config: Record<string, unknown> | null;
  enabled: boolean | null;
  license: string;
  attribution: string;
  public_display: boolean;
  auto_publish: boolean;
}

export interface CreateProviderAccountRequest {
  provider_id: number;
  name: string;
  base_url: string;
  cost_multiplier: string | null;
  dialect: unknown;
}

export interface CreateProviderInput {
  code: string;
  name: string;
  protocol: string;
  allowed_hosts: string[] | null;
  currency: string;
}

export interface CreatePublicAppRuleInput {
  app_key: string;
  action: string;
  merge_into: string | null;
  display_name: string | null;
  note: string;
}

export interface CreateSessionInput {
  title: string;
  playbook: string;
  context_ref: unknown;
}

export interface CreateVirtualModelInput {
  name: string;
  family: string;
  type: string;
  context_window: number;
  max_output: number;
  capabilities: string[] | null;
  visible_tiers: string[] | null;
}

export interface CreatedAPIKey {
  id: number;
  account_id: number;
  name: string;
  display_prefix: string;
  status: string;
  allowed_models: string[] | null;
  rpm_limit: number | null;
  tpm_limit: number | null;
  concurrency_limit: number | null;
  created_at: string;
  raw_key: string;
}

export interface CreditGrantInfo {
  id: number;
  source: string;
  promotion_id: number | null;
  amount_micro: number;
  remaining_micro: number;
  model_scope: string[] | null;
  expires_at: string | null;
  created_at: string;
}

export interface CreditGrantsResponse {
  data: CreditGrantInfo[] | null;
  truncated: boolean;
}

export interface CursorPage_AuditLogEntry {
  data: AuditLogEntry[] | null;
  next_cursor: string;
}

export interface CursorPage_LedgerEntry {
  data: LedgerEntry[] | null;
  next_cursor: string;
}

export interface CursorPage_RequestLogItem {
  data: RequestLogItem[] | null;
  next_cursor: string;
}

export interface CursorPage_Session {
  data: Session[] | null;
  next_cursor: string;
}

export interface DataSourceRun {
  id: number;
  source_id: number;
  started_at: string;
  finished_at: string | null;
  status: string;
  items_fetched: number | null;
  items_changed: number | null;
  error: string | null;
  detail: unknown;
}

export interface DecideChangeRequestBody {
  decided_by: number;
  reason: string;
  confirm_blocked: boolean;
}

export interface DecideInput {
  decision: string;
  note: string;
  args: unknown;
}

export interface DecideResult {
  tool_call_id: string;
  status: string;
  http_status: number;
  summary: string;
  result: unknown;
  target_type: string;
  target_id: string;
  resumed: boolean;
  outcome?: Outcome | null;
}

export interface Dialect {
  preset?: string;
  endpoints?: Record<string, Endpoint | null> | null;
  errors?: Errors;
  chat?: Chat;
  transport?: Transport;
  auth?: Auth;
  catalog?: Catalog;
  region?: string;
  notes?: string;
}

export interface DialectPreset {
  name: string;
  notes: string;
  dialect: unknown;
}

export interface DialectVersion {
  dialect: unknown;
  saved_at: string;
  saved_by: number | null;
}

export interface DismissListingRequest {
  reason: string;
}

export interface DryRunInput {
  fetcher: string;
  url: string;
  config: Record<string, unknown> | null;
  sample_size: number;
}

export interface DryRunResult {
  fetcher: string;
  count: number;
  sample: DryRunRow[] | null;
  warnings: string[] | null;
  error?: string;
}

export interface DryRunRow {
  upstream_model: string;
  currency: string;
  prices: Record<string, string> | null;
}

export interface Endpoint {
  supported?: boolean | null;
  codec?: string;
  by_model?: ModelRule[] | null;
  url?: string;
  path?: string;
  defaults?: Record<string, unknown> | null;
  force?: Record<string, unknown> | null;
  drop?: string[] | null;
  rename?: Record<string, string> | null;
  transforms?: string[] | null;
  voice_map?: Record<string, string> | null;
  size_map?: Record<string, string> | null;
  usage?: Record<string, string[] | null> | null;
  limits?: Limits;
}

export interface Errors {
  body_error_field?: string;
  in_band_patterns?: string[] | null;
  challenge_is_transient?: boolean;
}

export interface EvidenceInfo {
  observation_id: number;
  source_id: number;
  source_level: string;
  source_kind: string;
  source_url: string | null;
  observed_at: string;
  raw_excerpt: string;
}

export interface FXRateInfo {
  base: string;
  quote: string;
  rate: string;
  source: string;
  effective_date: string;
}

export interface GrantCreditRequest {
  source: string;
  amount_micro: number;
  amount: number;
  expires_at: string | null;
  model_scope: string[] | null;
  ref_id: string;
  reason: string;
}

export interface GrantedCredit {
  grant_id: number;
  bonus_after_micro: number;
}

export interface GrantsSummary {
  count: number;
  remaining_micro: number;
  nearest_expires_at: string | null;
}

export interface HealthEvent {
  id: number;
  channel_id: number | null;
  provider_key_id: number | null;
  event: string;
  detail: unknown;
  gateway: string | null;
  occurred_at: string;
}

export interface HealthThresholds {
  error_rate: string;
  p95_latency_ms: number;
}

export interface IdResponse {
  id: number;
}

export interface ImportModelItem {
  upstream_model: string;
  name: string;
  family: string;
  type: string;
  context_window: number;
  max_output: number;
  capabilities: string[] | null;
  visible_tiers: string[] | null;
  cost_input: string | null;
  cost_output: string | null;
  markup_percent: string | null;
  sell_input: string | null;
  sell_output: string | null;
  keep_existing_sell: boolean;
  cost_components: PreviewPrice[] | null;
  sell_components: PreviewPrice[] | null;
  param_overrides: Record<string, unknown> | null;
}

export interface ImportModelResult {
  virtual_model_id: number;
  channel_id: number;
  created_vm: boolean;
  created_channel: boolean;
  cost_book_id: number;
  sell_book_id: number | null;
}

export interface ImportModelResultItem {
  upstream_model: string;
  name: string;
  status: string;
  virtual_model_id: number | null;
  channel_id: number | null;
  cost_input_cny: string | null;
  cost_output_cny: string | null;
  sell_input: string | null;
  sell_output: string | null;
  margin_ratio: string | null;
  publish_sell_price: boolean;
  components?: PreviewComponent[] | null;
  errors: string[] | null;
  ok: boolean;
  result?: ImportModelResult | null;
  error?: ApiError | null;
}

export interface ImportModelsRequest {
  currency: string;
  markup_percent: string;
  items: ImportModelItem[] | null;
  dry_run: boolean;
}

export interface ImportModelsResponse {
  dry_run: boolean;
  currency: string;
  fx_rate: string | null;
  fx_date: string | null;
  fx_missing: boolean;
  items: ImportModelResultItem[] | null;
}

export interface IngestResultDTO {
  observation_id: number;
  change_request_id: number | null;
  decision: string;
  applied_book_id: number | null;
}

export interface Job {
  id: number;
  code: string;
  name: string;
  playbook: string;
  enabled: boolean;
  schedule: string;
  trigger_query: string | null;
  cursor: unknown;
  model: string | null;
  daily_token_budget: number;
  max_items_per_run: number;
  next_run_at: string | null;
  run_requested: boolean;
  failure_count: number;
  last_run_at: string | null;
  last_status: string;
  last_error: string;
  last_session_id: number | null;
  paused_reason: string;
  updated_at: string;
  tokens_today: number;
  pending_count: number;
  rejected_ratio: number | null;
  decided_in_ratio: number;
}

export interface LedgerEntry {
  id: number;
  type: string;
  amount_micro: number;
  balance_kind: string;
  cash_after_micro: number;
  bonus_after_micro: number;
  ref_type: string;
  ref_id: string;
  grant_id: number | null;
  created_at: string;
}

export interface Limits {
  b64_only?: boolean;
  max_file_bytes?: number;
  response_formats?: string[] | null;
  audio_formats?: string[] | null;
}

export interface ListData_AdminUser {
  data: AdminUser[] | null;
}

export interface ListData_BenchmarkSummary {
  data: BenchmarkSummary[] | null;
}

export interface ListData_DataSourceRun {
  data: DataSourceRun[] | null;
}

export interface ListData_FXRateInfo {
  data: FXRateInfo[] | null;
}

export interface ListData_Job {
  data: Job[] | null;
}

export interface ListData_PriceBookInfo {
  data: PriceBookInfo[] | null;
}

export interface ListData_PriceSourceInfo {
  data: PriceSourceInfo[] | null;
}

export interface ListData_PublicAppRule {
  data: PublicAppRule[] | null;
}

export interface ListData_Role {
  data: Role[] | null;
}

export interface ListData_String {
  data: string[] | null;
}

export interface ListData_UpstreamModel {
  data: UpstreamModel[] | null;
}

export interface ListingMeta {
  name?: string;
  type?: string;
  context_window?: number;
  max_output?: number;
  capabilities?: string[] | null;
  input_modalities?: string[] | null;
  output_modalities?: string[] | null;
  source?: string;
}

export interface ListingSuggestion {
  name: string;
  family: string;
  currency: string;
  input_price: string | null;
  output_price: string | null;
  type: string;
  context_window: number;
  max_output: number;
  capabilities: string[] | null;
}

export interface LoginRequest {
  email: string;
  password: string;
  totp_code: string;
}

export interface LoginResult {
  token: string;
  expires_at: string;
  user: AdminUser | null;
}

export interface MarketPrice {
  source_id: number;
  source_name: string;
  level: string;
  upstream_model: string;
  currency: string;
  input: string | null;
  output: string | null;
  input_cny: string | null;
  output_cny: string | null;
  observed_at: string;
}

export interface MeResponse {
  id: number;
  name: string;
  email: string;
  roles: string[] | null;
  permissions: string[] | null;
  break_glass: boolean;
  totp_enabled: boolean;
}

export interface Message {
  seq: number;
  role: string;
  content: string;
  tool_calls?: ToolCall[] | null;
  tool_call_id?: string;
  compacted?: boolean;
}

export interface MetaCodecsResponse {
  codecs: string[] | null;
  transforms: string[] | null;
}

export interface MetaDialectPresetsResponse {
  data: DialectPreset[] | null;
}

export interface MetaEnumsResponse {
  tiers: string[] | null;
  protocols: string[] | null;
  model_types: string[] | null;
  model_statuses: string[] | null;
  capabilities: string[] | null;
  meters: string[] | null;
  units: string[] | null;
  currencies: string[] | null;
  account_types: string[] | null;
  account_statuses: string[] | null;
  api_key_statuses: string[] | null;
  provider_statuses: string[] | null;
  provider_key_statuses: string[] | null;
  channel_statuses: string[] | null;
  change_statuses: string[] | null;
  change_directions: string[] | null;
  ledger_types: string[] | null;
  ledger_balance_kinds: string[] | null;
  grant_sources: string[] | null;
  source_levels: string[] | null;
  source_kinds: string[] | null;
  fetchers: string[] | null;
  listing_statuses: string[] | null;
  member_roles: string[] | null;
  source_domains: string[] | null;
  offer_types: string[] | null;
  offer_statuses: string[] | null;
  model_alias_statuses: string[] | null;
  data_source_run_statuses: string[] | null;
  benchmark_categories: string[] | null;
  benchmark_statuses: string[] | null;
  benchmark_origins: string[] | null;
  score_keys: string[] | null;
  design_arena_keys: string[] | null;
  permissions: string[] | null;
}

export interface MetadataSuggestion {
  virtual_model_id: number;
  display_name: SuggestedText;
  provider_display: SuggestedText;
  description: SuggestedText;
  tags: SuggestedTags;
  llm_available: boolean;
}

export interface MeterPrice {
  meter: string;
  unit: string;
  price: string;
}

export interface Metrics {
  requests: number;
  success: number;
  error_rate: string | null;
  input_tokens: number;
  output_tokens: number;
  cache_read_tokens: number;
  reasoning_tokens: number;
  revenue_micro: number;
  list_amount_micro: number;
  cost_micro: number;
  gross_profit_micro: number;
  gross_margin: string | null;
  p50_latency_ms: number | null;
  p95_latency_ms: number | null;
  p95_ttft_ms: number | null;
  estimated_ratio: string | null;
  active_accounts: number;
}

export interface ModelAlias {
  namespace: string;
  external_label: string;
  virtual_model_id: number | null;
  virtual_model: string | null;
  status: string;
  method: string;
  confidence: number | null;
  variant: string | null;
  seen_count: number;
  first_seen_at: string;
  last_seen_at: string;
  decided_by_name: string | null;
  decided_at: string | null;
}

export interface ModelRule {
  match: string;
  codec?: string;
  supported?: boolean | null;
}

export interface Offer {
  id: number;
  source_id: number | null;
  source_name?: string | null;
  provider_code: string;
  upstream_model: string | null;
  offer_type: string;
  discount_ratio: string | null;
  quota: unknown;
  limits: unknown;
  starts_at: string | null;
  ends_at: string | null;
  conditions: string | null;
  evidence_url: string | null;
  evidence_excerpt: string | null;
  detection: string;
  status: string;
  adopted_promotion_id: number | null;
  decided_by_name: string | null;
  decided_at: string | null;
  first_seen_at: string;
  last_seen_at: string;
  listing_id: number | null;
  listing_status: string | null;
}

export interface Outcome {
  status: string;
  reason?: string;
  usage: Usage;
  turns: number;
  pending: number;
  proposals: number;
}

export interface Page_APIKeyListItem {
  data: APIKeyListItem[] | null;
  total: number;
  page: number;
  page_size: number;
}

export interface Page_AccountSummary {
  data: AccountSummary[] | null;
  total: number;
  page: number;
  page_size: number;
}

export interface Page_ChangeRequestSummary {
  data: ChangeRequestSummary[] | null;
  total: number;
  page: number;
  page_size: number;
}

export interface Page_ChannelSummary {
  data: ChannelSummary[] | null;
  total: number;
  page: number;
  page_size: number;
}

export interface Page_ModelAlias {
  data: ModelAlias[] | null;
  total: number;
  page: number;
  page_size: number;
}

export interface Page_Offer {
  data: Offer[] | null;
  total: number;
  page: number;
  page_size: number;
}

export interface Page_PendingListing {
  data: PendingListing[] | null;
  total: number;
  page: number;
  page_size: number;
}

export interface Page_PriceComparisonRow {
  data: PriceComparisonRow[] | null;
  total: number;
  page: number;
  page_size: number;
}

export interface Page_ProviderAccountSummary {
  data: ProviderAccountSummary[] | null;
  total: number;
  page: number;
  page_size: number;
}

export interface Page_ProviderSummary {
  data: ProviderSummary[] | null;
  total: number;
  page: number;
  page_size: number;
}

export interface Page_VirtualModelSummary {
  data: VirtualModelSummary[] | null;
  total: number;
  page: number;
  page_size: number;
}

export interface PendingListing {
  id: number;
  status: string;
  provider_id: number;
  provider_code: string;
  provider_name: string;
  upstream_model: string;
  source_id: number;
  source_level: string;
  observed_spec: SpecJSON;
  observed_meta: ListingMeta | null;
  suggested: ListingSuggestion;
  origin: string;
  offer_id: number | null;
  free: boolean;
  attached: boolean;
  retired_at: string | null;
  existing_virtual_model_id: number | null;
  existing_virtual_model_status: string | null;
  published_virtual_model_id: number | null;
  published_channel_id: number | null;
  first_observed_at: string;
  last_observed_at: string;
  decided_at: string | null;
}

export interface PreviewComponent {
  meter: string;
  unit: string;
  cost_cny: string | null;
  sell: string;
  margin_ratio: string | null;
}

export interface PreviewInput {
  url: string;
  provider_code: string;
  keywords: string[] | null;
  max_chars: number;
}

export interface PreviewItem {
  upstream_model: string | null;
  offer_type: string;
  discount_ratio: number | null;
  starts_at: string | null;
  ends_at: string | null;
  conditions: string;
  quota: Record<string, unknown> | null;
  evidence: string;
  accepted: boolean;
}

export interface PreviewPrice {
  meter: string;
  unit: string;
  price: string;
}

export interface PreviewResult {
  url: string;
  text_chars: number;
  items: PreviewItem[] | null;
  accepted: number;
  dropped: number;
}

export interface PriceBookIDResponse {
  price_book_id: number;
}

export interface PriceBookInfo {
  id: number;
  kind: string;
  tier: string | null;
  currency: string;
  effective_from: string;
  effective_to: string | null;
  created_by: number | null;
  note: string | null;
  created_at: string;
  is_current: boolean;
  components: PriceComponentInput[] | null;
}

export interface PriceBrief {
  price_book_id: number;
  currency: string;
  input: string | null;
  output: string | null;
  media?: MeterPrice[] | null;
  effective_from: string;
}

export interface PriceComparisonRow {
  virtual_model_id: number;
  virtual_model: string;
  sell_currency: string | null;
  sell_input: string | null;
  sell_output: string | null;
  channels: ChannelCost[] | null;
  market: MarketPrice[] | null;
  margin_ratio: string | null;
  vs_market_lowest: string | null;
}

export interface PriceComponentInput {
  meter: string;
  unit: string;
  service_tier: string;
  tier_min_input: number;
  tier_max_input: number | null;
  window_start_min: number | null;
  window_end_min: number | null;
  unit_price: string;
}

export interface PriceComponentJSON {
  meter: string;
  unit: string;
  service_tier: string;
  tier_min_input: number;
  tier_max_input: number | null;
  window_start_min: number | null;
  window_end_min: number | null;
  unit_price: string;
}

export interface PriceObservationInfo {
  id: number;
  source_id: number;
  source_level: string;
  source_kind: string;
  observed_at: string;
  spec: unknown;
}

export interface PriceObservationRequest {
  source_id: number;
  level: string;
  upstream_model: string;
  currency: string;
  components: PriceComponentJSON[] | null;
  effective_from: string | null;
  expires_at: string | null;
  raw_object: string;
}

export interface PriceSourceInfo {
  id: number;
  domain: string;
  name: string;
  provider_id: number | null;
  provider_code: string | null;
  level: string;
  kind: string;
  fetcher: string;
  url: string | null;
  schedule: string;
  config: unknown;
  enabled: boolean;
  license: string | null;
  attribution: string | null;
  public_display: boolean;
  auto_publish: boolean;
  next_run_at: string | null;
  last_run_at: string | null;
  last_success_at: string | null;
  last_error: string | null;
  consecutive_failures: number;
  observation_count_7d: number;
  created_at: string;
}

export interface PricingPreviewInput {
  currency: string;
  cost_multiplier: string | null;
  markup_percent: string;
  items: PricingPreviewItem[] | null;
}

export interface PricingPreviewItem {
  key: string;
  cost_input: string | null;
  cost_output: string | null;
  sell_input: string | null;
  sell_output: string | null;
  markup_percent: string | null;
  cost_components: PreviewPrice[] | null;
  sell_components: PreviewPrice[] | null;
}

export interface PricingPreviewResult {
  currency: string;
  fx_rate: string | null;
  fx_date: string | null;
  fx_missing: boolean;
  items: PricingPreviewResultItem[] | null;
}

export interface PricingPreviewResultItem {
  key: string;
  cost_input_cny: string | null;
  cost_output_cny: string | null;
  sell_input: string | null;
  sell_output: string | null;
  margin_ratio: string | null;
  negative_margin: boolean;
  components?: PreviewComponent[] | null;
}

export interface Proposal {
  id: number;
  tool_call_id: string;
  session_id: number;
  session_title: string;
  session_mode: string;
  job_id: number | null;
  playbook: string;
  tool: string;
  target_type: string;
  target_id: string;
  summary: string;
  rationale: string;
  evidence: unknown;
  confidence: number | null;
  required_perm: string;
  status: string;
  args: unknown;
  before: unknown;
  after: unknown;
  result: unknown;
  decided_by: number | null;
  decided_by_name: string;
  decided_at: string | null;
  created_at: string;
}

export interface Provider {
  id: number;
  code: string;
  name: string;
  protocol: string;
}

export interface ProviderAccount {
  id: number;
  provider_id: number;
  name: string;
  base_url: string;
  cost_multiplier: string;
}

export interface ProviderAccountDetail {
  id: number;
  provider_id: number;
  provider_code: string;
  name: string;
  base_url: string;
  region: string | null;
  cost_multiplier: string;
  status: string;
  key_count: number;
  active_key_count: number;
  channel_count: number;
  keys: ProviderKeyInfo[] | null;
}

export interface ProviderAccountSummary {
  id: number;
  provider_id: number;
  provider_code: string;
  name: string;
  base_url: string;
  region: string | null;
  cost_multiplier: string;
  status: string;
  key_count: number;
  active_key_count: number;
  channel_count: number;
}

export interface ProviderDetail {
  id: number;
  code: string;
  name: string;
  protocol: string;
  currency: string;
  status: string;
  account_count: number;
  active_key_count: number;
  channel_count: number;
  pending_listing_count: number;
  allowed_hosts: string[] | null;
  accounts: ProviderAccountSummary[] | null;
  accounts_truncated: boolean;
  price_sources: PriceSourceInfo[] | null;
}

export interface ProviderKeyInfo {
  id: number;
  last4: string;
  weight: number;
  status: string;
  disabled_reason: string | null;
  rpm_limit: number | null;
  tpm_limit: number | null;
  concurrency_limit: number | null;
  created_at: string;
}

export interface ProviderKeySummary {
  id: number;
  provider_account_id: number;
  last4: string;
  weight: number;
}

export interface ProviderSummary {
  id: number;
  code: string;
  name: string;
  protocol: string;
  currency: string;
  status: string;
  account_count: number;
  active_key_count: number;
  channel_count: number;
  pending_listing_count: number;
  allowed_hosts: string[] | null;
}

export interface ProviderUpdateResult {
  id: number;
  code: string;
  name: string;
  protocol: string;
  currency: string;
  status: string;
  account_count: number;
  active_key_count: number;
  channel_count: number;
  pending_listing_count: number;
  allowed_hosts: string[] | null;
  accounts: ProviderAccountSummary[] | null;
  accounts_truncated: boolean;
  price_sources: PriceSourceInfo[] | null;
  affected_active_channels: number;
}

export interface PublicAppCandidate {
  app_key: string;
  app_name: string;
  app_url: string;
  requests: number;
  tokens: number;
  distinct_accounts: number;
  rule: PublicAppRule | null;
}

export interface PublicAppRule {
  id: number;
  app_key: string;
  action: string;
  merge_into: string | null;
  display_name: string | null;
  note: string;
  created_at: string;
  updated_at: string;
}

export interface PublicAppsResponse {
  data: PublicAppCandidate[] | null;
  min_distinct_accounts: number;
}

export interface PublishListingRequest {
  virtual_model: { name?: string; family: string; type: string; context_window: number; max_output: number; capabilities: string[] | null; visible_tiers: string[] | null; };
  provider_account_id: number;
  sell_markup: string;
}

export interface PublishListingResultDTO {
  virtual_model_id: number;
  channel_id: number;
  cost_book_id: number;
  sell_book_id: number;
  metadata_created: boolean;
}

export interface ReferencePriceLookupRequest {
  litellm_dataset_url: string;
  openrouter_models_url: string;
  upstream_models: string[] | null;
}

export interface ReferencePriceLookupResponse {
  data: Record<string, ReferencePriceLookupResult> | null;
  currency: string;
  litellm_dataset_url: string;
  openrouter_models_url: string;
  openrouter_error: string | null;
  litellm_error: string | null;
}

export interface ReferencePriceLookupResult {
  matched: boolean;
  source?: string;
  input?: string;
  output?: string;
}

export interface RequestLogDetail {
  request_id: string;
  created_at: string;
  account_id: number;
  api_key_id: number;
  virtual_model: string;
  channel_id: number | null;
  provider_key_id: number | null;
  endpoint: string;
  is_stream: boolean;
  status: string;
  http_status: number | null;
  error_code: string | null;
  attempts: number;
  ttft_ms: number | null;
  latency_ms: number | null;
  input_tokens: number | null;
  output_tokens: number | null;
  image_count: number | null;
  input_chars: number | null;
  audio_ms: number | null;
  usage_source: string;
  charged_amount_micro: number | null;
  list_amount_micro: number | null;
  cost_micro: number | null;
  cache_read_tokens: number | null;
  cache_write_tokens: number | null;
  reasoning_tokens: number | null;
  attempt_trace: unknown;
  sell_price_book_id: number | null;
  cost_price_book_id: number | null;
  promotion_ids: number[] | null;
  upstream_cost: string | null;
  fx_rate: string | null;
  client_ip: string | null;
  user_agent: string | null;
  experiment_key: string | null;
  variant_label: string | null;
  account_name: string | null;
  api_key_name: string | null;
  channel_label: string | null;
  provider_code: string | null;
}

export interface RequestLogItem {
  request_id: string;
  created_at: string;
  account_id: number;
  api_key_id: number;
  virtual_model: string;
  channel_id: number | null;
  provider_key_id: number | null;
  endpoint: string;
  is_stream: boolean;
  status: string;
  http_status: number | null;
  error_code: string | null;
  attempts: number;
  ttft_ms: number | null;
  latency_ms: number | null;
  input_tokens: number | null;
  output_tokens: number | null;
  image_count: number | null;
  input_chars: number | null;
  audio_ms: number | null;
  usage_source: string;
  charged_amount_micro: number | null;
  list_amount_micro: number | null;
  cost_micro: number | null;
}

export interface Role {
  code: string;
  name: string;
  permissions: string[] | null;
}

export interface Session {
  id: number;
  admin_user_id: number;
  admin_name: string;
  title: string;
  playbook: string | null;
  context_ref: unknown;
  mode: string;
  status: string;
  status_reason: string;
  model: string;
  tokens_in: number;
  tokens_out: number;
  turns: number;
  archived: boolean;
  job_id: number | null;
  created_at: string;
  updated_at: string;
}

export interface SetCostPriceRequest {
  currency: string;
  components: PriceComponentInput[] | null;
}

export interface SetDialectRequest {
  dialect: unknown;
}

export interface SetFXRateRequest {
  base: string;
  quote: string;
  rate: string;
  source: string;
  effective_date: string | null;
}

export interface SetMetadataRequest {
  display_name: string;
  description: string;
  provider_display: string;
  tags: string[] | null;
  scores: Record<string, unknown> | null;
}

export interface SetModelAliasInput {
  namespace: string;
  external_label: string;
  status: string;
  virtual_model_id: number | null;
  virtual_model: string | null;
}

export interface SetModelAliasResult {
  alias: ModelAlias | null;
  relinked_results: number;
  reprojected_runs: number;
}

export interface SetOfferStatusRequest {
  status: string;
}

export interface SetSellPriceRequest {
  tier: string;
  components: PriceComponentInput[] | null;
}

export interface SpecJSON {
  currency: string;
  components: PriceComponentInput[] | null;
  effective_from: string | null;
  expires_at: string | null;
}

export interface StatsOverview {
  source: string;
  from: string;
  to: string;
  current: Metrics;
  previous: Metrics;
}

export interface StatusResponse {
  status: string;
}

export interface SuggestedTags {
  value: string[] | null;
  source?: string;
}

export interface SuggestedText {
  value: string;
  source?: string;
  detail?: string;
}

export interface TOTPSetup {
  secret: string;
  otpauth_url: string;
}

export interface TodoCounts {
  price_changes_pending: number;
  price_changes_blocked: number;
  listings_pending: number;
  channels_negative_margin: number;
  channels_missing_cost: number;
  models_missing_sell_price: number;
  offers_new: number;
  aliases_suggested: number;
  data_sources_failing: number;
  agent_pending_approvals: number;
}

export interface ToolCall {
  id: string;
  name: string;
  arguments: string;
}

export interface ToolCallView {
  id: string;
  session_id: number;
  run_id: string;
  tool: string;
  risk: string;
  args: unknown;
  status: string;
  summary: string;
  required_perm: string;
  before?: unknown;
  after?: unknown;
  http_status?: number;
  result?: unknown;
  duration_ms?: number;
  decided_by: number | null;
  decided_by_name: string;
  decided_at: string | null;
  decision_note: string;
  created_at: string;
  proposal_id: number | null;
  rationale: string;
  evidence: unknown;
  confidence: number | null;
  target_type: string;
  target_id: string;
}

export interface TotpCodeRequest {
  code: string;
}

export interface Transport {
  extra_headers?: Record<string, string> | null;
  timeout_ms?: number;
  keyless?: boolean;
}

export interface UnmappedIngestResultDTO {
  mapped_results: IngestResultDTO[] | null;
  listing_id: number | null;
}

export interface UnmappedObservationRequest {
  source_id: number;
  level: string;
  upstream_model: string;
  currency: string;
  components: PriceComponentJSON[] | null;
  effective_from: string | null;
  expires_at: string | null;
  raw_object: string;
}

export interface UpdateAPIKeyInput {
  name: string | null;
  status: string | null;
  allowed_models: string[] | null | null;
  rpm_limit: number | null;
  tpm_limit: number | null;
  concurrency_limit: number | null;
  budget_limit_micro: number | null;
  budget_period: string | null;
  expires_at: string | null;
  clear_expires_at: boolean;
}

export interface UpdateAccountInput {
  name: string | null;
  status: string | null;
  tier: string | null;
  credit_limit_micro: number | null;
  exclude_from_public_stats: boolean | null;
}

export interface UpdateAdminInput {
  name: string | null;
  status: string | null;
  roles: string[] | null | null;
  password: string | null;
  reset_totp: boolean;
}

export interface UpdateBenchmarkInput {
  name: string | null;
  category: string | null;
  description: string | null;
  metric_name: string | null;
  metric_unit: string | null;
  higher_is_better: boolean | null;
  source_name: string | null;
  source_url: string | null;
  status: string | null;
  sort_order: number | null;
  score_key: string | null;
}

export interface UpdateChannelInput {
  priority: number | null;
  weight: number | null;
  status: string | null;
  allowed_tiers: string[] | null | null;
  allowed_account_ids: number[] | null | null;
  param_overrides: unknown | null;
  force: boolean;
}

export interface UpdateInput {
  enabled: boolean | null;
  schedule: string | null;
  model: string | null;
  daily_token_budget: number | null;
  max_items_per_run: number | null;
}

export interface UpdateMemberRequest {
  role: string;
}

export interface UpdatePriceSourceInput {
  enabled: boolean | null;
  url: string | null;
  schedule: string | null;
  config: unknown | null;
  name: string | null;
  license: string | null;
  attribution: string | null;
  public_display: boolean | null;
  auto_publish: boolean | null;
  provider_id: number | null;
}

export interface UpdateProviderAccountInput {
  name: string | null;
  base_url: string | null;
  region: string | null;
  cost_multiplier: string | null;
  status: string | null;
}

export interface UpdateProviderInput {
  name: string | null;
  status: string | null;
  allowed_hosts: string[] | null | null;
}

export interface UpdateProviderKeyInput {
  weight: number | null;
  status: string | null;
  disabled_reason: string | null;
  rpm_limit: number | null;
  tpm_limit: number | null;
  concurrency_limit: number | null;
}

export interface UpdateVirtualModelInput {
  type: string | null;
  status: string | null;
  visible_tiers: string[] | null | null;
  capabilities: string[] | null | null;
  context_window: number | null;
  max_output: number | null;
  aliases: string[] | null | null;
}

export interface UpstreamModel {
  id: string;
  owned_by: string;
}

export interface Usage {
  tokens_in: number;
  tokens_out: number;
}

export interface UsageGroup {
  key: string;
  label: string;
  totals: Metrics;
}

export interface UsagePoint {
  bucket: string;
  group: string;
  requests: number;
  success: number;
  error_rate: string | null;
  input_tokens: number;
  output_tokens: number;
  cache_read_tokens: number;
  reasoning_tokens: number;
  revenue_micro: number;
  list_amount_micro: number;
  cost_micro: number;
  gross_profit_micro: number;
  gross_margin: string | null;
  p50_latency_ms: number | null;
  p95_latency_ms: number | null;
  p95_ttft_ms: number | null;
  estimated_ratio: string | null;
  active_accounts: number;
}

export interface UsageResult {
  source: string;
  interval: string;
  group_by: string;
  totals: Metrics;
  groups: UsageGroup[] | null;
  series: UsagePoint[] | null;
}

export interface Validation {
  method?: string;
  url?: string;
  model?: string;
}

export interface ValidationIssue {
  rule: string;
  severity: string;
  message: string;
}

export interface VirtualModel {
  id: number;
  name: string;
  family: string;
  type: string;
  context_window: number;
  max_output: number;
  capabilities: string[] | null;
  visible_tiers: string[] | null;
}

export interface VirtualModelDetail {
  id: number;
  name: string;
  family: string;
  type: string;
  status: string;
  context_window: number;
  max_output: number;
  capabilities: string[] | null;
  visible_tiers: string[] | null;
  aliases: string[] | null;
  display_name: string | null;
  has_metadata: boolean;
  channel_count: number;
  active_channel_count: number;
  sell_price: PriceBrief | null;
  min_margin_ratio: string | null;
  metadata: VirtualModelMetadata | null;
  sell_price_book: PriceBookInfo | null;
  channels: ChannelSummary[] | null;
  channels_truncated: boolean;
}

export interface VirtualModelMetadata {
  display_name: string | null;
  description: string | null;
  provider_display: string | null;
  tags: string[] | null;
  scores: unknown;
  updated_at: string;
}

export interface VirtualModelSummary {
  id: number;
  name: string;
  family: string;
  type: string;
  status: string;
  context_window: number;
  max_output: number;
  capabilities: string[] | null;
  visible_tiers: string[] | null;
  aliases: string[] | null;
  display_name: string | null;
  has_metadata: boolean;
  channel_count: number;
  active_channel_count: number;
  sell_price: PriceBrief | null;
  min_margin_ratio: string | null;
}

export interface WalletAdjustDTO {
  ref_id: string;
  account_id: number;
  amount_micro: number;
  cash_after_micro: number;
  bonus_after_micro: number;
}

export interface WalletSummary {
  cash_balance_micro: number;
  bonus_balance_micro: number;
  frozen_micro: number;
}
