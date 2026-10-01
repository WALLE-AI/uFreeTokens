-- 免费模型直通"待上架"（docs/外部数据采集模块（价格情报与评测榜单）技术方案.md §3.4）：
--   observed_meta  来源接口里随价格给出的模型参数（上下文、最大输出、能力、模态），预填上架表单；
--   origin         price_source = 价格源"新模型发现"；free_offer = 优惠识别出的免费模型；
--   offer_id       免费模型对应的 upstream_offers 情报（免费结束 -> 情报 expired -> 自动下线）；
--   attached       上架方式：false = 新建虚拟模型；true = 作为渠道挂到已有虚拟模型（只降成本，不改售价）；
--   retired_at     上游免费结束后系统自动停用渠道的时间；
--   status 增加 expired：还没上架、上游就已不再免费的候选。

-- +goose Up
-- +goose StatementBegin
ALTER TABLE pending_model_listings
    ADD COLUMN observed_meta JSONB,
    ADD COLUMN origin        TEXT NOT NULL DEFAULT 'price_source' CHECK (origin IN ('price_source','free_offer')),
    ADD COLUMN offer_id      BIGINT REFERENCES upstream_offers(id),
    ADD COLUMN attached      BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN retired_at    TIMESTAMPTZ;
ALTER TABLE pending_model_listings DROP CONSTRAINT pending_model_listings_status_check;
ALTER TABLE pending_model_listings ADD CONSTRAINT pending_model_listings_status_check
    CHECK (status IN ('pending','dismissed','published','expired'));
CREATE INDEX idx_pending_model_listings_offer ON pending_model_listings (offer_id) WHERE offer_id IS NOT NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
UPDATE pending_model_listings SET status = 'dismissed' WHERE status = 'expired';
ALTER TABLE pending_model_listings DROP CONSTRAINT pending_model_listings_status_check;
ALTER TABLE pending_model_listings ADD CONSTRAINT pending_model_listings_status_check
    CHECK (status IN ('pending','dismissed','published'));
DROP INDEX IF EXISTS idx_pending_model_listings_offer;
ALTER TABLE pending_model_listings
    DROP COLUMN IF EXISTS retired_at, DROP COLUMN IF EXISTS attached, DROP COLUMN IF EXISTS offer_id,
    DROP COLUMN IF EXISTS origin, DROP COLUMN IF EXISTS observed_meta;
-- +goose StatementEnd
