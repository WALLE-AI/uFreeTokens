import { useEffect } from 'react';
import type { ContextRef } from '../api/agent';
import { ctxKey, pageContextStore, pageStateStore } from './agentStore';

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

// useAgentPageState 让页面登记不在 URL 里的状态（筛选条件、时间范围、选中的维度等），发送消息时随页面上下文
// 一起带给助手。值为空的键会被忽略；页面卸载时清空。URL 查询参数已经包含在页面路径里，不必重复登记。
export function useAgentPageState(state: Record<string, string | number | boolean | null | undefined>) {
  const entries = Object.entries(state)
    .filter(([, v]) => v !== null && v !== undefined && v !== '')
    .map(([k, v]) => [k, String(v)] as [string, string]);
  const key = JSON.stringify(entries);
  useEffect(() => {
    pageStateStore.set(Object.fromEntries(JSON.parse(key) as Array<[string, string]>));
    return () => pageStateStore.set({});
  }, [key]);
}
