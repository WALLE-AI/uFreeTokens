-- Price Book + Price Component：售价挂虚拟模型，成本挂渠道，价格版本化。
-- 见技术方案 §6.4。

-- +goose Up
-- +goose StatementBegin
CREATE TABLE price_books (
    id               BIGSERIAL PRIMARY KEY,
    kind             TEXT NOT NULL CHECK (kind IN ('sell','cost')),
    virtual_model_id BIGINT REFERENCES virtual_models(id),
    channel_id       BIGINT REFERENCES channels(id),
    tier             TEXT,
    currency         TEXT NOT NULL,
    effective_from   TIMESTAMPTZ NOT NULL,
    effective_to     TIMESTAMPTZ,
    created_by       BIGINT,
    note             TEXT,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK ((kind = 'sell' AND virtual_model_id IS NOT NULL AND channel_id IS NULL)
        OR (kind = 'cost' AND channel_id IS NOT NULL AND virtual_model_id IS NULL))
);
CREATE INDEX idx_price_books_sell ON price_books (virtual_model_id, tier, effective_from) WHERE kind = 'sell';
CREATE INDEX idx_price_books_cost ON price_books (channel_id, effective_from) WHERE kind = 'cost';
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE price_components (
    id                BIGSERIAL PRIMARY KEY,
    price_book_id     BIGINT NOT NULL REFERENCES price_books(id) ON DELETE CASCADE,
    meter             TEXT NOT NULL,
    unit              TEXT NOT NULL CHECK (unit IN ('per_1m_tokens','per_request','per_image','per_second')),
    service_tier      TEXT NOT NULL DEFAULT 'default',
    tier_min_input    INT NOT NULL DEFAULT 0,
    tier_max_input    INT,
    window_start_min  SMALLINT,
    window_end_min    SMALLINT,
    unit_price        NUMERIC(20,10) NOT NULL,
    UNIQUE (price_book_id, meter, service_tier, tier_min_input, window_start_min)
);
CREATE INDEX idx_price_components_book ON price_components (price_book_id);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE fx_rates (
    base           TEXT NOT NULL,
    quote          TEXT NOT NULL,
    rate           NUMERIC(12,6) NOT NULL,
    source         TEXT NOT NULL,
    effective_date DATE NOT NULL,
    PRIMARY KEY (base, quote, effective_date)
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS fx_rates;
DROP TABLE IF EXISTS price_components;
DROP TABLE IF EXISTS price_books;
-- +goose StatementEnd
