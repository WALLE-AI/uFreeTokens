---
name: alias_matching
title: 榜单模型映射
description: 判断外部榜单中待确认/未匹配的模型名是否对应平台虚拟模型，提案确认、忽略或改链
target_type: model_alias
allowed_tools: list_model_aliases, search_catalog, list_virtual_models, get_virtual_model, lookup_virtual_model, set_model_alias
max_turns: 24
max_tool_calls: 80
starter: 请处理榜单模型映射中待确认（suggested）与未匹配（unmatched）的条目，并提出映射提案。
---
## 目标
对 `suggested` 与 `unmatched` 的榜单模型名逐条判断是否为平台上的同一个模型，提出 `set_model_alias` 提案。

## 步骤
1. `list_model_aliases`（先 status=suggested，再 status=unmatched，page_size=50）。
2. 对每条外部名称用 `search_catalog` 检索候选虚拟模型，必要时 `get_virtual_model` 核对家族、版本与上下文。

## 判定规则
- **版本号必须一致**：`gpt-5.1` ≠ `gpt-5`；`qwen3-235b-a22b-2507` 与 `qwen3-235b-a22b` 视为不同版本（日期后缀代表新快照），除非平台只上架了其中一个且榜单无另一个。
- **推理档位后缀归并**：`-high` / `-medium` / `-low` / `-thinking` / `(reasoning)` 是同一模型的推理档位，映射到同一虚拟模型。
- **日期后缀**：`-2025-08-07` 这类快照日期在平台未单独上架快照时，映射到主版本。
- **参数规模必须一致**：`70b` ≠ `8b`。
- 平台确实没有该模型 → status=ignored；无法确定 → 不提案，在汇总中列出“需人工判断”。
- confirmed 提案给出 `virtual_model`（名称）或 `virtual_model_id`，rationale 写明匹配依据。
