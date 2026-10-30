'use strict';
const nodes = document.getElementById('nodes');
const poll = document.getElementById('poll');
const refresh = document.getElementById('refresh');
const observations = new Map();
const member = document.getElementById('member');
const logStatus = document.getElementById('log-status');
const entries = document.getElementById('entries');
const result = document.getElementById('result');
const logs = new Map();
let logBusy = false;
let writing = false;
let busy = false;
let lastPoll = 0;
let pollFailed = false;
function element(tag, text, className) {
  const el = document.createElement(tag);
  el.textContent = text;
  if (className) el.className = className;
  return el;
}
function render() {
  const now = Date.now();
  const cards = [];
  for (const [id, item] of observations) {
    const age = item.received ? now - item.received : Infinity;
    const stale = !item.available || age > 3000;
    const s = item.node;
    const card = element('article', '', 'node' + (stale ? ' stale' : s?.role === 'Leader' ? ' leader' : ''));
    card.append(element('h2', id));
    card.append(element('div', !item.available ? 'Недоступен · данные устарели' : stale ? 'Данные устарели · нет свежего опроса' : 'Доступен · свежий опрос', 'availability'));
    const list = document.createElement('dl');
    const fields = [
      ['Роль', s?.role ?? 'неизвестно'], ['Term', s?.term ?? 'неизвестно'],
      ['Лидер (leader_id)', s ? s.leader_id || 'неизвестен' : 'неизвестно'],
      ['Голос (voted_for)', s ? s.voted_for || 'не отдан' : 'неизвестно'],
      ['Голоса кандидата', s?.role === 'Candidate' ? s.votes : '— (только Candidate)'],
      ['Кворум', s?.quorum ?? 'неизвестно'], ['Running', s ? String(s.running) : 'неизвестно']
    ];
    for (const [key, value] of fields) list.append(element('dt', key), element('dd', String(value)));
    card.append(list, element('p', item.observed_at ? 'Последний успех: ' + new Date(item.observed_at).toLocaleTimeString() + ' · ' + Math.floor(age / 1000) + ' с назад' : 'Успешных опросов ещё нет', 'last'));
    cards.push(card);
  }
  nodes.replaceChildren(...cards);
  for (const id of observations.keys()) {
    if (![...member.options].some(option => option.value === id)) {
      const option = element('option', id); option.value = id; member.append(option);
    }
  }
  renderLog();
  poll.textContent = pollFailed ? 'Опрос недоступен. Последние данные могут устареть.' : lastPoll ? 'Неатомарный опрос · ' + new Date(lastPoll).toLocaleTimeString() : 'Загрузка состава…';
}
async function update() {
  if (busy) return;
  busy = true;
  refresh.disabled = true;
  try {
    const response = await fetch('/cluster/status', {cache: 'no-store', signal: AbortSignal.timeout(2000)});
    if (!response.ok) throw new Error('status');
    const data = await response.json();
    if (data.atomic !== false || !Array.isArray(data.members)) throw new Error('schema');
    for (const item of data.members) {
      const previous = observations.get(item.id) || {};
      observations.set(item.id, item.available && item.node ? {...item, received: Date.now()} : {...previous, available: false});
    }
    lastPoll = Date.now();
    pollFailed = false;
  } catch (_) {
    pollFailed = true;
  } finally {
    busy = false;
    refresh.disabled = false;
    render();
  }
}
refresh.addEventListener('click', update);
setInterval(render, 500);
// Schedule after completion so a slow request cannot overlap the next poll.
async function loop() { await update(); setTimeout(loop, 1000); }
loop();

function renderLog() {
  const snapshot = logs.get(member.value);
  const stale = !snapshot || !snapshot.available || Date.now() - snapshot.received > 3000;
  logStatus.textContent = snapshot ? (stale ? 'Данные устарели · ' : 'Свежий локальный снимок · ') +
    member.value + ' · commit_index ' + snapshot.node.commit_index + ' · last_log_index ' + snapshot.node.last_log_index +
    ' · последний успех ' + new Date(snapshot.received).toLocaleTimeString() : 'Успешного снимка ещё нет';
  const rows = (snapshot?.entries || []).map(entry => {
    const row = element('tr', '', entry.committed ? 'committed' : 'tail');
    for (const value of [entry.index, entry.term, entry.kind === 'noop' ? 'no-op (служебная)' : 'command',
      entry.committed ? 'committed' : 'неподтверждённый хвост', entry.kind === 'noop' ? '—' : JSON.stringify(entry.command)]) {
      row.append(element('td', String(value)));
    }
    return row;
  });
  entries.replaceChildren(...rows);
}
async function updateLog() {
  const id = member.value;
  if (!id || logBusy) return;
  logBusy = true;
  try {
    const response = await fetch('/cluster/log/' + encodeURIComponent(id), {cache: 'no-store', signal: AbortSignal.timeout(2000)});
    if (!response.ok) throw new Error('log');
    const data = await response.json();
    if (data.node?.id !== id || data.consistency !== 'local' || !Array.isArray(data.entries)) throw new Error('schema');
    logs.set(id, {...data, available: true, received: Date.now()});
  } catch (_) {
    if (logs.has(id)) logs.get(id).available = false;
  } finally { logBusy = false; renderLog(); }
}
member.addEventListener('change', () => { renderLog(); updateLog(); });
refresh.addEventListener('click', updateLog);
async function logLoop() { await updateLog(); setTimeout(logLoop, 1000); }
logLoop();
document.getElementById('add').addEventListener('submit', async event => {
  event.preventDefault();
  if (writing || !member.value) return;
  const command = document.getElementById('command').value;
  if (new TextEncoder().encode(command).length > 65536) { result.textContent = 'Строка превышает 64 КиБ UTF-8'; return; }
  writing = true;
  document.getElementById('submit').disabled = true;
  result.textContent = 'Ожидание commit/apply…';
  try {
    const response = await fetch('/cluster/entries/' + encodeURIComponent(member.value), {
      method: 'POST', headers: {'Content-Type': 'application/json'}, body: JSON.stringify({command}),
      redirect: 'error', signal: AbortSignal.timeout(9000)
    });
    const data = await response.json();
    const position = data.index !== undefined ? ' · index ' + data.index + ' · term ' + data.term : '';
    result.textContent = 'HTTP ' + response.status + position + ' · ' +
      (response.status === 201 ? 'Подтверждено и применено' : response.status === 504 ?
        'Неизвестный исход. Автоповтора нет; повтор может создать дубликат.' : data.error || 'Ошибка записи');
  } catch (_) {
    result.textContent = 'Неизвестный исход: ответ не получен. Автоповтора нет; повтор может создать дубликат.';
  } finally { writing = false; document.getElementById('submit').disabled = false; updateLog(); }
});
