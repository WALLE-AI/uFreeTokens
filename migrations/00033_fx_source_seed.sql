-- 汇率确定性采集（fetcher=fx_rate，实施方案 M1-B09 / 决策 D2）：种子一个免密钥的结构化源，每日一次。
-- 与库中最新汇率偏离 > 2% 时整批拒收、不写入（见 internal/fxsync），人工核实后手工录入。

-- +goose Up
-- +goose StatementBegin
INSERT INTO price_sources (domain, name, level, kind, fetcher, url, schedule, enabled, public_display, auto_publish, attribution, config)
SELECT 'price', '汇率 USD→CNY（ExchangeRate-API 开放接口）', 'L2', 'api', 'fx_rate', 'https://open.er-api.com/v6/latest/USD', '@daily', true,
       false, false, 'ExchangeRate-API',
       '{"base": "USD", "quotes": ["CNY"], "max_deviation": 0.02}'::jsonb
WHERE NOT EXISTS (SELECT 1 FROM price_sources WHERE fetcher = 'fx_rate');
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DELETE FROM price_sources ps WHERE fetcher = 'fx_rate' AND url = 'https://open.er-api.com/v6/latest/USD'
  AND NOT EXISTS (SELECT 1 FROM data_source_runs r WHERE r.source_id = ps.id);
-- +goose StatementEnd
