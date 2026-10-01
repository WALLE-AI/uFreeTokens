-- virtual_model_metadata.scores 键名统一为 snake_case（docs/基准测试与排行榜数据服务技术方案.md
-- §3.1，阶段 0）。此前运营后台按 web 的 camelCase 写入（intelligenceIndex 等），后端现在按
-- 白名单只接受 intelligence_index / coding_index / agentic_index / design_arena.*；这里把已有
-- 数据改写过来，避免旧数据在 web 端读不到、在后台再次保存时被 400 拒绝。
-- designArena 的子键同样改写（codeCategories→code、uiComponent→ui_component……）。

-- +goose Up
-- +goose StatementBegin
CREATE FUNCTION pg_temp.rename_keys(obj JSONB, mapping JSONB) RETURNS JSONB LANGUAGE sql AS $$
    SELECT COALESCE(jsonb_object_agg(COALESCE(mapping ->> e.key, e.key), e.value), '{}'::jsonb)
    FROM jsonb_each(obj) e
$$;

UPDATE virtual_model_metadata m
SET scores = s.fixed, updated_at = now()
FROM (
    SELECT virtual_model_id,
        CASE WHEN r ? 'design_arena' AND jsonb_typeof(r -> 'design_arena') = 'object'
            THEN jsonb_set(r, '{design_arena}', pg_temp.rename_keys(r -> 'design_arena',
                '{"codeCategories":"code","uiComponent":"ui_component","gameDev":"game_dev","dataViz":"data_viz","threeD":"three_d"}'))
            ELSE r END AS fixed
    FROM (
        SELECT virtual_model_id, pg_temp.rename_keys(scores,
            '{"intelligenceIndex":"intelligence_index","codingIndex":"coding_index","agenticIndex":"agentic_index","designArena":"design_arena"}') AS r
        FROM virtual_model_metadata
        WHERE scores IS NOT NULL AND jsonb_typeof(scores) = 'object'
    ) renamed
) s
WHERE m.virtual_model_id = s.virtual_model_id AND m.scores IS DISTINCT FROM s.fixed;
-- +goose StatementEnd

-- +goose Down
-- 数据修正不回滚：snake_case 是唯一合法写法，web 端也兼容读取旧键。
SELECT 1;
