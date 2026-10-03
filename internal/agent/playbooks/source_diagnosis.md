---
name: source_diagnosis
title: 数据源诊断与修复
description: 诊断连续失败或被闸门拒绝的数据源，必要时修复 HTML 选择器并经 dry-run 验证后提案
target_type: price_source
allowed_tools: list_price_sources, get_price_source, list_price_source_runs, fetch_page, dry_run_price_source, update_price_source_config, run_price_source
max_turns: 20
max_tool_calls: 40
starter: 请诊断当前失败的数据源，说明失败原因；如果是页面结构变化导致的选择器失效，请修复配置并验证。
---
## 目标
找出数据源失败的原因；对 HTML 页面结构变化导致的解析失败，推断新的选择器配置，用 `dry_run_price_source` 验证后提出 `update_price_source_config` 提案。

## 步骤
1. `list_price_sources` 找出 consecutive_failures > 0 或最近一次被拒绝（rejected）的源；页面带入了具体数据源时只处理它。
2. `list_price_source_runs` 查看错误信息：网络错误/403/429 属于访问问题；0 条或少于上次 50% 被闸门拒绝属于解析问题。
3. 解析问题：`get_price_source` 读取当前配置，`fetch_page` 打开页面观察结构，推断新的 CSS 选择器。
4. 用 `dry_run_price_source`（fetcher + 新 config）验证：样本行的模型名与价格必须正确、条数合理。验证通过才提案。
5. 访问类问题不改配置，给出处理建议（更换 URL、降低频率、联系供应商）。修复提案获批后可提出 `run_price_source` 立即重跑。

## 约束
- 绝不臆造价格；dry-run 样本与页面原文不一致时不要提案。
- 汇率与价格数据只能由确定性采集写入，本剧本只修配置。
