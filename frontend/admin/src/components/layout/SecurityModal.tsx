import { useState } from 'react';
import { authStore, useAuth } from '../../api/auth';
import { describeError } from '../../api/errors';
import { changePassword, disableTOTP, enableTOTP, fetchMe, setupTOTP, type TOTPSetup } from '../../api/session';
import { Button, Field, Input, Modal, useToast } from '../ui';

// 安全设置：修改自己的密码、绑定/解绑 TOTP 两步验证（后端 B5）。
// 修改密码会注销该管理员的其他会话；应急令牌身份（system）没有这些设置。
export function SecurityModal({ open, onClose }: { open: boolean; onClose: () => void }) {
  const { me } = useAuth();
  const toast = useToast();
  const [oldPw, setOldPw] = useState('');
  const [newPw, setNewPw] = useState('');
  const [pwBusy, setPwBusy] = useState(false);
  const [pwError, setPwError] = useState<string | null>(null);
  const [setup, setSetup] = useState<TOTPSetup | null>(null);
  const [code, setCode] = useState('');
  const [totpBusy, setTotpBusy] = useState(false);
  const [totpError, setTotpError] = useState<string | null>(null);

  const refreshMe = async () => {
    const token = authStore.get().token;
    if (token) authStore.setMe(await fetchMe());
  };

  const savePassword = async () => {
    setPwError(null);
    if (newPw.length < 10) return setPwError('新密码至少 10 个字符');
    setPwBusy(true);
    try {
      await changePassword(oldPw, newPw);
      toast.success('密码已修改，其他设备上的登录已失效');
      setOldPw('');
      setNewPw('');
    } catch (err) {
      setPwError(describeError(err));
    } finally {
      setPwBusy(false);
    }
  };

  const runTotp = async (fn: () => Promise<unknown>, ok: string) => {
    setTotpError(null);
    setTotpBusy(true);
    try {
      await fn();
      toast.success(ok);
      setSetup(null);
      setCode('');
      await refreshMe();
    } catch (err) {
      setTotpError(describeError(err));
    } finally {
      setTotpBusy(false);
    }
  };

  if (me?.break_glass) {
    return (
      <Modal open={open} onClose={onClose} title="安全设置">
        <p className="text-xs text-gray-600">当前以应急令牌（system）登录，没有密码与两步验证设置。</p>
      </Modal>
    );
  }

  return (
    <Modal open={open} onClose={onClose} title="安全设置" width="lg">
      <div className="space-y-6">
        <section className="space-y-3">
          <h3 className="text-xs font-semibold text-gray-900">修改密码</h3>
          <Field label="当前密码" htmlFor="old-pw">
            <Input id="old-pw" type="password" autoComplete="current-password" value={oldPw} onChange={(e) => setOldPw(e.target.value)} />
          </Field>
          <Field label="新密码" htmlFor="new-pw" hint="至少 10 个字符">
            <Input id="new-pw" type="password" autoComplete="new-password" value={newPw} onChange={(e) => setNewPw(e.target.value)} />
          </Field>
          {pwError && <div className="text-xs text-rose-600">{pwError}</div>}
          <Button variant="primary" loading={pwBusy} disabled={!oldPw || !newPw} onClick={() => void savePassword()}>
            保存新密码
          </Button>
        </section>

        <section className="space-y-3 border-t border-gray-100 pt-5">
          <h3 className="text-xs font-semibold text-gray-900">
            两步验证（TOTP）
            <span className={me?.totp_enabled ? 'ml-2 text-emerald-600' : 'ml-2 text-gray-400'}>{me?.totp_enabled ? '已启用' : '未启用'}</span>
          </h3>
          {me?.totp_enabled ? (
            <>
              <p className="text-xs text-gray-600">停用需要输入验证器当前显示的 6 位验证码。</p>
              <CodeInput value={code} onChange={setCode} />
              <Button loading={totpBusy} disabled={code.length !== 6} onClick={() => void runTotp(() => disableTOTP(code), '已停用两步验证')}>
                停用两步验证
              </Button>
            </>
          ) : setup ? (
            <>
              <p className="text-xs text-gray-600">在验证器 App（Google Authenticator、1Password 等）中手动添加以下密钥，或用支持 otpauth 链接的工具导入：</p>
              <div className="bg-gray-50 border border-gray-200 rounded-lg p-3 text-xs space-y-1.5">
                <div>
                  密钥 <span className="font-mono text-gray-900 select-all break-all">{setup.secret}</span>
                </div>
                <div className="text-gray-500 break-all font-mono text-[11px] select-all">{setup.otpauth_url}</div>
              </div>
              <p className="text-xs text-gray-600">添加后输入验证器显示的 6 位验证码以确认启用：</p>
              <CodeInput value={code} onChange={setCode} />
              <Button variant="primary" loading={totpBusy} disabled={code.length !== 6} onClick={() => void runTotp(() => enableTOTP(code), '已启用两步验证')}>
                确认启用
              </Button>
            </>
          ) : (
            <>
              <p className="text-xs text-gray-600">启用后登录时除了密码还需要验证器上的 6 位验证码。</p>
              <Button
                loading={totpBusy}
                onClick={async () => {
                  setTotpError(null);
                  setTotpBusy(true);
                  try {
                    setSetup(await setupTOTP());
                  } catch (err) {
                    setTotpError(describeError(err));
                  } finally {
                    setTotpBusy(false);
                  }
                }}
              >
                开始设置
              </Button>
            </>
          )}
          {totpError && <div className="text-xs text-rose-600">{totpError}</div>}
        </section>
      </div>
    </Modal>
  );
}

function CodeInput({ value, onChange }: { value: string; onChange: (v: string) => void }) {
  return (
    <Input
      mono
      className="w-32"
      inputMode="numeric"
      autoComplete="one-time-code"
      maxLength={6}
      placeholder="000000"
      value={value}
      onChange={(e) => onChange(e.target.value.replace(/\D/g, ''))}
    />
  );
}
