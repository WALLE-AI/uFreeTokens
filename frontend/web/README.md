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
`VITE_DOCS_PUBLIC_ORIGIN` 是文档站的正式域名（默认 `https://ufreetokens.com`），
只用于构建期生成的 `llms.txt` 等文件。

## 其他命令

```bash
npm run build       # 产出 dist/
npm run preview     # 本地预览 build 产物
npm run lint        # tsc --noEmit + 文档一致性检查
npm run docs:check  # 只跑文档一致性检查
```

## 编写开发者文档

文档在 `src/docs/content/{zh,en}/`，中文是源语言：

1. 在 `zh/` 新建或修改 `<slug>.mdx`（frontmatter：`title`、`description`、
   `section`（`getting-started|guides|integrations|changelog|api`）、`order`）。
   可用组件：`<Callout>`、`<Steps>`/`<Step>`、`<CodeTabs>`、`<LiveModelList />`、
   `<ErrorCodeTable />`；代码里的网关地址写 `{{BASE_URL}}`，Key 写
   `$UFREETOKENS_API_KEY`；站内链接写 `/docs/zh/<slug>`。
2. 在 `en/` 下写同名译文，然后运行 `npm run docs:check -- --write-hashes`，
   记录译文对应的中文版本（之后中文再改，检查会提示译文可能过期）。
3. 接口有变动时，先在仓库根目录运行
   `UPDATE_GATEWAY_API_DOC=1 go test ./internal/app -run TestGatewayOpenAPI_UpToDate`
   重新生成 `docs/gateway-openapi.json`，API 参考页会自动更新。

`npm run dev` 下修改 MDX 会自动刷新页面。

详细架构说明见 `ARCHITECTURE.md`。
