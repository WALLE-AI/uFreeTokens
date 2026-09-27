import React, { useState, useEffect } from 'react';
import {
  LayoutGrid,
  Key,
  FileText,
  Shield,
  HardDrive,
  ArrowLeftRight,
  SlidersHorizontal,
  Wrench,
  Eye,
  Tag,
  Settings,
  CircleUser,
  BarChart2,
  List,
  CreditCard,
  Lock,
  Bell,
  ShieldCheck,
  ChevronsUpDown,
  Plus,
  MoreVertical,
  Info,
  Check,
  Copy,
  Trash2,
  Search,
  CheckSquare,
  Square,
  AlertCircle,
  ExternalLink,
  X
} from 'lucide-react';
import { useApiKey, useConsoleUser, authStore } from '../api/auth';
import { getUsage, microToDisplay, UsageSnapshot } from '../api/usage';
import {
  listKeys,
  createKey as consoleCreateKey,
  revokeKey as consoleRevokeKey,
  getWallet as getConsoleWallet,
  ConsoleApiKey,
  ConsoleWallet,
} from '../api/console';
import { ApiError } from '../api/errors';
import { ConnectKeyModal } from './ConnectKeyModal';
import { LoginModal } from './LoginModal';
import { RegisterModal } from './RegisterModal';

export interface ApiKeyItem {
  id: string;
  name: string;
  maskedKey: string;
  fullKey: string;
  guardrails: string;
  expires: string;
  lastUsed: string;
  usage: string;
  limit: string;
  limitType?: string;
  createdAt: string;
}

interface PersonalDashboardPageProps {
  initialTab?: string;
  onNavigateTab?: (tab: string) => void;
  onBackToModels?: () => void;
}

export const PersonalDashboardPage: React.FC<PersonalDashboardPageProps> = ({
  initialTab = 'api-keys',
  onNavigateTab,
  onBackToModels
}) => {
  const [activeTab, setActiveTab] = useState<string>(initialTab);

  // credits tab 的真实数据（迭代2：/v1/usage 自助查询，需要已连接的 API Key）。
  const apiKey = useApiKey();
  const [usageSnapshot, setUsageSnapshot] = useState<UsageSnapshot | null>(null);
  const [usageError, setUsageError] = useState<string | null>(null);
  const [usageLoading, setUsageLoading] = useState(false);
  const [showConnectKeyModal, setShowConnectKeyModal] = useState(false);

  useEffect(() => {
    if (!apiKey) {
      setUsageSnapshot(null);
      setUsageError(null);
      return;
    }
    let cancelled = false;
    setUsageLoading(true);
    getUsage(apiKey)
      .then((snap) => {
        if (cancelled) return;
        setUsageSnapshot(snap);
        setUsageError(null);
      })
      .catch((err) => {
        if (cancelled) return;
        setUsageError(err instanceof ApiError ? err.message : '加载用量失败，请稍后重试');
      })
      .finally(() => {
        if (!cancelled) setUsageLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [apiKey]);

  const [currentWorkspace, setCurrentWorkspace] = useState<string>('默认工作区');
  const [isWorkspaceMenuOpen, setIsWorkspaceMenuOpen] = useState<boolean>(false);
  const [searchKeyQuery, setSearchKeyQuery] = useState<string>('');
  const [selectedKeyIds, setSelectedKeyIds] = useState<string[]>([]);
  const [actionMenuKeyId, setActionMenuKeyId] = useState<string | null>(null);
  const [authModal, setAuthModal] = useState<'login' | 'register' | null>(null);

  // api-keys tab 的真实数据（迭代4：/console/api-keys，需要控制台登录态，
  // 和 credits tab 的 API Key 鉴权是完全独立的两套机制）。
  const { me } = useConsoleUser();
  const [keys, setKeys] = useState<ConsoleApiKey[]>([]);
  const [keysLoading, setKeysLoading] = useState(false);
  const [keysError, setKeysError] = useState<string | null>(null);
  const [keyActionError, setKeyActionError] = useState<string | null>(null);

  // consoleWallet 是 !apiKey 但已登录控制台时 credits tab 的兜底数据源
  // （GET /console/wallet 不需要 API Key）；连了 Key 时优先用上面已有的
  // usageSnapshot（同时有余额和用量统计），见下面 credits tab 的渲染逻辑。
  const [consoleWallet, setConsoleWallet] = useState<ConsoleWallet | null>(null);

  useEffect(() => {
    if (!me) {
      setKeys([]);
      setKeysError(null);
      return;
    }
    let cancelled = false;
    setKeysLoading(true);
    listKeys()
      .then((ks) => {
        if (cancelled) return;
        setKeys(ks);
        setKeysError(null);
      })
      .catch((err) => {
        if (cancelled) return;
        setKeysError(err instanceof ApiError ? err.message : '加载密钥列表失败，请稍后重试');
      })
      .finally(() => {
        if (!cancelled) setKeysLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [me]);

  useEffect(() => {
    if (apiKey || !me) {
      setConsoleWallet(null);
      return;
    }
    let cancelled = false;
    getConsoleWallet()
      .then((w) => {
        if (!cancelled) setConsoleWallet(w);
      })
      .catch(() => {
        // 静默失败：credits tab 在这种降级路径下本来就只是"能看到点什么就
        // 看点什么"，不值得再单独维护一套错误展示。
      });
    return () => {
      cancelled = true;
    };
  }, [apiKey, me]);

  // New Key Modal state
  const [isCreateModalOpen, setIsCreateModalOpen] = useState<boolean>(false);
  const [newKeyName, setNewKeyName] = useState<string>('');
  const [useKeyForPlayground, setUseKeyForPlayground] = useState<boolean>(true);
  const [createKeyError, setCreateKeyError] = useState<string | null>(null);
  const [createdKeyObj, setCreatedKeyObj] = useState<ApiKeyItem | null>(null);
  const [copiedKeyId, setCopiedKeyId] = useState<string | null>(null);

  const workspaces = [
    '默认工作区',
    '生产环境工作区',
    '研发与测试工作区'
  ];

  // toApiKeyItem 把后端返回的 ConsoleApiKey 映射成表格渲染用的 ApiKeyItem。
  // guardrails/expires/lastUsed/usage 这几个字段后端目前没有对应数据（不是
  // 遗漏——guardrails 纯属 mock 概念，expires/usage 需要的 per-key 用量聚合
  // 是迭代5 GET /console/logs 的范畴），统一显示占位符"—"；limit 映射到
  // rpm_limit（技术方案要求）。fullKey 只有刚创建的那一把才有值，此后永远是
  // 空字符串——数据库只存 HMAC，明文真的拿不回来了。
  const toApiKeyItem = (k: ConsoleApiKey, fullKey = ''): ApiKeyItem => ({
    id: String(k.id),
    name: k.name,
    maskedKey: `${k.displayPrefix}…`,
    fullKey,
    guardrails: '—',
    expires: '—',
    lastUsed: '—',
    usage: '—',
    limit: k.rpmLimit ? `${k.rpmLimit} 次/分钟` : '不设上限',
    limitType: k.rpmLimit ? 'RPM' : undefined,
    createdAt: k.createdAt,
  });

  const handleCopyKey = (key: ApiKeyItem) => {
    if (!key.fullKey) return;
    navigator.clipboard.writeText(key.fullKey);
    setCopiedKeyId(key.id);
    setTimeout(() => {
      setCopiedKeyId(null);
    }, 2000);
  };

  const handleCreateNewKey = async (e: React.FormEvent) => {
    e.preventDefault();
    const name = newKeyName.trim() || '新建 API 密钥';
    setCreateKeyError(null);
    try {
      const created = await consoleCreateKey(name);
      setKeys((prev) => [created, ...prev]);
      setCreatedKeyObj(toApiKeyItem(created, created.key));
      setNewKeyName('');
      if (useKeyForPlayground) {
        // 让 /v1 调用（Playground）始终走 API Key 鉴权，和控制台的 Cookie
        // 会话鉴权完全分离（技术方案的"已确认的决策"）。
        authStore.setApiKey(created.key);
      }
    } catch (err) {
      setCreateKeyError(err instanceof ApiError ? err.message : '创建密钥失败，请稍后重试');
    }
  };

  const handleRevokeKey = async (id: string) => {
    setActionMenuKeyId(null);
    setKeyActionError(null);
    try {
      await consoleRevokeKey(Number(id));
      setKeys((prev) => prev.filter((k) => String(k.id) !== id));
      setSelectedKeyIds((prev) => prev.filter((item) => item !== id));
    } catch (err) {
      setKeyActionError(err instanceof ApiError ? err.message : '吊销密钥失败，请稍后重试');
    }
  };

  const toggleSelectAll = () => {
    if (selectedKeyIds.length === filteredKeys.length) {
      setSelectedKeyIds([]);
    } else {
      setSelectedKeyIds(filteredKeys.map((k) => k.id));
    }
  };

  const toggleSelectKey = (id: string) => {
    setSelectedKeyIds((prev) =>
      prev.includes(id) ? prev.filter((item) => item !== id) : [...prev, id]
    );
  };

  const filteredKeys = keys
    .map((k) => toApiKeyItem(k, createdKeyObj?.id === String(k.id) ? createdKeyObj.fullKey : ''))
    .filter(
      (k) =>
        k.name.toLowerCase().includes(searchKeyQuery.toLowerCase()) ||
        k.maskedKey.toLowerCase().includes(searchKeyQuery.toLowerCase())
    );

  return (
    <div className="flex-1 flex bg-white text-gray-800 antialiased min-h-[calc(100vh-3rem)]">
      {/* 1. Left Sidebar */}
      <aside className="w-56 shrink-0 border-r border-gray-200 bg-white flex flex-col justify-between py-3 select-none">
        <div className="px-3 space-y-4">
          {/* Workspace Dropdown */}
          <div className="relative">
            <button
              onClick={() => setIsWorkspaceMenuOpen(!isWorkspaceMenuOpen)}
              className="w-full flex items-center justify-between px-2.5 py-1.5 border border-gray-200 rounded-lg text-xs font-medium text-gray-800 hover:bg-gray-50 transition-colors shadow-2xs cursor-pointer"
            >
              <span className="truncate">{currentWorkspace}</span>
              <ChevronsUpDown className="w-3.5 h-3.5 text-gray-400 shrink-0 ml-1.5" />
            </button>

            {isWorkspaceMenuOpen && (
              <div className="absolute left-0 right-0 top-10 bg-white border border-gray-200 rounded-lg shadow-xl py-1 z-30 text-xs">
                {workspaces.map((ws) => (
                  <button
                    key={ws}
                    onClick={() => {
                      setCurrentWorkspace(ws);
                      setIsWorkspaceMenuOpen(false);
                    }}
                    className={`w-full px-3 py-1.5 text-left flex items-center justify-between hover:bg-gray-50 cursor-pointer ${
                      currentWorkspace === ws ? 'text-purple-700 font-semibold bg-purple-50/50' : 'text-gray-700'
                    }`}
                  >
                    <span className="truncate">{ws}</span>
                    {currentWorkspace === ws && <Check className="w-3.5 h-3.5 text-purple-600" />}
                  </button>
                ))}
              </div>
            )}
          </div>

          {/* Workspace Menu Section */}
          <nav className="space-y-0.5 text-xs">
            <button
              onClick={() => setActiveTab('overview')}
              className={`w-full flex items-center gap-2.5 px-2.5 py-1.5 rounded-lg text-left transition-colors cursor-pointer ${
                activeTab === 'overview'
                  ? 'bg-purple-100/70 text-purple-700 font-medium'
                  : 'text-gray-700 hover:bg-gray-100/80 hover:text-gray-900'
              }`}
            >
              <LayoutGrid className="w-4 h-4 text-gray-500 stroke-[1.75]" />
              <span>工作区概览</span>
            </button>

            <button
              onClick={() => setActiveTab('api-keys')}
              className={`w-full flex items-center gap-2.5 px-2.5 py-1.5 rounded-lg text-left transition-colors cursor-pointer ${
                activeTab === 'api-keys'
                  ? 'bg-purple-100/70 text-purple-700 font-medium'
                  : 'text-gray-700 hover:bg-gray-100/80 hover:text-gray-900'
              }`}
            >
              <Key className="w-4 h-4 text-purple-600 stroke-[1.75]" />
              <span>API 密钥</span>
            </button>

            <button
              onClick={() => setActiveTab('files')}
              className={`w-full flex items-center justify-between px-2.5 py-1.5 rounded-lg text-left transition-colors cursor-pointer ${
                activeTab === 'files'
                  ? 'bg-purple-100/70 text-purple-700 font-medium'
                  : 'text-gray-700 hover:bg-gray-100/80 hover:text-gray-900'
              }`}
            >
              <div className="flex items-center gap-2.5">
                <FileText className="w-4 h-4 text-gray-500 stroke-[1.75]" />
                <span>文件管理</span>
              </div>
              <span className="px-1.5 py-0.2 bg-blue-50 text-blue-600 border border-blue-200/60 rounded text-[10px] font-medium leading-none">
                测试版
              </span>
            </button>

            <button
              onClick={() => setActiveTab('guardrails')}
              className={`w-full flex items-center gap-2.5 px-2.5 py-1.5 rounded-lg text-left transition-colors cursor-pointer ${
                activeTab === 'guardrails'
                  ? 'bg-purple-100/70 text-purple-700 font-medium'
                  : 'text-gray-700 hover:bg-gray-100/80 hover:text-gray-900'
              }`}
            >
              <Shield className="w-4 h-4 text-gray-500 stroke-[1.75]" />
              <span>安全护栏</span>
            </button>

            <button
              onClick={() => setActiveTab('byok')}
              className={`w-full flex items-center gap-2.5 px-2.5 py-1.5 rounded-lg text-left transition-colors cursor-pointer ${
                activeTab === 'byok'
                  ? 'bg-purple-100/70 text-purple-700 font-medium'
                  : 'text-gray-700 hover:bg-gray-100/80 hover:text-gray-900'
              }`}
            >
              <HardDrive className="w-4 h-4 text-gray-500 stroke-[1.75]" />
              <span>自带密钥 (BYOK)</span>
            </button>

            <button
              onClick={() => setActiveTab('routing')}
              className={`w-full flex items-center gap-2.5 px-2.5 py-1.5 rounded-lg text-left transition-colors cursor-pointer ${
                activeTab === 'routing'
                  ? 'bg-purple-100/70 text-purple-700 font-medium'
                  : 'text-gray-700 hover:bg-gray-100/80 hover:text-gray-900'
              }`}
            >
              <ArrowLeftRight className="w-4 h-4 text-gray-500 stroke-[1.75]" />
              <span>模型路由</span>
            </button>

            <button
              onClick={() => setActiveTab('presets')}
              className={`w-full flex items-center gap-2.5 px-2.5 py-1.5 rounded-lg text-left transition-colors cursor-pointer ${
                activeTab === 'presets'
                  ? 'bg-purple-100/70 text-purple-700 font-medium'
                  : 'text-gray-700 hover:bg-gray-100/80 hover:text-gray-900'
              }`}
            >
              <SlidersHorizontal className="w-4 h-4 text-gray-500 stroke-[1.75]" />
              <span>系统预设</span>
            </button>

            <button
              onClick={() => setActiveTab('tools')}
              className={`w-full flex items-center gap-2.5 px-2.5 py-1.5 rounded-lg text-left transition-colors cursor-pointer ${
                activeTab === 'tools'
                  ? 'bg-purple-100/70 text-purple-700 font-medium'
                  : 'text-gray-700 hover:bg-gray-100/80 hover:text-gray-900'
              }`}
            >
              <Wrench className="w-4 h-4 text-gray-500 stroke-[1.75]" />
              <span>智能工具</span>
            </button>

            <button
              onClick={() => setActiveTab('observability')}
              className={`w-full flex items-center gap-2.5 px-2.5 py-1.5 rounded-lg text-left transition-colors cursor-pointer ${
                activeTab === 'observability'
                  ? 'bg-purple-100/70 text-purple-700 font-medium'
                  : 'text-gray-700 hover:bg-gray-100/80 hover:text-gray-900'
              }`}
            >
              <Eye className="w-4 h-4 text-gray-500 stroke-[1.75]" />
              <span>可观测性</span>
            </button>

            <button
              onClick={() => setActiveTab('classifiers')}
              className={`w-full flex items-center justify-between px-2.5 py-1.5 rounded-lg text-left transition-colors cursor-pointer ${
                activeTab === 'classifiers'
                  ? 'bg-purple-100/70 text-purple-700 font-medium'
                  : 'text-gray-700 hover:bg-gray-100/80 hover:text-gray-900'
              }`}
            >
              <div className="flex items-center gap-2.5">
                <Tag className="w-4 h-4 text-gray-500 stroke-[1.75]" />
                <span>分类器</span>
              </div>
              <span className="px-1.5 py-0.2 bg-blue-50 text-blue-600 border border-blue-200/60 rounded text-[10px] font-medium leading-none">
                测试版
              </span>
            </button>

            <button
              onClick={() => setActiveTab('settings')}
              className={`w-full flex items-center gap-2.5 px-2.5 py-1.5 rounded-lg text-left transition-colors cursor-pointer ${
                activeTab === 'settings'
                  ? 'bg-purple-100/70 text-purple-700 font-medium'
                  : 'text-gray-700 hover:bg-gray-100/80 hover:text-gray-900'
              }`}
            >
              <Settings className="w-4 h-4 text-gray-500 stroke-[1.75]" />
              <span>工作区设置</span>
            </button>
          </nav>

          {/* Account Group */}
          <div className="pt-2">
            <div className="text-[11px] font-medium text-gray-400 uppercase tracking-wider px-2.5 pb-1.5">
              个人账户
            </div>

            <nav className="space-y-0.5 text-xs">
              <button
                onClick={() => setActiveTab('profile')}
                className={`w-full flex items-center gap-2.5 px-2.5 py-1.5 rounded-lg text-left transition-colors cursor-pointer ${
                  activeTab === 'profile'
                    ? 'bg-purple-100/70 text-purple-700 font-medium'
                    : 'text-gray-700 hover:bg-gray-100/80 hover:text-gray-900'
                }`}
              >
                <CircleUser className="w-4 h-4 text-gray-500 stroke-[1.75]" />
                <span>个人资料</span>
              </button>

              <button
                onClick={() => setActiveTab('activity')}
                className={`w-full flex items-center gap-2.5 px-2.5 py-1.5 rounded-lg text-left transition-colors cursor-pointer ${
                  activeTab === 'activity'
                    ? 'bg-purple-100/70 text-purple-700 font-medium'
                    : 'text-gray-700 hover:bg-gray-100/80 hover:text-gray-900'
                }`}
              >
                <BarChart2 className="w-4 h-4 text-gray-500 stroke-[1.75]" />
                <span>活动记录</span>
              </button>

              <button
                onClick={() => setActiveTab('logs')}
                className={`w-full flex items-center gap-2.5 px-2.5 py-1.5 rounded-lg text-left transition-colors cursor-pointer ${
                  activeTab === 'logs'
                    ? 'bg-purple-100/70 text-purple-700 font-medium'
                    : 'text-gray-700 hover:bg-gray-100/80 hover:text-gray-900'
                }`}
              >
                <List className="w-4 h-4 text-gray-500 stroke-[1.75]" />
                <span>调用日志</span>
              </button>

              <button
                onClick={() => setActiveTab('credits')}
                className={`w-full flex items-center gap-2.5 px-2.5 py-1.5 rounded-lg text-left transition-colors cursor-pointer ${
                  activeTab === 'credits'
                    ? 'bg-purple-100/70 text-purple-700 font-medium'
                    : 'text-gray-700 hover:bg-gray-100/80 hover:text-gray-900'
                }`}
              >
                <CreditCard className="w-4 h-4 text-gray-500 stroke-[1.75]" />
                <span>余额与账单</span>
              </button>

              <button
                onClick={() => setActiveTab('management-keys')}
                className={`w-full flex items-center gap-2.5 px-2.5 py-1.5 rounded-lg text-left transition-colors cursor-pointer ${
                  activeTab === 'management-keys'
                    ? 'bg-purple-100/70 text-purple-700 font-medium'
                    : 'text-gray-700 hover:bg-gray-100/80 hover:text-gray-900'
                }`}
              >
                <Lock className="w-4 h-4 text-gray-500 stroke-[1.75]" />
                <span>管理密钥</span>
              </button>

              <button
                onClick={() => setActiveTab('notifications')}
                className={`w-full flex items-center gap-2.5 px-2.5 py-1.5 rounded-lg text-left transition-colors cursor-pointer ${
                  activeTab === 'notifications'
                    ? 'bg-purple-100/70 text-purple-700 font-medium'
                    : 'text-gray-700 hover:bg-gray-100/80 hover:text-gray-900'
                }`}
              >
                <Bell className="w-4 h-4 text-gray-500 stroke-[1.75]" />
                <span>消息通知</span>
              </button>

              <button
                onClick={() => setActiveTab('privacy')}
                className={`w-full flex items-center gap-2.5 px-2.5 py-1.5 rounded-lg text-left transition-colors cursor-pointer ${
                  activeTab === 'privacy'
                    ? 'bg-purple-100/70 text-purple-700 font-medium'
                    : 'text-gray-700 hover:bg-gray-100/80 hover:text-gray-900'
                }`}
              >
                <ShieldCheck className="w-4 h-4 text-gray-500 stroke-[1.75]" />
                <span>数据隐私</span>
              </button>

              <button
                onClick={() => setActiveTab('preferences')}
                className={`w-full flex items-center gap-2.5 px-2.5 py-1.5 rounded-lg text-left transition-colors cursor-pointer ${
                  activeTab === 'preferences'
                    ? 'bg-purple-100/70 text-purple-700 font-medium'
                    : 'text-gray-700 hover:bg-gray-100/80 hover:text-gray-900'
                }`}
              >
                <Settings className="w-4 h-4 text-gray-500 stroke-[1.75]" />
                <span>偏好设置</span>
              </button>
            </nav>
          </div>
        </div>

        {/* Return link */}
        {onBackToModels && (
          <div className="px-3 pt-4 border-t border-gray-100">
            <button
              onClick={onBackToModels}
              className="w-full text-center text-xs text-gray-500 hover:text-purple-700 hover:bg-purple-50 py-1.5 rounded transition-colors cursor-pointer"
            >
              ← 返回模型集市
            </button>
          </div>
        )}
      </aside>

      {/* 2. Main Content Area */}
      <main className="flex-1 px-8 py-6 overflow-y-auto max-w-6xl">
        {activeTab === 'api-keys' ? (
          <div>
            {/* Header: Title + Create Button */}
            <div className="flex items-center justify-between mb-4">
              <div>
                <h1 className="text-xl sm:text-2xl font-bold text-gray-900 tracking-tight">
                  API 密钥
                </h1>
                <div className="flex items-center gap-1.5 text-xs text-gray-500 mt-1">
                  <span>创建并管理您的 API 密钥与访问凭据。</span>
                  <Info className="w-3.5 h-3.5 text-gray-400 hover:text-gray-600 cursor-pointer" />
                </div>
              </div>

              {me && (
                <button
                  onClick={() => {
                    setCreatedKeyObj(null);
                    setCreateKeyError(null);
                    setIsCreateModalOpen(true);
                  }}
                  className="bg-[#7C3AED] hover:bg-[#6D28D9] text-white font-medium text-xs px-3.5 py-2 rounded-lg flex items-center gap-1.5 shadow-xs transition-colors cursor-pointer"
                >
                  <Plus className="w-3.5 h-3.5" />
                  <span>新建密钥</span>
                </button>
              )}
            </div>

            {!me ? (
              <div className="bg-gray-50 border border-dashed border-gray-200 rounded-xl p-6 text-center">
                <div className="text-xs text-gray-500 mb-3">登录控制台后即可创建和管理自己的 API 密钥。</div>
                <div className="flex items-center justify-center gap-2">
                  <button
                    onClick={() => setAuthModal('login')}
                    className="px-3.5 py-1.5 border border-gray-200 text-gray-700 rounded-lg text-xs cursor-pointer"
                  >
                    登录
                  </button>
                  <button
                    onClick={() => setAuthModal('register')}
                    className="px-3.5 py-1.5 bg-purple-600 hover:bg-purple-700 text-white rounded-lg text-xs font-medium cursor-pointer"
                  >
                    注册
                  </button>
                </div>
              </div>
            ) : (
              <>
                {keyActionError && (
                  <div className="mb-3 bg-rose-50 border border-rose-200 text-rose-700 text-xs rounded-lg px-3 py-2">
                    {keyActionError}
                  </div>
                )}

                {/* Search Bar */}
                <div className="mb-4">
                  <div className="relative w-full max-w-sm">
                    <Search className="w-3.5 h-3.5 text-gray-400 absolute left-3 top-1/2 -translate-y-1/2" />
                    <input
                      type="text"
                      value={searchKeyQuery}
                      onChange={(e) => setSearchKeyQuery(e.target.value)}
                      placeholder="按名称搜索或粘贴密钥片段..."
                      className="w-full bg-white border border-gray-200 rounded-lg pl-9 pr-3 py-1.5 text-xs text-gray-800 placeholder-gray-400 focus:outline-none focus:border-purple-500 focus:ring-1 focus:ring-purple-500 transition-colors"
                    />
                  </div>
                </div>

                {keysError ? (
                  <div className="bg-rose-50 border border-rose-200 text-rose-700 text-xs rounded-xl p-4">{keysError}</div>
                ) : keysLoading && keys.length === 0 ? (
                  <div className="text-xs text-gray-400 py-6 text-center">正在加载密钥列表...</div>
                ) : (
                <>
            {/* Keys Table */}
            <div className="border border-gray-200 rounded-xl overflow-hidden bg-white shadow-2xs">
              <div className="overflow-x-auto">
                <table className="w-full text-left text-xs">
                  <thead>
                    <tr className="border-b border-gray-200 bg-white text-gray-500 font-medium">
                      <th className="py-2.5 px-3 w-8">
                        <button
                          onClick={toggleSelectAll}
                          className="text-gray-400 hover:text-gray-600 cursor-pointer flex items-center"
                        >
                          {selectedKeyIds.length > 0 && selectedKeyIds.length === filteredKeys.length ? (
                            <CheckSquare className="w-4 h-4 text-purple-600" />
                          ) : (
                            <Square className="w-4 h-4" />
                          )}
                        </button>
                      </th>
                      <th className="py-2.5 px-3">密钥名称</th>
                      <th className="py-2.5 px-3">安全护栏</th>
                      <th className="py-2.5 px-3">过期时间</th>
                      <th className="py-2.5 px-3">最近使用</th>
                      <th className="py-2.5 px-3">已用额度</th>
                      <th className="py-2.5 px-3">额度上限</th>
                      <th className="py-2.5 px-3 w-10 text-right"></th>
                    </tr>
                  </thead>
                  <tbody className="divide-y divide-gray-100 text-gray-700">
                    {filteredKeys.length === 0 ? (
                      <tr>
                        <td colSpan={8} className="py-8 text-center text-gray-400">
                          没有找到符合条件的 API 密钥
                        </td>
                      </tr>
                    ) : (
                      filteredKeys.map((k) => {
                        const isSelected = selectedKeyIds.includes(k.id);
                        return (
                          <tr
                            key={k.id}
                            className={`hover:bg-gray-50/70 transition-colors ${
                              isSelected ? 'bg-purple-50/30' : ''
                            }`}
                          >
                            <td className="py-3 px-3">
                              <button
                                onClick={() => toggleSelectKey(k.id)}
                                className="text-gray-400 hover:text-gray-600 cursor-pointer flex items-center"
                              >
                                {isSelected ? (
                                  <CheckSquare className="w-4 h-4 text-purple-600" />
                                ) : (
                                  <Square className="w-4 h-4" />
                                )}
                              </button>
                            </td>
                            <td className="py-3 px-3">
                              <div className="font-semibold text-gray-900 text-xs">{k.name}</div>
                              <div className="flex items-center gap-1.5 text-[11px] text-gray-400 font-mono mt-0.5">
                                <span>{k.maskedKey}</span>
                                {k.fullKey && (
                                  <button
                                    onClick={() => handleCopyKey(k)}
                                    title="复制完整密钥"
                                    className="text-gray-400 hover:text-purple-600 cursor-pointer transition-colors p-0.5 rounded"
                                  >
                                    {copiedKeyId === k.id ? (
                                      <Check className="w-3 h-3 text-emerald-600" />
                                    ) : (
                                      <Copy className="w-3 h-3" />
                                    )}
                                  </button>
                                )}
                              </div>
                            </td>
                            <td className="py-3 px-3 text-gray-600">{k.guardrails}</td>
                            <td className="py-3 px-3 text-gray-600">{k.expires}</td>
                            <td className="py-3 px-3 text-gray-600">{k.lastUsed}</td>
                            <td className="py-3 px-3 font-mono text-gray-800">{k.usage}</td>
                            <td className="py-3 px-3">
                              <div className="flex items-center gap-1">
                                <span className="text-gray-700">{k.limit}</span>
                                {k.limitType && (
                                  <span className="px-1.5 py-0.2 bg-gray-100 text-gray-500 rounded text-[10px] font-mono">
                                    {k.limitType}
                                  </span>
                                )}
                              </div>
                            </td>
                            <td className="py-3 px-3 text-right relative">
                              <button
                                onClick={() =>
                                  setActionMenuKeyId(actionMenuKeyId === k.id ? null : k.id)
                                }
                                className="p-1 rounded text-gray-400 hover:text-gray-700 hover:bg-gray-100 cursor-pointer"
                              >
                                <MoreVertical className="w-4 h-4" />
                              </button>

                              {actionMenuKeyId === k.id && (
                                <div className="absolute right-3 top-8 bg-white border border-gray-200 rounded-lg shadow-lg py-1 z-20 w-32 text-xs">
                                  {k.fullKey && (
                                    <button
                                      onClick={() => {
                                        handleCopyKey(k);
                                        setActionMenuKeyId(null);
                                      }}
                                      className="w-full px-3 py-1.5 text-left flex items-center gap-2 hover:bg-gray-50 text-gray-700 cursor-pointer"
                                    >
                                      <Copy className="w-3.5 h-3.5 text-gray-400" />
                                      <span>复制密钥</span>
                                    </button>
                                  )}
                                  <button
                                    onClick={() => handleRevokeKey(k.id)}
                                    className="w-full px-3 py-1.5 text-left flex items-center gap-2 hover:bg-rose-50 text-rose-600 cursor-pointer border-t border-gray-100"
                                  >
                                    <Trash2 className="w-3.5 h-3.5" />
                                    <span>吊销密钥</span>
                                  </button>
                                </div>
                              )}
                            </td>
                          </tr>
                        );
                      })
                    )}
                  </tbody>
                </table>
              </div>

              {/* Table Footer Count */}
              <div className="py-2.5 px-3 border-t border-gray-100 text-[11px] text-gray-500 bg-white">
                共 {filteredKeys.length} 个密钥
              </div>
            </div>
                </>
                )}
              </>
            )}
          </div>
        ) : activeTab === 'credits' ? (
          <div>
            <h1 className="text-xl sm:text-2xl font-bold text-gray-900 tracking-tight mb-2">
              余额与账单
            </h1>
            <p className="text-xs text-gray-500 mb-6">管理您的账户余额、支付方式与额度预警。</p>

            {!apiKey && !me ? (
              <div className="bg-gray-50 border border-dashed border-gray-200 rounded-xl p-6 text-center">
                <div className="text-xs text-gray-500 mb-3">
                  登录控制台或连接 API Key 后即可查看真实的账户余额与累计用量。
                </div>
                <div className="flex items-center justify-center gap-2">
                  <button
                    onClick={() => setAuthModal('login')}
                    className="px-3.5 py-1.5 border border-gray-200 text-gray-700 rounded-lg text-xs cursor-pointer"
                  >
                    登录
                  </button>
                  <button
                    onClick={() => setShowConnectKeyModal(true)}
                    className="px-3.5 py-1.5 bg-purple-600 hover:bg-purple-700 text-white rounded-lg text-xs font-medium cursor-pointer"
                  >
                    连接 API Key
                  </button>
                </div>
              </div>
            ) : !apiKey && me ? (
              consoleWallet ? (
                <div className="grid grid-cols-1 sm:grid-cols-3 gap-4 mb-6">
                  <div className="bg-purple-50/60 border border-purple-100 p-4 rounded-xl">
                    <div className="text-xs text-purple-700 font-medium">可用总额度</div>
                    <div className="text-2xl font-bold text-purple-900 mt-1">
                      {microToDisplay(consoleWallet.cashBalanceMicro + consoleWallet.bonusBalanceMicro)}
                    </div>
                    <div className="text-[11px] text-purple-600/80 mt-1">
                      现金 {microToDisplay(consoleWallet.cashBalanceMicro)} · 赠送{' '}
                      {microToDisplay(consoleWallet.bonusBalanceMicro)}
                    </div>
                  </div>
                  <div className="sm:col-span-2 bg-gray-50 border border-dashed border-gray-200 p-4 rounded-xl flex items-center justify-between gap-3">
                    <div className="text-xs text-gray-500">
                      连接 API Key 后可以查看调用次数、消费明细与冻结中金额。
                    </div>
                    <button
                      onClick={() => setShowConnectKeyModal(true)}
                      className="px-3 py-1.5 bg-purple-600 hover:bg-purple-700 text-white rounded-lg text-xs font-medium cursor-pointer shrink-0"
                    >
                      连接 API Key
                    </button>
                  </div>
                </div>
              ) : (
                <div className="text-xs text-gray-400 py-6 text-center">正在加载余额...</div>
              )
            ) : usageLoading && !usageSnapshot ? (
              <div className="text-xs text-gray-400 py-6 text-center">正在加载余额与用量...</div>
            ) : usageError ? (
              <div className="bg-rose-50 border border-rose-200 text-rose-700 text-xs rounded-xl p-4">
                {usageError}
              </div>
            ) : usageSnapshot ? (
              <div className="grid grid-cols-1 sm:grid-cols-3 gap-4 mb-6">
                <div className="bg-purple-50/60 border border-purple-100 p-4 rounded-xl">
                  <div className="text-xs text-purple-700 font-medium">可用总额度</div>
                  <div className="text-2xl font-bold text-purple-900 mt-1">
                    {microToDisplay(
                      usageSnapshot.wallet.cashBalanceMicro + usageSnapshot.wallet.bonusBalanceMicro
                    )}
                  </div>
                  <div className="text-[11px] text-purple-600/80 mt-1">
                    现金 {microToDisplay(usageSnapshot.wallet.cashBalanceMicro)} · 赠送{' '}
                    {microToDisplay(usageSnapshot.wallet.bonusBalanceMicro)}
                  </div>
                </div>

                <div className="bg-gray-50 border border-gray-200 p-4 rounded-xl">
                  <div className="text-xs text-gray-500 font-medium">累计已消费</div>
                  <div className="text-2xl font-bold text-gray-800 mt-1">
                    {microToDisplay(usageSnapshot.usage.totalChargedAmountMicro)}
                  </div>
                  <div className="text-[11px] text-gray-400 mt-1">
                    包含 {usageSnapshot.usage.totalRequests} 次成功调用
                  </div>
                </div>

                <div className="bg-gray-50 border border-gray-200 p-4 rounded-xl">
                  <div className="text-xs text-gray-500 font-medium">冻结中金额</div>
                  <div className="text-2xl font-bold text-amber-600 mt-1">
                    {microToDisplay(usageSnapshot.wallet.frozenMicro)}
                  </div>
                  <div className="text-[11px] text-gray-400 mt-1">
                    输入 {usageSnapshot.usage.totalInputTokens.toLocaleString()} · 输出{' '}
                    {usageSnapshot.usage.totalOutputTokens.toLocaleString()} tokens
                  </div>
                </div>
              </div>
            ) : null}
          </div>
        ) : activeTab === 'profile' ? (
          <div>
            <h1 className="text-xl sm:text-2xl font-bold text-gray-900 tracking-tight mb-2">
              个人资料
            </h1>
            <p className="text-xs text-gray-500 mb-6">个人账号信息与开发者凭据。</p>

            {!me ? (
              <div className="bg-gray-50 border border-dashed border-gray-200 rounded-xl p-6 text-center max-w-xl">
                <div className="text-xs text-gray-500 mb-3">登录控制台后即可查看个人资料。</div>
                <div className="flex items-center justify-center gap-2">
                  <button
                    onClick={() => setAuthModal('login')}
                    className="px-3.5 py-1.5 border border-gray-200 text-gray-700 rounded-lg text-xs cursor-pointer"
                  >
                    登录
                  </button>
                  <button
                    onClick={() => setAuthModal('register')}
                    className="px-3.5 py-1.5 bg-purple-600 hover:bg-purple-700 text-white rounded-lg text-xs font-medium cursor-pointer"
                  >
                    注册
                  </button>
                </div>
              </div>
            ) : (
            <div className="bg-white border border-gray-200 rounded-xl p-6 max-w-xl space-y-4">
              <div className="flex items-center gap-4">
                <div className="w-12 h-12 rounded-full bg-purple-700 text-white flex items-center justify-center text-lg font-bold">
                  {me.email.charAt(0).toUpperCase()}
                </div>
                <div>
                  <div className="font-semibold text-gray-900">Personal Developer</div>
                  <div className="text-xs text-gray-500">{me.email}</div>
                </div>
              </div>

              <div className="border-t border-gray-100 pt-4 space-y-3 text-xs">
                <div>
                  <span className="text-gray-400 block mb-1">账号层级</span>
                  <span className="font-medium text-gray-800">{me.accountTier}</span>
                </div>
                <div>
                  <span className="text-gray-400 block mb-1">邮箱验证状态</span>
                  <span className="font-medium text-gray-800">{me.emailVerified ? '已验证' : '未验证'}</span>
                </div>
              </div>
            </div>
            )}
          </div>
        ) : activeTab === 'activity' || activeTab === 'logs' ? (
          <div>
            <h1 className="text-xl sm:text-2xl font-bold text-gray-900 tracking-tight mb-2">
              {activeTab === 'activity' ? '活动记录' : '调用日志'}
            </h1>
            <p className="text-xs text-gray-500 mb-6">实时请求追踪与审计历史。</p>

            <div className="bg-white border border-gray-200 rounded-xl p-4 text-xs">
              <div className="flex items-center justify-between pb-3 border-b border-gray-100 font-medium text-gray-500">
                <span>时间</span>
                <span>模型</span>
                <span>Tokens</span>
                <span>状态</span>
              </div>
              <div className="divide-y divide-gray-50">
                <div className="py-2.5 flex items-center justify-between text-gray-700">
                  <span className="font-mono text-gray-400 text-[11px]">7 天前 14:23</span>
                  <span className="font-semibold text-gray-900">deepseek/deepseek-pro</span>
                  <span className="font-mono text-gray-500">1,248 tok</span>
                  <span className="px-2 py-0.5 rounded bg-emerald-50 text-emerald-700 text-[10px] font-medium">200 成功</span>
                </div>
                <div className="py-2.5 flex items-center justify-between text-gray-700">
                  <span className="font-mono text-gray-400 text-[11px]">7 天前 14:21</span>
                  <span className="font-semibold text-gray-900">openai/gpt-4o</span>
                  <span className="font-mono text-gray-500">856 tok</span>
                  <span className="px-2 py-0.5 rounded bg-emerald-50 text-emerald-700 text-[10px] font-medium">200 成功</span>
                </div>
              </div>
            </div>
          </div>
        ) : (
          <div>
            <h1 className="text-xl sm:text-2xl font-bold text-gray-900 tracking-tight mb-2">
              {(() => {
                const tabTitles: Record<string, string> = {
                  overview: '工作区概览',
                  files: '文件管理',
                  guardrails: '安全护栏',
                  byok: '自带密钥 (BYOK)',
                  routing: '模型路由',
                  presets: '系统预设',
                  tools: '智能工具',
                  observability: '可观测性',
                  classifiers: '分类器',
                  settings: '工作区设置',
                  'management-keys': '管理密钥',
                  notifications: '消息通知',
                  privacy: '数据隐私',
                  preferences: '偏好设置'
                };
                return tabTitles[activeTab] || activeTab;
              })()}
            </h1>
            <p className="text-xs text-gray-500 mb-6">配置您的工作区与运行参数。</p>
            <div className="bg-white border border-gray-200 rounded-xl p-6 text-xs text-gray-500">
              当前模块运行正常，所有策略均已配置为系统默认优选。
            </div>
          </div>
        )}
      </main>

      {/* 3. New Key Modal */}
      {isCreateModalOpen && (
        <div className="fixed inset-0 bg-black/40 backdrop-blur-xs flex items-center justify-center z-50 p-4">
          <div className="bg-white border border-gray-200 rounded-2xl max-w-md w-full p-6 shadow-2xl animate-in fade-in zoom-in-95 duration-150">
            {createdKeyObj ? (
              <div>
                <div className="w-10 h-10 rounded-full bg-emerald-50 text-emerald-600 flex items-center justify-center mb-3">
                  <Check className="w-5 h-5" />
                </div>
                <h3 className="text-base font-bold text-gray-900">API 密钥已创建成功</h3>
                <p className="text-xs text-gray-500 mt-1">
                  请妥善保存此密钥。出于安全考虑，离开此页面后将无法再次查看完整密钥。
                </p>

                <div className="mt-4 bg-gray-50 border border-gray-200 rounded-lg p-3 flex items-center justify-between">
                  <span className="font-mono text-xs text-gray-800 break-all select-all">
                    {createdKeyObj.fullKey}
                  </span>
                  <button
                    onClick={() => handleCopyKey(createdKeyObj)}
                    className="ml-2 px-2.5 py-1.5 bg-purple-600 text-white rounded text-xs hover:bg-purple-700 flex items-center gap-1 shrink-0 cursor-pointer"
                  >
                    {copiedKeyId === createdKeyObj.id ? (
                      <>
                        <Check className="w-3.5 h-3.5" />
                        <span>已复制</span>
                      </>
                    ) : (
                      <>
                        <Copy className="w-3.5 h-3.5" />
                        <span>复制</span>
                      </>
                    )}
                  </button>
                </div>

                <div className="mt-6 flex justify-end">
                  <button
                    onClick={() => {
                      setIsCreateModalOpen(false);
                      setCreatedKeyObj(null);
                    }}
                    className="px-4 py-2 bg-gray-900 text-white text-xs rounded-lg hover:bg-gray-800 cursor-pointer"
                  >
                    完成
                  </button>
                </div>
              </div>
            ) : (
              <form onSubmit={handleCreateNewKey}>
                <div className="flex items-center justify-between pb-3 border-b border-gray-100">
                  <h3 className="text-sm font-bold text-gray-900">创建新的 API 密钥</h3>
                  <button
                    type="button"
                    onClick={() => setIsCreateModalOpen(false)}
                    className="text-gray-400 hover:text-gray-600 p-1"
                  >
                    <X className="w-4 h-4" />
                  </button>
                </div>

                <div className="space-y-4 py-4 text-xs">
                  <div>
                    <label className="block text-gray-700 font-medium mb-1">密钥名称</label>
                    <input
                      type="text"
                      required
                      placeholder="例如：开发环境密钥、Cursor 客户端"
                      value={newKeyName}
                      onChange={(e) => setNewKeyName(e.target.value)}
                      className="w-full border border-gray-200 rounded-lg px-3 py-2 focus:outline-none focus:border-purple-500"
                    />
                  </div>

                  <label className="flex items-start gap-2 cursor-pointer">
                    <input
                      type="checkbox"
                      checked={useKeyForPlayground}
                      onChange={(e) => setUseKeyForPlayground(e.target.checked)}
                      className="mt-0.5"
                    />
                    <span className="text-gray-600">
                      在本浏览器用于 Playground —— 勾选后这把密钥会保存在本机浏览器
                      （localStorage），供模型库的"测试"功能直接调用真实模型。
                    </span>
                  </label>

                  {createKeyError && <p className="text-rose-500">{createKeyError}</p>}
                </div>

                <div className="flex justify-end gap-2 pt-3 border-t border-gray-100">
                  <button
                    type="button"
                    onClick={() => setIsCreateModalOpen(false)}
                    className="px-3.5 py-1.5 border border-gray-200 text-gray-700 rounded-lg hover:bg-gray-50 text-xs cursor-pointer"
                  >
                    取消
                  </button>
                  <button
                    type="submit"
                    className="px-4 py-1.5 bg-purple-600 hover:bg-purple-700 text-white rounded-lg text-xs font-medium cursor-pointer shadow-xs"
                  >
                    立即创建
                  </button>
                </div>
              </form>
            )}
          </div>
        </div>
      )}

      {showConnectKeyModal && (
        <ConnectKeyModal onClose={() => setShowConnectKeyModal(false)} />
      )}
      {authModal === 'login' && (
        <LoginModal onClose={() => setAuthModal(null)} onSwitchToRegister={() => setAuthModal('register')} />
      )}
      {authModal === 'register' && (
        <RegisterModal onClose={() => setAuthModal(null)} onSwitchToLogin={() => setAuthModal('login')} />
      )}
    </div>
  );
};
