import React from 'react';
import ReactDOM from 'react-dom/client';
// antd v5 官方 React 19 兼容补丁：修复静态 message/notification/Modal 在 React 19 下不渲染
import '@ant-design/v5-patch-for-react-19';
import '@xyflow/react/dist/style.css';
import './design-system/tokens.css';
import './design-system/design-system.css';
import './styles/layout.css';
import './features/upload/upload.css';
import './features/clean/clean.css';
import './features/flow/flow-canvas.css';
import './features/flow/flow-panels.css';
import './features/flow/flow-nodes.css';
import './features/crypto/crypto.css';
import './features/analytics/analytics-shell.css';
import './styles/shared.css';
import './styles/responsive.css';
import { App } from './App';

ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <App />
  </React.StrictMode>,
);
