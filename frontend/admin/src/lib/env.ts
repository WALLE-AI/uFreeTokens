import type { AdminEnv } from '../types';

const raw = (import.meta.env.VITE_ADMIN_ENV ?? 'dev') as string;

export const ADMIN_ENV: AdminEnv = raw === 'production' || raw === 'staging' ? raw : 'dev';

export const ENV_LABEL: Record<AdminEnv, string> = {
  production: '生产环境',
  staging: '测试环境',
  dev: '开发环境',
};

// §11.9：生产 rose、测试 amber；开发环境用中性灰
export const ENV_BADGE_CLASS: Record<AdminEnv, string> = {
  production: 'bg-rose-50 text-rose-700 border-rose-200',
  staging: 'bg-amber-50 text-amber-700 border-amber-200',
  dev: 'bg-gray-50 text-gray-600 border-gray-200',
};

export const ENV_DOT_CLASS: Record<AdminEnv, string> = {
  production: 'bg-rose-500',
  staging: 'bg-amber-500',
  dev: 'bg-gray-400',
};
