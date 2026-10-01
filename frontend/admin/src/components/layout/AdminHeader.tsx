import { useCallback, useRef, useState } from 'react';
import { Link, useMatches, useNavigate, type UIMatch } from 'react-router';
import { ChevronDown, ChevronRight, Keyboard, LogOut, Menu, Search, ShieldCheck } from 'lucide-react';
import { authStore, useAuth } from '../../api/auth';
import { logout } from '../../api/session';
import { SecurityModal } from './SecurityModal';
import { cn } from '../../lib/cn';
import { ADMIN_ENV } from '../../lib/env';
import { useDismiss } from '../../hooks/useDismiss';

// 路由 handle.crumb：字符串，或根据 params 生成（如 "账户 #1234"）
export interface RouteHandle {
  crumb?: string | ((params: Record<string, string | undefined>) => string);
}

function Breadcrumbs() {
  const matches = useMatches() as UIMatch<unknown, RouteHandle | undefined>[];
  const crumbs = matches
    .filter((m) => m.handle?.crumb)
    .map((m) => ({
      path: m.pathname,
      label: typeof m.handle!.crumb === 'function' ? m.handle!.crumb(m.params) : (m.handle!.crumb as string),
    }));
  return (
    <nav className="flex items-center gap-1 min-w-0 text-xs" aria-label="面包屑">
      {crumbs.map((c, i) => {
        const last = i === crumbs.length - 1;
        return (
          <span key={c.path + i} className="flex items-center gap-1 min-w-0">
            {i > 0 && <ChevronRight className="w-3 h-3 text-gray-300 shrink-0" />}
            {last ? (
              <span className="text-gray-900 font-medium truncate">{c.label}</span>
            ) : (
              <Link to={c.path} className="text-gray-500 hover:text-gray-900 truncate">
                {c.label}
              </Link>
            )}
          </span>
        );
      })}
    </nav>
  );
}

export interface AdminHeaderProps {
  onOpenPalette: () => void;
  onOpenShortcuts: () => void;
  onOpenMobileNav: () => void;
}

// AdminHeader：对齐 web Header.tsx（h-12 sticky top-0 z-40 border-b）。左侧是面包屑
// 而不是顶部 tab（导航已在侧栏）；生产环境额外加 2px 红色顶边（§11.9）。
export function AdminHeader({ onOpenPalette, onOpenShortcuts, onOpenMobileNav }: AdminHeaderProps) {
  const { actorName, me } = useAuth();
  const navigate = useNavigate();
  const [menuOpen, setMenuOpen] = useState(false);
  const [securityOpen, setSecurityOpen] = useState(false);
  const menuRef = useRef<HTMLDivElement>(null);
  const closeMenu = useCallback(() => setMenuOpen(false), []);
  useDismiss(menuRef, menuOpen, closeMenu);

  const isMac = typeof navigator !== 'undefined' && /Mac|iPhone|iPad/.test(navigator.platform);

  return (
    <header
      className={cn(
        'h-12 border-b border-gray-200 px-3 md:px-4 flex items-center justify-between gap-3 text-xs bg-white sticky top-0 z-40 select-none',
        ADMIN_ENV === 'production' && 'border-t-2 border-t-rose-500',
      )}
    >
      <div className="flex items-center gap-3 min-w-0">
        <button type="button" onClick={onOpenMobileNav} className="md:hidden p-1 text-gray-500 hover:text-gray-900 cursor-pointer" aria-label="打开导航">
          <Menu className="w-4 h-4" />
        </button>
        <Link to="/" className="flex items-center gap-1.5 shrink-0">
          <span className="w-6 h-6 rounded-md bg-purple-600 text-white flex items-center justify-center">
            <ShieldCheck className="w-3.5 h-3.5" />
          </span>
          <span className="hidden sm:inline text-[13px] font-semibold text-gray-900 tracking-tight">
            uFreeTokens <span className="text-purple-600">Admin</span>
          </span>
        </Link>
        <span className="hidden sm:block w-px h-4 bg-gray-200" />
        <Breadcrumbs />
      </div>

      <div className="flex items-center gap-2 shrink-0">
        <button
          type="button"
          onClick={onOpenPalette}
          className="hidden sm:flex items-center gap-2 w-64 bg-gray-50 border border-gray-200 rounded-lg px-3 py-1.5 text-gray-400 hover:border-gray-300 cursor-pointer"
        >
          <Search className="w-3.5 h-3.5" />
          <span className="flex-1 text-left">跳转到… 账户ID/模型/渠道</span>
          <kbd className="font-mono text-[10px] bg-white border border-gray-200 rounded px-1">{isMac ? '⌘K' : 'Ctrl K'}</kbd>
        </button>
        <button type="button" onClick={onOpenPalette} className="sm:hidden p-1 text-gray-500 cursor-pointer" aria-label="搜索">
          <Search className="w-4 h-4" />
        </button>

        <div ref={menuRef} className="relative">
          <button
            type="button"
            onClick={() => setMenuOpen((o) => !o)}
            className="flex items-center gap-1.5 px-2 py-1 rounded-lg hover:bg-gray-50 text-gray-700 cursor-pointer"
          >
            <span className="w-6 h-6 rounded-full bg-purple-50 text-purple-700 border border-purple-200 flex items-center justify-center text-[11px] font-semibold">
              {(actorName ?? '?').slice(0, 1).toUpperCase()}
            </span>
            <span className="hidden md:inline max-w-24 truncate">{actorName}</span>
            <ChevronDown className="w-3 h-3 text-gray-400" />
          </button>
          {menuOpen && (
            <div className="absolute right-0 top-9 bg-white border border-gray-200 rounded-lg shadow-lg py-1 z-50 text-xs w-44 animate-in fade-in zoom-in-95 duration-100">
              <div className="px-3 py-2 border-b border-gray-100">
                <div className="text-[10px] text-gray-400 uppercase tracking-wider font-semibold">当前登录</div>
                <div className="text-gray-900 font-medium truncate mt-0.5">{actorName}</div>
                <div className="text-[11px] text-gray-400 truncate">{me?.break_glass ? '应急令牌（system）' : me?.email}</div>
              </div>
              <button
                type="button"
                onClick={() => {
                  setMenuOpen(false);
                  onOpenShortcuts();
                }}
                className="w-full px-3 py-1.5 text-left flex items-center gap-2 hover:bg-gray-50 text-gray-700 cursor-pointer"
              >
                <Keyboard className="w-3.5 h-3.5" />
                快捷键
                <kbd className="ml-auto font-mono text-[10px] text-gray-400">?</kbd>
              </button>
              <button
                type="button"
                onClick={() => {
                  setMenuOpen(false);
                  setSecurityOpen(true);
                }}
                className="w-full px-3 py-1.5 text-left flex items-center gap-2 hover:bg-gray-50 text-gray-700 cursor-pointer"
              >
                <ShieldCheck className="w-3.5 h-3.5" />
                安全设置
                {me && !me.break_glass && !me.totp_enabled && <span className="ml-auto text-[10px] text-amber-600">未开两步验证</span>}
              </button>
              <button
                type="button"
                onClick={async () => {
                  // 先让服务端注销会话（令牌立即失效），失败也照常清理本地登录态
                  await logout().catch(() => undefined);
                  authStore.clear();
                  navigate('/login', { replace: true });
                }}
                className="w-full px-3 py-1.5 text-left flex items-center gap-2 hover:bg-rose-50 text-rose-600 cursor-pointer border-t border-gray-100"
              >
                <LogOut className="w-3.5 h-3.5" />
                退出登录
              </button>
            </div>
          )}
        </div>
      </div>
      <SecurityModal open={securityOpen} onClose={() => setSecurityOpen(false)} />
    </header>
  );
}
