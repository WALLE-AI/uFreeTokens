import { createContext, useContext } from 'react';
import type { TocItem } from './components/Toc';

// 页面 → 布局的回传通道：页面渲染完正文后把 TOC 交给布局右栏显示。
export interface DocsChrome {
  setToc: (items: TocItem[]) => void;
}

export const DocsChromeContext = createContext<DocsChrome>({ setToc: () => undefined });

export function useDocsChrome(): DocsChrome {
  return useContext(DocsChromeContext);
}
