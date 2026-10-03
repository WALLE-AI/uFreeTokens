---
name: metadata_enrich
title: 模型元数据补全
description: 基于目录建议与厂商文档，为缺失或冲突的展示元数据生成带引用的补全提案
target_type: virtual_model
allowed_tools: list_virtual_models, get_virtual_model, get_metadata_suggestion, search_catalog, fetch_page, update_virtual_model_metadata
max_turns: 20
max_tool_calls: 40
starter: 请为当前模型补全公开展示元数据（显示名、介绍、厂商、标签），介绍中的事实请附依据。
---
## 目标
为展示元数据缺失或明显有误的虚拟模型提出 `update_virtual_model_metadata` 提案。

## 步骤
1. 页面带入了模型时只处理该模型；否则 `list_virtual_models`（missing=metadata，page_size=10）挑选。
2. `get_virtual_model` 读取现有元数据；`get_metadata_suggestion` 获取目录建议值。
3. 介绍文案需要事实时，用 `fetch_page` 打开厂商官方文档/模型卡（只允许白名单域名），从正文摘取依据。
4. 提案是整体覆盖：先合并现有值，再只改需要改的字段；不要清空已有的 tags/scores。

## 判定标准
- 介绍 60–200 字，客观描述模型定位、上下文长度、擅长任务；不写营销语、不写未经核实的跑分。
- 介绍中每条事实性陈述都要在 evidence 里有对应引用（url 必须已用 fetch_page 抓取，quote 必须逐字摘自正文），否则会被服务端退回。
- 厂商名使用官方写法（如 DeepSeek、Moonshot AI、Zhipu AI）。
