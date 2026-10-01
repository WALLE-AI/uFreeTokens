// ApiError 统一封装 cmd/admin 的错误响应（httpx.WriteError 的
// {"error":{"message","type","code","request_id"}} 形状），并把已知的 code
// 映射成中文提示。后台用户是运营人员，除了中文提示还会保留后端原文
// （detail）和 request_id，方便对照后端日志排查。
export interface ApiErrorBody {
  message: string;
  type: string;
  code: string;
  request_id?: string;
}

const CODE_MESSAGES: Record<string, string> = {
  not_found: '对象不存在或已被删除',
  conflict: '操作冲突：对象状态已变化，或重复提交',
  invalid_request: '请求参数有误',
  upstream_unavailable: '上游服务不可用，请检查供应商地址与密钥',
  not_implemented: '该功能在当前服务器上未启用',
  balance_changed: '余额已发生变化，请刷新后重新确认',
  authentication_error: '登录已失效，请重新登录',
  unauthorized: '登录已失效，请重新登录',
  invalid_credentials: '邮箱或密码错误',
  totp_required: '请输入两步验证码',
  invalid_totp: '两步验证码不正确或已使用',
  precondition_required: '请刷新页面后再修改（需要最新版本号）',
  permission_denied: '没有执行此操作的权限，请联系管理员分配角色',
  version_conflict: '数据已被他人修改，请刷新页面后重试',
  invalid_reference: '引用的对象不存在或仍在使用中',
  constraint_violation: '数据不满足约束条件，请检查输入',
  unsafe_upstream_url: '上游地址不安全：需要 https、在供应商域名白名单内且不能指向内网地址',
  kek_not_configured: '服务器未配置密钥加密密钥（KEK），无法使用上游密钥',
  idempotency_key_reused: '重复提交的内容与第一次不一致，请刷新后重试',
  idempotency_in_progress: '上一次提交仍在处理中，请稍候',
  retry: '并发冲突，请重试',
  rate_limit_exceeded: '请求过于频繁，请稍后重试',
  internal_error: '服务内部错误，请稍后重试',
  network_error: '无法连接到管理后台服务，请检查网络或服务是否启动',
  timeout: '请求超时，请稍后重试',
};

export class ApiError extends Error {
  readonly status: number;
  readonly code: string;
  readonly type: string;
  readonly detail: string;
  readonly requestId?: string;

  constructor(message: string, status: number, code: string, detail = '', type = '', requestId?: string) {
    super(message);
    this.name = 'ApiError';
    this.status = status;
    this.code = code;
    this.detail = detail;
    this.type = type;
    this.requestId = requestId;
  }

  static async fromResponse(res: Response): Promise<ApiError> {
    let body: { error?: ApiErrorBody } | null = null;
    try {
      body = await res.json();
    } catch {
      // 反代/服务本身没起来时响应体可能不是 JSON，保留状态码兜底提示。
    }
    const err = body?.error;
    const code = err?.code ?? (res.status === 401 ? 'unauthorized' : 'unknown_error');
    const friendly = CODE_MESSAGES[code] ?? err?.message ?? `请求失败（HTTP ${res.status}）`;
    const requestId = err?.request_id ?? res.headers.get('X-Request-Id') ?? undefined;
    return new ApiError(friendly, res.status, code, err?.message ?? '', err?.type ?? '', requestId);
  }

  static network(timedOut: boolean): ApiError {
    const code = timedOut ? 'timeout' : 'network_error';
    return new ApiError(CODE_MESSAGES[code], 0, code);
  }
}

export function errorMessage(err: unknown, fallback = '操作失败'): string {
  if (err instanceof ApiError) return err.message;
  if (err instanceof Error && err.message) return err.message;
  return fallback;
}

// describeError：中文提示 + 后端原文（两者不同时），用于 toast / 表单错误。
export function describeError(err: unknown, fallback = '操作失败'): string {
  const msg = errorMessage(err, fallback);
  if (err instanceof ApiError && err.detail && err.detail !== msg) return `${msg}：${err.detail}`;
  return msg;
}
