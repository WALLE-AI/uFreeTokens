---
name: listing_triage
title: 待上架模型处理
description: 核对待上架模型的元数据完整度、价格与渠道健康，提案发布或忽略
target_type: pending_listing
allowed_tools: get_todo_counts, list_pending_listings, lookup_virtual_model, search_catalog, list_virtual_models, get_virtual_model, reference_price_lookup, get_channels_health, list_upstream_offers, fetch_page, publish_listing, dismiss_listing
max_turns: 24
max_tool_calls: 60
starter: 请处理当前待上架的模型：逐个判断应发布还是忽略，并提出提案。
---
## 目标
对 `pending` 状态的待上架模型逐个判断：值得上架的提出 `publish_listing`，不应上架的提出 `dismiss_listing`。

## 步骤
1. `list_pending_listings`（status=pending）拿到清单；页面带入了具体条目时只处理那几条。
2. 对每个上游模型：`lookup_virtual_model` / `search_catalog` 检查平台是否已有同名或同一模型（已有时发布只会新增渠道）。
3. `reference_price_lookup` 核对价格是否合理；`origin=free_offer` 的免费模型要核实免费条款（限流、有效期、是否需绑卡），可用 `list_upstream_offers` 或 `fetch_page` 读取供应商公告。
4. 发布提案需要填写：`provider_account_id`（取自待上架记录）、`virtual_model` 的 type / context_window / max_output / capabilities（不确定的能力不要写）、`sell_markup`（默认 0.2；免费模型为 0）。

## 判定标准
- **忽略**：测试/内部/已废弃模型（名称含 test、preview-internal、deprecated 等）、与已上架模型完全重复且无价格优势、免费条款不明确或需要绑卡。
- **发布**：正式发布的模型，价格可核实，能力可确定。
- 涉及外部网页事实（免费期限、能力）时附 evidence。

## 输出
汇总表：编号、上游模型、来源、建议（发布/忽略）、理由。
