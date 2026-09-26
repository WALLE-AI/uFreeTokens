// parseSSE 把一个 fetch 响应体（ReadableStream<Uint8Array>）解析成逐条 SSE
// "data:" 负载字符串。按空行（"\n\n"）切分事件，不假设单个事件在一次
// read() 里就能读全——大段 tool_calls/长回复会跨多个 chunk 到达。遇到
// "data: [DONE]" 时结束（OpenAI 流式约定的终止标记）。
export async function* parseSSE(body: ReadableStream<Uint8Array>): AsyncGenerator<string> {
  const reader = body.getReader();
  const decoder = new TextDecoder();
  let buffer = '';

  try {
    while (true) {
      const { done, value } = await reader.read();
      if (done) break;
      buffer += decoder.decode(value, { stream: true });

      let sepIndex = buffer.indexOf('\n\n');
      while (sepIndex !== -1) {
        const rawEvent = buffer.slice(0, sepIndex);
        buffer = buffer.slice(sepIndex + 2);
        const data = extractData(rawEvent);
        if (data !== null) {
          if (data === '[DONE]') return;
          yield data;
        }
        sepIndex = buffer.indexOf('\n\n');
      }
    }
    // 流结束但缓冲区里还残留最后一段没有以 "\n\n" 收尾的内容（少见，比如上游
    // 忘了发终止空行就直接关闭了连接），尽量把它当最后一个事件处理掉。
    const data = extractData(buffer);
    if (data !== null && data !== '[DONE]') yield data;
  } finally {
    reader.releaseLock();
  }
}

function extractData(rawEvent: string): string | null {
  const dataLines = rawEvent
    .split('\n')
    .filter((line) => line.startsWith('data:'))
    .map((line) => line.slice(5).replace(/^ /, ''));
  if (dataLines.length === 0) return null;
  return dataLines.join('\n');
}
