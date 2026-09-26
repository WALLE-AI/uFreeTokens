import tailwindcss from '@tailwindcss/vite';
import react from '@vitejs/plugin-react';
import path from 'path';
import {defineConfig} from 'vite';

export default defineConfig(() => {
  return {
    plugins: [react(), tailwindcss()],
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
        '/console': { target: 'http://localhost:8080', changeOrigin: true },
      },
    },
  };
});
