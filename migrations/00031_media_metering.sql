-- 多模态计量（Gateway 多模态接口补全与缺陷修复技术实施方案 §2.3）：
--   price_components  新计量项 image / input_char / audio_second，新单位 per_1m_chars；
--   request_logs      记录图像张数、语音合成输入字符数、语音识别音频时长（毫秒）；
--   usage_hourly      同步汇总这三个维度；
--   v_admin_channel_margin  毛利率把多模态计量项（按 meter+unit 配对）也算进去，
--                           口径与 internal/admin/pricectx.go 的 marginRatio 一致。
-- request_logs 是分区表，父表加列后现有分区自动继承。

-- +goose Up
-- +goose StatementBegin
ALTER TABLE price_components DROP CONSTRAINT chk_price_components_meter;
ALTER TABLE price_components ADD CONSTRAINT chk_price_components_meter
    CHECK (meter IN ('input','input_cache_read','input_cache_write','output','output_reasoning','request',
                     'image','input_char','audio_second'));
ALTER TABLE price_components DROP CONSTRAINT price_components_unit_check;
ALTER TABLE price_components ADD CONSTRAINT price_components_unit_check
    CHECK (unit IN ('per_1m_tokens','per_request','per_image','per_second','per_1m_chars'));

ALTER TABLE request_logs
    ADD COLUMN image_count INT,
    ADD COLUMN input_chars INT,
    ADD COLUMN audio_ms    INT;

ALTER TABLE usage_hourly
    ADD COLUMN image_count BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN input_chars BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN audio_ms    BIGINT NOT NULL DEFAULT 0;

CREATE OR REPLACE VIEW v_admin_channel_margin AS
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
        cost.input AS cost_input, cost.output AS cost_output, pa.cost_multiplier,
        -- 多模态计量项（image/input_char/audio_second/request）按 meter+unit 配对后的最差毛利
        (SELECT min(CASE WHEN sc.unit_price = 0 THEN NULL
                         ELSE round(1 - round(cc.unit_price * (CASE WHEN cost.currency = '' OR cost.currency = 'CNY' THEN 1::numeric ELSE fx.rate END) * pa.cost_multiplier, 6) / sc.unit_price, 4) END)
           FROM price_components sc
           JOIN price_components cc ON cc.price_book_id = cost.book_id AND cc.meter = sc.meter AND cc.unit = sc.unit
                AND cc.service_tier = 'default' AND cc.tier_min_input = 0 AND cc.window_start_min IS NULL
          WHERE sc.price_book_id = sell.book_id AND sc.meter IN ('image','input_char','audio_second','request')
            AND sc.service_tier = 'default' AND sc.tier_min_input = 0 AND sc.window_start_min IS NULL) AS media_margin
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
            CASE WHEN sell_output IS NULL OR cost_output_cny IS NULL OR sell_output = 0 THEN NULL ELSE round(1 - cost_output_cny / sell_output, 4) END,
            media_margin)
    END AS margin_ratio
FROM cny;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE VIEW v_admin_channel_margin AS
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

ALTER TABLE usage_hourly DROP COLUMN image_count, DROP COLUMN input_chars, DROP COLUMN audio_ms;
ALTER TABLE request_logs DROP COLUMN image_count, DROP COLUMN input_chars, DROP COLUMN audio_ms;
DELETE FROM price_components WHERE meter IN ('image','input_char','audio_second') OR unit = 'per_1m_chars';
ALTER TABLE price_components DROP CONSTRAINT price_components_unit_check;
ALTER TABLE price_components ADD CONSTRAINT price_components_unit_check
    CHECK (unit IN ('per_1m_tokens','per_request','per_image','per_second'));
ALTER TABLE price_components DROP CONSTRAINT chk_price_components_meter;
ALTER TABLE price_components ADD CONSTRAINT chk_price_components_meter
    CHECK (meter IN ('input','input_cache_read','input_cache_write','output','output_reasoning','request'));
-- +goose StatementEnd
