import tailwindcss from '@tailwindcss/vite';
import react from '@vitejs/plugin-react';
import path from 'path';
import {defineConfig} from 'vite';

export default defineConfig(() => {
  return {
    plugins: [react(), tailwindcss()],
    resolve: {
      alias: {
        '@': path.resolve(import.meta.dirname, '.'),
      },
    },
    server: {
      // cmd/admin 的接口挂在根路径（/accounts、/providers……），和 SPA 自己的
      // 路由（/accounts/:id 页面）同名冲突，所以前端统一给接口加 /admin-api
      // 前缀，这里转发到本地 cmd/admin（默认 :8081）时再去掉前缀。prod 环境
      // 同样的反代由 Nginx 做（见 deploy-nginx.example.conf）。
      // ADMIN_API_TARGET 可覆盖转发目标（例如本机 8081 已被占用、或联调远端环境）。
      proxy: {
        '/admin-api': {
          target: process.env.ADMIN_API_TARGET || 'http://localhost:8081',
          changeOrigin: true,
          rewrite: (p: string) => p.replace(/^\/admin-api/, ''),
        },
      },
    },
  };
});
