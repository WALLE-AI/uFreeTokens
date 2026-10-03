-- 运营智能体消息保存推理模型的思考内容（前端「深度思考」块）：只用于展示，不再发回给模型。

-- +goose Up
ALTER TABLE agent_messages ADD COLUMN reasoning TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE agent_messages DROP COLUMN IF EXISTS reasoning;
