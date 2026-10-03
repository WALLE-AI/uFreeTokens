import { useEffect } from 'react';
import { Navigate, useParams } from 'react-router';
import { useAgent } from '../../agent/AgentProvider';

// /agent、/agent/:sessionId 的兼容重定向：会话工作台已并入全局 Dock，旧链接（审计日志、书签）
// 打开 Dock（有会话 ID 时定位到该会话）后回到工作台。
export default function AgentRedirect() {
  const { sessionId } = useParams();
  const { openSession, setDockOpen } = useAgent();
  useEffect(() => {
    const id = Number(sessionId);
    if (Number.isFinite(id) && id > 0) openSession(id);
    else setDockOpen(true);
  }, [sessionId, openSession, setDockOpen]);
  return <Navigate to="/" replace />;
}
