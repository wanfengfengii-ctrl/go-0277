// Live frontend state: reads the Go backend health endpoint and task status
// over the real HTTP API, updating the page without any framework.
async function checkHealth() {
  const el = document.getElementById('health');
  try {
    const res = await fetch('/healthz');
    const body = await res.json();
    el.textContent = `状态：${body.status || 'unknown'}`;
  } catch (err) {
    el.textContent = `无法连接后端：${err.message}`;
  }
}

async function queryTask(event) {
  event.preventDefault();
  const number = document.getElementById('task-number').value.trim();
  const out = document.getElementById('task-result');
  if (!number) {
    out.textContent = '请输入任务编号';
    return;
  }
  try {
    const res = await fetch(`/api/v1/tasks/${encodeURIComponent(number)}`);
    const body = await res.json();
    out.textContent = JSON.stringify(body, null, 2);
  } catch (err) {
    out.textContent = `查询失败：${err.message}`;
  }
}

document.getElementById('task-form').addEventListener('submit', queryTask);
checkHealth();
