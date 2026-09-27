import { useCallback, useEffect, useRef, type RefObject } from 'react';

// 浮层栈：弹窗、抽屉、下拉菜单可以层层叠加（例如抽屉里打开弹窗、弹窗里打开菜单）。
// Esc 只应该关闭最上面的那一层——每个浮层打开时入栈，关闭时出栈，按键处理前
// 先确认自己在栈顶。
const overlayStack: symbol[] = [];

function useOverlayLayer(active: boolean): () => boolean {
  const id = useRef(Symbol('overlay'));
  useEffect(() => {
    if (!active) return;
    const me = id.current;
    overlayStack.push(me);
    return () => {
      const i = overlayStack.lastIndexOf(me);
      if (i >= 0) overlayStack.splice(i, 1);
    };
  }, [active]);
  return useCallback(() => overlayStack[overlayStack.length - 1] === id.current, []);
}

// useDismiss：浮层（下拉菜单、用户菜单）点外关闭 + Esc 关闭。web 的
// MoreVertical 菜单缺少这两点（UI_DESIGN.md §10），admin 统一补齐。
export function useDismiss(ref: RefObject<HTMLElement | null>, open: boolean, onClose: () => void) {
  const isTop = useOverlayLayer(open);
  useEffect(() => {
    if (!open) return;
    const onDown = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) onClose();
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape' && isTop()) onClose();
    };
    document.addEventListener('mousedown', onDown);
    document.addEventListener('keydown', onKey);
    return () => {
      document.removeEventListener('mousedown', onDown);
      document.removeEventListener('keydown', onKey);
    };
  }, [ref, open, onClose, isTop]);
}

// useEscape：弹窗、抽屉按 Esc 关闭；叠加时只关闭最上层。
export function useEscape(active: boolean, onEscape: () => void) {
  const isTop = useOverlayLayer(active);
  useEffect(() => {
    if (!active) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape' && isTop()) onEscape();
    };
    document.addEventListener('keydown', onKey);
    return () => document.removeEventListener('keydown', onKey);
  }, [active, onEscape, isTop]);
}
