import { useCallback, useEffect, useState } from 'react';
import { listAgentProposals, type AgentProposal } from '../api/agent';
import { useCan } from '../api/auth';
import { useAgentMutated } from './agentEvents';
import { useAgentEnabled } from './agentStore';

// useAgentSuggestions：列表页一次请求取回所有行的智能体建议（GET /agent/proposals?target_type=&target_ids=），
// 返回 target_id → 最新的待处理提案。智能体未启用或无 agent:use 时不发请求。
export function useAgentSuggestions(targetType: string, ids: Array<string | number>): { byId: Map<string, AgentProposal>; reload: () => void } {
  const agentEnabled = useAgentEnabled();
  const allowed = useCan('agent:use');
  const enabled = agentEnabled && allowed;
  const [byId, setById] = useState<Map<string, AgentProposal>>(new Map());
  const key = ids.map(String).join(',');
  const load = useCallback(() => {
    if (!enabled || !key) {
      setById(new Map());
      return;
    }
    listAgentProposals({ target_type: targetType, target_ids: key.split(','), status: 'pending', mine: 0, limit: 500 })
      .then((r) => {
        const m = new Map<string, AgentProposal>();
        for (const p of r.data ?? []) if (!m.has(p.target_id)) m.set(p.target_id, p);
        setById(m);
      })
      .catch(() => setById(new Map()));
  }, [enabled, key, targetType]);
  useEffect(() => {
    load();
  }, [load]);
  useAgentMutated('*', load);
  return { byId, reload: load };
}

// verdict 把提案工具翻译成一句建议（列表标记用）。
export function verdict(p: AgentProposal): { label: string; tone: 'purple' | 'gray' | 'amber' } {
  const args = (p.args ?? {}) as Record<string, unknown>;
  const map: Record<string, string> = {
    approve_price_change: '建议通过',
    reject_price_change: '建议驳回',
    publish_listing: '建议发布',
    dismiss_listing: '建议忽略',
    adopt_offer: '建议采纳',
    update_virtual_model_metadata: '建议补全',
    create_public_app_rule: `建议${{ block: '屏蔽', merge: '合并', rename: '改名' }[String(args.action)] ?? '治理'}`,
    update_price_source_config: '建议修复配置',
    run_price_source: '建议重跑',
    publish_benchmark_run: '建议发布',
  };
  let label = map[p.tool] ?? p.tool;
  if (p.tool === 'set_offer_status') label = args.status === 'confirmed' ? '建议确认' : args.status === 'ignored' ? '建议忽略' : '建议复核';
  if (p.tool === 'set_model_alias') label = args.status === 'confirmed' ? '建议确认' : args.status === 'ignored' ? '建议忽略' : '建议交回自动';
  const negative = /驳回|忽略|屏蔽/.test(label);
  const lowConf = typeof p.confidence === 'number' && p.confidence < 0.6;
  return { label, tone: lowConf ? 'amber' : negative ? 'gray' : 'purple' };
}
