import { extendTailwindMerge } from 'tailwind-merge';

// 项目自定义的 .text-xxs（11px，见 index.css）是字号，必须告诉 tailwind-merge，
// 否则它会被当成文字颜色，和 text-gray-400 之类互相覆盖。
const twMerge = extendTailwindMerge({
  extend: { classGroups: { 'font-size': [{ text: ['xxs'] }] } },
});

// 拼接 className，忽略 falsy 值；冲突的 Tailwind 工具类后者覆盖前者——组件内置的
// w-full / py-2 等可以被调用方传入的 w-20 / py-1 覆盖。
export function cn(...parts: Array<string | false | null | undefined>): string {
  return twMerge(parts.filter(Boolean).join(' '));
}
