import { useCallback, useEffect, useRef, useState } from 'react';
import { api } from './api';
import type { RuntimeCapabilities } from './types';

const labels = { identity: '本机身份', history: '会话历史', execution: '任务执行', rag: '知识检索', trace: '追踪记录' } as const;
const reasons: Record<string, string> = {
  'local persistent authentication': '本机身份已启用。',
  'canonical Session Ledger': '会话持久化已启用，重启后仍可查看。',
  'provider, sandbox and budget admission are required': '配置模型、批准执行环境和确认 token 额度后才能开始任务。',
  'optional services are disabled in the local profile': '可选服务未启用，基础会话仍可使用。',
  'trace backend has not been verified': '尚未验证追踪后端，不能确认记录完整。',
};

export function CapabilityPanel({ capabilities, onChange }: {
  capabilities: RuntimeCapabilities | null;
  onChange: (value: RuntimeCapabilities | null) => void;
}) {
  const [checking, setChecking] = useState(false);
  const [error, setError] = useState('');
  const request = useRef<AbortController | null>(null);
  const refresh = useCallback(async () => {
    request.current?.abort();
    const controller = new AbortController();
    request.current = controller;
    const timeout = window.setTimeout(() => controller.abort(), 1500);
    setChecking(true);
    setError('');
    onChange(null);
    try {
      const data = await api.capabilities(controller.signal);
      if (!controller.signal.aborted) onChange(data || null);
    } catch {
      if (request.current === controller) setError('无法确认能力状态，请检查服务连接后重试。');
    } finally {
      window.clearTimeout(timeout);
      if (request.current === controller) setChecking(false);
    }
  }, [onChange]);
  useEffect(() => {
    void refresh();
    return () => {
      request.current?.abort();
      request.current = null;
    };
  }, [refresh]);

  return <section className="capability-panel" aria-label="运行能力">
    <h2>运行能力</h2>
    <p className="capability-intro">{checking ? '正在检查当前运行条件…' : '每项能力独立报告状态'}</p>
    {Object.entries(labels).map(([key, label]) => {
      const capability = capabilities?.[key as keyof RuntimeCapabilities];
      const state = ['ready', 'blocked', 'degraded', 'unknown'].includes(capability?.state || '') ? capability!.state : 'unknown';
      const reason = capability?.reason ? reasons[capability.reason] || capability.reason : '状态尚未确认。';
      return <div className="capability-row" key={key}>
        <div><strong>{label}</strong><p>{reason}</p></div>
        <span className={`capability-state ${state}`}>{state.toUpperCase()}</span>
      </div>;
    })}
    {error && <p className="capability-error" role="status">{error}</p>}
    <button className="primary-btn capability-refresh" type="button" aria-label="重新检查能力" disabled={checking} onClick={() => void refresh()}>{checking ? '检查中…' : '重新检查能力'}</button>
  </section>;
}
