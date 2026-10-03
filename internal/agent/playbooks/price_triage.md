---
name: price_triage
title: 调价审批预审
description: 拉取待审/被拦截的调价，比对参考价与成本、评估毛利，逐条给出通过或驳回提案
target_type: price_change_request
allowed_tools: get_todo_counts, list_price_change_requests, get_price_change_request, preview_pricing, reference_price_lookup, get_price_comparison, list_fx_rates, get_channel, get_virtual_model, list_price_source_runs, approve_price_change, reject_price_change
max_turns: 24
max_tool_calls: 60
starter: 请预审当前所有待审批和被拦截的调价申请，逐条给出建议并提出审批提案。
---
## 目标
对 `pending` 与 `blocked` 状态的调价申请逐条判断是否应当通过，并为每一条提出 `approve_price_change` 或 `reject_price_change` 提案。

## 步骤
1. `list_price_change_requests`（status=pending,blocked，page_size=50）拿到清单；页面带入了具体调价时只处理那几条。
2. 对每一条调用 `get_price_change_request` 读取新旧价格分量、来源级别（L1–L5）与影响评估。
3. 用 `reference_price_lookup` 查询对应上游模型的公开参考价（USD/1M tokens）；必要时 `get_price_comparison` 看其他渠道。
4. 涉及币种换算时用 `list_fx_rates`；判断售价侧影响时用 `preview_pricing` 试算毛利。
5. 逐条提出提案，rationale 用 2–3 句话写清依据，confidence 反映把握程度。

## 判定标准
- **建议通过**：新成本价与至少一个外部参考价一致（偏差 ≤ 5%），或来源为 L1/L2 官方价格；调价后毛利仍为正。
- **建议驳回**：参考价未变化而观测价大幅变动（疑似把缓存价/批量价读成输入价、单位错误）；来源为 L4/L5 单次观测且无旁证；调价会导致负毛利且无售价调整计划。
- `blocked`（变化超过阈值）的申请如要通过，必须设置 `confirm_blocked=true`，并在 rationale 中说明为何确认，confidence 不得高于 0.8。
- 拿不准时宁可驳回并写明“需人工核实”，不要猜测。

## 输出
最后用表格汇总：编号、模型/渠道、变化幅度、建议、理由（一句话）。
