---
name: daily_ops_report
title: 运营日报
description: 汇总昨日用量、收入、毛利、错误率与待办，生成带图表的运营日报（只读，产出报表）
allowed_tools: get_todo_counts, get_channels_health, query_analytics, get_dataset, render_chart, create_report
max_turns: 16
max_tool_calls: 30
starter: 请生成昨天的运营日报：核心指标与环比、收入 Top 模型、错误率异常的渠道、今日待办，并保存为报表。
---
## 目标
生成一份「运营日报」报表：昨天（运营时区的自然日）的核心指标、变化与需要关注的问题。本剧本只读，只产出报表，不提出任何写操作。

## 步骤
1. `query_analytics`：subject=usage，from=昨天、to=昨天，metrics=requests, revenue, cost, gross_profit, gross_margin, error_rate, active_accounts，compare=previous_period（与前一天对比）。
2. `query_analytics`：group_by=virtual_model，metrics=revenue, requests, gross_margin，top=10，from/to 同上，compare=previous_period。
3. `query_analytics`：group_by=channel，metrics=requests, error_rate, p95_latency_ms，order_by=error_rate，top=10，from/to 同上。
4. `query_analytics`：不分组，interval=day，近 14 天，metrics=revenue, gross_profit（趋势图用）。
5. `get_todo_counts` 获取今日待办；`get_channels_health`（window_minutes=1440）补充熔断/冷却状态。
6. `create_report`，标题「运营日报 YYYY-MM-DD」，按顺序：
   - markdown：三句话以内的结论（收入、毛利率、错误率较前一天的变化，引用数据集中的 *_change 值）；
   - chart：kpi（第 1 步的数据集，y=revenue, gross_profit, gross_margin, error_rate）；
   - chart：line（第 4 步的数据集，x=bucket，y=revenue）；
   - chart：bar（第 2 步的数据集，x=label，y=revenue）；
   - chart：table（第 3 步的数据集）；
   - markdown：需要关注的问题与今日待办（错误率 > 5% 的渠道、待审调价、失败数据源等）。

## 输出
报表生成后，用两三句话告诉用户结论和报表编号（「报表 #N」）。数据缺失时在报表里如实说明，不要编造。
