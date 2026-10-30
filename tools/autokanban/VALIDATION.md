# Проверка первой версии

2026-10-31. Отдельное поручение автоматизации, ID карточки канбана не назначен.
Ветка `chore/autokanban`, база `prepare/in-memory` на `567482d`.
Worktree `/home/bogdan/rafting-autokanban`.

Добавлены `autokanban`, `tools/autokanban/runner.py`, `test_runner.py`,
`README.md`, этот отчёт и локальный `.gitignore` Python-кэша.
Код Raft и существующий общий канбан не изменялись.

Фактические проверки: Go `go1.27.3-X:nodwarf5 linux/amd64`, go.mod без изменений.
`.golangci.yml` отсутствует.

```text
$ go build ./...
exit=0 (вывод пуст)

$ go test -count=1 ./...
?   raftgo/cmd/raftgo [no test files]
ok  raftgo/kv       0.002s
ok  raftgo/raft     1.021s
ok  raftgo/server   5.678s
ok  raftgo/storage  0.003s
?   raftgo/transport [no test files]
exit=0

$ go test -race -count=1 ./...
?   raftgo/cmd/raftgo [no test files]
ok  raftgo/kv       1.015s
ok  raftgo/raft     6.627s
ok  raftgo/server   6.785s
ok  raftgo/storage  1.014s
?   raftgo/transport [no test files]
exit=0

$ go vet ./...
exit=0 (вывод пуст)

$ gofmt -l .
exit=0 (вывод пуст)

$ git diff --check
exit=0 (вывод пуст)

$ python3 -m unittest discover -s tools/autokanban -p 'test_*.py' -v
Ran 17 tests in 1.279s
OK
exit=0

$ ./autokanban plan
Следующая: S1-API, база stage/1-election, цель stage/1-election
exit=0
```

Тесты создают временные Git-репозитории. Проверены реальная CLI-цепочка двух
карточек с подменёнными Codex/Go, отдельные сессии, интеграция и общий журнал,
остановка перед DEMO, блокировка двух контроллеров и доступность status,
blocked с exit=0 процесса, отсутствие JSON, неверный ID/SHA/ограничения,
неуспешная race-проверка, вывод gofmt при exit=0, изменённая target-ветка,
грязный worker, таймаут, сохранение usage и продолжение по ID сессии.

Ограничения: живой запрос модели и реальное выполнение карточки не запускались;
синтаксис команды resume проверен установленным CLI через --help. Docker/UI
не запускались: их код этим поручением не изменён. Доступность этих инструментов
в конкретной сессии CLI выясняется при исполнении карточки. Дополнительные
критерии карточек проверяет исполнитель, независимого LLM-review нет.
Следующий доступный ID — S1-API. Общий журнал/стадии/main не изменялись,
push отсутствует; автоматизация остаётся отдельной служебной веткой.
