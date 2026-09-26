// ApiError 统一封装网关的错误响应（httpx.WriteError 的
// {"error":{"message","type","code","request_id"}} 形状），并把已知的 code
// 映射成中文提示，供 UI 直接展示。
export interface ApiErrorBody {
  message: string;
  type: string;
  code: string;
  request_id?: string;
}

const CODE_MESSAGES: Record<string, string> = {
  insufficient_balance: '余额不足，请先充值',
  invalid_api_key: 'API Key 无效或已被吊销，请重新连接',
  model_not_allowed: '当前 Key 不允许调用该模型',
  model_not_found: '模型不存在，或对当前账户不可见',
  rate_limit_exceeded: '请求过于频繁，请稍后重试',
  concurrency_limit_exceeded: '并发请求过多，请稍后重试',
  no_available_channel: '暂无可用上游渠道，请稍后重试',
  upstream_error: '上游服务异常，请稍后重试',
  content_filtered: '内容被安全策略拦截',
  invalid_request: '请求参数有误',
  not_implemented: '该功能尚未开放',
  internal_error: '服务内部错误，请稍后重试',
};

export class ApiError extends Error {
  readonly status: number;
  readonly code: string;
  readonly type: string;
  readonly requestId?: string;

  constructor(message: string, status: number, code: string, type = '', requestId?: string) {
    super(message);
    this.name = 'ApiError';
    this.status = status;
    this.code = code;
    this.type = type;
    this.requestId = requestId;
  }

  static async fromResponse(res: Response): Promise<ApiError> {
    let body: { error?: ApiErrorBody } | null = null;
    try {
      body = await res.json();
    } catch {
      // 网络层/网关本身没起来时响应体可能根本不是 JSON，保留状态码兜底提示。
    }
    const err = body?.error;
    const code = err?.code ?? 'unknown_error';
    const friendly = CODE_MESSAGES[code] ?? err?.message ?? `请求失败（HTTP ${res.status}）`;
    return new ApiError(friendly, res.status, code, err?.type ?? '', err?.request_id);
  }
}
