import tailwindcss from '@tailwindcss/vite';
import react from '@vitejs/plugin-react';
import path from 'path';
import {defineConfig} from 'vite';
import mdx from '@mdx-js/rollup';
import remarkFrontmatter from 'remark-frontmatter';
import remarkGfm from 'remark-gfm';
import rehypeSlug from 'rehype-slug';
import rehypeShiki from '@shikijs/rehype';
import {docsPlugin} from './tools/docsPlugin.ts';

export default defineConfig(() => {
  return {
    plugins: [
      // 文档站（src/docs）：MDX 在构建期编译，代码块由 shiki 在构建期高亮，
      // 运行时不带高亮器。必须排在 react() 之前。
      {
        enforce: 'pre',
        ...mdx({
          providerImportSource: '@mdx-js/react',
          // frontmatter 只在构建期由 tools/docsContent.ts 读取，这里只需让
          // remark 认出并丢掉它，不导出到运行时。
          remarkPlugins: [remarkFrontmatter, remarkGfm],
          rehypePlugins: [
            rehypeSlug,
            [
              rehypeShiki,
              {
                theme: 'github-dark-default',
                addLanguageClass: true,
                // ```bash title="cURL" → <pre data-title="cURL">，CodeTabs 用它当标签名。
                parseMetaString: (meta: string) => {
                  const m = /title="([^"]*)"/.exec(meta);
                  return m ? {'data-title': m[1]} : {};
                },
              },
            ],
          ],
        }),
      },
      docsPlugin(),
      react({include: /\.(mdx|js|jsx|ts|tsx)$/}),
      tailwindcss(),
    ],
    resolve: {
      alias: {
        '@': path.resolve(__dirname, '.'),
      },
    },
    server: {
      // HMR is disabled in AI Studio via DISABLE_HMR env var.
      // Do not modifyâfile watching is disabled to prevent flickering during agent edits.
      hmr: process.env.DISABLE_HMR !== 'true',
      // Disable file watching when DISABLE_HMR is true to save CPU during agent edits.
      watch: process.env.DISABLE_HMR === 'true' ? null : {},
      // 把 /v1 和 /console 转发到本地 gateway（scripts/dev-web-up.sh 起的
      // cmd/gateway，默认 :8080），这样 dev 环境下前端和后端就是同源，不需要
      // 配置 CORS。prod 环境同样的反代由 Nginx 做（deploy/nginx/web.conf）。
      proxy: {
        '/v1': { target: 'http://localhost:8080', changeOrigin: true },
        // /console 不能 changeOrigin：CSRFGuard 要求 Origin 与 Host 一致，改写成
        // localhost:8080 后会和浏览器的 Origin（localhost:3000）对不上，写操作
        // 被判为跨站请求。Nginx 那边同样用 proxy_set_header Host $host 保留原 Host。
        '/console': { target: 'http://localhost:8080', changeOrigin: false },
      },
    },
  };
});
