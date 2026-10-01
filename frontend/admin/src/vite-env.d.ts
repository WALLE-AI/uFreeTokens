/// <reference types="vite/client" />

interface ImportMetaEnv {
  readonly VITE_ADMIN_API_BASE?: string;
  readonly VITE_ADMIN_ENV?: string;
  readonly VITE_ADMIN_TZ?: string;
}

interface ImportMeta {
  readonly env: ImportMetaEnv;
}
