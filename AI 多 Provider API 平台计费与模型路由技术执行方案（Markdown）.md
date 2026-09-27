# AI 多 Provider API 平台技术执行方案

> **版本：V1.0**
>
> **定位：OpenRouter + SiliconFlow + 火山方舟 + LiteLLM + OneAPI**
>
> **目标：**
>
> 构建一个支持 **40+ Provider、100+ AI 模型、1000+ 用户、多租户、统一 OpenAI API** 的 AI API 聚合平台。

---

# 一、整体架构

```text
                            User
                              │
                Web Console / SDK(OpenAI)
                              │
                Authorization: Bearer sk-user-xxxx
                              │
                         API Gateway
                              │
 ┌────────────────────────────────────────────────────┐
 │ Authentication                                    │
 │ User API Key                                      │
 │ Wallet                                             │
 │ Rate Limit                                         │
 │ Permission                                          │
 │ Billing Admission                                  │
 └────────────────────────────────────────────────────┘
                              │
                       Request Session
                              │
                       Model Router
                              │
                Capability Matcher
                              │
                  Provider Scheduler
                              │
                   Provider Key Pool
                              │
              OpenAI / DeepSeek / Claude /
               Gemini / Qwen / SiliconFlow...
```

---

# 二、核心设计思想

平台内部存在四层对象。

```
Provider

↓

Provider API Key

↓

Virtual Model

↓

User API Key
```

注意：

**用户永远不知道真实 Provider API Key。**

例如：

```
User

↓

sk-user-xxxx

↓

deepseek-v4-flash

↓

Router

↓

SiliconFlow

↓

Provider Key 12

↓

DeepSeek API
```

---

# 三、系统模块划分

整个系统建议拆成 12 个微服务。

```
Gateway

Authentication

User Center

Wallet

API Key

Router

Provider

Billing

Pricing

Promotion

Monitoring

Admin Console
```

---

# 四、数据库设计

## 4.1 用户

```sql
users
```

|字段|说明|
|------|------|
id|主键
username|用户名
email|邮箱
password|密码
status|状态
plan_id|套餐
create_time|创建时间

---

## 4.2 钱包

```sql
wallet
```

|字段|说明|
|------|------|
user_id|用户
balance|余额
freeze_amount|冻结金额
total_recharge|累计充值
total_consume|累计消费

---

## 4.3 User API Key

```sql
user_api_key
```

|字段|说明|
|------|------|
id||
user_id||
key_hash||
quota||
rpm||
status||

说明：

数据库只保存 Hash。

---

## 4.4 Provider

```sql
provider
```

例如：

```
DeepSeek

OpenAI

Claude

Google

OpenRouter

SiliconFlow

火山

阿里
```

字段：

```
base_url

status

weight

priority

health
```

---

## 4.5 Provider API Key

```sql
provider_api_key
```

字段：

```
provider_id

secret

rpm

tpm

quota

used

latency

success_rate

429_count

health

weight

cooldown_until
```

---

## 4.6 Virtual Model

```sql
model
```

例如：

```
deepseek-v4-flash

gpt-5

claude-sonnet

qwen-max
```

---

## 4.7 Model Mapping

```
deepseek-v4-flash

↓

DeepSeek

↓

deepseek-v4-flash
```

也可以：

```
↓

SiliconFlow

↓

deepseek-v4-flash
```

---

## 4.8 Pricing

新增：

```sql
model_pricing
```

|字段|说明|
|------|------|
provider_id||
model||
input_cost||
output_cost||
profit_rate||
input_sell||
output_sell||
effective_time||

---

## 4.9 Promotion

新增：

```sql
promotion
```

用于处理：

Provider 免费模型

平台补贴

新人免费

限时免费

字段：

```
promotion_type

provider

model

start_time

end_time

quota

condition

status
```

---

## 4.10 Usage

记录一次调用。

```
request_id

user_id

provider_id

model

prompt_tokens

completion_tokens

reasoning_tokens

cost

promotion_id

create_time
```

---

## 4.11 Transaction

钱包流水。

```
Consume

Recharge

Refund

Promotion
```

---

# 五、新用户流程

## 注册

```
POST

/register
```

流程：

```
创建User

↓

创建Wallet

↓

赠送5元

↓

生成Transaction

↓

登录
```

Transaction：

```
Promotion

+5
```

---

# 六、创建API Key

```
Dashboard

↓

创建API Key

↓

生成

sk-user-xxxxx
```

数据库：

```
保存Hash
```

不保存明文。

---

# 七、模型调用流程

```
POST

/v1/chat/completions
```

Header

```
Authorization

Bearer sk-user-xxxx
```

Body

```
model

deepseek-v4-flash
```

Gateway：

```
Authentication

↓

Wallet Check

↓

Permission

↓

Rate Limit

↓

Router
```

---

# 八、Router算法

输入：

```
Virtual Model
```

例如：

```
deepseek-v4-flash
```

查：

```
Model Mapping
```

得到：

```
DeepSeek

SiliconFlow

OpenRouter
```

然后：

Capability Filter

↓

Pricing

↓

Latency

↓

Health

↓

Quota

↓

Weighted Score

↓

Provider

↓

Provider API Key

---

## Provider Score

建议：

```
Score

=

0.35 Health

+

0.25 Latency

+

0.20 Weight

+

0.10 Cost

+

0.10 RemainingQuota
```

选择最高。

---

# 九、Provider Key调度

维护：

```
RPM

TPM

Quota

Latency

429

Health

Weight
```

失败：

```
429

↓

Cooldown

↓

Retry

↓

Next Key
```

失败：

```
Provider Down

↓

Next Provider
```

---

# 十、Billing Engine

这是平台核心。

Provider返回：

```json
usage

prompt_tokens

completion_tokens

total_tokens
```

例如：

```
1250

870
```

查询：

```
model_pricing
```

得到：

```
采购价：

4.5

18
```

利润：

```
3%
```

售价：

```
4.635

18.54
```

计算：

```
Fee

=

Input

+

Output
```

写：

```
Usage

↓

Transaction

↓

Wallet
```

余额：

```
5

↓

4.978
```

---

# 十一、免费模型处理

这是平台后续最重要的部分。

例如：

DeepSeek：

```
9月

免费
```

平台不能修改代码。

因此：

增加：

```
promotion
```

例如：

```
type

Provider Free

model

deepseek-v4-flash

start

2026-09-01

end

2026-09-30
```

Billing：

```
Promotion

↓

匹配

↓

Cost=0
```

用户：

```
免费
```

平台：

```
记录Usage

不扣钱
```

但是：

统计：

```
免费调用

Token

次数
```

全部记录。

---

# 十二、支持的平台活动

不仅支持：

Provider 免费。

还支持：

```
新人注册送5元

↓

每天免费100万Token

↓

VIP免费

↓

企业补贴

↓

邀请码

↓

节假日活动
```

统一：

Promotion Engine。

---

# 十三、请求生命周期

```
Request

↓

Authentication

↓

Wallet

↓

Admission Check

↓

Router

↓

Provider

↓

Usage

↓

Billing

↓

Transaction

↓

Wallet

↓

Response
```

---

# 十四、Request Session

建议增加：

```
request_session
```

记录：

```
RequestID

User

API Key

Provider

Provider Key

Model

Retry

Latency

Status

Billing

Promotion
```

后续：

日志

审计

重放

统计

全部依赖这里。

---

# 十五、风控

建议增加：

```
IP

Device

Country

UA

Concurrent

RPM

TPM

Abnormal

Risk Score
```

例如：

```
一分钟

1000次

↓

自动封禁
```

---

# 十六、监控

建议Prometheus：

监控：

```
QPS

Latency

429

5xx

Provider

Success Rate

Cost

Profit

Token

Wallet

Promotion
```

Grafana：

展示：

```
Provider Health

Model Usage

User Ranking

Profit

Today's Cost

Today's Revenue
```

---

# 十七、后续演进路线

## Phase1

- User
- Wallet
- API Key
- Router
- Billing

---

## Phase2

增加：

- 多Provider
- Promotion
- Dynamic Pricing
- Provider Discovery

---

## Phase3

增加：

- A/B Routing
- AI Router
- Cost Optimizer
- Auto Provider Switch
- 多区域部署

---

## Phase4

增加：

- 企业租户
- Organization
- Team
- Budget
- 审批流
- 发票
- SaaS Billing

---

# 十八、最终总体架构

```text
                    User
                      │
             User API Key
                      │
                 API Gateway
                      │
    ┌────────────────────────────────────┐
    │ Authentication                     │
    │ Wallet                             │
    │ API Key                            │
    │ Permission                         │
    │ Admission Control                  │
    └────────────────────────────────────┘
                      │
              Request Session
                      │
                Model Router
                      │
             Promotion Engine
                      │
             Dynamic Pricing
                      │
            Provider Scheduler
                      │
          Provider API Key Pool
                      │
     OpenAI / DeepSeek / Claude / Gemini
                      │
             Usage + Billing
                      │
          Wallet + Transaction
                      │
                 Monitoring
```

---

# 十九、平台核心设计原则

1. **用户只接触 User API Key，永远不暴露 Provider API Key。**
2. **所有模型统一抽象为 Virtual Model，通过 Model Mapping 动态映射 Provider。**
3. **计费以 Provider 返回的实际 Token Usage 为准，不做本地估算。**
4. **价格、利润率、促销活动全部配置化，不写死在代码中。**
5. **所有请求必须具备唯一 Request Session，实现全链路可追踪、可审计。**
6. **Promotion Engine 独立于 Billing Engine，支持限时免费、额度免费、平台补贴等活动。**
7. **Router、Billing、Promotion、Wallet 解耦，便于未来扩展更多 Provider、更多模型以及企业级 SaaS 能力。**