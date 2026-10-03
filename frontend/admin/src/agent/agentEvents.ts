import { useEffect, useRef } from 'react';
import { refreshTodoCounts } from '../hooks/useTodoCounts';

// 'mutated' 事件总线（设计 §19.2 数据刷新）：智能体审批执行成功后发出，现有页面订阅后重新拉取，
// 侧栏徽标同步刷新。不引入状态库，用 window 事件。

export interface MutatedDetail {
  target_type: string;
  target_id: string;
}

const EVENT = 'uft:agent-mutated';

export function emitMutated(detail: MutatedDetail) {
  window.dispatchEvent(new CustomEvent<MutatedDetail>(EVENT, { detail }));
  refreshTodoCounts();
}

// useAgentMutated 在指定类型的对象被智能体审批修改后调用 reload（targetType 为 '*' 时任意类型）。
export function useAgentMutated(targetType: string | string[], reload: (detail: MutatedDetail) => void) {
  const ref = useRef(reload);
  ref.current = reload;
  const key = Array.isArray(targetType) ? targetType.join(',') : targetType;
  useEffect(() => {
    const types = key.split(',');
    const onEvent = (e: Event) => {
      const d = (e as CustomEvent<MutatedDetail>).detail;
      if (types.includes('*') || types.includes(d.target_type)) ref.current(d);
    };
    window.addEventListener(EVENT, onEvent);
    return () => window.removeEventListener(EVENT, onEvent);
  }, [key]);
}
