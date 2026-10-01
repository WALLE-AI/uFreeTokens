import { request } from './client';
import type { AdminMe, AdminRole, AdminUser, ListData } from '../types';

// 登录 / 会话 / 管理员管理（B5，见 docs/admin-api.md §1）。

export interface LoginResult {
  token: string;
  expires_at: string;
}

// 启用了两步验证的管理员需要 totpCode；缺少时后端返回 401 totp_required。
export function login(email: string, password: string, totpCode?: string) {
  // 登录失败的 401 不应触发"跳回登录页"，传一个空的显式令牌跳过自动跳转。
  return request<LoginResult>('/auth/login', { method: 'POST', body: { email, password, totp_code: totpCode ?? '' }, token: '' });
}

export interface TOTPSetup {
  secret: string;
  otpauth_url: string;
}

export function setupTOTP() {
  return request<TOTPSetup>('/auth/totp/setup', { method: 'POST' });
}

export function enableTOTP(code: string) {
  return request<void>('/auth/totp/enable', { method: 'POST', body: { code } });
}

export function disableTOTP(code: string) {
  return request<void>('/auth/totp/disable', { method: 'POST', body: { code } });
}

// fetchMe 用给定令牌获取身份与权限（登录后、或用应急令牌登录时校验令牌）。
export function fetchMe(token?: string, signal?: AbortSignal) {
  return request<AdminMe>('/me', { token, signal });
}

export function logout() {
  return request<void>('/auth/logout', { method: 'POST' });
}

export function changePassword(oldPassword: string, newPassword: string) {
  return request<void>('/auth/password', { method: 'POST', body: { old_password: oldPassword, new_password: newPassword } });
}

export function listAdminUsers(signal?: AbortSignal) {
  return request<ListData<AdminUser>>('/admin-users', { signal });
}

export function listAdminRoles(signal?: AbortSignal) {
  return request<ListData<AdminRole>>('/admin-roles', { signal });
}

export function createAdminUser(body: { email: string; name: string; password: string; roles: string[] }) {
  return request<AdminUser>('/admin-users', { method: 'POST', body });
}

export function updateAdminUser(id: number, body: { name?: string; status?: 'active' | 'disabled'; roles?: string[]; password?: string; reset_totp?: boolean }) {
  return request<AdminUser>(`/admin-users/${id}`, { method: 'PATCH', body });
}
