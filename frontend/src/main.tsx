import React from 'react';
import ReactDOM from 'react-dom/client';
import '@xyflow/react/dist/style.css';
import './styles/layout.css';
import './features/upload/upload.css';
import './features/clean/clean.css';
import './features/flow/flow-canvas.css';
import './features/flow/flow-panels.css';
import './features/flow/flow-nodes.css';
import './styles/shared.css';
import './styles/responsive.css';
import { App } from './App';

ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <App />
  </React.StrictMode>,
);
