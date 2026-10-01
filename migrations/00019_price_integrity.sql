-- 价格版本完整性（B6 数据一致性），见
-- docs/运营后台接口与数据库设计问题分析及执行方案.md §4.4。
--
-- 此前发布新价格只是插入一本 effective_to 为 NULL 的新版本，旧版本也一直是
-- NULL——同一虚拟模型/渠道的多本价格在时间上全部重叠，只靠"取最新一本"的
-- 查询约定来决定生效价格。现在：
--   - 发布新版本时把上一本的 effective_to 设为新版本的 effective_from
--     （internal/admin.setPrice），形成首尾相接的版本链；
--   - 数据库用排他约束保证同一条链上的生效区间不重叠；
--   - effective_to = effective_from 表示"生效前就被取代"（空区间）。
-- 本迁移先按同样规则修复历史数据，再加约束。

-- +goose Up
-- +goose StatementBegin
CREATE EXTENSION IF NOT EXISTS btree_gist;

-- 修复历史重叠：每条链按 (effective_from, id) 排序，每本的 effective_to 不晚于下一本的 effective_from。
WITH ordered AS (
    SELECT id, effective_from, effective_to,
           lead(effective_from) OVER (
               PARTITION BY kind, virtual_model_id, channel_id, COALESCE(tier, '')
               ORDER BY effective_from, id) AS next_from
    FROM price_books
)
UPDATE price_books pb SET effective_to = o.next_from
FROM ordered o
WHERE pb.id = o.id AND o.next_from IS NOT NULL
  AND (o.effective_to IS NULL OR o.effective_to > o.next_from);

ALTER TABLE price_books ADD CONSTRAINT chk_price_books_effective_range
    CHECK (effective_to IS NULL OR effective_to >= effective_from);
ALTER TABLE price_books ADD CONSTRAINT ex_price_books_sell_overlap EXCLUDE USING gist (
    virtual_model_id WITH =, (COALESCE(tier, '')) WITH =, tstzrange(effective_from, effective_to) WITH &&
) WHERE (kind = 'sell');
ALTER TABLE price_books ADD CONSTRAINT ex_price_books_cost_overlap EXCLUDE USING gist (
    channel_id WITH =, tstzrange(effective_from, effective_to) WITH &&
) WHERE (kind = 'cost');
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE price_components ADD CONSTRAINT chk_price_components_unit_price CHECK (unit_price >= 0) NOT VALID;
ALTER TABLE price_components ADD CONSTRAINT chk_price_components_tier_range
    CHECK (tier_min_input >= 0 AND (tier_max_input IS NULL OR tier_max_input > tier_min_input)) NOT VALID;
ALTER TABLE price_components ADD CONSTRAINT chk_price_components_window CHECK (
    (window_start_min IS NULL AND window_end_min IS NULL)
    OR (window_start_min BETWEEN 0 AND 1439 AND window_end_min BETWEEN 1 AND 1440)
) NOT VALID;
ALTER TABLE price_components ADD CONSTRAINT chk_price_components_meter CHECK (
    meter IN ('input','input_cache_read','input_cache_write','output','output_reasoning','request')
) NOT VALID;
ALTER TABLE price_components VALIDATE CONSTRAINT chk_price_components_unit_price;
ALTER TABLE price_components VALIDATE CONSTRAINT chk_price_components_tier_range;
ALTER TABLE price_components VALIDATE CONSTRAINT chk_price_components_window;
ALTER TABLE price_components VALIDATE CONSTRAINT chk_price_components_meter;
ALTER TABLE price_components DROP CONSTRAINT price_components_price_book_id_meter_service_tier_tier_min__key;
ALTER TABLE price_components ADD CONSTRAINT uq_price_components_slot
    UNIQUE NULLS NOT DISTINCT (price_book_id, meter, service_tier, tier_min_input, window_start_min);
-- +goose StatementEnd

-- 用户分组字典：此前 tier 是散落在 accounts / price_books / visible_tiers /
-- allowed_tiers 里的自由文本。
-- +goose StatementBegin
CREATE TABLE tiers (
    code TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    sort INT  NOT NULL DEFAULT 0
);
INSERT INTO tiers (code, name, sort) VALUES ('free', '免费', 10), ('pro', '专业', 20), ('enterprise', '企业', 30);
INSERT INTO tiers (code, name, sort)
SELECT DISTINCT t, t, 100 FROM (
    SELECT tier AS t FROM accounts UNION SELECT tier FROM price_books WHERE tier IS NOT NULL
) x WHERE t IS NOT NULL ON CONFLICT DO NOTHING;
ALTER TABLE accounts    ADD CONSTRAINT fk_accounts_tier    FOREIGN KEY (tier) REFERENCES tiers(code);
ALTER TABLE price_books ADD CONSTRAINT fk_price_books_tier FOREIGN KEY (tier) REFERENCES tiers(code);
CREATE INDEX IF NOT EXISTS idx_price_change_requests_current_book ON price_change_requests (current_book_id) WHERE current_book_id IS NOT NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_price_change_requests_current_book;
ALTER TABLE price_books DROP CONSTRAINT IF EXISTS fk_price_books_tier;
ALTER TABLE accounts DROP CONSTRAINT IF EXISTS fk_accounts_tier;
DROP TABLE IF EXISTS tiers;
ALTER TABLE price_components DROP CONSTRAINT IF EXISTS uq_price_components_slot;
ALTER TABLE price_components ADD CONSTRAINT price_components_price_book_id_meter_service_tier_tier_min__key
    UNIQUE (price_book_id, meter, service_tier, tier_min_input, window_start_min);
ALTER TABLE price_components DROP CONSTRAINT IF EXISTS chk_price_components_meter;
ALTER TABLE price_components DROP CONSTRAINT IF EXISTS chk_price_components_window;
ALTER TABLE price_components DROP CONSTRAINT IF EXISTS chk_price_components_tier_range;
ALTER TABLE price_components DROP CONSTRAINT IF EXISTS chk_price_components_unit_price;
ALTER TABLE price_books DROP CONSTRAINT IF EXISTS ex_price_books_cost_overlap;
ALTER TABLE price_books DROP CONSTRAINT IF EXISTS ex_price_books_sell_overlap;
ALTER TABLE price_books DROP CONSTRAINT IF EXISTS chk_price_books_effective_range;
-- 历史 effective_to 的修复不回滚：它只是把隐含的"被下一本取代"写成显式值。
-- +goose StatementEnd
