import type { ReactNode } from 'react';
import { useCan } from '../../api/auth';
import type { Permission } from '../../types';

// Can 只在当前管理员拥有 perm 时渲染 children：没有权限的写操作按钮直接不显示，
// 而不是点了才收到 403。服务端仍会对每个请求做权限校验，这里只是界面层的显隐。
export function Can({ perm, children, fallback = null }: { perm: Permission; children: ReactNode; fallback?: ReactNode }) {
  return useCan(perm) ? <>{children}</> : <>{fallback}</>;
}
