import React, { useState, useEffect, useRef } from 'react';
import {
  X,
  Send,
  Sparkles,
  Bot,
  User,
  Sliders,
  RotateCcw,
  Check,
  Copy,
  Zap
} from 'lucide-react';
import { Model } from '../types';
import { ProviderIcon } from './ProviderIcon';
import { useApiKey } from '../api/auth';
import { streamChat, ChatUsage } from '../api/chat';
import { ApiError } from '../api/errors';
import { ConnectKeyModal } from './ConnectKeyModal';

interface PlaygroundModalProps {
  model: Model;
  onClose: () => void;
}

interface ChatMessage {
  role: 'user' | 'assistant' | 'system';
  content: string;
  usage?: ChatUsage;
  isError?: boolean;
}

export const PlaygroundModal: React.FC<PlaygroundModalProps> = ({ model, onClose }) => {
  const apiKey = useApiKey();
  const [messages, setMessages] = useState<ChatMessage[]>([
    {
      role: 'assistant',
      content: `你好！我是 **${model.name}**。当前已接入 uFreeTokens API 路由体系。你可以向我提出任何编程、逻辑推演、数学运算或通用创作任务。`,
    },
  ]);
  const [input, setInput] = useState('');
  const [isGenerating, setIsGenerating] = useState(false);
  const [temperature, setTemperature] = useState(0.7);
  const [maxTokens, setMaxTokens] = useState(2048);
  const [systemPrompt, setSystemPrompt] = useState('你是一个专业、严谨且乐于助人的 AI 智能助手。');
  const [showSettings, setShowSettings] = useState(false);
  const [showConnectKeyModal, setShowConnectKeyModal] = useState(false);
  const abortRef = useRef<AbortController | null>(null);

  // 弹窗关闭（卸载）时如果还有一个请求在飞，abort 掉——技术方案要求"关闭
  // 弹窗或清空对话时 abort"，App.tsx 是条件渲染这个组件的（activeModelForPlayground
  // 为 null 时直接卸载），所以卸载清理就覆盖了"关闭弹窗"这一种情况；
  // "清空对话"另见 handleClear。
  useEffect(() => {
    return () => {
      abortRef.current?.abort();
    };
  }, []);

  const samplePrompts = [
    '用 React 编写一个带虚拟滚动的列表组件',
    '解释量子计算的基本原理与传统密码学的关系',
    '给这段 Python 代码做性能分析与优化建议',
  ];

  const appendDelta = (delta: string) => {
    setMessages((prev) => {
      const next = [...prev];
      const last = next[next.length - 1];
      next[next.length - 1] = { ...last, content: last.content + delta };
      return next;
    });
  };

  const appendUsage = (usage: ChatUsage) => {
    setMessages((prev) => {
      const next = [...prev];
      next[next.length - 1] = { ...next[next.length - 1], usage };
      return next;
    });
  };

  const handleSend = async () => {
    if (!input.trim() || isGenerating) return;
    if (!apiKey) {
      setShowConnectKeyModal(true);
      return;
    }

    const userText = input.trim();
    setInput('');

    const newMessages: ChatMessage[] = [...messages, { role: 'user', content: userText }];
    setMessages([...newMessages, { role: 'assistant', content: '' }]);
    setIsGenerating(true);

    const controller = new AbortController();
    abortRef.current = controller;

    try {
      await streamChat(
        {
          model: model.id,
          messages: [
            { role: 'system', content: systemPrompt },
            ...newMessages.map((m) => ({ role: m.role, content: m.content })),
          ],
          temperature,
          maxTokens,
          signal: controller.signal,
        },
        appendDelta,
        appendUsage
      );
    } catch (err) {
      if (!(err instanceof DOMException && err.name === 'AbortError')) {
        const message = err instanceof ApiError ? err.message : '生成失败，请稍后重试。';
        setMessages((prev) => {
          const next = [...prev];
          next[next.length - 1] = { ...next[next.length - 1], content: message, isError: true };
          return next;
        });
      }
    } finally {
      if (abortRef.current === controller) {
        abortRef.current = null;
      }
      setIsGenerating(false);
    }
  };

  const handleClear = () => {
    abortRef.current?.abort();
    setIsGenerating(false);
    setMessages([
      {
        role: 'assistant',
        content: `对话已重置。当前模型为 **${model.name}**，欢迎开始新的测试。`,
      },
    ]);
  };

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center p-3 sm:p-4 bg-black/40 backdrop-blur-xs">
      <div
        className="bg-white rounded-xl shadow-2xl max-w-4xl w-full h-[85vh] flex flex-col overflow-hidden border border-gray-200 animate-in fade-in zoom-in-95 duration-150"
        onClick={(e) => e.stopPropagation()}
      >
        {/* Top Header */}
        <div className="px-5 py-3 border-b border-gray-200 flex items-center justify-between bg-gray-50/80">
          <div className="flex items-center space-x-2">
            <ProviderIcon
              provider={model.provider}
              className="w-4 h-4 rounded"
              fallbackBg={model.iconBg}
              fallbackTextClassName="text-[9px]"
            />
            <div>
              <div className="flex items-center space-x-2">
                <span className="font-bold text-xs text-gray-900">{model.name}</span>
                <span className="text-[10px] bg-purple-100 text-purple-700 font-mono px-1.5 py-0.2 rounded">
                  Playground 测试场
                </span>
              </div>
            </div>
          </div>

          <div className="flex items-center space-x-2">
            <button
              onClick={() => setShowSettings(!showSettings)}
              className={`p-1.5 rounded-lg border text-xs flex items-center space-x-1 transition-colors ${
                showSettings
                  ? 'bg-purple-50 text-purple-700 border-purple-300'
                  : 'text-gray-500 hover:text-gray-900 border-gray-200 bg-white'
              }`}
              title="调节推理参数"
            >
              <Sliders className="w-3.5 h-3.5" />
              <span className="text-[11px] hidden sm:inline">参数调节</span>
            </button>

            <button
              onClick={handleClear}
              className="p-1.5 rounded-lg border border-gray-200 text-gray-500 hover:text-gray-900 hover:bg-gray-100 text-xs flex items-center space-x-1"
              title="清空会话"
            >
              <RotateCcw className="w-3.5 h-3.5" />
              <span className="text-[11px] hidden sm:inline">重置</span>
            </button>

            <button
              onClick={onClose}
              className="p-1 rounded-lg text-gray-400 hover:text-gray-700 hover:bg-gray-200/60"
            >
              <X className="w-5 h-5" />
            </button>
          </div>
        </div>

        {/* Main Body */}
        <div className="flex-1 flex overflow-hidden">
          {/* Messages list */}
          <div className="flex-1 overflow-y-auto p-4 space-y-4 text-xs">
            {messages.map((m, idx) => (
              <div
                key={idx}
                className={`flex space-x-2.5 ${
                  m.role === 'user' ? 'justify-end' : 'justify-start'
                }`}
              >
                {m.role === 'assistant' && (
                  <div className="w-6 h-6 rounded-full bg-purple-600 text-white flex items-center justify-center shrink-0 mt-0.5 shadow-xs">
                    <Bot className="w-3.5 h-3.5" />
                  </div>
                )}

                <div
                  className={`max-w-2xl px-3.5 py-2.5 rounded-xl ${
                    m.role === 'user'
                      ? 'bg-purple-600 text-white rounded-br-xs'
                      : m.isError
                        ? 'bg-rose-50 text-rose-700 border border-rose-200 rounded-bl-xs'
                        : 'bg-gray-100 text-gray-800 rounded-bl-xs'
                  }`}
                >
                  <div className="whitespace-pre-wrap leading-relaxed font-sans text-xs">
                    {m.content}
                  </div>
                  {m.usage && (
                    <div className="mt-1.5 pt-1.5 border-t border-black/5 text-[10px] font-mono text-gray-400">
                      输入 {m.usage.promptTokens} · 输出 {m.usage.completionTokens} tokens
                    </div>
                  )}
                </div>

                {m.role === 'user' && (
                  <div className="w-6 h-6 rounded-full bg-gray-800 text-white flex items-center justify-center shrink-0 mt-0.5 shadow-xs">
                    <User className="w-3.5 h-3.5" />
                  </div>
                )}
              </div>
            ))}

            {isGenerating && messages[messages.length - 1]?.content === '' && (
              <div className="flex items-center space-x-2 text-gray-400 text-xs italic pl-9">
                <Sparkles className="w-3.5 h-3.5 text-purple-500 animate-spin" />
                <span>{model.name} 正在思考与流式生成中...</span>
              </div>
            )}
          </div>

          {/* Settings Sidebar if opened */}
          {showSettings && (
            <div className="w-60 border-l border-gray-200 p-4 bg-gray-50/70 space-y-4 text-xs overflow-y-auto shrink-0">
              <div className="font-bold text-gray-800 text-xs">推理参数配置</div>

              <div className="space-y-1.5">
                <div className="flex justify-between text-[11px] text-gray-600">
                  <span>Temperature</span>
                  <span className="font-mono text-purple-700">{temperature}</span>
                </div>
                <input
                  type="range"
                  min={0}
                  max={2}
                  step={0.1}
                  value={temperature}
                  onChange={(e) => setTemperature(Number(e.target.value))}
                  className="purple-track"
                />
              </div>

              <div className="space-y-1.5">
                <div className="flex justify-between text-[11px] text-gray-600">
                  <span>Max Tokens</span>
                  <span className="font-mono text-purple-700">{maxTokens}</span>
                </div>
                <input
                  type="range"
                  min={256}
                  max={8192}
                  step={256}
                  value={maxTokens}
                  onChange={(e) => setMaxTokens(Number(e.target.value))}
                  className="purple-track"
                />
              </div>

              <div className="space-y-1.5">
                <label className="text-[11px] text-gray-600 block">系统提示词 (System)</label>
                <textarea
                  value={systemPrompt}
                  onChange={(e) => setSystemPrompt(e.target.value)}
                  rows={4}
                  className="w-full text-xs p-2 border border-gray-200 rounded bg-white focus:outline-none focus:border-purple-500"
                />
              </div>

              <div className="border-t border-gray-200 pt-3 text-[11px] text-gray-500 space-y-1">
                <div>上下文上限: {model.contextDisplay || '未限'}</div>
                <div>输入定价: {model.inputPriceDisplay}</div>
              </div>
            </div>
          )}
        </div>

        {/* Sample prompt quick chips */}
        <div className="px-4 py-1.5 bg-gray-50 border-t border-gray-100 flex items-center space-x-2 overflow-x-auto text-[11px]">
          <span className="text-gray-400 shrink-0">预设提问:</span>
          {samplePrompts.map((p, i) => (
            <button
              key={i}
              onClick={() => setInput(p)}
              className="shrink-0 bg-white hover:bg-purple-50 hover:text-purple-700 hover:border-purple-200 px-2 py-0.5 rounded border border-gray-200 text-gray-600 transition-colors"
            >
              {p}
            </button>
          ))}
        </div>

        {/* Not connected banner */}
        {!apiKey && (
          <div className="mx-3 mb-2 flex items-center justify-between gap-2 bg-amber-50 border border-amber-200 text-amber-800 text-[11px] rounded-lg px-3 py-2">
            <span>尚未连接 API Key，无法调用真实模型进行测试。</span>
            <button
              onClick={() => setShowConnectKeyModal(true)}
              className="px-2 py-1 bg-amber-600 hover:bg-amber-700 text-white rounded font-medium shrink-0 cursor-pointer"
            >
              连接 API Key
            </button>
          </div>
        )}

        {/* Input Bar */}
        <div className="p-3 border-t border-gray-200 bg-white flex items-center space-x-2">
          <input
            type="text"
            value={input}
            onChange={(e) => setInput(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter' && !e.shiftKey) {
                e.preventDefault();
                handleSend();
              }
            }}
            placeholder={`给 ${model.name} 发送测试消息 (回车直接发送)...`}
            className="flex-1 border border-gray-200 rounded-lg px-3 py-2 text-xs focus:outline-none focus:border-purple-500"
          />

          <button
            onClick={handleSend}
            disabled={!input.trim() || isGenerating}
            className="px-4 py-2 bg-purple-600 hover:bg-purple-700 disabled:opacity-50 text-white rounded-lg text-xs font-medium flex items-center space-x-1 transition-colors cursor-pointer"
          >
            <Send className="w-3.5 h-3.5" />
            <span>发送</span>
          </button>
        </div>
      </div>

      {showConnectKeyModal && (
        <ConnectKeyModal onClose={() => setShowConnectKeyModal(false)} />
      )}
    </div>
  );
};
