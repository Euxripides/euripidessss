import React from 'react';
import ReactDOM from 'react-dom/client';
// antd v5 官方 React 19 兼容补丁：修复静态 message/notification/Modal 在 React 19 下不渲染
import '@ant-design/v5-patch-for-react-19';
import '@xyflow/react/dist/style.css';
import './design-system/tokens.css';
import './design-system/design-system.css';
import './design-system/animations.css';
import './styles/layout.css';
import './styles/feedback.css';
import './features/upload/upload.css';
import './features/clean/clean.css';
import './features/flow/flow-canvas.css';
import './features/flow/flow-panels.css';
import './features/flow/flow-nodes.css';
import './features/crypto/crypto.css';
import './features/analytics/analytics-shell.css';
import './styles/shared.css';
import './styles/responsive.css';
import { message, notification } from 'antd';
import { App } from './App';
import { AnalysisProvider } from './features/explorer-intelligence/analysisContext';

// 全局弹窗参数：静态 message/notification 的位置、时长与数量上限
message.config({ top: 20, duration: 3, maxCount: 3 });
notification.config({ placement: 'topRight', top: 20, duration: 4, maxCount: 4 });

let bootErrorTimer: number | undefined;

function errorMessage(reason: unknown): string {
  if (reason instanceof Error && reason.message) return reason.message;
  if (typeof reason === 'string' && reason.trim()) return reason;
  if (reason && typeof reason === 'object') {
    const candidate = reason as { detail?: unknown; message?: unknown; error?: unknown };
    for (const value of [candidate.detail, candidate.message, candidate.error]) {
      if (typeof value === 'string' && value.trim()) return value;
    }
    try {
      const serialized = JSON.stringify(reason);
      if (serialized && serialized !== '{}') return serialized;
    } catch {
      // Fall through to the stable generic message below.
    }
  }
  return '发生未处理的前端错误，请重试或刷新页面';
}

// 前端自检：运行时错误提供可读提示，并在应用恢复后自动清除。
function showBootError(reason: unknown, stack?: string) {
  const message = errorMessage(reason);
  // 浏览器钱包扩展冲突（MetaMask/OKX 等重复定义 window.ethereum）不是应用错误，
  // 静默忽略，不显示任何提示。
  if (/ethereum|Cannot redefine property/i.test(message)) {
    return;
  }
  let el = document.getElementById('__boot_error');
  if (!el) {
    el = document.createElement('div');
    el.id = '__boot_error';
    el.style.cssText =
      'position:fixed;top:0;left:0;right:0;z-index:99999;background:#dc2626;color:#fff;' +
      'padding:12px 16px;font:12px/1.5 ui-monospace,Consolas,monospace;white-space:pre-wrap;';
    document.body.appendChild(el);
  }
  el.textContent = `前端错误：${message}${stack ? `\n${stack}` : ''}`;
  if (bootErrorTimer !== undefined) window.clearTimeout(bootErrorTimer);
  bootErrorTimer = window.setTimeout(() => {
    document.getElementById('__boot_error')?.remove();
    bootErrorTimer = undefined;
  }, 8000);
}

window.addEventListener('error', (e) => showBootError(e.error ?? e.message, e.error?.stack));
window.addEventListener('unhandledrejection', (e) => showBootError(e.reason));

console.info('Investigation OS build: 20260808-1900');

// React 渲染错误边界：崩溃时显示错误而不是白屏
class ErrorBoundary extends React.Component<{ children: React.ReactNode }, { error: Error | null }> {
  constructor(props: { children: React.ReactNode }) {
    super(props);
    this.state = { error: null };
  }
  static getDerivedStateFromError(error: Error) {
    return { error };
  }
  render() {
    if (this.state.error) {
      return (
        <div
          style={{
            position: 'fixed',
            inset: 0,
            zIndex: 99999,
            background: '#fef2f2',
            color: '#991b1b',
            padding: 24,
            font: '13px/1.6 ui-monospace,Consolas,monospace',
            whiteSpace: 'pre-wrap',
          }}
        >
          <strong>前端渲染错误：</strong>
          <br />
          {String(this.state.error?.message ?? this.state.error)}
          <br />
          {this.state.error?.stack}
          <br />
          <button onClick={() => window.location.reload()}>刷新页面</button>
        </div>
      );
    }
    return this.props.children;
  }
}

ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <ErrorBoundary>
      <AnalysisProvider>
        <App />
      </AnalysisProvider>
    </ErrorBoundary>
  </React.StrictMode>,
);
