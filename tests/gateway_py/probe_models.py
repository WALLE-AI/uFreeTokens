"""核对 providers.py 档案里的模型 ID 是否在上游模型列表中（只读）。

用法：UFT_PROVIDER=openrouter python probe_models.py
"""
import httpx

import config


def main():
    h = {"Authorization": "Bearer " + config.DIRECT_KEY}
    ids = set()
    for path in ("/models", "/embeddings/models"):
        r = httpx.get(config.DIRECT_BASE_URL + path, headers=h, timeout=30)
        if r.status_code == 200:
            ids |= {m.get("id") for m in r.json().get("data", [])}
        print("%s %s -> %d" % (config.PROVIDER, path, r.status_code))
    print("上游共 %d 个模型" % len(ids))
    for kind, model in config.MODELS.items():
        if model:
            print("  %-10s %-45s %s" % (kind, model, "OK" if model in ids else "不在列表中"))


if __name__ == "__main__":
    main()
