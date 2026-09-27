import { useEffect, useState } from 'react';
import { getTodoCounts } from '../api/todo';
import type { TodoCounts } from '../types';

const POLL_MS = 60_000;
const REFRESH_EVENT = 'uft:todo-refresh';

// refreshTodoCounts 让所有 useTodoCounts 实例（侧栏徽标、工作台待办条）立即重新拉取。
// 在审批、上架、忽略等会改变待办数量的写操作成功后调用。
export function refreshTodoCounts() {
  window.dispatchEvent(new Event(REFRESH_EVENT));
}

// 侧栏待办徽标每 60 秒刷新一次；标签页不可见时暂停轮询。接口不可用时返回 null。
export function useTodoCounts(): TodoCounts | null {
  const [counts, setCounts] = useState<TodoCounts | null>(null);

  useEffect(() => {
    let controller: AbortController | null = null;
    let stopped = false;
    const load = () => {
      if (document.visibilityState === 'hidden') return;
      controller?.abort();
      controller = new AbortController();
      getTodoCounts(controller.signal)
        .then((c) => !stopped && setCounts(c))
        .catch(() => {
          // 接口未上线或暂时失败：不打扰运营，徽标不显示即可
        });
    };
    load();
    const timer = setInterval(load, POLL_MS);
    document.addEventListener('visibilitychange', load);
    window.addEventListener(REFRESH_EVENT, load);
    return () => {
      window.removeEventListener(REFRESH_EVENT, load);
      stopped = true;
      controller?.abort();
      clearInterval(timer);
      document.removeEventListener('visibilitychange', load);
    };
  }, []);

  return counts;
}
