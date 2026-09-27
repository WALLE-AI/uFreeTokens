import { useEffect, useRef } from 'react';

// 输入框、下拉、可编辑区域聚焦时，全局单键快捷键全部失效（UI_DESIGN.md §7）。
export function isTypingTarget(target: EventTarget | null): boolean {
  if (!(target instanceof HTMLElement)) return false;
  const tag = target.tagName;
  return tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT' || target.isContentEditable;
}

export interface GlobalHotkeyHandlers {
  onGoto: (key: string) => void; // G 之后按下的键（小写）
  onFocusSearch: () => void; // /
  onHelp: () => void; // ?
}

// useGlobalHotkeys 处理 "G 然后 X" 两段式跳转、"/" 聚焦搜索、"?" 帮助面板。
// ⌘K 由 CommandPalette 自己监听（输入框聚焦时也要能打开）。
export function useGlobalHotkeys(handlers: GlobalHotkeyHandlers) {
  const ref = useRef(handlers);
  ref.current = handlers;

  useEffect(() => {
    let gPressedAt = 0;
    const onKey = (e: KeyboardEvent) => {
      if (e.metaKey || e.ctrlKey || e.altKey) return;
      if (isTypingTarget(e.target)) return;
      if (document.querySelector('[data-modal-open="true"]')) return;

      const key = e.key;
      if (gPressedAt && Date.now() - gPressedAt < 1200) {
        gPressedAt = 0;
        e.preventDefault();
        ref.current.onGoto(key.toLowerCase());
        return;
      }
      if (key === 'g' || key === 'G') {
        gPressedAt = Date.now();
        return;
      }
      if (key === '/') {
        e.preventDefault();
        ref.current.onFocusSearch();
      } else if (key === '?') {
        e.preventDefault();
        ref.current.onHelp();
      }
    };
    document.addEventListener('keydown', onKey);
    return () => document.removeEventListener('keydown', onKey);
  }, []);
}
