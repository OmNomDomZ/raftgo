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
Ни один labN пока не принят; не создавать их заранее из полного main.

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

Пока заполнен только источник; ни один новый этап не принят.

| Результат | Принятый SHA | Go/race | Docker | UI | Статус |
| --- | --- | --- | --- | --- | --- |
| Полный исходник | `70782f23fe695d184981dff932c47181f9eadd21` | build/test/race/vet/gofmt пройдены при планировании | Не запускался | Отсутствует | Источник |
| prepare/in-memory | `4f6499180cb7c22d429507506431218a445be18b` | build/test/race/vet/gofmt PASS | task1–4, cleanup и полный сброс PASS | Не требуется | B0–B2 готовы |
| lab1 | — | — | — | — | Не начат |
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
