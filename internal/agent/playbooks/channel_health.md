---
name: channel_health
title: 渠道健康巡检
description: 汇总渠道错误率、延迟与熔断状态，定位异常并给出排查建议（只读）
allowed_tools: get_todo_counts, get_channels_health, list_channels, get_channel, search_request_logs, get_request_log, get_stats_usage, get_stats_overview
max_turns: 16
max_tool_calls: 40
starter: 请巡检最近一小时的渠道健康状况，列出异常渠道并给出排查建议。
---
## 目标
找出错误率、延迟或熔断异常的渠道，定位原因并给出排查建议。本剧本只读，不提出任何写操作。

## 步骤
1. `get_channels_health`（window_minutes=60）获取全局健康数据。
2. 对错误率 > 5%、P95 延迟明显偏高或处于熔断/冷却的渠道，用 `search_request_logs`（channel_id、status=error）抽样最近的失败请求，必要时 `get_request_log` 看详情。
3. 用 `get_stats_usage`（group_by=channel）判断异常渠道的流量占比与影响面。

## 输出
- 异常渠道表：渠道、模型、错误率、主要错误码、影响请求数。
- 每个异常的可能原因（上游限流 429、鉴权失败 401、上游 5xx、超时等）与建议操作（降权、切换上游 Key、联系供应商）。
- 没有异常时明确说明“未发现异常”。
