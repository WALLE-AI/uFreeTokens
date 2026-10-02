# Gateway 模型接口 Python 测试

用真实上游供应商端到端测试 `cmd/gateway` 的 `/v1/*` 模型接口。支持 SiliconFlow（默认）、OpenRouter、阿里云百炼、火山方舟，用环境变量 `UFT_PROVIDER` 选择，档案见 `providers.py`，兼容性结论见仓库根目录《多供应商接口兼容性评估》。方案见仓库根目录
`Gateway 模型接口 Python 测试方案（SiliconFlow）.md`。

## 两层测试

| 层 | 标记 | 目标 | 作用 |
|---|---|---|---|
| L0 | `direct` | 直连所选供应商（`providers.py` 中的 base_url） | 确认上游 Key、模型名、请求格式本身没问题 |
| L1 | `gateway` | 本地 `http://localhost:8080/v1` | 鉴权、路由、模型名改写、流式、计费 |

同名用例 L0 过、L1 挂 → 网关问题；L0 也挂 → 上游/配置问题。

档案里某种能力的模型为 `None`（该供应商不支持，或网关尚未适配）时，相关用例自动跳过。
档案的 `direct_unsupported` 列出上游**直连**不是 OpenAI 形状、由网关方言 / codec 适配的能力（如百炼的图像、语音），
这些能力只跳过 L0 直连用例，L1 网关用例照常执行——它们正是方言适配是否生效的验收。

`onboard.py` 会给上游账号绑定同名的方言预设（`internal/dialect/presets/<code>.json`），也可以在档案里用 `dialect` 指定。
供应商接入的完整流程见 `docs/供应商接入手册.md`。

```bash
UFT_PROVIDER=openrouter python onboard.py && UFT_PROVIDER=openrouter pytest -v
```

## 运行

```bash
./scripts/dev-all.sh start                  # 仓库根目录，先拉起本地栈
cd tests/gateway_py
pip install -r requirements.txt
cp .env.example .env                        # 填 SILICONFLOW_API_KEY、UFT_API_KEY
python onboard.py                           # 接入 SiliconFlow + 导入 7 个测试模型（按类型定价），可重复执行；
                                            # 已存在的模型会被校正（补能力、补多模态价格、补渠道参数）

pytest -v                                   # 全部
pytest -v -m gateway                        # 只跑网关层
pytest -v -m "not paid"                     # 跳过会产生上游费用的用例
pytest -v -m stream                         # 只跑流式用例
pytest -v --junitxml=report.xml             # 输出 JUnit 报告

# 其它供应商（.env 里填 OPENROUTER_API_KEY / DASHSCOPE_API_KEY / ARK_API_KEY）
for p in siliconflow openrouter dashscope; do
  UFT_PROVIDER=$p python onboard.py && UFT_PROVIDER=$p pytest -q
done
```

> Windows 下中文输出乱码时加 `PYTHONIOENCODING=utf-8`。

## 文件

| 文件 | 覆盖 |
|---|---|
| `onboard.py` | 通过 admin API 接入供应商/账号/密钥，dry_run 预览后导入模型，等网关 `/v1/models` 可见 |
| `test_chat.py` | 文本非流式 / 流式 / include_usage / TTFT / tool calling / max_tokens / 错误码 / openai SDK |
| `test_vision.py` | VLM：公网 URL、base64、流式、多图、大图、请求体上限、`/v1/messages` 图片 |
| `test_messages.py` | Anthropic 兼容：非流式、流式事件顺序、system、多轮、鉴权头、tools、anthropic SDK |
| `test_embeddings.py` | 单条 / 批量 / 语义 / base64 / usage / 类型校验 / openai SDK |
| `test_rerank.py` | 重排序：排序结果、id/model 改写、参数校验、类型校验 |
| `test_images.py` | 图像生成：OpenAI 形状、openai SDK、张数上限 |
| `test_audio.py` | TTS（短音色名、流式）+ ASR（multipart、回灌识别、openai SDK、415、类型校验） |
| `test_billing.py` | 余额扣减、先扣赠送、失败不扣费、X-Request-Id、流式中途断开、按张 / 按字符 / 按秒计费 |

## xfail 约定

目前没有 xfail 用例。以后发现网关缺陷或未实现的接口时，按下面的约定登记：

- `not_implemented`：网关尚未实现的接口。每个接口两条用例：一条断言**现状**，一条
  `xfail(strict=True)` 按**目标行为**断言。接口实现后目标用例会变成 XPASS 并让测试失败，提醒去掉 xfail。
- `known_defect`：已确认的网关缺陷/限制，同样是 `xfail(strict=True)`，修复后会 XPASS。

第 1 轮发现的缺陷与缺口已全部修复，见 `REPORT.md`。

## 模型

在 `config.py` 的 `MODELS` 里集中维护；换模型后重跑 `onboard.py`。
