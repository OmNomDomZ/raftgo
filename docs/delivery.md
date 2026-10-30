# Как выполнять задачи и сдавать этапы

## Основная разработка

С 2026-10-31 единственная рабочая база — `main` в `/home/bogdan/rafting`.
В неё объединены `origin/main` (включая внешний dashboard) и `chore/autokanban`
(включая B0–B2, Memory Storage, канбан и контроллер). Новые карточки, тесты,
UI и общий журнал коммитятся прямо в main. Отдельных task/worktree/копий нет.
`master` не создаётся. Push без поручения не выполняется.

```text
main: полный код + Memory + инфраструктура + канбан + autokanban
  ├── DEMO 1 → lab1 (проверенный снимок выборов)
  ├── DEMO 2 → lab2 (проверенный снимок выборов и строкового лога)
  ├── DEMO 3 → lab3 (проверенный снимок с CRUD)
  └── DEMO 4 → lab4 (проверенный снимок с CAS/lock и клиентом)
```

`reference/full`, `prepare/in-memory`, `stage/*`, `task/*`, `chore/*` сохраняются
как история. `stage/1-election` содержит уже выделенное ядро S1-CORE; использовать
его как источник при подготовке lab1, не заменять им полную реализацию main.
Исторические отчёты ниже описывают прежний процесс, их пути не инструкции запуска.
`lab1` принят в S1-DEMO на `a2f486172f435f2e469802935ecec43048abee69`.
`lab2`–`lab4` пока не приняты; не создавать их заранее из полного main.

## Задание агенту и очередь

«Выполни S1-API» означает: прочитать AGENTS, контракт, карточку и зависимости;
в чистом main пометить только свою карточку «В работе», выполнить работу,
проверки, обновить журнал и статус, сохранить в коммитах. Будущие возможности
main не удалять ради контракта ранней лабораторной. Если зависимость не готова,
назвать ID; чужую работу не коммитить. При занятом checkout остановиться.

`./autokanban plan` выбирает карточку из main без модели.
`./autokanban run --max-tasks 2` разрешает две последовательные карточки.
Каждая получает свою сессию Codex, но ту же папку и ветку main. Контроллер
повторяет обязательные проверки точного SHA, сохраняет независимые результаты
в `.git/autokanban/`, не переключает ветки и не меняет labN. Ошибка останавливает
очередь; `status`, `resume`, `resume --repair` описаны в
[инструкции](../tools/autokanban/README.md). DEMO выполняется явным поручением.

## Подготовка снимка в DEMO

1. В чистом main выполнить критерии DEMO, записать исходный SHA main.
   Убедиться, что нужная labN ещё не существует и другие процессы не работают
   с checkout. Остановить свой сценарный стенд целиком перед переключением.
2. В этой же папке создать `git switch -c snapshot/labN <SHA-main>`.
   Это единственное временное ответвление для приёмки; никаких копий папки.
3. В снимке удалить будущую функциональность из исходников, сборки, API, UI,
   моделей, декодеров, тестов и сценариев. Для lab1 доступно ядро S1-CORE из
   `stage/1-election`; переносить выбранные файлы, не ветку целиком.
   Удалить `autokanban`, `tools/autokanban`, `Kanban.md`, `.obsidian`,
   `docs/tasks.md`, `docs/delivery.md` и документацию старого процесса.
   Оставить необходимые инструкции запуска/сдачи, конфиги и тесты.
   AGENTS снимка должен описывать его как результат сдачи, направляя разработку в main.
4. Закоммитить кандидат, выполнить весь Go-набор, сценарии 1..N и ручной UI
   именно на этом SHA. Контракт — docs/stages.md; флаги вместо удаления запрещены.
   Если проверка не пройдена, сохранить кандидат; labN не создавать.
5. При успехе `git branch labN <проверенный-SHA>`, затем `git switch main`.
   Зафиксировать SHA main и labN, файлы, реальные результаты и ограничения в
   этом журнале; только после этого отметить DEMO готовой в общем канбане.
   Временную ветку кандидата можно сохранить для аудита; отдельной папки нет.
6. Следующие карточки выполняются в main. Не сдвигать уже принятые labN вслед
   за main, не мержить снимок назад. Для проверки нескольких лабораторных
   переключать чистый checkout последовательно с полным сбросом своего стенда.

На время ручного переключения никакой контроллер/агент не должен работать.
При прерывании не удалять изменения: коммит кандидата, возврат в main, отчёт
о незавершённых проверках. Принятый снимок изменяется лишь по отдельному поручению.

## Определение готовности

Карточка готова, когда её объём реализован, критерии проверены и отчёт содержит
фактические проверки. Этап принят только после DEMO-карточки и создания проверенной labN. Не скрывать провалившиеся тесты удалением safety-проверок; удаление
тестов будущих функций при выделении этапа, напротив, необходимо.

Обязательные проверки из AGENTS: build, test, race, vet (или golangci-lint при
наличии конфига), gofmt. Если окружение не позволяет их выполнить, отметить
«ожидает проверки», не выдавать `PASS`. Перед сдачей — реальный браузер и Docker
на пяти узлах; unit-тесты не заменяют сценарий.

Сценарный runner должен оставаться на Python stdlib, ожидать условия с общим
дедлайном, печатать шаг/HTTP-код/ожидание и при ошибке краткую диагностику
доступных узлов. Восстановление pause/unpause обязательно через finally;
после SIGKILL возможен ручной unpause. Ошибка завершается ненулевым кодом.
`PASS taskN` печатается лишь после проверок; без ручного управления нодами
параллельно runner. Каждая ветка хранит только сценарии доступных этапов.

Целевой запуск (после соответствующих карточек, сейчас команда запускает прототип):

```sh
docker compose -p raftgo-scenarios -f scenarios/compose.yaml up -d --build
python3 scenarios/run.py all
```

Для показа отказа — pause/unpause нужного node, не stop/start.
После прогона можно осмотреть UI и локальные статусы. Перед переключением
этапа сначала снять паузы и удалить **весь** стенд, затем собрать заново:

```sh
docker compose -p raftgo-scenarios -f scenarios/compose.yaml unpause
docker compose -p raftgo-scenarios -f scenarios/compose.yaml down
```

После миграции down уничтожает память; следующий up начинает пустой кластер.
Не запускать разные ветки одновременно на тех же портах/Compose project.
Для task1–N wrapper `bash scenarios/taskN/run.sh` сохраняется.

## Реестр приёмки

Этап 1 принят; остальные строки заполняются после соответствующих DEMO.

| Результат | Принятый SHA | Go/race | Docker | UI | Статус |
| --- | --- | --- | --- | --- | --- |
| Полный исходник | `70782f23fe695d184981dff932c47181f9eadd21` | build/test/race/vet/gofmt пройдены при планировании | Не запускался | Отсутствует | Источник |
| prepare/in-memory | `4f6499180cb7c22d429507506431218a445be18b` | build/test/race/vet/gofmt PASS | task1–4, cleanup и полный сброс PASS | Не требуется | B0–B2 готовы |
| lab1 | `a2f486172f435f2e469802935ecec43048abee69` | build/test/race/vet/gofmt PASS | task1 и cleanup PASS | Firefox PASS | S1-DEMO готова |
| lab2 | — | — | — | — | Не начат |
| lab3 | — | — | — | — | Не начат |
| lab4 | — | — | — | — | Не начат |

В строке результата записывать реальный SHA проверенного кода. При изменениях
после приёмки проверки повторяются для нового SHA. Исправления разрабатывать в main; принятые снимки без поручения не перемещать.

## B0 — исходная база (2026-10-31)

- Рабочая ветка: `task/B0-baseline`, база `chore/obsidian-vault` на
  `70782f23fe695d184981dff932c47181f9eadd21`; отдельный worktree
  `/home/bogdan/rafting-b0`. Исходный checkout не переключался.
- `reference/full` создана на том же SHA; не перемещать эту ссылку.
  `git log -3 --oneline`: `70782f2`, `93aa401`, `a722fd4`.
- Перенесены утверждённые AGENTS, Kanban и docs. Незакоммиченные README,
  сценарные README и `.obsidian` исходного checkout не включались в коммиты.
- Go: `/usr/bin/go`, `go1.27.3-X:nodwarf5 linux/amd64`; требование
  `go 1.27.1` в go.mod сохранено. Docker client/server 29.9.2,
  Compose 5.6.1; daemon доступен.
- Полный код всё ещё использует файловый Storage. B0 не принимает миграцию
  памяти или какой-либо stage; далее доступна только B1.

Проверки выполнялись на исходном коде SHA выше; изменения B0 — документация.
Конфига `.golangci.yml` нет, использован `go vet`. Фактический вывод:

```text
$ go build ./...
exit=0

$ go test -count=1 ./...
?   	raftgo/cmd/raftgo	[no test files]
ok  	raftgo/kv	0.002s
ok  	raftgo/raft	0.922s
ok  	raftgo/server	5.837s
ok  	raftgo/storage	0.002s
?   	raftgo/transport	[no test files]
exit=0

$ go test -race -count=1 ./...
?   	raftgo/cmd/raftgo	[no test files]
ok  	raftgo/kv	1.005s
ok  	raftgo/raft	6.336s
ok  	raftgo/server	6.767s
ok  	raftgo/storage	1.008s
?   	raftgo/transport	[no test files]
exit=0

$ go vet ./...
exit=0

$ gofmt -l .
exit=0
```

Исходный Docker-замер запускается из корня подготовительного worktree:

```sh
docker compose -p raftgo-scenarios -f scenarios/compose.yaml up -d --build
python3 scenarios/run.py all
docker compose -p raftgo-scenarios -f scenarios/compose.yaml down
```

Сборка пяти контейнеров завершилась с exit=0. Использованы существующие
volumes проекта `raftgo-scenarios`: это замер прототипа с сохранёнными данными,
а не проверка пустого кластера. Другой запущенный проект `rafting` не затронут.
Сценарии пока используют stop/start и восстановление с диска; замена в B2.

Результат `python3 scenarios/run.py all` (exit=0):

```text
PASS task1
PASS task2
PASS task3
PASS task4
```

Исходных ошибок в выполненных проверках не обнаружено. UI отсутствует и
не проверялся: его создание вне B0. Подготовительная база после B0 содержит
план, но миграция B1/B2 ещё не выполнена; строка приёмки prepare относится
к завершению всей подготовки, а не только B0.

B0 готова: результат `task/B0-baseline` — `8bdb98d348ef111d0fec03a34af727e1fd4241e2`;
локально интегрирован в `prepare/in-memory`. Task-ветка сохранена.
Стенд остановлен через `down`, volumes сохранены. Push не выполнялся.


## B1 — Memory Storage (2026-10-31)

- Ветка `task/B1-memory-storage`, база `prepare/in-memory` на
  `7121e6e6684441b89efe6c2fc9e542f6cac1331b`; отдельный worktree
  `/home/bogdan/rafting-b1`. Исходный checkout и чужие изменения сохранены.
- Добавлен `storage/memory.go`: готовый к работе нулевой `Memory`, mutex,
  синхронные атомарные операции, глубокие копии срезов и command bytes,
  замена/усечение суффикса и ошибка неверного индекса без изменения данных.
- В `storage/file.go` только уточнён комментарий пакета и перенесён общий
  `cloneLog` в `memory.go`; поведение файлового backend сохранено.
  В `raft/storage.go` обновлён контракт безопасной памяти, ошибок и копирования.
- `storage/memory_test.go` проверяет границы (включая максимальный индекс),
  пустой лог, append/truncate/replace, отсутствие aliasing, конкурентные
  load/save и отсутствие частично видимых state/log. Новый Raft с тем же
  Memory сохраняет term/голос/лог, отклоняет второй голос в term,
  несовпадающий префикс и кандидата с устаревшим логом; замена хвоста также
  переживает повторное создание Raft. Использованы fake-транспорт и часы
  без реального времени, сна и опроса условий.
- Канбан: B1 готова. Следующая доступная карточка — B2.

Фактические проверки в worktree B1 (`go.mod` не менялся):

```text
$ go build ./...
exit=0 (вывод пуст)

$ go test -count=1 ./...
?   raftgo/cmd/raftgo [no test files]
ok  raftgo/kv       0.003s
ok  raftgo/raft     1.061s
ok  raftgo/server   5.709s
ok  raftgo/storage  0.005s
?   raftgo/transport [no test files]
exit=0

$ go test -race -count=1 ./...
?   raftgo/cmd/raftgo [no test files]
ok  raftgo/kv       1.008s
ok  raftgo/raft     6.406s
ok  raftgo/server   6.735s
ok  raftgo/storage  1.017s
?   raftgo/transport [no test files]
exit=0

$ go vet ./...
exit=0 (вывод пуст; .golangci.yml отсутствует)

$ gofmt -l .
exit=0 (вывод пуст)

$ git diff --check
exit=0 (вывод пуст)
```

Ограничения: приложение пока использует File, переключение и удаление дисковых
настроек выполняются только в B2. Memory не переживает завершение процесса;
симулируемый перезапуск использует тот же объект. Docker/UI в B1 не запускались:
это не приёмка этапа, wiring и сценарии не изменены, UI ещё отсутствует.

Проверенный результат B1: `983654e` (`task/B1-memory-storage`).
Результат локально интегрирован fast-forward в `prepare/in-memory` вместе
с этим журналом; task-ветка сохранена. Push не выполнялся.

## B2 — приложение и стенды на памяти (2026-10-31)

- Ветка `task/B2-memory-runtime`, база `prepare/in-memory` на
  `af754ca729c1f3c9be5260afee762c9a7b655900`; отдельный worktree
  `/home/bogdan/rafting-b2`. Проверенный код: `4f6499180cb7c22d429507506431218a445be18b`.
- `cmd/raftgo/main.go` использует отдельную пустую `storage.Memory` на процесс.
  Удалены `storage/file.go` и `storage/file_test.go`, дисковая настройка из
  `server/config.go`, обоих JSON-конфигов и data volumes обоих Compose.
  В основном Compose отключён автоматический restart: пустую память нельзя
  незаметно возвращать под прежним ID в продолжающийся кластер.
- `server/http_test.go` и `server/replay_test.go` используют Memory;
  `server/config_test.go` дополнительно проверяет загрузку без data_dir и явный
  отказ для старого дискового конфига. Failure-injection ядра сохранён без изменений.
  `storage/memory.go` получил комментарий пакета, `.gitignore` больше не
  описывает каталог состояния. README, onboarding, текущий статус в stages и
  все сценарные README согласованы с безопасной памятью.
- `scenarios/run.py`: pause/unpause вместо stop/start, пауза всего кластера в
  task3/task4, исключение паузных процессов из выборки лидера, короткий timeout
  диагностики, вывод ожиданий и диагностика при ошибке. Cleanup в finally
  опрашивает Docker и снимает каждую фактическую паузу, даже после частичного
  сбоя Docker-команды; ошибка одной ноды не мешает попыткам вернуть остальные.
- Канбан: B2 готова. Далее доступна только S1-CORE; stage-ветки не создавались.

Проверки выполнены с Go `go1.27.3-X:nodwarf5 linux/amd64`; `go.mod` не менялся.
Фактические результаты после последних Go-изменений:

```text
$ go build ./...
exit=0 (вывод пуст)

$ go test -count=1 ./...
?   raftgo/cmd/raftgo [no test files]
ok  raftgo/kv       0.002s
ok  raftgo/raft     0.950s
ok  raftgo/server   5.723s
ok  raftgo/storage  0.003s
?   raftgo/transport [no test files]
exit=0

$ go test -race -count=1 ./...
?   raftgo/cmd/raftgo [no test files]
ok  raftgo/kv       1.007s
ok  raftgo/raft     6.414s
ok  raftgo/server   6.742s
ok  raftgo/storage  1.012s
?   raftgo/transport [no test files]
exit=0

$ go vet ./...
exit=0 (вывод пуст; .golangci.yml отсутствует)

$ gofmt -l .
exit=0 (вывод пуст)

$ git diff --check
exit=0 (вывод пуст)

$ python3 -m py_compile scenarios/run.py
exit=0

$ docker compose -p raftgo-scenarios -f scenarios/compose.yaml up -d --build
exit=0 (пять контейнеров, образ собран Go 1.27.1 из Dockerfile)

$ python3 scenarios/run.py all
PASS task1
PASS task2
PASS task3
PASS task4
exit=0
```

Дополнительные фактические проверки:

- Оба `docker compose ... config --format json`: exit=0. В каждом пять
  сервисов, только read-only bind конфига, нет data volumes и автоперезапуска.
- В отдельном процессе подменён task1: pause node1/node3, затем намеренный
  `AssertionError`. Runner вывел диагностику (паузные ноды — timeout), вернул
  ненулевой код и снял обе паузы. Docker inspect: все пять Running, ни одной
  Paused, StartedAt всех процессов остался прежним. Проверочный harness exit=0:
  `PASS cleanup: injected failure exit != 0; all five unpaused; no process restarted`.
- Записано значение и захвачен lock с уникальным ключом. Docker inspect
  подтвердил только read-only конфиг; docker diff содержит лишь каталоги и путь
  монтирования `/etc/raftgo/cluster.json`, файлов состояния нет. Затем `down`
  **всех** пяти нод и `up -d`: GET прежнего ключа — 404, на всех нодах в логах
  только новые no-op. Harness exit=0:
  `PASS full reset: no prior user log, KV, or locks; fresh cluster only has no-op entries`.
- Поиск по Go-коду: нет `storage.Open`, `storage.File`, `DataDir`, `fsync`,
  `raft.json` или `syscall.Flock`. Единственный `data_dir` — негативный тест
  старого конфига. Исторические записи B0/B1 и текст карточек сохранены.

При первом прогоне все task1–4 прошли свои проверки, но cleanup вернул ошибку
Docker «container is not paused». Cleanup исправлен на опрос фактических пауз;
приведённый выше повторный прогон полностью завершился с exit=0.

Ограничения: это безопасная RAM в модели задания, не crash-recovery. Kill теряет
память; fencing монотонен только внутри одной жизни кластера. UI ещё отсутствует
и не проверялся, ни один stage этой карточкой не принимается. Сценарный стенд
после проверок полностью удалён через down; запущенный чужой проект `rafting`
не затронут. Исходный checkout не переключался, чужие файлы не коммитились.
Локальная интеграция результата и этого журнала в `prepare/in-memory` выполняется
fast-forward, task-ветка сохраняется. Push и изменения main не выполняются.

## S1-CORE — только выборы (2026-10-31)

- Ветка `task/S1-CORE-election-only`, база `prepare/in-memory` на
  `98ad91d00b3b8c6f48cc1b10d98d8ff50ceaa119` (короткий SHA `98ad91d`);
  отдельный worktree `/home/bogdan/rafting-s1-core`. Зависимость B2 готова.
- Проверенный код: `99787d4257c33b5617a371943c303b7a07868906`.
  Результат локально интегрирован в новую `stage/1-election`; task-ветка
  сохранена. Stage пока не принят: далее S1-API, S1-UI, S1-DEMO.
- `raft/`: удалены log/read/apply/replication и их тесты, log-типы и поля
  Config/Status; изменены raft, run, election, handlers, storage, transport,
  types, inspect и соответствующие election/cluster/quorum/stepdown/helpers/
  fakes/raft-тесты. Новый `heartbeat.go` отправляет только term/leader ID,
  ограничивает один RPC на peer и сохраняет check quorum и отмену RPC.
  RequestVote сохраняет стандартные нулевые log-поля, ненулевые отклоняются.
- `storage/memory.go` хранит только term/голос. `memory_test.go` проверяет
  атомарность пары при конкурентном доступе и реальный restart Raft с тем же
  Memory: прежний кандидат получает повторный голос, другой и старый term
  отклоняются. В ядре сохранён failure-injection Storage.
- `server/http.go`, `http_test.go`, `cmd/raftgo/main.go`: удалены обработчики
  будущих маршрутов, apply consumer и его wiring; `kv/`, `server/kv.go` и
  replay-тесты удалены. Тест подтверждает отсутствие будущих маршрутов (404).
- `scenarios/run.py`, README и task1/README содержат только этап 1;
  task2–4 удалены. README, docs/onboarding и вводный статус docs/stages
  соответствуют ветке выборов. Канбан: S1-CORE готова; следующая — S1-API.
- Сохранены проверки Election Safety на каждом переходе в лидера, split vote,
  повторы голосов и старые ответы, старшие term, изоляция меньшинства,
  restart с сохранённым Storage; 20 seed случайных отказов и 100 seed
  задержек/переупорядочивания RPC. Fake-транспорт и виртуальное время,
  без Sleep и реальной сети в core-тестах. После изоляции лидер может снова
  победить в новом term: логовых ограничений голосования на этапе 1 нет.

Фактические проверки: Go `go1.27.3-X:nodwarf5 linux/amd64`, go.mod не менялся.
`.golangci.yml` отсутствует, выполнен vet. Последний полный прогон:

```text
$ go build ./...
exit=0 (вывод пуст)

$ go test -count=1 ./...
?   raftgo/cmd/raftgo [no test files]
ok  raftgo/raft      0.187s
ok  raftgo/server    0.003s
ok  raftgo/storage   0.003s
?   raftgo/transport [no test files]
exit=0

$ go test -race -count=1 ./...
?   raftgo/cmd/raftgo [no test files]
ok  raftgo/raft      2.048s
ok  raftgo/server    1.017s
ok  raftgo/storage   1.018s
?   raftgo/transport [no test files]
exit=0

$ go vet ./...
exit=0 (вывод пуст)

$ gofmt -l .
exit=0 (вывод пуст)

$ git diff --check
exit=0 (вывод пуст)

$ python3 -m py_compile scenarios/run.py
exit=0

$ docker compose -p raftgo-scenarios -f scenarios/compose.yaml up -d --build
exit=0 (пять контейнеров, Dockerfile собирает Go 1.27.1)

$ python3 scenarios/run.py all
1.1: все пять узлов согласились о лидере node5, term 1
1.2: pause node5; новый лидер node4 в старшем term
1.3: unpause node5; прежний лидер стал follower
1.4: pause node4/node1/node2; два оставшихся не выбрали лидера за 5 секунд
unpause всех; кластер выбрал лидера node3
PASS task1
exit=0

$ docker compose -p raftgo-scenarios -f scenarios/compose.yaml down
exit=0 (удалены пять контейнеров и сеть)
```

Ограничения: UI ещё отсутствует, браузерная проверка не выполнялась; приёмка
всего этапа — отдельная S1-DEMO после API/UI. Safe Memory сохраняется при
pause/unpause и повторном использовании объекта Storage; завершение процесса
теряет RAM. Чужой запущенный Compose-проект `rafting` не затронут. Исходный
checkout не переключался, чужие изменения не коммитились. В общий журнал
`prepare/in-memory` перенесены только статус карточки и эта запись; код
этапа в подготовительную ветку не переносился. Push/main не изменялись.


## Переход на main и снимки labN (2026-10-31)

- Поручение пользователя вне карточек; новые карточки не запускались.
- База локального main: c976e45, fast-forward до origin/main fda7bed,
  затем merge chore/autokanban b5c88cd. Сохранены Memory, полный протокол,
  KV/CAS/lock, все тесты, канбан, контроллер и внешний dashboard.
- Незакоммиченные изменения исходного chore/obsidian-vault сохранены через
  `git stash push --include-untracked` с сообщением
  `preserve local vault before main workflow migration`; stash не удалён.
  Личные .obsidian восстановлены в той же папке и исключены из Git.
  Старые ветки/ссылки сохранены; worktree не создавались; push отсутствует.
- Изменены AGENTS.md, README.md, docs/stages.md, docs/tasks.md, этот журнал,
  tools/autokanban/{runner.py,test_runner.py,README.md,VALIDATION.md}, .gitignore.
  Разработка/статусы/журнал — main, сдаваемые lab1–lab4 создаются только после
  проверки кандидата DEMO в том же checkout. Старые отчёты остаются историей.
- Dashboard сохранён из origin/main и адаптирован под безопасную RAM:
  Docker pause/unpause, локально SIGSTOP/SIGCONT без замены процесса,
  отказ от возврата завершённого процесса под тем же ID, конфиги без data_dir,
  уникальная папка только для бинарника/конфигов/логов, cleanup всех процессов.
- Контроллер не создаёт ветки/папки, не переключает checkout и не интегрирует
  результаты из других веток. Проверяет точный SHA main и неизменность labN.
  После ошибки сохраняет изменения и останавливает очередь до resume.

Фактические проверки, Go go1.27.3-X:nodwarf5 linux/amd64, go.mod не менялся:

```text
$ go build ./...
exit=0 (вывод пуст)

$ go test ./...
?    raftgo/cmd/raftgo [no test files]
ok   raftgo/kv       (cached)
ok   raftgo/raft     1.041s
ok   raftgo/server   5.808s
ok   raftgo/storage  0.002s
?    raftgo/transport [no test files]
exit=0

$ go test -race ./...
?    raftgo/cmd/raftgo [no test files]
ok   raftgo/kv       (cached)
ok   raftgo/raft     6.439s
ok   raftgo/server   6.807s
ok   raftgo/storage  1.014s
?    raftgo/transport [no test files]
exit=0

$ go vet ./...
exit=0 (вывод пуст)

$ gofmt -l .
exit=0 (вывод пуст)

$ python3 -m unittest discover -s tools/autokanban -p 'test_*.py' -v
Ran 21 tests in 1.300s
OK
exit=0
```

Дополнительный harness dashboard (пять реальных локальных Go-процессов):

```text
PASS Docker dashboard controls: pause/unpause
PASS local dashboard: node1 -> node5; same PID 53933, KV survives pause/failover/resume
PASS cleanup: all five local processes terminated
```

Docker-кнопки проверены с подменой subprocess, локальные процессы — реально:
PUT до паузы лидера, GET на новом лидере, PUT после смены, возврат прежнего
PID follower, проверка значения и завершения всех процессов в finally.
Первый прогон harness ошибочно ожидал lowercase role; после исправления
сравнения с реальными Leader/Follower весь прогон завершился с exit=0.
Существующий Compose-проект rafting не затронут.

Ограничения: живой Codex не запускался, новые карточки не исполнялись;
Docker-сценарии/ручной UI лабораторных не повторялись, lab1–lab4 ещё не приняты
и не созданы. Доступный следующий ID — S1-API, теперь от main.

## S1-API — диагностика выборов (2026-10-31)

- Ветка `main`, база `186206964a7b457f235428f6b7ddf20958ceaea2`, существующий
  checkout `/home/bogdan/rafting`. Он был чистым; зависимость S1-CORE готова.
  Реализация и проверки завершены; после снятия ограничения записи в Git
  карточка отмечена «Готово». Следующий доступный ID — S1-UI; его выполнение
  не запускалось.
- `raft/inspect.go`: выделены `ElectionStatus` и `Status.Election()` — контракт
  восьми полей выборов для переноса в lab1. Уточнены комментарии о voted_for
  текущего term и votes только кандидата. Полный Status и оба HTTP alias в
  main, InspectLog и все log/CRUD/CAS/lock API сохранены.
- `raft/inspect_test.go`: Election Safety при повторном grant одного peer,
  проверка обнуления диагностического votes после избрания/смены term,
  отсутствие выдуманного voted_for по leader_id; точный JSON представления
  выборов без логовых полей, включая нулевые значения.
- `server/http_election_test.go`: оба alias проверены для начального и
  работающего follower, кандидата с одним/двумя голосами, лидера, остановленного
  узла; проверены node/members, точный набор полей полного main и одинаковые
  ответы alias. Через RPC HTTP повторный голос тому же кандидату разрешён,
  другому в этом term отказано. Проверены same-term stepdown с сохранённым
  voted_for, старший term без выдуманного голоса, GET/HEAD с query string,
  405/Allow для других методов и 404 неизвестных путей. Время виртуальное
  (`testing/synctest`), транспорт управляемый, без реальной сети.
- `docs/api.md` и ссылка из `README.md`: локальность, неатомарность опроса
  кластера, точная семантика полей и методов, отсутствие истории голосов,
  порядок выделения lab1 в S1-DEMO. `Kanban.md` обновлён.

Фактическая среда: `go version go1.27.3-X:nodwarf5 linux/amd64`.
`go.mod` не менялся, `.golangci.yml` отсутствует. Стандартный Go-кэш недоступен
для записи в sandbox: первая попытка адресных тестов завершилась setup failed
с `read-only file system`. Все приведённые успешные Go-команды выполнены с
`GOCACHE=/tmp/raftgo-s1-api-go-cache`. В первом адресном прогоне тест отправлял
неверное имя RPC-поля `candidate_id` (400); исправлен на сериализацию штатного
RequestVoteArgs, production RPC не менялся.

```text
$ go test ./server ./raft -run 'TestHTTPElection|TestElectionStatus' -count=1 -v
PASS TestHTTPElectionRolesAndVotes
PASS TestHTTPElectionRouting
ok  raftgo/server 0.006s
PASS TestElectionStatusDuplicateGrantsAndStepdown
PASS TestElectionStatusJSONSchema
ok  raftgo/raft 0.003s
exit=0

$ go test ./server ./raft -run 'TestHTTPElection|TestElectionStatus' -count=100
ok  raftgo/server 0.179s
ok  raftgo/raft   0.117s
exit=0

$ go build ./...
exit=0 (вывод пуст)

$ go test -count=1 ./...
?   raftgo/cmd/raftgo [no test files]
ok  raftgo/kv       0.003s
ok  raftgo/raft     1.299s
ok  raftgo/server   5.785s
ok  raftgo/storage  0.004s
?   raftgo/transport [no test files]
exit=0

$ go test -race -count=1 ./...
?   raftgo/cmd/raftgo [no test files]
ok  raftgo/kv       1.006s
ok  raftgo/raft     6.277s
ok  raftgo/server   6.864s
ok  raftgo/storage  1.012s
?   raftgo/transport [no test files]
exit=0

$ go vet ./...
exit=0 (вывод пуст)

$ gofmt -l .
exit=0 (вывод пуст)

$ git diff --check
exit=0 (вывод пуст)
```

Обязательные проверки доступны и пройдены. Это сдача S1-API в main;
создание lab1, проверка отсутствия будущего кода/API именно в снимке,
Docker-сценарий и ручной UI относятся к S1-DEMO и здесь не выполнялись.
Ветки/checkout не переключались, lab1–lab4, контроллер и его состояние не
изменялись; другие карточки, агенты и push не запускались.

Первая попытка завершения S1-API была заблокирована средой:

```text
$ git add Kanban.md README.md docs/api.md docs/delivery.md raft/inspect.go raft/inspect_test.go server/http_election_test.go
fatal: Unable to create '/home/bogdan/rafting/.git/index.lock': Read-only file system
exit=128
```

В той попытке Git add не выполнен, commit не запускался. HEAD остался на базе
`186206964a7b457f235428f6b7ddf20958ceaea2`; все изменения сохранены в рабочем
дереве main. Тогда статус сдачи — blocked: для локального коммита и чистого
дерева требовался недоступный в той среде доступ на запись к `.git`.
Карточка возвращена из предварительного «Готово» в «В работе».

### Продолжение S1-API после снятия ограничения записи (2026-10-31)

Пользователь повторно поручил ту же карточку с доступом на запись. Проверены
прежняя ветка main и точная база; продолжены только сохранённые выше изменения
S1-API, новых файлов от других задач нет. Git add теперь выполнен успешно;
прежнее препятствие устранено. Код с момента первого прогона не менялся.
Повторены обязательные проверки в новой среде перед завершением сдачи.
Go и `GOCACHE=/tmp/raftgo-s1-api-go-cache` те же; реальные результаты:

```text
$ go build ./...
exit=0 (вывод пуст)

$ go test -count=1 ./...
?   raftgo/cmd/raftgo [no test files]
ok  raftgo/kv       0.002s
ok  raftgo/raft     0.989s
ok  raftgo/server   5.710s
ok  raftgo/storage  0.002s
?   raftgo/transport [no test files]
exit=0

$ go test -race -count=1 ./...
?   raftgo/cmd/raftgo [no test files]
ok  raftgo/kv       1.008s
ok  raftgo/raft     6.316s
ok  raftgo/server   6.754s
ok  raftgo/storage  1.014s
?   raftgo/transport [no test files]
exit=0

$ go vet ./...
exit=0 (вывод пуст; .golangci.yml отсутствует)

$ gofmt -l .
exit=0 (вывод пуст)

$ git diff --check
exit=0 (вывод пуст)

$ go test ./server ./raft -run 'TestHTTPElection|TestElectionStatus' -count=100
ok  raftgo/server 0.109s
ok  raftgo/raft   0.057s
exit=0
```

S1-API готова без оставшихся препятствий; изменения сохраняются одним локальным
коммитом в main. Снимки lab1–lab4 не создавались и не изменялись. Следующая
доступная карточка — S1-UI; другие карточки, агенты и push не запускались.

## S1-UI — экран выборов пяти узлов (2026-10-31)

- Ветка `main`, база `95f1a2bb0ae31031fe5abb67458372a44c23b6dc`,
  существующий чистый checkout `/home/bogdan/rafting`. S1-API готова.
- `server/ui.go`, `server/ui/{index.html,election.css,election.js}` и подключение
  в `server/http.go`: встроенный экран этапа 1 на `GET /`, карточки участников,
  role/term/leader_id/voted_for/votes/quorum/running, обновление и время успеха.
  Пустой голос не выводится из leader_id, votes видны только у Candidate.
  Потерянные ответы сохраняют прежние данные с явной отметкой устаревания;
  отсутствие ответа origin также старит карточки. Только текстовый вывод.
- Same-origin `GET /cluster/status`: фиксированный состав, параллельный опрос,
  общий дедлайн 600 мс и отмена запроса, частичный 200, `atomic:false`, no-store.
  Сам узел читается напрямую; peers — через настроенные `api_url/status`.
  Redirect и proxy окружения запрещены; query и состав из ответов игнорируются.
  Ограничение тела 64 КиБ, проверяется ID участника. HTTP и RPC доступность
  различаются, локальная роль не объявляется доказательством кворума.
- `server/ui_test.go`: маршруты и ресурсы, 404/405, зависший peer, частичный
  результат, время опроса, отказ от redirect/подмены состава, совпадение ID,
  поля только выборов и сохранение HTML-подобных строк как данных.
- `docs/api.md`: контракт агрегата, таймауты/устаревание и инструкция переноса
  election UI в lab1 с заменой проекции на election-only Status. README,
  docs/onboarding.md и docs/stages.md отражают наличие экрана; Kanban обновлён.
  Полные core/API main сохранены. Снимки lab1–lab4 не создавались/не менялись.

Среда: Go `go1.27.3-X:nodwarf5 linux/amd64`, Firefox `158.0.1`, X11.
Go.mod не менялся, `.golangci.yml` отсутствует. Фактический успешный прогон:

```text
$ go test ./server -run 'TestElectionUI|TestClusterStatus' -count=1 -v
PASS TestElectionUIRouting
PASS TestClusterStatusPartialFixedMembership (0.60s)
ok  raftgo/server 0.607s
exit=0

$ go build ./...
exit=0 (вывод пуст)

$ go test -count=1 ./...
?   raftgo/cmd/raftgo [no test files]
ok  raftgo/kv       0.002s
ok  raftgo/raft     0.915s
ok  raftgo/server   6.331s
ok  raftgo/storage  0.002s
?   raftgo/transport [no test files]
exit=0

$ go test -race -count=1 ./...
?   raftgo/cmd/raftgo [no test files]
ok  raftgo/kv       1.006s
ok  raftgo/raft     6.049s
ok  raftgo/server   7.461s
ok  raftgo/storage  1.013s
?   raftgo/transport [no test files]
exit=0

$ go vet ./...
exit=0 (вывод пуст)

$ gofmt -l .
exit=0 (вывод пуст)

$ git diff --check
exit=0 (вывод пуст)
```

Первый адресный прогон обнаружил недостающий ApplyCh в конфигурации теста;
исправлен только тест, после чего выполнены приведённые проверки.

Ручной визуальный прогон в настоящем Firefox через Marionette: открыта страница,
проверены отрисованные карточки и снимки экрана, нажата кнопка «Обновить».
Стенд — пять отдельных Go-процессов, собранных из этого checkout, случайные
локальные API/RPC-порты. Конфиги/бинарник/логи и harness находятся только в
`/tmp/raftgo-s1-ui-*`; копии исходников и checkout не создавались.
Паузы — SIGSTOP/SIGCONT с сохранением PID и RAM, без stop/start.

```text
PASS five cards; leader node5, term 1; origin node1
PASS pause leader node5 -> node4, term 2
PASS no quorum: node2/node3/node5 paused, three stale cards;
     live node1 Candidate term 5, votes 2 / quorum 3; no live Leader
PASS unpause, five fresh cards, all browser requests same origin
PASS origin unavailable: five stale cards, browser request timeout
PASS refresh button; HTML strings displayed literally, no DOM injection
PASS narrow viewport: no horizontal overflow
PASS cleanup: all five test node PIDs exited and reaped
```

Проверка HTML использовала временную подмену fetch в тестовой вкладке: ID с
`<img onerror=...>`, leader_id с `<b>` и voted_for с `<script>` видны буквально,
внутри карточек не появились img/b/script, JS-маркер не сработал. После проверки
fetch восстановлен, страница перезагружена. В Resource Timing все URL одного
origin; CORS-заголовки и прямой браузерный доступ к peer-портам не требовались.
Визуально просмотрены desktop, потеря кворума, HTML-строки и узкий экран;
снимки `/tmp/raftgo-s1-ui-{normal,no-quorum,text,mobile}.png`.
Тестовый Firefox закрыт. Чужие процессы/стенды не изменялись.

Все критерии S1-UI выполнены, карточка готова. Следующий доступный ID —
S1-DEMO; он не запускался. Приёмка/выделение lab1 остаются отдельным поручением.
Ветки не переключались, другие агенты/карточки и push не запускались,
контроллер и его состояние не изменялись. Препятствий к сдаче S1-UI нет.

## S1-DEMO — приёмка выборов и lab1 (2026-10-31)

- Исходный чистый `main`: `06462d73d8a2a8f589ff0c38107c27cb1528dd6a`.
  Зависимости S1-CORE/S1-API/S1-UI готовы. Статус «В работе» сохранён локальным
  коммитом `a8fe349`; это точная база `snapshot/lab1`, созданной в том же
  checkout `/home/bogdan/rafting`. Другие checkout/ветки разработки не создавались.
- Кандидат и принятый `lab1`: `a2f486172f435f2e469802935ecec43048abee69`.
  Ядро `raft/`, `storage/`, `cmd/raftgo/` и election-only HTTP взяты из S1-CORE
  `e2b144b` с выбранными файлами. Перенесены UI и тесты диагностики из main,
  Status содержит ровно восемь полей. Внедрение Clock/Transport и safety-тесты
  сохранены; HTTP-тесты ролей/голосов адаптированы без ApplyCh/логовой проекции.
- Файлы снимка: `raft/` (выборы/heartbeat, без пользовательского лога,
  репликации, no-op, commit/apply и ReadIndex), `storage/memory{,_test}.go`
  (только term/голос), `server/http{,_test,_election_test}.go`,
  `server/ui{,_test}.go`, `cmd/raftgo/main.go`, `config/cluster.json`,
  `scenarios/{cluster.json,run.py,README.md,task1/README.md}`, README, AGENTS,
  `docs/api.md`, `.gitignore`. Удалены kv, будущие server-файлы/тесты,
  task2–4, внешний dashboard, autokanban/tools, доска и документы старого
  процесса. `docs/stages.md` сохранён как контракт. Личные .obsidian и кэши
  исключены из принятого дерева; локальные файлы пользователя сохранены.
- В обоих Docker-конфигах снимка `api_url` заменены на внутренние
  `http://nodeN:8080`: серверный агрегатор должен обращаться к контейнерам,
  а не к loopback опубликованных портов хоста. В task1 добавлена общая
  pause/unpause с проверкой container ID/PID/StartedAt, неубывания term
  и сохранения voted_for в том же term. Лист ручного показа дополнен.
- Main сохранил весь код лога/KV/CAS/lock и полный набор тестов. В main
  изменены только Kanban, README, docs/tasks и этот журнал; изменения
  снимка обратно не мержились. lab2–4 и исторические ветки не перемещались.

Среда: Go `go1.27.3-X:nodwarf5 linux/amd64`, Docker `29.9.2`, Compose `5.6.1`,
настоящий Firefox `158.0.1` на X11. Go.mod не менялся; `.golangci.yml` нет.
Обязательные проверки выполнены на точном SHA кандидата; реальные результаты:

```text
$ go build ./...
exit=0 (вывод пуст)
$ go test -count=1 ./...
?    raftgo/cmd/raftgo [no test files]
ok   raftgo/raft      0.220s
ok   raftgo/server    0.607s
ok   raftgo/storage   0.002s
?    raftgo/transport [no test files]
exit=0
$ go test -race -count=1 ./...
?    raftgo/cmd/raftgo [no test files]
ok   raftgo/raft      2.060s
ok   raftgo/server    1.627s
ok   raftgo/storage   1.011s
?    raftgo/transport [no test files]
exit=0
$ go vet ./...
exit=0 (вывод пуст)
$ gofmt -l .
exit=0 (вывод пуст)
$ git diff --check
exit=0 (вывод пуст)
$ go test ./server ./raft -run 'TestHTTPElection|TestElectionStatus' -count=100
ok   raftgo/server 0.095s
ok   raftgo/raft   0.041s
exit=0
```

Docker собран из кандидата и проверен отдельно от существующего проекта rafting:

```text
$ docker compose -p raftgo-scenarios -f scenarios/compose.yaml up -d --build
exit=0 (пять контейнеров)
$ python3 scenarios/run.py all
PASS task1
exit=0
PASS all five nodes: eight election fields, future routes 404 for all tested methods
PASS tracked snapshot tree: future packages, scenarios and controller absent
PASS runner finally cleanup after injected exception: no paused containers
```

Task1 проверил согласие term/лидера, pause лидера → новый term, возврат Follower,
пять секунд без лидера в меньшинстве, общую паузу с теми же процессами и RAM.
На всех пяти узлах проверены точные поля обоих alias и 404 на GET/POST/PUT/DELETE
для /entries, /log, /kv/x, /kv/x/cas, /kv/x/lock. Просмотрены исходники и дерево:
нет реализации будущих функций, флагов их скрытия и будущих сценариев.
Остались только стандартные нулевые LastLogIndex/LastLogTerm в RequestVote;
heartbeat не содержит записей. Проверки отсутствия маршрутов не удалялись.
Cleanup дополнительно проверен через намеренное исключение после pause node5:
runner напечатал диагностику, пробросил исключение и снял паузу в finally.

Ручная визуальная проверка через Marionette в отдельном настоящем Firefox
на Docker-кандидате (изображения просмотрены агентом):

```text
PASS five fresh cards, leader node1
PASS leader failover node1 -> node4
PASS old leader rejoins as follower
PASS minority: three stale cards, no live leader
PASS refresh button; all browser requests same origin
PASS origin unavailable: five stale cards
```

В меньшинстве node2/node5 — Candidate, term 12, у node5 votes=2, quorum=3;
старые данные трёх паузных узлов помечены недоступными/устаревшими.
После восстановления снова пять свежих карточек. Неназначенный голос node1
отображается «не отдан», даже когда известен лидер node2. Снимки экрана и логи:
`/tmp/raftgo-s1-demo-{normal,failover,minority,origin}.png`,
`/tmp/raftgo-s1-demo-{go,build,task1,browser}.log` (вне репозитория).
Первый браузерный harness прервался на `compose unpause` всех сервисов:
Docker отказал для уже работающего контейнера. Harness исправлен на выбор
только паузных сервисов через `ps --status paused`; повторный прогон успешен.
Production-код/кандидат при исправлении harness не менялись.

После проверок сняты все паузы; `compose down` удалил только пять тестовых
контейнеров и сеть. Тестовый Firefox закрыт. Существующий проект `rafting`
с пятью контейнерами не изменялся. После успешных проверок создан lab1 на
точном SHA, checkout возвращён в main. Временная snapshot/lab1 сохранена для
аудита. Push, другие карточки и агенты не запускались. Ограничений приёмки нет;
это тестовая проверка свойств, а не формальное доказательство всех исполнений.
Следующий доступный ID — S2-CORE, его выполнение не запускалось.

После возврата в main повторён полный обязательный набор (код main не менялся):

```text
$ go build ./...
exit=0
$ go test -count=1 ./...
?   	raftgo/cmd/raftgo	[no test files]
ok  	raftgo/kv	0.001s
ok  	raftgo/raft	0.937s
ok  	raftgo/server	6.419s
ok  	raftgo/storage	0.002s
?   	raftgo/transport	[no test files]
exit=0
$ go test -race -count=1 ./...
?   	raftgo/cmd/raftgo	[no test files]
ok  	raftgo/kv	1.009s
ok  	raftgo/raft	6.392s
ok  	raftgo/server	7.478s
ok  	raftgo/storage	1.014s
?   	raftgo/transport	[no test files]
exit=0
$ go vet ./...
exit=0
$ gofmt -l .
exit=0
$ git diff --check
exit=0
```

Все команды завершились с exit=0; gofmt и diff --check без вывода.
Доска отмечена «Готово», изменения отчёта сохранены локальным коммитом main.

## S2-CORE — проверка строкового Raft-лога (2026-10-31)

Ветка `main`, исходная база `6c132ef5c1f40976d2774377d9060876e84fabe8`,
существующий чистый checkout `/home/bogdan/rafting`. Зависимость S1-DEMO готова.
Карточка переведена в «В работе» до изменений, затем в «Готово» после проверок.

Существующая реализация прошла проверку без изменений production-кода:
новый лидер сохраняет no-op, commit продвигается большинством только через
запись текущего term; apply доставляет подтверждённый префикс по порядку,
включая no-op. Конфликт заменяет только неподтверждённый суффикс, старые RPC
не усекают уже совпавший лог. Ответ записи ждёт apply собственной пары
index/term; commit без consumer ещё не означает успех, замена позиции другим
term даёт конфликт. Потерянный ответ RPC не считается подтверждением;
повторная доставка той же записи не добавляет дубль. KV main сохранён.

Изменённые файлы:

- `raft/replication_cluster_test.go`: State Machine Safety и Leader Completeness
  теперь сравнивают term/kind/command всех применённых записей, включая no-op,
  вместо одних байтов пользовательской команды. Во всех кластерных сценариях
  сохранены Election Safety и Log Matching. BasicAgreement проверяет пустую
  строку, Unicode, две одинаковые строки на разных индексах и непрозрачность
  текста SET/внутреннего префикса для самого Raft (HTTP по-прежнему запрещает
  зарезервированный префикс).
- `raft/replication_loss_test.go`: адресная потеря ответа после сохранения на
  follower, отсутствие преждевременного commit, повтор RPC без новой записи
  в Storage, commit через повторное подтверждение и задержанный дубликат
  после объявления commit. Используются существующие fake Storage/Transport.
- `server/string_apply_test.go`: реальный Raft и Memory с fake-транспортом
  и виртуальным временем synctest. Отложенный запуск Consume доказывает
  ожидание apply; изоляция лидера и запись нового term на той же позиции
  доказывают 409 после применения замены вместо ложного 201.
- `Kanban.md`, `docs/delivery.md`: статус и этот журнал.

Среда: `go version go1.27.3-X:nodwarf5 linux/amd64`; go.mod не менялся,
`.golangci.yml` отсутствует, использован vet. Реальные результаты:

```text
$ go build ./...
exit=0 (вывод пуст)

$ go test -count=1 ./...
?   raftgo/cmd/raftgo [no test files]
ok  raftgo/kv       0.005s
ok  raftgo/raft     0.998s
ok  raftgo/server   6.417s
ok  raftgo/storage  0.003s
?   raftgo/transport [no test files]
exit=0

$ go test -race -count=1 ./...
?   raftgo/cmd/raftgo [no test files]
ok  raftgo/kv       1.012s
ok  raftgo/raft     6.203s
ok  raftgo/server   7.387s
ok  raftgo/storage  1.013s
?   raftgo/transport [no test files]
exit=0

$ go vet ./...
exit=0 (вывод пуст)

$ gofmt -l .
exit=0 (вывод пуст)

$ git diff --check
exit=0 (вывод пуст)

$ go test ./raft ./server -run 'TestSendAppendEntries_LostReply|TestStringWriteWaits' -count=100
ok  raftgo/raft   0.075s
ok  raftgo/server 0.046s
exit=0
```

Дополнительно выполнен адресный прогон критериев карточки:
`go test ./raft -run 'TestCluster_|TestHandleAppendEntries_|TestSendAppendEntries_|TestAdvanceCommit_' -count=1` — PASS, exit=0.
Он включает BasicAgreement, no-op старого term, запрет commit без большинства,
замену неподтверждённого хвоста изолированного лидера, ремонт конфликтных term,
старые запросы/ответы, защиту committed-записи, выборы и возврат узлов.
RandomFaultsKeepSafety прошёл 20 seed, RandomDelaysKeepSafety — 100 seed
(с проверкой фактической поздней доставки), LeaderChurnKeepsSafety — 10 seed.
Это проверки конкретных исполнений с потерями/задержками/переупорядочиванием,
а не формальное доказательство протокола.

S2-CORE готова, обязательные проверки доступны и пройдены. Снимок lab2
выделяется только в S2-DEMO: из него нужно удалить KV-кодирование и ReadIndex,
оставив строковый лог, no-op, commit/apply и consumer. В рамках этой карточки
снимки lab1–lab4, контроллер и его состояние не менялись, checkout и ветка
не переключались, push и другие агенты не запускались. Docker и ручной UI
относятся к приёмке этапа S2-DEMO и здесь не запускались. Следующий доступный
ID — S2-API; он не выполнялся. Препятствий к сдаче S2-CORE нет.

## S2-API — add и лог на эндпоинте состояния (2026-10-31)

Ветка `main`, база `afa3f9d99c669c7a55e5a0395b6da80ffd510083`, checkout
`/home/bogdan/rafting`. Зависимость S2-CORE готова. До изменений карточка
перенесена в «В работе», после всех проверок — в «Готово».

Поведение и файлы:

- `server/http.go`: `/status` и `/election` возвращают node/members,
  consistency=local и entries; node и entries формируются одним InspectLog.
  `/log` сохраняет прежние поля без members. Видны committed, хвост, no-op
  и пустая пользовательская строка. До JSON-декодирования `/entries`
  отклоняет неверный UTF-8: encoding/json иначе заменял бы байты на U+FFFD.
  Сохранены лимит 65536 декодированных байт, внутренний KV-префикс, ожидание
  apply своей пары index/term и отсутствие повторного Start при ошибке.
- `server/ui.go`: необходимая совместимость существующего экрана выборов с
  выросшим `/status`. Node сериализуется перед логом; агрегат читает только
  JSON-префикс до конца node, максимум 64 КиБ, с прежними таймаутами и
  фиксированными адресами. Неиспользованный остаток с логом не валидируется.
  Это сохраняет ограничение памяти опроса и не помечает узел недоступным
  из-за допустимой команды размером 64 КиБ. Панель add/log не добавлялась.
- `server/http_entries_test.go`: реальные Raft/Memory, fake-транспорт и
  виртуальное время. Проверены пустая строка, Unicode и экранирование,
  одинаковые строки на разных индексах, ASCII/Unicode ровно 65536 байт,
  максимально экранированная строка, превышение на байт, неверный UTF-8,
  JSON/типы/null/лишние поля/два объекта/слишком большое тело и KV-префикс.
  Проверены 307 без пересылки, 503 без известного лидера, 504 с index/term,
  одна запись после таймаута и её последующий commit без нового POST,
  локальный хвост и no-op. Через httptest проверен агрегат с реальным
  сериализованным статусом больше 64 КиБ и валидация ограниченного префикса.
- `server/http_election_test.go`: полный envelope, local consistency,
  entries=[] для пустого лога и согласованность last_log_index/entries
  при всех проверяемых ролях. Alias и семантика голосов сохранены.
- `server/string_apply_test.go`: существующие проверки ожидания consumer
  и 409 при применении другого term на том же индексе теперь вызывают
  публичный HTTP Handler, а не метод appendEntry напрямую.
- `README.md`, `docs/api.md`: обновлены контракт снимка, лимиты, ошибки,
  применение index/term, неизвестный исход и правила опроса агрегатом.
- `Kanban.md`, `docs/delivery.md`: статус и этот журнал.

Среда: `go version go1.27.3-X:nodwarf5 linux/amd64`. Версия go.mod сохранена;
`.golangci.yml` отсутствует, использован go vet. Финальные реальные результаты:

```text
$ go build ./...
exit=0 (вывод пуст)

$ go test -count=1 ./...
?   raftgo/cmd/raftgo [no test files]
ok  raftgo/kv       0.007s
ok  raftgo/raft     1.162s
ok  raftgo/server   6.427s
ok  raftgo/storage  0.003s
?   raftgo/transport [no test files]
exit=0

$ go test -race -count=1 ./...
?   raftgo/cmd/raftgo [no test files]
ok  raftgo/kv       1.009s
ok  raftgo/raft     6.425s
ok  raftgo/server   7.531s
ok  raftgo/storage  1.014s
?   raftgo/transport [no test files]
exit=0

$ go vet ./...
exit=0 (вывод пуст)

$ gofmt -l .
exit=0 (вывод пуст)

$ git diff --check
exit=0 (вывод пуст)

$ go test ./server -run 'TestHTTPEntries|TestHTTPLogSnapshot|TestClusterStatusWithLarge|TestElectionNode|TestStringWriteWaits|TestHTTPElection' -count=100
ok  raftgo/server 1.647s
exit=0
```

S2-API готова, препятствий нет. Полный KV/CAS/lock main сохранён; код raft
не менялся. Docker и ручной браузер не запускались: это API-карточка,
приёмка этапа выполняется в S2-DEMO, ручная форма — в S2-UI. Снимки lab1–lab4,
контроллер и его состояние не менялись. Ветки, checkout и история не
переключались и не переписывались; другие агенты и push не запускались.
Следующий доступный ID — S2-UI; он не выполнялся.

## S2-UI — добавление строки и наблюдение реплик (2026-10-31)

Ветка `main`, база `5705f58243c6be46c466aaf45cb5dadc4615c4fc`, чистый
существующий checkout `/home/bogdan/rafting`; зависимость S2-API готова.
Карточка переведена «В работе», после нижеследующих проверок — «Готово».

Изменённые файлы и поведение:

- `server/ui/{index.html,election.js,election.css}`: экран этапа 2 сохраняет
  диагностику выборов и добавляет выбор реплики, локальный лог и форму строки.
  Index/term, committed/неподтверждённый хвост и служебный no-op различимы.
  Пустая команда видна как `""`; HTML и Unicode отображаются текстом.
  Успех показывается только после 201 с index/term; 504 и потеря ответа явно
  означают неизвестный исход. Автоповтора нет, двойная отправка во время
  ожидания заблокирована, строка сохраняется. Лог имеет отдельный опрос,
  время последнего успеха и отметку устаревания; снимки локальны.
- `server/ui_log.go`, подключение в `server/http.go`: GET /cluster/log/{id}
  и POST /cluster/entries/{id} только для фиксированного состава Handler.
  Чтение имеет дедлайн 600 мс, запись — 8 секунд, без proxy окружения и
  переноса cookies/произвольных заголовков/путей. Только явный 307 на точный
  настроенный api_url/entries разрешает пересылку; циклы запрещены, после
  503/504/сетевой ошибки повтора нет. Исходные /entries и 307 не менялись.
- `server/ui_log_test.go`: фиксированные адреса при мутации исходного состава,
  query/посторонний redirect/цикл, методы и неизвестный ID, локальный и
  удалённый лог, сохранение 504 index/term, потерянный ответ без повтора.
  `server/ui_test.go`: номер этапа встроенного экрана.
- `README.md`, `docs/api.md`: форма, маршруты, таймауты и неизвестный исход;
  `Kanban.md`, `docs/delivery.md`: статус и этот журнал.

Go `go1.27.3-X:nodwarf5 linux/amd64`; go.mod сохранён, `.golangci.yml`
отсутствует. Реальный успешный вывод обязательного прогона:

```text
$ go build ./...
exit=0 (вывод пуст)

$ go test -count=1 ./...
?   raftgo/cmd/raftgo [no test files]
ok  raftgo/kv       0.002s
ok  raftgo/raft     0.903s
ok  raftgo/server   6.403s
ok  raftgo/storage  0.002s
?   raftgo/transport [no test files]
exit=0

$ go test -race -count=1 ./...
?   raftgo/cmd/raftgo [no test files]
ok  raftgo/kv       1.006s
ok  raftgo/raft     6.255s
ok  raftgo/server   7.461s
ok  raftgo/storage  1.016s
?   raftgo/transport [no test files]
exit=0

$ go vet ./...
exit=0 (вывод пуст)

$ gofmt -l .
exit=0 (вывод пуст)

$ git diff --check
exit=0 (вывод пуст)

$ go test ./server -run 'TestLogUI|TestElectionUI' -count=1 -v
PASS TestLogUIFixedForwarding
PASS TestLogUILostWriteResponseIsNotRetried
PASS TestElectionUIRouting
ok  raftgo/server 0.005s
exit=0

$ go test ./server -run 'TestLogUI' -count=100
ok  raftgo/server 0.141s
exit=0
```

Ручной браузерный прогон: настоящий Firefox 158 на X11, Marionette,
пять отдельных процессов из `/tmp/raftgo-s2-ui-node`, собранного из этого
checkout. Случайные localhost API/RPC-порты, конфиги и логи в
`/tmp/raftgo-s2-ui-live-*`, harness `/tmp/raftgo-s2-ui-browser.py`.
Отказы — SIGSTOP/SIGCONT, PID и память сохранялись. Визуально просмотрены
снимки normal/tail/no-quorum/mobile в `/tmp/raftgo-s2-ui-*.png`.
Реальные результаты `/tmp/raftgo-s2-ui-browser.log` (exit=0):

```text
PASS follower origin: repeated/empty/HTML/Unicode strings; five replicas; 201 index/term; no-op distinct; HTML literal
PASS all five replica logs selectable via same origin
PASS pause leader node2 -> node4; write through follower succeeds
PASS no majority: real 504 index/term, unknown outcome; visible uncommitted tail; one POST, no retry; no live leader
PASS no leader: 503 shown without retry
PASS unpause/recovery; all browser requests same origin; narrow screen without page overflow
PASS cleanup: test browser closed; five processes resumed, exited and reaped
```

Для настоящего 504 три follower были приостановлены, запись принята лидером
до check-quorum и осталась неподтверждённой. В браузере виден её хвост, затем
HTTP 504 index=9/term=2, явное сообщение неизвестного исхода; счётчик fetch
подтвердил ровно один POST, включая последующие опросы. После unpause
кластер снова работал. Повторяющиеся, пустые, HTML и Unicode строки добавлялись
через follower и проверены на всех пяти репликах; DOM не содержал внедрённых
img/script, JS-маркер не исполнялся. Все browser resource URL одного origin.

Все критерии S2-UI выполнены; ограничений нет. Docker-сценарии и выделение
lab2 относятся к следующей S2-DEMO и здесь не запускались. Полный core/KV/CAS/
lock main сохранён, lab1–lab4, контроллер и его состояние не изменялись.
Другие карточки, агенты, ветки/worktree/копии, переключение checkout и push
не запускались. Следующий доступный ID — S2-DEMO.

## S2-DEMO — подготовка приёмки строкового лога (2026-10-31)

Ветка `main`, исходная база `9f87844fecf2819cdec8538e7a85f943c94f1744`,
чистый checkout `/home/bogdan/rafting`; зависимость S2-UI готова.
S2-DEMO переведена «В работе». `lab1` не меняется.

В main добавлен лист показа `docs/demo2.md`, шаг 2.5 в `scenarios/run.py`
и описание в `scenarios/task2/README.md`. Он проверяет замену неподтверждённого
хвоста при смене лидера, сохранение подтверждённого префикса и отсутствие 201
у старого запроса. Три приостановленные реплики временно отключаются от своей
Docker-сети до предложения хвоста, затем подключаются с исходными IP/алиасами;
finally восстанавливает сеть и снимает паузы, процессы и RAM сохраняются.

Первый вариант только с pause/unpause не создавал требуемого конфликта:
буферизованные TCP RPC доставляли хвост после unpause, и он законно сохранялся
новым лидером (старый запрос 201). После добавления сетевой изоляции реальный
Docker task2 прошёл: старый запрос 409, конфликтный хвост удалён на пяти
репликах, подтверждённый префикс сохранён. task1 также PASS. Чужой проект
`rafting` не затронут. Логи `/tmp/raftgo-s2-demo-main-{build,task1,task2}.log`.

Обязательный Go-набор main: build, test -count=1, race -count=1, vet — exit=0;
gofmt -l . — вывод пуст. Go 1.27.3, go.mod 1.27.1 сохранён, lint-конфига нет.
Фактические test: kv 0.001s, raft 1.023s, server 6.357s, storage 0.003s;
race: kv 1.007s, raft 8.216s, server 7.525s, storage 1.017s.
Python py_compile и git diff --check — exit=0. Приёмка снимка продолжается;
lab2 ещё не создана. Итоговые SHA и проверки будут записаны после DEMO.

Первый кандидат `snapshot/lab2` на `00c25c761703dd210325b7e349d5fb27b16309c3`
прошёл Go-набор и Docker task1–2, но не принят: настоящий браузер обнаружил,
что Docker UI пытается опрашивать/пересылать на host localhost из контейнера.
Стенд остановлен, checkout возвращён в main для исправления.

В main исправлена адресация: необязательный `Member.InternalAPIURL` /
`internal_api_url` для серверных запросов UI, fallback на APIURL; APIURL и
HTTP 307 остаются публичными. Оба Docker-конфига задают `http://nodeN:8080`
внутри, прежние публичные адреса сохранены. Агрегат и log UI используют
внутренний адрес; разрешённый публичный Location точно сопоставляется с
настроенным внутренним адресом без произвольного proxy. Проверки валидности
URL, неизменяемого состава, отдельной внешней/внутренней адресации,
запрета постороннего Location, циклов и потери ответа сохранены/расширены.
Изменены server/config.go, config_test.go, ui.go, ui_log.go, ui_log_test.go,
ui_test.go, config/cluster.json, scenarios/cluster.json, docs/api.md и demo2.md.
