package cryptodownload

const guiHistoryJS = `
let historyRecords = [];
const historySelected = new Set();

function historyInit() {
  const panel = document.createElement('section');
  panel.id = 'historyPanel';
  panel.className = 'historyPanel';
  panel.innerHTML = '<div class="historyHeader"><div><h2>历史下载记录</h2><div class="historyHint">记录单地址和任务组；历史文件独立保存，删除导出数据不会删除这里的记录。</div></div><div class="historyActions"><button id="historyRefreshBtn" type="button" class="secondary">刷新</button><button id="historyImportBtn" type="button" disabled>重新导入所选</button></div></div><div id="historyStatus" class="historyStatus"></div><div id="historyList" class="historyList"></div>';
  document.querySelector('main').appendChild(panel);
  const style = document.createElement('style');
  style.textContent = '.historyPanel{grid-column:1/-1}.historyHeader{display:flex;justify-content:space-between;gap:16px;align-items:flex-start}.historyHint,.historyStatus{font-size:12px;color:var(--muted)}.historyActions,.historyItemActions{display:flex;gap:8px;flex-wrap:wrap}.historyList{display:grid;gap:8px;margin-top:12px}.historyItem{border:1px solid var(--line);border-radius:7px;padding:12px;display:grid;grid-template-columns:auto 1fr auto;gap:12px;align-items:center}.historyItemMeta{display:grid;gap:4px;min-width:0}.historyTitle{font-weight:650;word-break:break-all}.historySub{font-size:12px;color:var(--muted);word-break:break-all}.historyEmpty{color:var(--muted);padding:12px 0}@media(max-width:760px){.historyHeader,.historyItem{grid-template-columns:1fr}.historyHeader{align-items:stretch}.historyActions{width:100%}.historyActions button{flex:1}}';
  document.head.appendChild(style);
  $('historyRefreshBtn').addEventListener('click', historyLoad);
  $('historyImportBtn').addEventListener('click', historyImportSelected);
  $('historyList').addEventListener('change', historyToggleSelection);
  $('historyList').addEventListener('click', historyHandleAction);
  historyLoad();
}

async function historyLoad() {
  const status = $('historyStatus');
  status.textContent = '正在加载历史记录…';
  try {
    const response = await fetch('/api/history');
    if (!response.ok) throw new Error(await response.text());
    historyRecords = await response.json();
    const known = new Set(historyRecords.map(record => record.id));
    for (const id of historySelected) if (!known.has(id)) historySelected.delete(id);
    historyRender();
    status.textContent = historyRecords.length ? '共 ' + historyRecords.length + ' 条记录' : '';
  } catch (error) {
    status.textContent = '加载历史记录失败：' + error.message;
  }
}

function historyRender() {
  const list = $('historyList');
  if (!historyRecords.length) {
    list.innerHTML = '<div class="historyEmpty">暂无历史下载记录。</div>';
    $('historyImportBtn').disabled = true;
    return;
  }
  list.innerHTML = historyRecords.map(historyRenderItem).join('');
  $('historyImportBtn').disabled = historySelected.size === 0;
}

function historyRenderItem(record) {
  const entries = Array.isArray(record.entries) ? record.entries : [];
  const isGroup = entries.length > 1;
  const first = entries[0] || {};
  const label = isGroup ? '任务组 · ' + entries.length + ' 个地址' : '单地址 · ' + (first.address || '未知地址');
  const chains = Array.from(new Set(entries.map(entry => entry.chain).filter(Boolean))).join('、') || '-';
  const resumable = record.status === 'paused' || record.status === 'cooling';
  const taskDir = record.taskDir ? '输出目录：' + record.taskDir : '输出目录尚未生成';
  const checked = historySelected.has(record.id) ? ' checked' : '';
  return '<article class="historyItem">' +
    '<input type="checkbox" aria-label="选择历史记录" data-history-select="' + escapeAttr(record.id) + '"' + checked + '>' +
    '<div class="historyItemMeta"><div class="historyTitle">' + escapeHTML(label) + '</div>' +
    '<div class="historySub">状态：' + escapeHTML(historyStatusLabel(record.status)) + ' · 链：' + escapeHTML(chains) + ' · 开始：' + escapeHTML(record.startedAt || '-') + '</div>' +
    '<div class="historySub">' + escapeHTML(taskDir) + '</div></div>' +
    '<div class="historyItemActions">' +
    '<button type="button" class="secondary" data-history-action="import" data-history-id="' + escapeAttr(record.id) + '">重新导入</button>' +
    (resumable ? '<button type="button" data-history-action="resume" data-history-id="' + escapeAttr(record.id) + '">断点继续</button>' : '') +
    '<button type="button" class="secondary" data-history-action="delete" data-history-id="' + escapeAttr(record.id) + '">删除记录</button></div></article>';
}

function historyStatusLabel(status) {
  const labels = { running: '下载中', done: '完成', failed: '失败', paused: '已暂停', queued: '排队中', cooling: '冷却中', cancelled: '已取消' };
  return labels[status] || '等待';
}

function historyToggleSelection(event) {
  const target = event.target;
  if (!target.matches('[data-history-select]')) return;
  const id = target.dataset.historySelect;
  if (target.checked) historySelected.add(id); else historySelected.delete(id);
  $('historyImportBtn').disabled = historySelected.size === 0;
}

async function historyHandleAction(event) {
  const button = event.target.closest('[data-history-action]');
  if (!button) return;
  const id = button.dataset.historyId;
  const action = button.dataset.historyAction;
  if (action === 'import') await historyImport([id]);
  if (action === 'resume') await historyResume(id);
  if (action === 'delete') await historyDelete(id);
}

async function historyImportSelected() {
  await historyImport(Array.from(historySelected));
}

async function historyImport(ids) {
  const response = await fetch('/api/history/import', {
    method: 'POST', headers: {'Content-Type':'application/json'}, body: JSON.stringify({ids: ids})
  });
  if (!response.ok) {
    $('historyStatus').textContent = '重新导入失败：' + await response.text();
    return;
  }
  const jobs = await response.json();
  const job = jobs[jobs.length - 1];
  if (job) {
    currentJob = job.id;
    render(job);
    clearInterval(timer);
    if (job.running) timer = setInterval(poll, 1000);
  }
  historySelected.clear();
  await historyLoad();
}

async function historyResume(id) {
  const response = await fetch('/api/history/resume?id=' + encodeURIComponent(id), { method: 'POST' });
  if (!response.ok) {
    $('historyStatus').textContent = '断点继续失败：' + await response.text();
    return;
  }
  const job = await response.json();
  currentJob = job.id;
  render(job);
  clearInterval(timer);
  if (job.running) timer = setInterval(poll, 1000);
  await historyLoad();
}

async function historyDelete(id) {
  if (!window.confirm('删除这条历史记录？导出的数据文件不会被删除。')) return;
  const response = await fetch('/api/history?id=' + encodeURIComponent(id), { method: 'DELETE' });
  if (!response.ok) {
    $('historyStatus').textContent = '删除历史记录失败：' + await response.text();
    return;
  }
  historySelected.delete(id);
  await historyLoad();
}

historyInit();
`
