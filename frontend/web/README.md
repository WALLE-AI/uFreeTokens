# frontend/web

uFreeTokens 模型集市的用户端前端（React + Vite + Tailwind CSS）。

## 本地开发

**依赖：** Node.js

```bash
npm install
npm run dev      # http://localhost:3000
```

`vite.config.ts` 里配置了 dev proxy，把 `/v1` 和 `/console` 转发到
`http://localhost:8080`（本地 gateway），所以先用仓库根目录的
`scripts/dev-web-up.sh` 把 `cmd/admin` + `cmd/gateway` 起起来，再跑这个前端。

## 环境变量

见 `.env.example`：`VITE_GATEWAY_BASE_URL` 留空表示同源（走 dev proxy /
prod Nginx 反代），只有前端和 gateway 不同源部署时才需要显式填写。

## 其他命令

```bash
npm run build     # 产出 dist/
npm run preview   # 本地预览 build 产物
npm run lint       # tsc --noEmit
```

详细架构说明见 `ARCHITECTURE.md`。
