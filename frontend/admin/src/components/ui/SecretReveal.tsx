import { useState } from 'react';
import { AlertTriangle, Check, Copy } from 'lucide-react';
import { Button } from './Button';
import { Modal } from './Overlay';

// SecretReveal：明文只展示一次（web 创建 Key 成功视图），用于代用户创建 API Key
// 的 raw_key。关闭前如果还没复制，二次提示"关闭后无法再次查看"。
export function SecretReveal({
  open,
  onClose,
  title = '密钥已创建',
  secret,
  description,
}: {
  open: boolean;
  onClose: () => void;
  title?: string;
  secret: string;
  description?: string;
}) {
  const [copied, setCopied] = useState(false);
  const [confirming, setConfirming] = useState(false);

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(secret);
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
      setConfirming(false);
    } catch {
      // 剪贴板不可用时让用户手动选中复制
    }
  };

  const tryClose = () => {
    if (!copied && !confirming) {
      setConfirming(true);
      return;
    }
    setConfirming(false);
    setCopied(false);
    onClose();
  };

  return (
    <Modal
      open={open}
      onClose={tryClose}
      title={title}
      footer={
        <Button variant={confirming ? 'danger' : 'primary'} onClick={tryClose}>
          {confirming ? '我已保存，关闭' : '完成'}
        </Button>
      }
    >
      <div className="space-y-3">
        <div className="bg-amber-50/80 border border-amber-200 rounded-xl p-3 flex items-start gap-2 text-xs text-amber-900">
          <AlertTriangle className="w-3.5 h-3.5 shrink-0 mt-0.5" />
          <span>{description ?? '明文只在这里显示一次，关闭后无法再次查看。请立即复制并通过安全渠道交给用户。'}</span>
        </div>
        <div className="flex items-center gap-2">
          <code className="flex-1 min-w-0 bg-gray-50 border border-gray-200 rounded-lg px-3 py-2 font-mono text-xs text-gray-900 break-all select-all">
            {secret}
          </code>
          <Button onClick={copy} icon={copied ? <Check className="w-3.5 h-3.5 text-emerald-600" /> : <Copy className="w-3.5 h-3.5" />}>
            {copied ? '已复制' : '复制'}
          </Button>
        </div>
        {confirming && <p className="text-[11px] text-rose-600">你还没有复制密钥，关闭后将无法再次查看。确定要关闭吗？</p>}
      </div>
    </Modal>
  );
}
