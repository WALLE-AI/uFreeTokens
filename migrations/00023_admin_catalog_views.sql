-- 虚拟模型 / 渠道列表的派生字段视图（方案 §2.3 A3）：让"缺售价 / 缺元数据 / 无活跃
-- 渠道 / 负毛利 / 缺成本价"这些过滤条件、按毛利排序、分页与总数都能在 SQL 里完成，
-- 列表接口不再把全部模型、渠道加载进内存再过滤分页。
--
-- 口径与 internal/admin/pricectx.go（以及 internal/catalog 的快照加载器）完全一致：
--   - 当前生效的价格版本 = effective_from <= now() 且未过期的最新一本；售价不区分 tier；
--   - 基础价 = meter input/output、unit per_1m_tokens、service_tier default、
--     tier_min_input 0、不带时段窗口；
--   - 非 CNY 成本按 quote=CNY、effective_date <= 今天的最新汇率折算，再乘上游账号的
--     cost_multiplier，保留 6 位小数；没有汇率时无法折算（毛利为 NULL）；
--   - 毛利率 = 1 - 成本/售价，保留 4 位小数，取 input/output 中较差的一项；
--     售价不是 CNY 时为 NULL。
-- 列表只用视图决定"哪些行、什么顺序、共多少"，当前页的价格明细仍由 Go 代码
-- （loadPriceContext）填充，两边口径由 TestCatalogViews_MatchGoMargins 保证一致。

-- +goose Up
-- +goose StatementBegin
CREATE VIEW v_admin_channel_margin AS
WITH cost AS (
    SELECT DISTINCT ON (pb.channel_id) pb.channel_id, pb.id AS book_id, pb.currency,
        (SELECT unit_price FROM price_components pc WHERE pc.price_book_id = pb.id AND pc.meter = 'input'
            AND pc.unit = 'per_1m_tokens' AND pc.service_tier = 'default' AND pc.tier_min_input = 0 AND pc.window_start_min IS NULL LIMIT 1) AS input,
        (SELECT unit_price FROM price_components pc WHERE pc.price_book_id = pb.id AND pc.meter = 'output'
            AND pc.unit = 'per_1m_tokens' AND pc.service_tier = 'default' AND pc.tier_min_input = 0 AND pc.window_start_min IS NULL LIMIT 1) AS output
    FROM price_books pb
    WHERE pb.kind = 'cost' AND pb.effective_from <= now() AND (pb.effective_to IS NULL OR pb.effective_to > now())
    ORDER BY pb.channel_id, pb.effective_from DESC
), sell AS (
    SELECT DISTINCT ON (pb.virtual_model_id) pb.virtual_model_id, pb.id AS book_id, pb.currency,
        (SELECT unit_price FROM price_components pc WHERE pc.price_book_id = pb.id AND pc.meter = 'input'
            AND pc.unit = 'per_1m_tokens' AND pc.service_tier = 'default' AND pc.tier_min_input = 0 AND pc.window_start_min IS NULL LIMIT 1) AS input,
        (SELECT unit_price FROM price_components pc WHERE pc.price_book_id = pb.id AND pc.meter = 'output'
            AND pc.unit = 'per_1m_tokens' AND pc.service_tier = 'default' AND pc.tier_min_input = 0 AND pc.window_start_min IS NULL LIMIT 1) AS output
    FROM price_books pb
    WHERE pb.kind = 'sell' AND pb.effective_from <= now() AND (pb.effective_to IS NULL OR pb.effective_to > now())
    ORDER BY pb.virtual_model_id, pb.effective_from DESC
), fx AS (
    SELECT DISTINCT ON (base) base, rate FROM fx_rates
    WHERE quote = 'CNY' AND effective_date <= CURRENT_DATE
    ORDER BY base, effective_date DESC
), conv AS (
    SELECT c.id AS channel_id, c.virtual_model_id, c.status,
        cost.book_id AS cost_book_id,
        sell.book_id AS sell_book_id,
        sell.currency AS sell_currency, sell.input AS sell_input, sell.output AS sell_output,
        CASE WHEN cost.book_id IS NULL THEN NULL
             WHEN cost.currency = '' OR cost.currency = 'CNY' THEN 1::numeric
             ELSE fx.rate END AS rate,
        cost.input AS cost_input, cost.output AS cost_output, pa.cost_multiplier
    FROM channels c
    JOIN provider_accounts pa ON pa.id = c.provider_account_id
    LEFT JOIN cost ON cost.channel_id = c.id
    LEFT JOIN sell ON sell.virtual_model_id = c.virtual_model_id
    LEFT JOIN fx ON fx.base = cost.currency
), cny AS (
    SELECT conv.*,
        round(cost_input * rate * cost_multiplier, 6) AS cost_input_cny,
        round(cost_output * rate * cost_multiplier, 6) AS cost_output_cny
    FROM conv
)
SELECT channel_id, virtual_model_id, status, cost_book_id, sell_book_id,
    CASE WHEN rate IS NULL OR sell_book_id IS NULL OR (sell_currency <> '' AND sell_currency <> 'CNY') THEN NULL
         ELSE LEAST(
            CASE WHEN sell_input IS NULL OR cost_input_cny IS NULL OR sell_input = 0 THEN NULL ELSE round(1 - cost_input_cny / sell_input, 4) END,
            CASE WHEN sell_output IS NULL OR cost_output_cny IS NULL OR sell_output = 0 THEN NULL ELSE round(1 - cost_output_cny / sell_output, 4) END)
    END AS margin_ratio
FROM cny;

CREATE VIEW v_admin_model_summary AS
SELECT vm.id AS virtual_model_id,
    EXISTS (SELECT 1 FROM price_books pb WHERE pb.kind = 'sell' AND pb.virtual_model_id = vm.id
            AND pb.effective_from <= now() AND (pb.effective_to IS NULL OR pb.effective_to > now())) AS has_sell_price,
    EXISTS (SELECT 1 FROM virtual_model_metadata md WHERE md.virtual_model_id = vm.id) AS has_metadata,
    (SELECT count(*) FROM channels c WHERE c.virtual_model_id = vm.id) AS channel_count,
    (SELECT count(*) FROM channels c WHERE c.virtual_model_id = vm.id AND c.status = 'active') AS active_channel_count,
    (SELECT min(m.margin_ratio) FROM v_admin_channel_margin m WHERE m.virtual_model_id = vm.id AND m.status = 'active') AS min_margin_ratio
FROM virtual_models vm;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP VIEW IF EXISTS v_admin_model_summary;
DROP VIEW IF EXISTS v_admin_channel_margin;
-- +goose StatementEnd
