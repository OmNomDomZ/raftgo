'use strict';
const kvMember = document.getElementById('kv-member');
const kvForm = document.getElementById('kv-form');
const kvResult = document.getElementById('kv-result');
const kvData = document.getElementById('kv-data');
let kvBusy = false;
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
  const method = event.submitter.value;
  const id = kvMember.value;
  const context = method + ' · ' + id + ' · ключ ' + JSON.stringify(key);
  kvBusy = true;
  for (const button of kvForm.querySelectorAll('button')) button.disabled = true;
  kvResult.textContent = context + ' · ' + (method === 'GET' ? 'Ожидание кворумного барьера и apply…' : 'Ожидание commit/apply…');
  let status = '';
  try {
    const response = await fetch('/cluster/kv/' + encodeURIComponent(id) + '?key=' + encodeURIComponent(key), {
      method, cache: 'no-store', redirect: 'error', headers: {'Content-Type': 'application/json'},
      body: method === 'POST' || method === 'PUT' ? JSON.stringify({value: document.getElementById('kv-value').value}) : undefined,
      signal: AbortSignal.timeout(9000)
    });
    status = 'HTTP ' + response.status + ' · ';
    const data = await response.json();
    const descriptions = {
      404: 'Ключ отсутствует', 409: 'Конфликт: ключ уже существует или запись заменена; см. ответ API',
      503: 'Лидер или кворум недоступен. Запрос не повторялся.',
      504: method === 'GET' ? 'Чтение не подтверждено; значения нет.' : 'Неизвестный исход. Автоповтора нет; повтор может выполнить операцию снова.'
    };
    kvResult.textContent = context + ' · ' + status + (response.ok ?
      (method === 'GET' ? 'Линейризуемый GET завершён' : 'Подтверждено и применено') : descriptions[response.status] || data.error || 'Ошибка запроса');
    kvData.textContent = JSON.stringify(data, null, 2);
  } catch (_) {
    kvResult.textContent = context + ' · ' + status + (method === 'GET' ? 'Чтение не подтверждено: ответ не получен.' :
      'Неизвестный исход: ответ не получен. Автоповтора нет; повтор может выполнить операцию снова.');
  } finally {
    kvBusy = false;
    for (const button of kvForm.querySelectorAll('button')) button.disabled = false;
  }
});
