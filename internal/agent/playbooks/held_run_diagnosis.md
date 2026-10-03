---
name: held_run_diagnosis
title: 扣留运行诊断
description: 解释基准测试导入被发布闸门扣留的原因，提案发布或给出丢弃建议
target_type: benchmark_run
allowed_tools: get_benchmark_run, list_model_aliases, search_catalog, publish_benchmark_run
max_turns: 12
max_tool_calls: 30
starter: 请诊断这次被扣留的基准测试运行，说明闸门原因并给出发布或丢弃的建议。
---
## 目标
解释一次基准测试运行为什么被发布闸门扣留为草稿（行数骤降、大面积分数漂移、映射缺失），判断是数据源异常还是真实变化。

## 步骤
1. `get_benchmark_run` 读取运行详情与闸门原因。
2. 行数骤降：检查是否大量模型映射失效（`list_model_aliases` status=unmatched）。
3. 分数漂移：判断是榜单方法论变化（全体平移）还是抓取错位（个别列错乱）。

## 判定
- 真实变化（榜单更新、方法论调整且有公告）→ 提出 `publish_benchmark_run`，rationale 说明依据。
- 数据异常 → 不提案，说明原因并建议在后台丢弃该运行、修复数据源。
