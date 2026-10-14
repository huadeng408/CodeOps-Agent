import { useCallback, useEffect, useRef, useState } from 'react';
import { api, ApiError } from './api';
import type { RuntimeCapabilities, Session } from './types';

const labels = { identity: '本机身份', history: '会话历史', provider: '模型接入', sandbox: '执行环境', budget: 'token 批次', execution: '任务执行', mcp: '仓库 MCP', rag: '知识检索', trace: '追踪记录' } as const;
const reasons: Record<string, string> = {
  'local persistent authentication': '本机身份已启用。',
  'canonical Session Ledger': '会话持久化已启用，重启后仍可查看。',
  'provider, sandbox and budget admission are required': '配置模型、批准执行环境和确认 token 额度后才能开始任务。',
  'optional services are disabled in the local profile': '可选服务未启用，基础会话仍可使用。',
  'trace backend has not been verified': '尚未验证追踪后端，不能确认记录完整。',
  'model admission is not enabled': '启用受 Go 计量的模型接入后重新检查。',
  'model credentials are missing': '安全注入模型凭据后重新检查；凭据不会在这里显示。',
  'model and endpoint are missing': '配置已批准的模型和 API 地址后重新检查。',
  'model token bounds are missing': '配置该模型的输入、输出 token 上界后重新检查。',
  'model profile is invalid': '核对模型协议、地址和 token 上界；当前配置不能准入。',
  'model profile configured; live validity is not confirmed': '模型配置已齐全，实际可用性尚待调用确认。',
  'local task runtime is not attached': '启用已批准的隔离执行环境后才能修改或运行代码。',
  'bundled read-only repository MCP is unavailable': '只读仓库工具尚未通过身份与版本核验。',
  'pinned read-only repository manifest tool; per-call approval required': '读取当前任务的依赖清单与校验值，调用前需批准。',
  'token Ledger is unavailable; restore it before calling a model': '无法读取额度账本，恢复账本后才能调用模型。',
  'persistent token admission; price unknown': '累计额度跨进程保留；当前费用未知。',
  'provider enforces budget; local usage is audited': '预算上限由服务商执行，本地记录已确认用量；费用未知。',
  'provider enforces budget; some usage is unconfirmed': '部分调用用量尚未确认，记录已保留；可以继续，预算上限由服务商执行。',
  'a model attempt is still active; wait or recover its execution': '模型调用仍在执行，请等待完成；进程中断后需先恢复执行状态。',
  'token usage is unknown; reconcile retained reservations': '用量未知，预留继续保留；对账后才能继续。',
  'a model attempt is unresolved; wait or reconcile after restart': '有未结算调用；等待完成，重启后仍未结算则需对账。',
  'token batch is exhausted': '本批次 token 额度已耗尽，不能新增调用。',
  'token batch initializes on the first admitted call': '首次获准调用时创建持久批次，随后重启不会重置余额。',
  'verification batch already has another task': '任务仍有活动调用，或批次正被其他任务占用；等待完成后重新核验。',
  'verification usage requires reconciliation': '模型调用仍在执行或执行状态尚未恢复；确认停止后才能结束任务，用量记录会保留。',
  'session state conflict': '任务仍在运行或工具尚未确认停止；等待完成或对账后再结束。',
};

export function CapabilityPanel({ capabilities, onChange, session }: {
  capabilities: RuntimeCapabilities | null;
  onChange: (value: RuntimeCapabilities | null) => void;
  session: Session | null;
}) {
  const [checking, setChecking] = useState(false);
  const [error, setError] = useState('');
  const [finishing, setFinishing] = useState(false);
  const [finishedTask, setFinishedTask] = useState('');
  const [finishError, setFinishError] = useState<{ taskId: string; message: string } | null>(null);
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

  const taskId = capabilities?.budget?.tokens?.task_id;
  const finishTask = async () => {
    if (!taskId || taskId !== session?.id || finishing) return;
    setFinishing(true);
    setError('');
    setFinishError(null);
    try {
      await api.finishTask(taskId);
      setFinishedTask(taskId);
      await refresh();
    } catch (cause) {
      setFinishError({ taskId, message: cause instanceof ApiError ? reasons[cause.message] || cause.message : '无法确认任务已结束，请检查连接后重新核验。' });
    } finally {
      setFinishing(false);
    }
  };

  return <section className="capability-panel" aria-label="运行能力">
    <h2>运行能力</h2>
    <p className="capability-intro">{checking ? '正在检查当前运行条件…' : '每项能力独立报告状态'}</p>
    {Object.entries(labels).map(([key, label]) => {
      const capability = capabilities?.[key as keyof RuntimeCapabilities];
      const state = ['ready', 'blocked', 'degraded', 'unknown'].includes(capability?.state || '') ? capability!.state : 'unknown';
      const reason = capability?.reason ? reasons[capability.reason] || capability.reason : '状态尚未确认。';
      const tokens = capability?.tokens;
      const hasTokens = tokens && [tokens.used, tokens.limit, tokens.reserved].every(value => Number.isSafeInteger(value) && value >= 0);
      return <div className="capability-row" key={key}>
        <div><strong>{label}</strong><p>{reason}</p>
          {key === 'budget' && hasTokens && <p>
            {tokens.batch_id && `批次 ${tokens.batch_id.slice(0, 8)}。`}
            已确认 {tokens.used.toLocaleString()} tokens，批准上限 {tokens.limit.toLocaleString()}，原预留估计 {tokens.reserved.toLocaleString()}。费用{tokens.cost_status === 'unknown' ? '未知' : '待核对'}。
          </p>}
        </div>
        <span className={`capability-state ${state}`}>{state.toUpperCase()}</span>
      </div>;
    })}
    {taskId && taskId === session?.id && <button className="primary-btn capability-refresh" type="button" aria-label="结束代码任务"
      disabled={checking || finishing || (capabilities?.budget?.state !== 'ready' && capabilities?.budget?.reason !== 'token batch is exhausted') || session.run?.status === 'queued' || session.run?.status === 'running' || session.run?.status === 'canceling'}
      onClick={() => void finishTask()}>{finishing ? '结束中…' : '结束代码任务'}</button>}
    {finishedTask && finishedTask === session?.id && <p role="status">代码任务已结束。历史和用量记录保留；后续调用仍受服务商预算上限约束。</p>}
    {finishError && finishError.taskId === session?.id && <p className="capability-error" role="status">{finishError.message}</p>}
    {error && <p className="capability-error" role="status">{error}</p>}
    <button className="primary-btn capability-refresh" type="button" aria-label="重新检查能力" disabled={checking} onClick={() => void refresh()}>{checking ? '检查中…' : '重新检查能力'}</button>
  </section>;
}
