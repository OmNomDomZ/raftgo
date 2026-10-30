'use strict';
const nodes = document.getElementById('nodes');
const poll = document.getElementById('poll');
const refresh = document.getElementById('refresh');
const observations = new Map();
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
