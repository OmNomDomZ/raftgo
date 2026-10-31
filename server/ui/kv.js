'use strict';
const kvMember = document.getElementById('kv-member');
const kvForm = document.getElementById('kv-form');
const kvResult = document.getElementById('kv-result');
const kvData = document.getElementById('kv-data');
let kvBusy = false;
// Page memory only: no localStorage/sessionStorage (which can be cloned on open).
const kvOwned = new Map();
const kvToken = document.getElementById('kv-token');
const kvKey = document.getElementById('kv-key');
kvKey.addEventListener('input', () => { kvToken.value = ''; });
function renderOwned() {
  const list = document.getElementById('kv-owned');
  list.replaceChildren();
  for (const [key, token] of kvOwned) {
    const item = document.createElement('li');
    item.append(document.createTextNode(JSON.stringify(key) + ' · токен ' + token + ' '));
    const use = document.createElement('button');
    use.type = 'button';
    use.textContent = 'Подставить ключ и токен';
    use.disabled = kvBusy;
    use.addEventListener('click', () => { kvKey.value = key; kvToken.value = token; });
    item.append(use);
    list.append(item);
  }
  if (!kvOwned.size) list.textContent = 'В этой вкладке нет сохранённых токенов.';
}
renderOwned();
// The election poll owns membership; retain the independent CRUD selection.
new MutationObserver(() => {
  for (const option of document.getElementById('member').options) {
    if (![...kvMember.options].some(item => item.value === option.value)) kvMember.append(option.cloneNode(true));
  }
}).observe(document.getElementById('member'), {childList: true});
kvForm.addEventListener('submit', async event => {
  event.preventDefault();
  if (kvBusy || !kvMember.value || !event.submitter) return;
  const key = document.getElementById('kv-key').value;
  const length = new TextEncoder().encode(key).length;
  kvData.textContent = '';
  if (!length || length > 1024) { kvResult.textContent = 'Ключ должен занимать 1–1024 байта UTF-8'; return; }
  const operation = event.submitter.value;
  const method = {CAS: 'POST', LOCK: 'POST', UNLOCK: 'DELETE'}[operation] || operation;
  const suffix = {CAS: '/cas', LOCK: '/lock', UNLOCK: '/lock'}[operation] || '';
  const token = operation === 'LOCK' ? '' : kvToken.value;
  const value = document.getElementById('kv-value').value;
  const body = operation === 'CAS' ? {expected: document.getElementById('kv-expected').value, value} :
    (operation === 'POST' || operation === 'PUT' ? {value} : undefined);
  const id = kvMember.value;
  const context = operation + ' · ' + id + ' · ключ ' + JSON.stringify(key);
  kvBusy = true;
  renderOwned();
  for (const button of kvForm.querySelectorAll('button')) button.disabled = true;
  kvResult.textContent = context + ' · ' + (method === 'GET' ? 'Ожидание кворумного барьера и apply…' : 'Ожидание commit/apply…');
  let status = '';
  try {
    const response = await fetch('/cluster/kv/' + encodeURIComponent(id) + suffix + '?key=' + encodeURIComponent(key), {
      method, cache: 'no-store', redirect: 'error', headers: {'Content-Type': 'application/json', ...(token ? {'X-Lock-Token': token} : {})},
      body: body === undefined ? undefined : JSON.stringify(body),
      signal: AbortSignal.timeout(9000)
    });
    status = 'HTTP ' + response.status + ' · ';
    const data = await response.json();
    const descriptions = {
      404: 'Ключ отсутствует', 409: 'Конфликт: expected не совпал, ключ уже существует, токен устарел или запись заменена; см. ответ API',
      423: 'Ключ заблокирован: токен отсутствует/чужой или lock уже занят. Lock не реентерабелен.',
      503: 'Лидер или кворум недоступен. Запрос не повторялся.',
      504: method === 'GET' ? 'Чтение не подтверждено; значения нет.' : 'Неизвестный исход. Автоповтора нет; повтор может выполнить операцию снова.'
    };
    kvResult.textContent = context + ' · ' + status + (response.ok ?
      (method === 'GET' ? 'Линейризуемый GET завершён' : 'Подтверждено и применено') : descriptions[response.status] || data.error || 'Ошибка запроса');
    kvData.textContent = JSON.stringify(data, null, 2);
    if (response.ok && operation === 'LOCK' && typeof data.token === 'string' && data.token) {
      kvOwned.set(key, data.token);
    }
    if (response.ok && operation === 'UNLOCK') {
      if (kvOwned.get(key) === token) kvOwned.delete(key);
      // Keep the explicit field: a subsequent request demonstrates stale-token 409.
    }
    if (operation === 'LOCK' && (response.status === 504 || (response.ok && !data.token))) {
      kvResult.textContent += ' Токен не получен: lock мог быть захвачен. TTL нет; автоматического освобождения нет.';
    }
  } catch (_) {
    kvResult.textContent = context + ' · ' + status + (method === 'GET' ? 'Чтение не подтверждено: ответ не получен.' :
      'Неизвестный исход: ответ не получен. Автоповтора нет; повтор может выполнить операцию снова.');
    if (operation === 'LOCK') kvResult.textContent += ' Токен не получен: lock мог быть захвачен. TTL нет; автоматического освобождения нет.';
  } finally {
    kvBusy = false;
    renderOwned();
    for (const button of kvForm.querySelectorAll('button')) button.disabled = false;
  }
});
