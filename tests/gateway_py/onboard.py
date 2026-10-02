"""把上游供应商（UFT_PROVIDER，见 providers.py）接入本地网关，并导入测试用模型。可重复执行。

步骤与 tools/devseed/main.go 一致，走 cmd/admin 的接口：
  1. 供应商 code=UFT_PROVIDER（已存在则复用）
  2. 上游账号 base_url=档案里的 base_url（已存在则复用）
  3. 上游密钥（按 last4 判断是否已添加）
  4. GET upstream-models 核对测试模型都在上游列表里
  5. import-models：先 dry_run 预览，再对 status != listed 的条目正式导入
  6. 对已存在的模型做校正：补齐能力（audio 的 tts/asr）、售价/成本价缺少必需计量项时
     重新发布、渠道 param_overrides 补齐（语音合成的音色前缀指令）
导入后网关每 10 秒刷新目录快照，脚本最后会轮询 GET /v1/models 直到模型可见。

用法：UFT_PROVIDER=siliconflow python onboard.py
"""
import sys
import time

import httpx

import config

CODE = config.PROVIDER

# 每个供应商导入哪些模型、什么类型、怎么定价，写在 providers.py 的 items 里。
ITEMS = [i for i in config.PROFILE["items"] if config.MODELS.get(i["kind"])]
MARKUP_PERCENT = "30"


class Admin:
    def __init__(self):
        self.c = httpx.Client(base_url=config.ADMIN_URL, timeout=60,
                              headers={"Authorization": "Bearer " + config.ADMIN_TOKEN})

    def call(self, method, path, **kw):
        r = self.c.request(method, path, **kw)
        if r.status_code >= 400:
            sys.exit("admin %s %s -> %d: %s" % (method, path, r.status_code, r.text[:500]))
        return r.json() if r.content else None


def step(msg):
    print("==> " + msg)


def import_item(i):
    it = {"upstream_model": config.MODELS[i["kind"]], "family": i["family"], "type": i["type"],
          "context_window": i["ctx"], "max_output": i["out"], "capabilities": i["caps"]}
    if "components" in i:
        it["cost_components"] = [{"meter": m, "unit": u, "price": c} for m, u, c, _ in i["components"]]
        it["sell_components"] = [{"meter": m, "unit": u, "price": s} for m, u, _, s in i["components"] if s]
    else:
        it["cost_input"], it["cost_output"] = i["cost"]
        if i["sell"]:
            it["sell_input"], it["sell_output"] = i["sell"]
    if i.get("overrides"):
        it["param_overrides"] = i["overrides"]
    return it


def reconcile(a, acc_id, i, plan):
    """模型已存在时（多半是旧版脚本按 token 计价导入的），补齐能力、价格与渠道参数。"""
    name = config.MODELS[i["kind"]]
    page = a.call("GET", "/channels", params={"provider_account_id": acc_id, "q": name, "page_size": 50})
    ch = next((c for c in page["data"] if c["upstream_model"] == name), None)
    if ch is None:
        print("    %-32s 找不到渠道，跳过校正" % name)
        return
    fixes = []

    vm = a.call("GET", "/virtual-models/%d" % ch["virtual_model_id"])
    caps = vm.get("capabilities") or []
    if not set(i["caps"]) <= set(caps):
        a.call("PATCH", "/virtual-models/%d" % ch["virtual_model_id"],
               json={"capabilities": sorted(set(caps) | set(i["caps"]))})
        fixes.append("能力 +" + ",".join(sorted(set(i["caps"]) - set(caps))))

    if "components" in i:
        want = {m for m, _, _, _ in i["components"]}
        have_sell = {p["meter"] for p in (ch.get("sell_price") or {}).get("media") or []}
        have_cost = {p["meter"] for p in (ch.get("cost_price") or {}).get("media") or []}
        if not want <= have_cost:
            a.call("POST", "/channels/%d/cost-price" % ch["id"], json={"currency": "CNY", "components": [
                {"meter": m, "unit": u, "unit_price": c} for m, u, c, _ in i["components"]]})
            fixes.append("成本价")
        if not want <= have_sell:
            sells = {c["meter"]: c["sell"] for c in plan.get("components") or []}
            a.call("POST", "/virtual-models/%d/sell-price" % ch["virtual_model_id"], json={"components": [
                {"meter": m, "unit": u, "unit_price": s or sells.get(m)} for m, u, _, s in i["components"]]})
            fixes.append("售价")

    overrides = ch.get("param_overrides") or {}
    missing = {k: v for k, v in (i.get("overrides") or {}).items() if overrides.get(k) != v}
    if missing:
        merged = dict(overrides)
        merged.update(missing)
        a.call("PATCH", "/channels/%d" % ch["id"], json={"param_overrides": merged})
        fixes.append("渠道参数")
    print("    %-32s %s" % (name, "、".join(fixes) if fixes else "无需校正"))


def main():
    if not config.DIRECT_KEY:
        sys.exit("请先在 .env 里设置 %s" % config.PROFILE["key_env"])
    a = Admin()

    step("供应商")
    providers = a.call("GET", "/providers", params={"page_size": 200})["data"]
    provider = next((p for p in providers if p["code"] == CODE), None)
    if provider is None:
        provider = a.call("POST", "/providers", json={"code": CODE, "name": config.PROFILE["name"], "protocol": "openai"})
        print("    新建 %s #%d" % (CODE, provider["id"]))
    else:
        print("    复用 %s #%d" % (CODE, provider["id"]))

    step("上游账号")
    accounts = a.call("GET", "/provider-accounts", params={"provider_id": provider["id"], "page_size": 100})["data"]
    account = next((x for x in accounts if x["base_url"].rstrip("/") == config.DIRECT_BASE_URL), None)
    if account is None:
        account = a.call("POST", "/provider-accounts", json={
            "provider_id": provider["id"], "name": CODE + "-pytest", "base_url": config.DIRECT_BASE_URL, "cost_multiplier": "1"})
        print("    新建账号 #%d" % account["id"])
    else:
        print("    复用账号 #%d %s" % (account["id"], account["name"]))
    acc_id = account["id"]

    step("供应商方言")
    # 档案里显式声明的方言优先；否则用与供应商 code 同名的内置预设（如果有）
    presets = {p["name"] for p in a.call("GET", "/meta/dialect-presets")["data"]}
    want = config.PROFILE.get("dialect") or ({"preset": CODE} if CODE in presets else None)
    d = a.call("GET", "/provider-accounts/%d/dialect" % acc_id)
    current = (d.get("effective") or {}).get("preset")
    if want is not None and current != want.get("preset"):
        d = a.call("PUT", "/provider-accounts/%d/dialect" % acc_id, json={"dialect": want})
        print("    已绑定方言预设 %s" % want.get("preset"))
    else:
        print("    方言：%s" % (current or "无（纯透传）"))
    print("    端点支持：%s" % ", ".join(k for k, v in sorted((d.get("endpoints") or {}).items()) if v))

    step("上游密钥")
    detail = a.call("GET", "/provider-accounts/%d" % acc_id)
    if any(k["last4"] == config.DIRECT_KEY[-4:] and k["status"] == "active" for k in detail.get("keys") or []):
        print("    密钥 %s 已存在" % config.mask(config.DIRECT_KEY))
    else:
        a.call("POST", "/provider-accounts/%d/keys" % acc_id, json={"secret": config.DIRECT_KEY, "weight": 100})
        print("    已添加密钥 %s" % config.mask(config.DIRECT_KEY))

    step("核对上游模型列表")
    upstream = {m.get("id") for m in a.call("GET", "/provider-accounts/%d/upstream-models" % acc_id)["data"]}
    missing = [config.MODELS[i["kind"]] for i in ITEMS if config.MODELS[i["kind"]] not in upstream]
    print("    上游共 %d 个模型；缺少：%s" % (len(upstream), missing or "无"))

    items = [import_item(i) for i in ITEMS]
    body = {"currency": "CNY", "markup_percent": MARKUP_PERCENT, "items": items}

    step("导入预览（dry_run）")
    plan = a.call("POST", "/provider-accounts/%d/import-models" % acc_id, json=dict(body, dry_run=True))
    todo, existing = [], []
    for i, it, p in zip(ITEMS, items, plan["items"]):
        price = "/".join(str(c["sell"]) for c in p.get("components") or []) or "%s/%s" % (p.get("sell_input"), p.get("sell_output"))
        print("    %-32s %-9s 售价 %-12s %s" % (p["upstream_model"], p["status"], price, "、".join(p["errors"]) or "OK"))
        if p["status"] == "new":
            todo.append(it)
        else:
            existing.append((i, p))

    if todo:
        step("正式导入 %d 个模型" % len(todo))
        res = a.call("POST", "/provider-accounts/%d/import-models" % acc_id, json=dict(body, items=todo, dry_run=False))
        failed = [r for r in res["items"] if not r["ok"]]
        for r in res["items"]:
            print("    %-32s %s" % (r["upstream_model"], "OK" if r["ok"] else (r.get("error") or {}).get("message")))
        if failed:
            sys.exit("有 %d 个模型导入失败" % len(failed))
    if existing:
        step("校正已存在的模型")
        for i, p in existing:
            reconcile(a, acc_id, i, p)

    if not config.GATEWAY_KEY:
        print("未设置 UFT_API_KEY，跳过网关可见性检查")
        return
    step("等待网关目录快照刷新")
    want = {config.MODELS[i["kind"]] for i in ITEMS}
    seen = set()
    for _ in range(15):
        r = httpx.get(config.GATEWAY_URL + "/models", headers={"Authorization": "Bearer " + config.GATEWAY_KEY}, timeout=10)
        seen = {m["id"] for m in r.json().get("data", [])} if r.status_code == 200 else set()
        if want <= seen:
            print("    网关 /v1/models 已包含全部 %d 个测试模型" % len(want))
            time.sleep(10)  # 校正后的能力/价格也要等快照刷新
            return
        time.sleep(2)
    sys.exit("网关 /v1/models 仍缺少：%s" % sorted(want - seen))


if __name__ == "__main__":
    main()
