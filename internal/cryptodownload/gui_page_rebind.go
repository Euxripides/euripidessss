package cryptodownload

const guiPageRebindJS = `
async function loadJobs() {
  try {
    const res = await fetch('/api/jobs');
    if (!res.ok) throw new Error(await res.text());
    const jobs = await res.json();
    const active = jobs.filter(job => job.status === 'running' || job.status === 'paused');
    active.sort((left, right) => String(left.startedAt || '').localeCompare(String(right.startedAt || '')) || String(left.id).localeCompare(String(right.id)));
    const job = active[active.length - 1];
    if (!job) return;
    currentJob = job.id;
    document.body.dataset.currentJob = job.id;
    render(job);
    clearInterval(timer);
    if (job.running) timer = setInterval(poll, 1000);
  } catch (e) {
    $('statusText').textContent = e.message;
  }
}
loadSettings().then(loadJobs);
`
