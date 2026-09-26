import { resolveURL } from './client';
import { authStore } from './auth';
import { ApiError } from './errors';
import { parseSSE } from './sse';

export interface ChatMessage {
  role: 'system' | 'user' | 'assistant';
  content: string;
}

export interface ChatUsage {
  promptTokens: number;
  completionTokens: number;
  totalTokens: number;
}

export interface StreamChatParams {
  model: string;
  messages: ChatMessage[];
  temperature?: number;
  maxTokens?: number;
  signal?: AbortSignal;
}

// streamChat 调用 /v1/chat/completions（流式），逐块把内容增量回调给
// onDelta，结束时（如果上游给了）把用量回调给 onUsage。总是自己带上
// stream_options.include_usage=true——这是这条链路唯一关心 usage 的调用方，
// 即便迭代1让网关本来就会无条件向上游注入它，前端自己声明一下更清楚地表达
// "我要看 usage"这个意图，也不依赖后端实现细节。
export async function streamChat(
  params: StreamChatParams,
  onDelta: (text: string) => void,
  onUsage?: (usage: ChatUsage) => void
): Promise<void> {
  const apiKey = authStore.getApiKey();
  if (!apiKey) {
    throw new ApiError('尚未连接 API Key', 401, 'invalid_api_key');
  }

  const res = await fetch(resolveURL('/v1/chat/completions'), {
    method: 'POST',
    credentials: 'same-origin',
    signal: params.signal,
    headers: {
      'Content-Type': 'application/json',
      Authorization: `Bearer ${apiKey}`,
    },
    body: JSON.stringify({
      model: params.model,
      messages: params.messages,
      temperature: params.temperature,
      max_tokens: params.maxTokens,
      stream: true,
      stream_options: { include_usage: true },
    }),
  });

  if (!res.ok) {
    throw await ApiError.fromResponse(res);
  }
  if (!res.body) {
    throw new ApiError('当前浏览器不支持流式响应', 0, 'stream_unsupported');
  }

  for await (const data of parseSSE(res.body)) {
    let chunk: {
      choices?: { delta?: { content?: string } }[];
      usage?: { prompt_tokens?: number; completion_tokens?: number; total_tokens?: number };
    };
    try {
      chunk = JSON.parse(data);
    } catch {
      continue; // 个别厂商会在流里夹杂非标准行，忽略，继续读下一条
    }

    const delta = chunk.choices?.[0]?.delta?.content;
    if (typeof delta === 'string' && delta.length > 0) {
      onDelta(delta);
    }
    if (chunk.usage && onUsage) {
      const prompt = chunk.usage.prompt_tokens ?? 0;
      const completion = chunk.usage.completion_tokens ?? 0;
      onUsage({
        promptTokens: prompt,
        completionTokens: completion,
        totalTokens: chunk.usage.total_tokens ?? prompt + completion,
      });
    }
  }
}
