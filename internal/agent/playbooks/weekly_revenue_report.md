---
name: weekly_revenue_report
title: 经营周报
description: 上周收入、成本、毛利的构成与环比，模型/供应商/账户贡献与异常，生成带图表的经营周报（只读，产出报表）
allowed_tools: query_analytics, get_dataset, render_chart, create_report, get_todo_counts
max_turns: 18
max_tool_calls: 30
starter: 请生成上周（周一至周日）的经营周报：收入、成本、毛利与环比，按模型与供应商的构成，Top 账户，并保存为报表。
---
## 目标
生成一份「经营周报」报表：上一个完整自然周（周一至周日，运营时区）的经营结果、构成变化与异常。本剧本只读，只产出报表。

## 步骤
1. `query_analytics`：subject=usage，from=上周一、to=上周日，metrics=revenue, cost, gross_profit, gross_margin, requests, active_accounts，compare=previous_period。
2. `query_analytics`：同一时间范围，interval=day，metrics=revenue, gross_profit（日趋势）。
3. `query_analytics`：group_by=virtual_model，metrics=revenue, gross_profit, gross_margin，top=10，compare=previous_period。
4. `query_analytics`：group_by=provider，metrics=revenue, cost, gross_margin，top=10。
5. `query_analytics`：group_by=account，metrics=revenue, requests，top=10，compare=previous_period。
6. 有 account:read 权限时，`query_analytics` subject=wallet，interval=none，同一时间范围，compare=previous_period（充值与赠金）；无权限（403）则跳过并在报表中注明。
7. `create_report`，标题「经营周报 YYYY-MM-DD ~ YYYY-MM-DD」，按顺序：
   - markdown：结论（收入、毛利、毛利率的环比，增长/下滑的主要来源，引用 *_change 值）；
   - chart：kpi（第 1 步，y=revenue, gross_profit, gross_margin, active_accounts）；
   - chart：line（第 2 步，x=bucket，y=revenue）；
   - chart：bar（第 3 步，x=label，y=revenue）；
   - chart：pie（第 4 步，x=label，y=revenue）；
   - chart：table（第 5 步）；
   - chart：kpi（第 6 步，如有）；
   - markdown：异常与建议（毛利率为负或明显下滑的模型、集中度过高的账户等）。

## 输出
报表生成后，用两三句话告诉用户结论和报表编号（「报表 #N」）。
