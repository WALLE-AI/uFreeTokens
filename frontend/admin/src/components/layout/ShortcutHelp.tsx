import { Modal } from '../ui';
import { NAV_ITEMS } from '../../nav';

const GENERAL: Array<[string, string]> = [
  ['⌘K / Ctrl+K', '命令面板：跳转页面、按 ID 定位对象、常用操作'],
  ['⌘J / Ctrl+J', '打开 / 收起智能体侧边坞'],
  ['/', '聚焦当前页搜索框'],
  ['?', '显示本帮助'],
  ['Esc', '关闭抽屉 / 弹窗 / 菜单'],
];

const INBOX: Array<[string, string]> = [
  ['J / K', '审批收件箱：下一条 / 上一条'],
  ['A / R', '审批收件箱：批准 / 驳回'],
];

// 快捷键帮助面板（UI_DESIGN.md §7）。输入框聚焦时单键快捷键全部失效。
export function ShortcutHelp({ open, onClose }: { open: boolean; onClose: () => void }) {
  const gotos = NAV_ITEMS.filter((i) => i.gotoKey).map((i) => [`G 然后 ${i.gotoKey!.toUpperCase()}`, `跳转到${i.label}`] as [string, string]);
  return (
    <Modal open={open} onClose={onClose} title="快捷键" description="输入框聚焦时，单键快捷键不生效。" width="lg">
      <div className="space-y-4">
        {[
          ['通用', GENERAL],
          ['页面跳转', gotos],
          ['调价审批 / 提案收件箱', [...INBOX, ['E', '提案收件箱：编辑参数'] as [string, string]]],
        ].map(([title, rows]) => (
          <div key={title as string}>
            <div className="text-[10px] text-gray-400 uppercase tracking-wider font-semibold mb-1.5">{title as string}</div>
            <div className="border border-gray-200 rounded-xl divide-y divide-gray-100">
              {(rows as Array<[string, string]>).map(([k, d]) => (
                <div key={k} className="flex items-center justify-between px-3 py-2">
                  <span className="text-gray-700">{d}</span>
                  <kbd className="font-mono text-[11px] bg-gray-100 text-gray-700 px-1.5 py-0.5 rounded">{k}</kbd>
                </div>
              ))}
            </div>
          </div>
        ))}
      </div>
    </Modal>
  );
}
