# raftgo — лабораторная 1

Проверяемый снимок выборов Raft: пять узлов, term, голоса, heartbeat,
переизбрание и встроенный экран наблюдения. Фиксированный состав, большинство
из пяти — три. Зависимости: Go версии из go.mod, Docker Compose, Python 3.

```sh
docker compose -p raftgo-scenarios -f scenarios/compose.yaml up -d --build
python3 scenarios/run.py all
```

Откройте http://localhost:18001/ — экран этапа 1. Можно открыть порт любого
из пяти узлов (18001–18005). Браузер опрашивает только свой origin;
сервер собирает локальные статусы фиксированных участников с таймаутом 600 мс.
Опросы неатомарны, недоступные/устаревшие наблюдения отмечены явно.
`api_url` в Docker-конфигах — внутренние адреса `http://nodeN:8080`.

[API](docs/api.md), [сценарий и ручной показ](scenarios/task1/README.md),
[контракт этапов](docs/stages.md).

Отказы моделируются только pause/unpause: RAM с term и голосом сохраняется.
Stop/start отдельного узла с пустой памятью не является безопасным возвратом.
Полное удаление стенда и новый запуск создают новый пустой кластер:

```sh
docker compose -p raftgo-scenarios -f scenarios/compose.yaml unpause
docker compose -p raftgo-scenarios -f scenarios/compose.yaml down
```

Проверки:

```sh
go build ./...
go test ./...
go test -race ./...
go vet ./...
gofmt -l .
```

В снимке только выборы и heartbeat; пользовательские операции последующих
этапов отсутствуют. RequestVote сохраняет стандартные нулевые поля
lastLogTerm/lastLogIndex; heartbeat не передаёт записи.
Разработка продолжается в main; этот снимок не база для новых изменений.
