# raftgo — этап 2: выборы и реплицированные строки

Собственная реализация Raft на Go версии из go.mod (1.27.1), пять узлов,
фиксированный состав, безопасная память модели. Снимок подготовлен из main
`c0c3138452e599c5980be8fe8f2040f932119b11`; разработка продолжается в main.

## Запуск

```sh
docker compose -p raftgo-scenarios -f scenarios/compose.yaml up -d --build
python3 scenarios/run.py all
```

Открыть http://localhost:18001/ — встроенный UI этапа 2. Выбрать реплику,
добавить строку и сравнить локальные логи пяти узлов. Строки могут повторяться,
быть пустыми, содержать Unicode и HTML (отображается текстом). Успех только
после 201 с index/term; no-op после выборов показаны отдельно.

```sh
curl -sS http://localhost:18001/status | python3 -m json.tool
curl -sS -i -L --max-redirs 5 http://localhost:18001/entries \
  -H 'Content-Type: application/json' --data '{"command":"Привет"}'
```

Follower отвечает 307; без известного лидера — 503. 504 означает неизвестный
исход: запись могла подтвердиться позднее. Не повторять её автоматически.
`/status`, `/election`, `/log` — локальные наблюдения, могут отставать.
Опросы разных реплик не образуют атомарного снимка кластера.

Основной `docker compose up -d --build` использует отдельный стенд на портах
8001–8005. Сценарный стенд использует 18001–18005 и проект raftgo-scenarios.
Не запускать runner и ручное управление отказами одновременно.

## Проверки и показ

```sh
go build ./...
go test ./...
go test -race ./...
go vet ./...
gofmt -l .
```

[Лист показа](docs/demo2.md), [API](docs/api.md),
[контракт](docs/stages.md), [сценарии](scenarios/README.md).
Ядро в raft/, Memory Storage в storage/, HTTP/RPC и встроенный UI в server/,
HTTP-транспорт в transport/, приложение в cmd/raftgo/.
Тесты ядра проверяют выборы, репликацию, замену хвоста, потери сообщений,
ограничение commit текущим term и порядок apply на виртуальных часах.

## Остановка

```sh
docker compose -p raftgo-scenarios -f scenarios/compose.yaml unpause
docker compose -p raftgo-scenarios -f scenarios/compose.yaml down
```

Отказы — pause/unpause или реализованная в runner сетевая изоляция с
восстановлением адресов и алиасов. Term, голос и лог остаются в RAM.
Завершение процесса уничтожает память: нельзя возвращать пустую ноду того же
ID в продолжающийся кластер. Новый запуск всех процессов — новый пустой кластер.
Файлов состояния и data volumes нет. Перед сменой снимка удалить весь свой стенд.
