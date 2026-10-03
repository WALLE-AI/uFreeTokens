import { useEffect } from 'react';
import type { ContextRef } from '../api/agent';
import { ctxKey, pageContextStore } from './agentStore';

// useAgentContext 让页面注册“当前对象”（详情页、收件箱选中项、列表勾选项）：Dock 顶部显示为可移除的
// 上下文芯片，发送时作为 context_ref 传给后端。只带 ID，不自动发送页面数据（后端按 ID 用工具读取）。
export function useAgentContext(refs: ContextRef | ContextRef[] | null | undefined) {
  const list = refs ? (Array.isArray(refs) ? refs : [refs]) : [];
  const key = JSON.stringify(list.map((r) => ({ type: r.type, id: String(r.id), label: r.label })));
  useEffect(() => {
    const mine = JSON.parse(key) as ContextRef[];
    if (mine.length === 0) return;
    const mineKeys = new Set(mine.map(ctxKey));
    pageContextStore.set([...pageContextStore.get().filter((c) => !mineKeys.has(ctxKey(c))), ...mine]);
    return () => pageContextStore.set(pageContextStore.get().filter((c) => !mineKeys.has(ctxKey(c))));
  }, [key]);
}
