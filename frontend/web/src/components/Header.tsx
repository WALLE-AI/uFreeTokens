import React, { useState, useRef, useEffect } from 'react';
import {
  Layers,
  Search,
  ChevronDown,
  SlidersHorizontal,
  Archive,
  CircleUser,
  BarChart2,
  List,
  CreditCard,
  FlaskConical,
  Settings,
  LogOut,
  Sun,
  Moon,
  Monitor,
  KeyRound,
  ShieldCheck
} from 'lucide-react';
import { useApiKey } from '../api/auth';
import { ConnectKeyModal } from './ConnectKeyModal';

interface HeaderProps {
  searchQuery: string;
  onSearchChange: (q: string) => void;
  onOpenCommandPalette: () => void;
  onToggleMobileSidebar: () => void;
  userEmail?: string;
  activeNav?: string;
  onSelectNav?: (nav: string) => void;
  onOpenPersonalDashboard?: (tab?: string) => void;
}

export const Header: React.FC<HeaderProps> = ({
  searchQuery,
  onSearchChange,
  onOpenCommandPalette,
  onToggleMobileSidebar,
  userEmail = 'gaojing850063636@gmail.com',
  activeNav = '模型',
  onSelectNav,
  onOpenPersonalDashboard,
}) => {
  const [showUserMenu, setShowUserMenu] = useState(false);
  const [theme, setTheme] = useState<'light' | 'dark' | 'system'>('system');
  const [toastMessage, setToastMessage] = useState<string | null>(null);
  const [showConnectKeyModal, setShowConnectKeyModal] = useState(false);
  const userMenuRef = useRef<HTMLDivElement>(null);
  const apiKey = useApiKey();

  // Close menu on click outside
  useEffect(() => {
    const handleClickOutside = (event: MouseEvent) => {
      if (userMenuRef.current && !userMenuRef.current.contains(event.target as Node)) {
        setShowUserMenu(false);
      }
    };
    if (showUserMenu) {
      document.addEventListener('mousedown', handleClickOutside);
    }
    return () => {
      document.removeEventListener('mousedown', handleClickOutside);
    };
  }, [showUserMenu]);

  const triggerToast = (msg: string) => {
    setToastMessage(msg);
    setTimeout(() => {
      setToastMessage(null);
    }, 2200);
  };

  const navItems = [
    { name: '首页', href: '#' },
    { name: '模型', href: '#', active: true },
    { name: '基准测试', href: '#' },
    { name: '排行榜', href: '#' },
    { name: 'Harness', href: '#' },
    { name: '文档', href: '#' },
  ];

  return (
    <header className="h-12 border-b border-gray-200 px-3 md:px-4 flex items-center justify-between text-xs bg-white sticky top-0 z-40 select-none">
      {/* Toast Feedback Notification */}
      {toastMessage && (
        <div className="fixed top-14 right-4 z-50 bg-gray-900/90 backdrop-blur-md text-white px-3.5 py-2 rounded-lg shadow-lg text-xs flex items-center gap-2 animate-in fade-in slide-in-from-top-2 duration-200">
          <span className="w-1.5 h-1.5 rounded-full bg-purple-400"></span>
          <span>{toastMessage}</span>
        </div>
      )}

      {/* Left: Brand + Search */}
      <div className="flex items-center space-x-3 md:space-x-4">
        {/* Mobile filter toggle */}
        <button
          onClick={onToggleMobileSidebar}
          className="md:hidden p-1.5 rounded text-gray-600 hover:bg-gray-100 hover:text-gray-900"
          title="打开筛选器"
        >
          <SlidersHorizontal className="w-4 h-4 text-purple-600" />
        </button>

        <div
          onClick={() => onSelectNav && onSelectNav('模型')}
          className="flex items-center space-x-1.5 font-bold text-gray-900 cursor-pointer hover:opacity-90"
        >
          <Layers className="w-4 h-4 text-purple-600" />
          <span className="text-sm font-semibold tracking-tight text-gray-900">uFreeTokens</span>
          <span className="hidden sm:inline-block px-1.5 py-0.2 bg-purple-50 text-purple-700 border border-purple-200 rounded text-[10px] font-medium ml-1">
            集市
          </span>
        </div>

        {/* Global Search Bar */}
        <div className="relative w-36 sm:w-56">
          <Search className="w-3.5 h-3.5 absolute left-2.5 top-1/2 -translate-y-1/2 text-gray-400" />
          <input
            type="text"
            value={searchQuery}
            onChange={(e) => onSearchChange(e.target.value)}
            onClick={onOpenCommandPalette}
            placeholder="搜索 ⌘ K"
            className="w-full bg-gray-50 border border-gray-200 rounded pl-7 pr-6 py-1 text-xs focus:outline-none focus:border-purple-400 focus:bg-white transition-colors"
          />
          <kbd className="hidden sm:block absolute right-2 top-1/2 -translate-y-1/2 text-[10px] text-gray-400 font-mono bg-gray-100 border border-gray-200 rounded px-1">
            ⌘K
          </kbd>
        </div>
      </div>

      {/* Right: Navigation Links + User Profile */}
      <div className="flex items-center space-x-2 sm:space-x-4 text-gray-600 font-normal">
        <nav className="flex items-center space-x-2 sm:space-x-4">
          {navItems.map((item) => {
            const isActive = item.name === activeNav;
            return (
              <button
                key={item.name}
                onClick={() => onSelectNav && onSelectNav(item.name)}
                className={`transition-colors text-xs cursor-pointer py-1 px-1.5 rounded ${
                  isActive
                    ? 'text-purple-700 font-bold bg-purple-50 sm:bg-transparent sm:border-b-2 sm:border-purple-600 sm:rounded-none'
                    : 'hover:text-gray-900 hover:bg-gray-50'
                }`}
              >
                {item.name}
              </button>
            );
          })}
        </nav>

        {/* User profile dropdown */}
        <div className="relative" ref={userMenuRef}>
          <button
            onClick={() => {
              const nextState = !showUserMenu;
              setShowUserMenu(nextState);
              if (nextState && onOpenPersonalDashboard) {
                onOpenPersonalDashboard('api-keys');
              }
            }}
            className="flex items-center space-x-1.5 cursor-pointer py-1 px-2 rounded-md hover:bg-gray-100 transition-colors"
          >
            <div className="w-4 h-4 rounded-full bg-purple-700 text-white flex items-center justify-center text-[10px] font-semibold leading-none shadow-2xs">
              j
            </div>
            <span className="hidden sm:inline font-medium text-gray-700">个人中心</span>
            <ChevronDown className={`w-3 h-3 text-gray-400 transition-transform duration-150 ${showUserMenu ? 'rotate-180 text-gray-600' : ''}`} />
          </button>

          {showUserMenu && (
            <div className="absolute right-0 top-9 w-[220px] bg-white border border-gray-200/90 rounded-2xl shadow-xl shadow-gray-400/20 p-2 z-50 select-none animate-in fade-in-50 zoom-in-95 duration-150">
              {/* Personal Header Card */}
              <div
                onClick={() => {
                  setShowUserMenu(false);
                  onOpenPersonalDashboard && onOpenPersonalDashboard('api-keys');
                  triggerToast('已进入个人工作区');
                }}
                className="bg-[#FAF7FE] hover:bg-[#F3EAFF] rounded-xl px-2.5 py-1.5 flex items-center justify-between transition-colors cursor-pointer group"
              >
                <div className="flex items-center gap-2">
                  <div className="w-5 h-5 rounded-full bg-purple-700 text-white flex items-center justify-center text-xs font-semibold leading-none shadow-2xs shrink-0">
                    j
                  </div>
                  <span className="text-[13px] font-semibold text-purple-600 tracking-tight">个人工作区</span>
                </div>
                <Settings className="w-3.5 h-3.5 text-gray-400 group-hover:text-gray-700 transition-colors" />
              </div>

              {/* Menu Items List */}
              <div className="py-1 space-y-0.5">
                <button
                  onClick={() => {
                    setShowUserMenu(false);
                    setShowConnectKeyModal(true);
                  }}
                  className="w-full flex items-center gap-3 px-2.5 py-1.5 rounded-lg text-[13px] text-gray-700 hover:text-gray-950 hover:bg-gray-50/80 transition-colors text-left cursor-pointer group"
                >
                  {apiKey ? (
                    <ShieldCheck className="w-4 h-4 text-emerald-600 shrink-0 stroke-[1.75]" />
                  ) : (
                    <KeyRound className="w-4 h-4 text-gray-500 group-hover:text-gray-800 shrink-0 stroke-[1.75]" />
                  )}
                  <span className="font-normal">{apiKey ? '已连接 API Key' : '连接 API Key'}</span>
                </button>

                <button
                  onClick={() => {
                    setShowUserMenu(false);
                    onOpenPersonalDashboard && onOpenPersonalDashboard('overview');
                    triggerToast('工作区已同步');
                  }}
                  className="w-full flex items-center gap-3 px-2.5 py-1.5 rounded-lg text-[13px] text-gray-700 hover:text-gray-950 hover:bg-gray-50/80 transition-colors text-left cursor-pointer group"
                >
                  <Archive className="w-4 h-4 text-gray-500 group-hover:text-gray-800 shrink-0 stroke-[1.75]" />
                  <span className="font-normal">工作区</span>
                </button>

                <button
                  onClick={() => {
                    setShowUserMenu(false);
                    onOpenPersonalDashboard && onOpenPersonalDashboard('profile');
                    triggerToast(`当前账号: ${userEmail}`);
                  }}
                  className="w-full flex items-center gap-3 px-2.5 py-1.5 rounded-lg text-[13px] text-gray-700 hover:text-gray-950 hover:bg-gray-50/80 transition-colors text-left cursor-pointer group"
                >
                  <CircleUser className="w-4 h-4 text-gray-500 group-hover:text-gray-800 shrink-0 stroke-[1.75]" />
                  <span className="font-normal">个人资料</span>
                </button>

                <button
                  onClick={() => {
                    setShowUserMenu(false);
                    onOpenPersonalDashboard && onOpenPersonalDashboard('activity');
                    triggerToast('已加载近期 API 调用活动与用量明细');
                  }}
                  className="w-full flex items-center gap-3 px-2.5 py-1.5 rounded-lg text-[13px] text-gray-700 hover:text-gray-950 hover:bg-gray-50/80 transition-colors text-left cursor-pointer group"
                >
                  <BarChart2 className="w-4 h-4 text-gray-500 group-hover:text-gray-800 shrink-0 stroke-[1.75]" />
                  <span className="font-normal">活动记录</span>
                </button>

                <button
                  onClick={() => {
                    setShowUserMenu(false);
                    onOpenPersonalDashboard && onOpenPersonalDashboard('logs');
                    triggerToast('日志服务已连接 · 遵循 ZDR 零数据保留');
                  }}
                  className="w-full flex items-center gap-3 px-2.5 py-1.5 rounded-lg text-[13px] text-gray-700 hover:text-gray-950 hover:bg-gray-50/80 transition-colors text-left cursor-pointer group"
                >
                  <List className="w-4 h-4 text-gray-500 group-hover:text-gray-800 shrink-0 stroke-[1.75]" />
                  <span className="font-normal">调用日志</span>
                </button>

                <button
                  onClick={() => {
                    setShowUserMenu(false);
                    onOpenPersonalDashboard && onOpenPersonalDashboard('credits');
                    triggerToast('已加载账户余额与账单信息');
                  }}
                  className="w-full flex items-center gap-3 px-2.5 py-1.5 rounded-lg text-[13px] text-gray-700 hover:text-gray-950 hover:bg-gray-50/80 transition-colors text-left cursor-pointer group"
                >
                  <CreditCard className="w-4 h-4 text-gray-500 group-hover:text-gray-800 shrink-0 stroke-[1.75]" />
                  <span className="font-normal">额度与账单</span>
                </button>

                <button
                  onClick={() => {
                    setShowUserMenu(false);
                    onSelectNav && onSelectNav('Harness');
                    triggerToast('已进入 Ori Labs 实验室智能体环境');
                  }}
                  className="w-full flex items-center gap-3 px-2.5 py-1.5 rounded-lg text-[13px] text-gray-700 hover:text-gray-950 hover:bg-gray-50/80 transition-colors text-left cursor-pointer group"
                >
                  <FlaskConical className="w-4 h-4 text-gray-500 group-hover:text-gray-800 shrink-0 stroke-[1.75]" />
                  <span className="font-normal">实验室</span>
                </button>

                <button
                  onClick={() => {
                    setShowUserMenu(false);
                    onOpenPersonalDashboard && onOpenPersonalDashboard('preferences');
                    triggerToast('偏好设置已更新');
                  }}
                  className="w-full flex items-center gap-3 px-2.5 py-1.5 rounded-lg text-[13px] text-gray-700 hover:text-gray-950 hover:bg-gray-50/80 transition-colors text-left cursor-pointer group"
                >
                  <Settings className="w-4 h-4 text-gray-500 group-hover:text-gray-800 shrink-0 stroke-[1.75]" />
                  <span className="font-normal">偏好设置</span>
                </button>

                <button
                  onClick={() => {
                    setShowUserMenu(false);
                    triggerToast('已安全退出登录');
                  }}
                  className="w-full flex items-center gap-3 px-2.5 py-1.5 rounded-lg text-[13px] text-rose-500 hover:text-rose-600 hover:bg-rose-50/70 transition-colors text-left cursor-pointer group"
                >
                  <LogOut className="w-4 h-4 text-rose-500 shrink-0 stroke-[1.75]" />
                  <span className="font-normal text-rose-500">退出登录</span>
                </button>
              </div>

              {/* Bottom Theme Switcher Bar */}
              <div className="pt-1 mt-0.5">
                <div className="bg-[#F8F9FA] border border-gray-200/80 rounded-xl p-1 grid grid-cols-3 gap-1 items-center">
                  <button
                    type="button"
                    onClick={() => {
                      setTheme('light');
                      triggerToast('已切换为浅色主题 (Light)');
                    }}
                    title="浅色模式 (Light)"
                    className={`py-1.5 flex items-center justify-center rounded-lg transition-all cursor-pointer ${
                      theme === 'light'
                        ? 'bg-white shadow-xs border border-gray-200/90 text-purple-600 font-medium'
                        : 'text-gray-500 hover:text-gray-800'
                    }`}
                  >
                    <Sun className="w-4 h-4" />
                  </button>

                  <button
                    type="button"
                    onClick={() => {
                      setTheme('dark');
                      triggerToast('已切换为深色模式 (Dark)');
                    }}
                    title="深色模式 (Dark)"
                    className={`py-1.5 flex items-center justify-center rounded-lg transition-all cursor-pointer ${
                      theme === 'dark'
                        ? 'bg-white shadow-xs border border-gray-200/90 text-purple-600 font-medium'
                        : 'text-gray-500 hover:text-gray-800'
                    }`}
                  >
                    <Moon className="w-4 h-4 fill-current" />
                  </button>

                  <button
                    type="button"
                    onClick={() => {
                      setTheme('system');
                      triggerToast('已切换为跟随系统设置 (System)');
                    }}
                    title="跟随系统 (System)"
                    className={`py-1.5 flex items-center justify-center rounded-lg transition-all cursor-pointer ${
                      theme === 'system'
                        ? 'bg-white shadow-xs border border-gray-200/90 text-purple-600 font-medium'
                        : 'text-gray-500 hover:text-gray-800'
                    }`}
                  >
                    <Monitor className="w-4 h-4" />
                  </button>
                </div>
              </div>
            </div>
          )}
        </div>
      </div>

      {showConnectKeyModal && (
        <ConnectKeyModal onClose={() => setShowConnectKeyModal(false)} />
      )}
    </header>
  );
};
