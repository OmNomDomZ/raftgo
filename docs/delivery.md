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
`lab2` принят в S2-DEMO на `0006d8422076f19336ad9ab8cdb5a9431fc2d4c6`.
`lab3` принят в S3-DEMO на `d93c829d8de20cfccf92ee06cf0b577b66b46010`.
`lab4` принят в S4-DEMO на `a4b1f2e8eebb39fe9e0cdff8b19e2dea094f30e1`.

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

Все четыре этапа приняты; повторная проверка снимков записана в S4-DEMO ниже.

| Результат | Принятый SHA | Go/race | Docker | UI | Статус |
| --- | --- | --- | --- | --- | --- |
| Полный исходник | `70782f23fe695d184981dff932c47181f9eadd21` | build/test/race/vet/gofmt пройдены при планировании | Не запускался | Отсутствует | Источник |
| prepare/in-memory | `4f6499180cb7c22d429507506431218a445be18b` | build/test/race/vet/gofmt PASS | task1–4, cleanup и полный сброс PASS | Не требуется | B0–B2 готовы |
| lab1 | `a2f486172f435f2e469802935ecec43048abee69` | build/test/race/vet/gofmt PASS | task1 и cleanup PASS | Firefox PASS | S1-DEMO готова |
| lab2 | `0006d8422076f19336ad9ab8cdb5a9431fc2d4c6` | build/test/race/vet/gofmt PASS | task1–2, конфликт хвоста и cleanup PASS | Firefox PASS | S2-DEMO готова |
| lab3 | `d93c829d8de20cfccf92ee06cf0b577b66b46010` | build/test/race/vet/gofmt PASS | task1–3 и cleanup PASS | Firefox PASS | S3-DEMO готова |
| lab4 | `a4b1f2e8eebb39fe9e0cdff8b19e2dea094f30e1` | build/test/race/vet/gofmt PASS | task1–4 и cleanup PASS | Firefox PASS | S4-DEMO готова |

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

### S2-DEMO — итоговая приёмка lab2

- Ветка разработки `main`: начальная база `9f87844fecf2819cdec8538e7a85f943c94f1744`,
  подготовка сценария `90a0c42811a4daf7dc813eb1065a43c6790d0757`, исправление
  Docker-адресации `c0c3138452e599c5980be8fe8f2040f932119b11`.
- Кандидат создан в том же checkout как `snapshot/lab2` от `90a0c42`;
  после неудачной браузерной проверки первого кандидата исправление сделано
  в main на `c0c3138` и включено merge-коммитом в кандидата. Никаких копий
  checkout, worktree или task-веток. Проверенный и принятый SHA lab2:
  **`0006d8422076f19336ad9ab8cdb5a9431fc2d4c6`**. Именно он проверен Go,
  Docker, браузером и инспекцией исходников. Временная ветка сохранена.
- `lab1` осталась на `a2f486172f435f2e469802935ecec43048abee69`.
  `lab3`/`lab4` не создавались. Полный код main сохранён; снимок в main не мержился.

Состав снимка (diff относительно main `c0c3138`):

- Удалены kv/, server/kv.go, replay KV-тест, KV-маршруты, состояние Store/results,
  декодирование команд и зарезервированный префикс. Список хранит произвольные
  строки, включая пустые/повторяющиеся, без интерпретации.
- Удалены raft/read.go/read_test.go, pendingRead/readRound/ackedRound,
  разрешение/отмена чтений и параметры раундов в репликации. Тесты вызовов
  репликации адаптированы к сокращённой сигнатуре, safety-проверки сохранены.
- Сохранены no-op/commit/apply, выборы, голосование по актуальности лога,
  конфликты и безопасная замена суффикса, ограничение commit текущим term,
  Election Safety, Log Matching, Leader Completeness и State Machine Safety.
  Соответствующие core-тесты остались и прошли, включая потери сообщений,
  churn лидеров, replay с тем же Memory и отсутствие commit без большинства.
- Удалены scenarios/task3–4, dashboard старого полного прототипа, autokanban,
  tools/autokanban, Kanban и документация очереди/передачи/полного onboarding.
  README, AGENTS, docs/api.md, stages.md, demo2.md и scenarios/README.md
  описывают только доступный этап. UI этапа 2 сохранён полностью.
- Личные .obsidian не включены в Git-снимок и не удалялись у пользователя.
  Игнорируемый Python cache контроллера временно сохранён вне checkout и
  возвращён в main; содержимое контроллера не менялось.

Фактический Go-прогон на точном SHA `0006d84` (Go 1.27.3; go.mod 1.27.1,
конфига golangci нет):

```text
$ go build ./...
exit=0 (вывод пуст)
$ go test -count=1 ./...
?   raftgo/cmd/raftgo [no test files]
ok  raftgo/raft      1.010s
ok  raftgo/server    6.309s
ok  raftgo/storage   0.003s
?   raftgo/transport [no test files]
exit=0
$ go test -race -count=1 ./...
?   raftgo/cmd/raftgo [no test files]
ok  raftgo/raft      6.786s
ok  raftgo/server    7.477s
ok  raftgo/storage   1.016s
?   raftgo/transport [no test files]
exit=0
$ go vet ./...
exit=0 (вывод пуст)
$ gofmt -l .
exit=0 (вывод пуст)
$ git diff --check
exit=0 (вывод пуст)
```

Docker собран из этого SHA, отдельный проект `raftgo-scenarios`:

```text
$ docker compose -p raftgo-scenarios -f scenarios/compose.yaml up -d --build
exit=0 (пять контейнеров)
$ python3 scenarios/run.py all
PASS task1
PASS task2
exit=0
```

Task2.5: старый запрос 409, неподтверждённый хвост заменён записью нового term
на всех пяти репликах, подтверждённый префикс сохранён. Ноды не перезапускались.
После браузера выполнен отдельный последовательный повтор `all`: оба PASS,
снова старый запрос 409, подтверждённый префикс до index=32 одинаков на пяти нодах.
Логи `/tmp/raftgo-s2-demo-{build,scenarios,final-scenarios}.log`.

Дополнительная проверка контракта `/tmp/raftgo-s2-demo-contract.log`:

```text
PASS tracked tree and source: future packages, API, decoder, rounds, tests, scenarios and controller absent
PASS node1..node5: local status/log schema; seven future API methods return 404
PASS candidate contract
```

Проверены `/status`, `/election`, `/log` всех узлов: consistency=local,
число записей равно last_log_index, commit_index не выше длины лога.
GET/POST/PUT/DELETE /kv/x, POST /kv/x/cas, POST/DELETE /kv/x/lock дают 404.
Проверены Git-дерево и исходники: будущих реализаций, полей, декодеров,
ReadIndex-раундов и скрывающих флагов нет.

Реальный Firefox 158, X11/Marionette, Docker на том же SHA. После исправления
конфига выполнен отдельный браузерный прогон без параллельного управления
сценариями; лог `/tmp/raftgo-s2-demo-browser.log`, exit=0:

```text
PASS follower origin: repeated/empty/HTML/Unicode strings; five replicas; 201 index/term; no-op distinct; HTML literal
PASS all five replica logs selectable via same origin
PASS pause leader node1 -> node5; write through follower succeeds
PASS no majority: real 504 index/term, unknown outcome; visible uncommitted tail; one POST, no retry; no live leader
PASS no leader: 503 shown without retry
PASS unpause/recovery; all browser requests same origin; narrow screen without page overflow
PASS cleanup: test browser closed; five Docker nodes resumed
```

Просмотрены изображения `/tmp/raftgo-s2-demo-{normal,tail,no-quorum,mobile}.png`:
no-op и пустая строка различимы, HTML текстовый, неподтверждённый хвост выделен,
504 index=20/term=15 явно помечен неизвестным исходом, три недоступные карточки
устарели, данные восстановились после unpause. Горизонтального переполнения
страницы на узком экране нет; таблица имеет собственную прокрутку.
Первый запуск harness после исправления начался до конца runner и получил 503
до управления отказами; итоговый отдельный запуск выше и последующий повтор
runner завершились успешно. Продуктовый код после SHA `0006d84` не менялся.

Cleanup проверен искусственным RuntimeError внутри task1 после pause и
отсоединения сети node5: runner вывел диагностику, пробросил исходную ошибку,
finally восстановил прежние IP/алиасы и снял паузу. Container ID и PID прежние,
RAM не терялась, кластер снова согласен по лидеру. Лог:
`/tmp/raftgo-s2-demo-cleanup.log`, exit=0 у проверяющего harness.

После проверок свой стенд целиком удалён через compose down; Firefox закрыт.
Существующий проект `rafting` с пятью контейнерами не перезапускался и не менялся.
Только после успеха создана lab2 на проверенном SHA, checkout возвращён в main.
S2-DEMO готова, канбан и реестр обновлены. Ограничений приёмки нет.
Push и другие карточки/агенты не запускались. Следующий доступный ID — S3-CORE.

После возврата в main выполнен полный обязательный набор на коде `c0c3138`
с итоговыми изменениями канбана/отчёта:

```text
$ go build ./...
exit=0 (вывод пуст)
$ go test -count=1 ./...
?   raftgo/cmd/raftgo [no test files]
ok  raftgo/kv        0.002s
ok  raftgo/raft      0.946s
ok  raftgo/server    6.334s
ok  raftgo/storage   0.002s
?   raftgo/transport [no test files]
exit=0
$ go test -race -count=1 ./...
?   raftgo/cmd/raftgo [no test files]
ok  raftgo/kv        1.006s
ok  raftgo/raft      6.437s
ok  raftgo/server    7.484s
ok  raftgo/storage   1.016s
?   raftgo/transport [no test files]
exit=0
$ go vet ./...
exit=0 (вывод пуст)
$ gofmt -l .
exit=0 (вывод пуст)
$ git diff --check
exit=0 (вывод пуст)
```

## S3-CORE — KV CRUD и безопасные чтения (2026-10-31)

Ветка `main`, база `5512ac12008bdba454f772ac0ae650a9478d3286`, checkout
`/home/bogdan/rafting` перед началом чистый. Зависимость S2-DEMO готова.
Проверена существующая реализация: `kv.Store` детерминированно применяет
string-string CRUD, consumer меняет KV только по ApplyCh, GET подтверждает
ReadIndex и ждёт применения барьера. Рабочий код исправлений не потребовал;
CAS/lock и их тесты полной реализации main сохранены.

Изменены `server/kv_safety_test.go`, `Kanban.md`, `docs/delivery.md`.
Добавлены проверки с fake-транспортом и виртуальным временем synctest:

- Реальный Raft-applier коммитит PUT, но consumer задержан: GET не отвечает
  до применения и затем возвращает подтверждённое значение.
- Изолированный лидер с уже существующим значением не отвечает успешным GET.
  Проверены отмена ожидающего чтения, старший term и отсутствие кворума
  до таймаута запроса. При старшем term ожидающий ReadIndex отказывается
  с 503: уведомление об утрате лидерства предшествует установке нового hint.
- 24 конкурентных клиента выполняют PUT/GET своих ключей: чтение каждого
  клиента обязано видеть его завершённую запись при конкурирующих операциях.
- Два последовательных экземпляра Raft используют один объект Memory Storage
  и новые KV/consumer. До подтверждения commit index replay отсутствует;
  после AppendEntries применяются только шесть подтверждённых записей,
  седьмая сохранённая запись не меняет KV. Совпадают все результаты replay:
  create пустой строки, конфликт create, Unicode put, delete, отсутствующий
  delete и upsert JSON-подобной строки.
- `/entries` отвергает внутренний префикс, включая JSON Unicode-экранирование,
  без добавления в лог. Обычная JSON-подобная строка реплицируется как непрозрачная
  команда и не меняет KV.

Существующие `raft/read_test.go` дополнительно проверяют ответы на запросы,
отправленные до ReadIndex, commit собственного term, барьер no-op нового
лидера, смену лидера, потерю кворума, отмену и изоляцию прежнего лидера после
записи большинства. Полный набор сохраняет State Machine Safety, Election
Safety, Log Matching, Leader Completeness и HTTP-проверки failover/no-quorum
полной реализации. Проверки S3-API/UI и подготовка lab3 остаются своими карточками.

Фактические результаты на итоговом тестовом коде:

```text
$ go build ./...
exit=0 (вывод пуст)
$ go test -count=1 ./...
?   raftgo/cmd/raftgo [no test files]
ok  raftgo/kv        0.004s
ok  raftgo/raft      1.151s
ok  raftgo/server    6.461s
ok  raftgo/storage   0.002s
?   raftgo/transport [no test files]
exit=0
$ go test -race -count=1 ./...
?   raftgo/cmd/raftgo [no test files]
ok  raftgo/kv        1.009s
ok  raftgo/raft      8.503s
ok  raftgo/server    7.578s
ok  raftgo/storage   1.018s
?   raftgo/transport [no test files]
exit=0
$ go vet ./...
exit=0 (вывод пуст; .golangci.yml отсутствует)
$ gofmt -l .
exit=0 (вывод пуст)
$ go test -race -count=20 ./server -run '^TestKV'
ok  raftgo/server 3.680s
exit=0
$ go test -race -count=20 ./raft -run 'ReadIndex|CutOffLeaderCannotServeReads'
ok  raftgo/raft 1.812s
exit=0
$ git diff --check
exit=0 (вывод пуст)
```

S3-CORE готова. Ограничений нет. Ветки и снимки lab1–lab4 не менялись,
контроллер, другие карточки и агенты не запускались, push не выполнялся.
Следующий доступный ID — S3-API.

## S3-API — HTTP CRUD строк (2026-10-31)

Ветка `main`, база `bb2f6a607f8b5b0dd41fbc6a83158b5c8bec8b47`, checkout
`/home/bogdan/rafting` перед началом чистый. Зависимость S3-CORE готова.
Карточка прошла «В работе» → «Готово» после всех проверок.

Изменены `server/kv.go`, добавлен `server/http_kv_test.go`, обновлены
`docs/api.md`, `Kanban.md` и этот журнал.

POST/PUT теперь отвергают неверный UTF-8, неизвестные/повторные поля и неточный
регистр имён, null и нестроковые значения, лишний JSON и поле expected вне CAS.
Ключ проверяется на UTF-8 и 1..1024 байта после URL-декодирования. Существующий
лимит полной внутренней KV-команды 65536 байт сохранён и описан явно. HEAD,
который ServeMux допускает для GET, теперь выполняет чтение с барьером,
а не предлагает команду с пустой операцией. CAS/lock полного main сохранены.

Новые HTTP-тесты с реальным Raft и виртуальным временем проверяют:

- POST 201/409 с сохранением прежнего значения, PUT upsert 200, GET/DELETE
  отсутствующего ключа 404; пустые строки, Unicode, JSON-подобные значения.
- Ключи с Unicode, пробелом, экранированными `/`, `?`, `#`, `%`, буквальной
  строкой `%2F`, плюс encoded dot segments. Декодирование не меняет ключ.
- Ключ ровно 1024 UTF-8 байта; отклонение 1025/1026 байт и неверного UTF-8
  на всех четырёх методах. `/kv/` без ключа — 404. Неверные тела не попадают
  в лог; полная команда ровно 65536 байт проходит, следующий байт даёт 400.
- Follower GET/PUT возвращают 307 с исходным экранированием и query;
  неизвестный лидер — 503 с Retry-After: 1, запись не предлагается.
- Успех PUT ждёт consumer apply даже после commit. HEAD не меняет лог.
- При изоляции 504 содержит index/term и неизвестный исход, в логе ровно
  одно предложение, KV не изменён. После восстановления кворума та же запись
  применяется без второго запроса и становится доступна через GET.

Существующие `TestKVReadWaitsForApplication`,
`TestKVReadCancellationLeadershipAndIsolation` и
`TestKVEntriesCannotInjectStateMachineCommands` также прошли: GET ждёт apply,
изолированный stale leader с существующим значением не отвечает успешным GET,
а `/entries` отвергает внутренний префикс, в том числе после Unicode-escape.
Полный HTTP cluster test прошёл с CAS/lock и failover; core не менялся.

Фактические проверки (Go 1.27.3, go.mod 1.27.1 сохранён):

```text
$ go build ./...
exit=0 (вывод пуст)
$ go test -count=1 ./...
?   raftgo/cmd/raftgo [no test files]
ok  raftgo/kv        0.002s
ok  raftgo/raft      1.443s
ok  raftgo/server    6.648s
ok  raftgo/storage   0.003s
?   raftgo/transport [no test files]
exit=0
$ go test -race -count=1 ./...
?   raftgo/cmd/raftgo [no test files]
ok  raftgo/kv        1.007s
ok  raftgo/raft      9.662s
ok  raftgo/server    8.249s
ok  raftgo/storage   1.031s
?   raftgo/transport [no test files]
exit=0
$ go vet ./...
exit=0 (вывод пуст; .golangci.yml отсутствует)
$ gofmt -l .
exit=0 (вывод пуст)
$ go test ./server -run '^TestHTTPKV' -count=1 -v
PASS все пять TestHTTPKV; ok raftgo/server 0.043s
exit=0
$ go test -race -count=20 ./server -run '^(TestHTTPKV|TestKVRead|TestKVEntries)'
ok  raftgo/server 7.158s
exit=0
$ git diff --check
exit=0 (вывод пуст)
```

S3-API готова, ограничений нет. Docker и ручной UI относятся к приёмке
этапа S3-DEMO и здесь не запускались. Снимки lab1–lab4 не менялись;
ветки/worktree/копии, другие карточки, агенты и push не запускались,
контроллер и его состояние не изменялись. Следующий доступный ID — S3-UI.

## S3-UI — Форма CRUD (2026-10-31)

Ветка `main`, база `fa0165d96542c826579438e20e8903de7c5a869d`, checkout
`/home/bogdan/rafting` перед началом чистый. Зависимость S3-API готова.
Карточка переведена «В работе» → «Готово» после проверок.

Изменены `server/http.go`, `server/ui.go`, `server/ui/index.html`,
`server/ui/election.css`, `server/ui_test.go`; добавлены `server/ui/kv.js`,
`server/ui_kv.go`, `server/ui_kv_test.go`. Обновлены `docs/api.md`,
`Kanban.md` и этот журнал.

Форма Create/Get/Put/Delete имеет независимый выбор участника, ключ и значение,
показывает контекст запроса, HTTP-код и полный JSON-ответ как текст. Пустые строки
различимы, HTML не исполняется. Результат линейризуемого GET отделён от локальной
диагностики и не меняется при фоновом опросе. Перед новым запросом старый ответ
очищается. Понятны 404/409/503/504; при таймауте записи показан неизвестный исход,
автоповтора нет. Экран выборов, диагностика устаревания и строковый лог сохранены.

Same-origin CRUD пересылается на обычный API выбранного фиксированного участника.
Только явный 307 на точный публичный адрес другого участника с тем же ключом
позволяет пересылку с сохранением метода/тела; cookies, произвольные URL/пути,
токены и query клиента не пересылаются. Ключ передаётся форме сервера через
query, чтобы браузер не нормализовал `.`/`..`, затем сервер кодирует его в один
сегмент `/kv/{key}`. После 503/504/потери ответа нет повторов; index/term и
Retry-After сохраняются. Полный main с CAS/lock сохранён, будущие UI-операции
не добавлены. Выделение lab3 остаётся только S3-DEMO.

Фактические обязательные проверки (Go 1.27.3, go.mod 1.27.1 сохранён;
`.golangci.yml` отсутствует):

```text
$ go build ./...
exit=0 (вывод пуст)
$ go test -count=1 ./...
?   raftgo/cmd/raftgo [no test files]
ok  raftgo/kv        0.002s
ok  raftgo/raft      1.271s
ok  raftgo/server    6.570s
ok  raftgo/storage   0.005s
?   raftgo/transport [no test files]
exit=0
$ go test -race -count=1 ./...
?   raftgo/cmd/raftgo [no test files]
ok  raftgo/kv        1.011s
ok  raftgo/raft      10.013s
ok  raftgo/server    8.169s
ok  raftgo/storage   1.031s
?   raftgo/transport [no test files]
exit=0
$ go vet ./...
exit=0 (вывод пуст)
$ gofmt -l .
exit=0 (вывод пуст)
$ git diff --check
exit=0 (вывод пуст)
$ node --check server/ui/kv.js
$ node --check server/ui/election.js
оба exit=0 (Node только для проверки синтаксиса, UI не требует Node)
$ go test -race ./server -run '^TestKVUI' -count=1 -v
PASS TestKVUIForwarding
PASS TestKVUILostResponseAndUnavailableLeader (GET/POST/PUT/DELETE)
ok raftgo/server 1.045s
exit=0
```

HTTP-тесты проверяют четыре метода через follower, экранирование ключа,
сохранение тела, HTTP-кодов и index/term, большие JSON-экранированные ответы,
запрет посторонних Location/другого ключа/циклов/CAS-путей, отсутствие cookies
и токенов, потерю ответа без повторной отправки и Retry-After при 503.
Первый локальный прогон обнаружил ожидаемый старый номер этапа в routing-тесте;
ожидание обновлено на этап 3, последующие проверки выше успешны.

Реальный Firefox 158 (headless, Marionette), пять Docker-процессов отдельного
проекта `raftgo-scenarios`, собранных из итогового кода main без смены checkout:

```text
$ docker compose -p raftgo-scenarios -f scenarios/compose.yaml up -d --build
exit=0
$ python3 /tmp/raftgo-s3-ui-browser.py
PASS follower CRUD: 201/200/404/409; empty, HTML, Unicode, encoded keys and dot segments; explicit GET survives polling; election/string log retained
PASS leader pause: CRUD through follower after election; stale node visible
PASS no majority: real PUT 504 index/term, unknown outcome, no retry; GET and PUT 503 without leader; old GET value cleared
PASS recovery; same-origin resources; mobile without page overflow
PASS cleanup: test browser closed; five Docker nodes resumed
exit=0
```

Ввод и кнопки проверены в браузере через DOM; ответы сервера реальные, без
подмены fetch (обёртка только считала запросы). До восстановления большинства
после PUT 504 отправлен ровно один изменяющий запрос. GET без кворума не
показывает старое значение как успешное. Все обращения браузера same-origin.
Просмотрен `/tmp/raftgo-s3-ui-mobile.png`: форма, результат и локальный лог
разделены, кнопки доступны, горизонтального переполнения страницы нет.
Логи: `/tmp/raftgo-s3-ui-{build,browser,down}.log`, harness
`/tmp/raftgo-s3-ui-browser.py`; снимки normal/failover/no-quorum/mobile там же.
Два первых прогона harness были исправлены: ошибка кавычек в генерируемом JS
и неверное ожидание отсутствия поля value в 404 (API возвращает пустую строку
вместе с error). Продуктовый код после этих прогонов не менялся; итоговый
полный браузерный прогон выше успешен. Свой стенд после unpause удалён целиком
через compose down; существующий проект `rafting` не менялся.

Ограничений нет. Lab1–lab4, история main, контроллер и его состояние не менялись.
Другие карточки, агенты и push не запускались; ветки/worktree/копии проекта
не создавались. Следующий доступный ID — S3-DEMO, здесь не запускается.

После итогового обновления документации и канбана обязательный набор повторён:
`go build ./...`, `go test -count=1 ./...`, `go test -race -count=1 ./...`,
`go vet ./...`, `gofmt -l .`, `git diff --check` — все exit=0, форматирование
и diff без замечаний. Повторный test: kv 0.001s, raft 0.919s, server 6.383s,
storage 0.003s; race: kv 1.006s, raft 6.173s, server 7.695s, storage 1.013s.

## S3-DEMO — подготовка CRUD-снимка (2026-10-31)

Ветка main, исходная база `6e6a5c426c006cc18b85598f738037fac6854b7f`,
checkout чистый. Следующая карточка S3-DEMO выбрана по поручению пользователя;
S3-UI готова. Карточка переведена «В работе». Готовится lab3 в том же checkout,
принятые lab1/lab2 не меняются. SHA базы кандидата будет зафиксирован отдельно.

Подготовлен `docs/demo3.md` с воспроизводимым запуском и браузерной проверкой.
На main перед выделением кандидата: build/test/race/vet/gofmt и diff --check
exit=0. Test: kv 0.002s, raft 1.004s, server 6.493s, storage 0.003s;
race: kv 1.006s, raft 7.713s, server 7.799s, storage 1.013s.
Docker task1, task2, task3 — PASS, каждый exit=0; task2 заменил конфликтный
хвост на пяти репликах, task3 получил PUT 504 и GET 503 без кворума.
Логи `/tmp/raftgo-s3-demo-main-{build,task1,task2,task3,down}.log`.
Свой стенд полностью удалён перед переключением. Существующий проект rafting
не менялся. После этого коммита готовится snapshot/lab3 от его точного SHA.

### S3-DEMO — итоговая приёмка lab3

- Исходный main: `6e6a5c426c006cc18b85598f738037fac6854b7f`.
- Точная база кандидата main: `718376b1c2710581fdc9b41f88c2c101dae55f8e`
  (инструкция demo3 и результаты main); перед переключением checkout чистый,
  свой стенд полностью удалён. Другие агенты и контроллер не запускались.
- Кандидат `snapshot/lab3` и принятый `lab3`:
  `d93c829d8de20cfccf92ee06cf0b577b66b46010`.
- Ветка lab3 создана только после успешных проверок этого SHA; checkout
  возвращён в main, снимок обратно не мержился. lab1/lab2 не перемещались,
  lab4 не создавалась, push не выполнялся.

Изменения в main: `docs/demo3.md`, `docs/delivery.md`, `Kanban.md`, `README.md`.
Полный рабочий код main сохранён. В отдельном снимке изменены `kv/store.go`
и тесты (только CRUD, без модели и операций этапа 4), `server/kv.go`,
`server/http.go`, HTTP/replay/safety/UI-proxy тесты; ReadIndex, ожидание apply,
запрет внутреннего префикса /entries и все core safety-тесты сохранены.
Core `raft/` не менялся. Удалён только тестовый код будущих функций, смешанные
replay/failover-тесты переведены на CRUD с сохранением проверки результатов.
CRUD-панель UI этапа 3, выборы и add сохранены. В runner отсутствует task4,
`all` выполняет 1–3; удалены каталог task4, autokanban, tools/autokanban,
канбан, задачи и журнал разработки, прежний demo2. AGENTS направляет разработку
в main. README, API и onboarding описывают возможности снимка; docs/stages.md
оставлен как план-контракт. Личные .obsidian не отслеживаются Git и не удалялись.

Фактические проверки точного SHA d93c829 (Go 1.27.3, go.mod 1.27.1 сохранён;
.golangci.yml отсутствует):

```text
$ go build ./...
exit=0 (вывод пуст)
$ go test -count=1 ./...
?   raftgo/cmd/raftgo [no test files]
ok  raftgo/kv        0.001s
ok  raftgo/raft      0.983s
ok  raftgo/server    6.423s
ok  raftgo/storage   0.002s
?   raftgo/transport [no test files]
exit=0
$ go test -race -count=1 ./...
?   raftgo/cmd/raftgo [no test files]
ok  raftgo/kv        1.006s
ok  raftgo/raft      6.870s
ok  raftgo/server    7.860s
ok  raftgo/storage   1.023s
?   raftgo/transport [no test files]
exit=0
$ go vet ./...
exit=0 (вывод пуст)
$ gofmt -l .
exit=0 (вывод пуст)
$ git diff --check
exit=0 (вывод пуст)
$ node --check server/ui/election.js
$ node --check server/ui/kv.js
$ python3 -m py_compile scenarios/run.py
все exit=0
```

Предварительная сборка до коммита обнаружила неиспользуемую переменную,
оставшуюся после удаления сравнения; исправлена до d93c829. Итоговые проверки
выше выполнены после коммита. Проверка tracked tree и текстов моделей,
декодеров, маршрутов, UI и сценариев: PASS отсутствие кода этапа 4, контроллера
и личных настроек; POST /kv/probe/cas и /kv/probe/lock на всех пяти узлах — 404.

```text
$ docker compose -p raftgo-scenarios -f scenarios/compose.yaml up -d --build
exit=0
$ python3 scenarios/run.py all
PASS task1
PASS task2
PASS task3
exit=0
```

Task1 проверил перевыборы и невозможность выбрать лидера меньшинством.
Task2 проверил follower redirect, commit/apply, догоняющую репликацию,
сохранение committed prefix и замену конфликтного хвоста на всех пяти репликах.
Task3 проверил 201/409/404/200 CRUD, пустую/Unicode/JSON-строку, чтение после
смены лидера и pause/unpause всех процессов; без кворума PUT 504, GET 503.
Логи `/tmp/raftgo-s3-demo-candidate-{build,scenarios}.log`.

Реальный Firefox 158.0.1 headless с Marionette, DOM-ввод и кнопки, реальные
ответы пяти Docker-процессов на том же d93c829:

```text
$ /tmp/raftgo-s3-demo-venv/bin/python /tmp/raftgo-s3-demo-browser.py
PASS browser CRUD, string add, empty/Unicode/HTML/escaped/dot keys; explicit GET retained
PASS browser leader pause, value retained, stale card visible
PASS browser no quorum PUT 504 with index/term, no automatic retry, GET/PUT 503, recovery
PASS browser same-origin resources and mobile width
Browser closed and scenario nodes resumed
exit=0
```

CRUD через follower проверен для Create/Get/Put/Delete, включая повторный POST,
отсутствующий DELETE, upsert, Unicode, пустое значение, HTML как текст,
экранированные и dot-only ключи. Результат GET сохраняется при polling.
После паузы лидера подтверждённое значение читается через нового лидера,
устаревшая карточка видна. Без кворума PUT вернул 504 с index/term; UI показал
неизвестный исход, Resource Timing подтвердил один запрос без автоповтора.
GET/PUT после утраты лидерства — 503; старое успешное значение очищено.
После восстановления PUT/GET успешны. Все ресурсы same-origin, ширина 390 px
без горизонтального переполнения. Скриншот mobile просмотрен: карточки, лог,
CRUD и ответ разделены, поля и кнопки доступны.

Лог `/tmp/raftgo-s3-demo-browser.log`, harness `/tmp/raftgo-s3-demo-browser.py`,
скриншоты `/tmp/raftgo-s3-demo-ui-{normal,failover,no-quorum,mobile}.png`.
Два предварительных запуска harness исправлены без изменения продукта:
нужен реальный binary /usr/lib/firefox/firefox; перехват fetch через Marionette
мешал запросам, поэтому заменён наблюдением Resource Timing без подмены fetch.
Итоговый прогон успешен. После завершения Python напечатал предупреждения
деструктора mozprofile при shutdown; exit=0, браузер закрыт явно, это не сбой UI.

Искусственный RuntimeError после pause и network disconnect node5 проверил
finally runner: ошибка сохранена, пауза снята, IP/алиасы восстановлены,
container ID и PID всех пяти узлов прежние, RAM не терялась; лидер согласован.
`/tmp/raftgo-s3-demo-cleanup.log`: PASS, exit=0.

Перед возвратом в main свой стенд полностью удалён через compose down (exit=0).
Общий unpause уже работающих контейнеров вернул «not paused»; список paused
проверен пустым, затем down завершён успешно. Существующий проект rafting
и пользовательский Firefox не менялись. Ограничений приёмки нет.
S3-DEMO готова, реестр и канбан обновлены. Следующий доступный ID — S4-CORE;
другие карточки не запускались.

После возврата в main и итогового обновления README/реестра/канбана повторены
`go build ./...`, `go test -count=1 ./...`, `go test -race -count=1 ./...`,
`go vet ./...`, `gofmt -l .`, `git diff --check` — все exit=0; build/vet/gofmt/diff
без вывода. Test: kv 0.002s, raft 0.930s, server 6.549s, storage 0.003s;
race: kv 1.006s, raft 6.542s, server 7.897s, storage 1.013s.
Точный вывод: `/tmp/raftgo-s3-demo-final-go.log`.

## S4-CORE — CAS и распределённые блокировки (2026-10-31)

Ветка `main`, база `3c07774b04319af1b5415ad7d7003ac46e471334`.
Зависимость S3-DEMO готова. Работа выполнена в существующем checkout;
снимки lab1–lab4 не менялись. Следующий доступный ID — S4-API, не запускался.

Проверена существующая реализация: CAS сравнивает и заменяет значение в одном
apply, lock/unlock — отдельные одиночные реплицируемые команды. Изменений
алгоритма не потребовалось. Fencing token равен индексу подтверждённого захвата,
не зависит от времени/TTL/рандома; lock не реентерабелен и существует отдельно
от значения. Удаление/пересоздание значения не меняет владельца. Без/с чужим
токеном под lock — 423; после unlock старый токен — 409, под новым lock — 423.

Изменённые файлы:

- `kv/atomic_test.go`: отсутствующий ключ и пустое значение в CAS, отказ без
  изменения состояния, ABA и CAS без изменения значения; матрица Get, Create,
  Put, Delete, CAS, Unlock, Lock с пустым/чужим/текущим токеном на существующем
  и отсутствующем ключе; удаление/пересоздание под lock, старый токен после
  unlock, отсутствие реентерабельности и следующий больший fence.
- `server/kv_atomic_test.go`: три настоящих Raft-узла, Memory Storage,
  fake-транспорт и виртуальные таймеры testing/synctest. Конкурентные Start
  двух CAS с одним expected и разными новыми значениями дают 200/409 и значение
  победителя; два lock отсутствующего ключа дают 201/423. На каждую операцию
  приходится ровно одна запись. Результаты и логи сверяются на всех репликах.
  Изоляция лидера сохраняет память: его неподтверждённый lock не применяется.
  Новый лидер сохраняет lock/токен, выполняет CAS и удаление/пересоздание
  владельцем, отклоняет чужой доступ и выдаёт больший токен после unlock.
  Старый узел пересоздан с тем же объектом Memory и новой Store: до получения
  commit от лидера ничего не применяет; после соединения отбрасывает хвост
  и воспроизводит те же результаты, значения и действующую блокировку.
- `kv/store.go`: комментарий к Apply фиксирует сериализацию вызывающим,
  положительные возрастающие подтверждённые индексы и replay в новую Store;
  клиентские повторы не дедуплицируются. Исполняемый код не менялся.
- `docs/onboarding.md`: уточнено время жизни токена, числовое сравнение индексов,
  пропуски между захватами, отличие replay от повторной отправки запроса;
  добавлены ссылки на тесты. Описание ABA и границ одной жизни кластера сохранено.
- `Kanban.md`: S4-CORE переведена из «В работе» в «Готово» после проверок.

Go `go1.27.3-X:nodwarf5 linux/amd64`; go.mod не менялся, `.golangci.yml` нет.
Фактические результаты обязательных проверок, все exit=0:

```text
$ go build ./...
(без вывода)
$ go test -count=1 ./...
?   raftgo/cmd/raftgo [no test files]
ok  raftgo/kv        0.003s
ok  raftgo/raft      1.053s
ok  raftgo/server    6.476s
ok  raftgo/storage   0.004s
?   raftgo/transport [no test files]
$ go test -race -count=1 ./...
?   raftgo/cmd/raftgo [no test files]
ok  raftgo/kv        1.008s
ok  raftgo/raft      6.277s
ok  raftgo/server    7.861s
ok  raftgo/storage   1.014s
?   raftgo/transport [no test files]
$ go vet ./...
(без вывода)
$ gofmt -l .
(пусто)
$ git diff --check
(без вывода)
```

Дополнительные критерии карточки:

```text
$ go test ./kv ./server -run 'Test(CASValue|LockAccess|KVAtomic)' -count=1
ok  raftgo/kv     0.003s
ok  raftgo/server 0.006s
$ go test -race ./kv ./server -run 'Test(CASValue|LockAccess|KVAtomic)' -count=20
ok  raftgo/kv     1.039s
ok  raftgo/server 1.103s
```

Оба запуска exit=0. Docker и ручной UI относятся к приёмке этапа S4-DEMO,
в S4-CORE не запускались. Ограничений выполнения карточки нет.

## S4-API — HTTP CAS, lock и unlock (2026-10-31)

Ветка `main`, база `7eebb2f1c6d1e35d65c7583799a2785c9610b30f`;
checkout перед работой чистый, зависимость S4-CORE готова. Работа выполнена
в существующей папке без переключений, новых веток, worktree, других агентов
и push. Снимки lab1–lab4, контроллер и его состояние не менялись.

Существующие маршруты POST `/kv/{key}/cas`, POST/DELETE `/kv/{key}/lock`
сохранены; CAS сравнивает и меняет значение одной командой при apply.
`X-Lock-Token` передаётся в GET/CRUD/CAS/unlock. Чужой/пустой токен под lock
даёт 423, старый токен после unlock — 409; удаление значения не снимает lock.
Ответы на изменения теперь содержат и `index`, и `term` своей применённой
записи (включая отказы state machine). Проверка пары не позволяет выдать успех
для заменённой записи. Lock/unlock отклоняют непустое тело; повторные заголовки
токена, списки через запятую, невалидный UTF-8 и токены длиннее 1024 байт
отклоняются с 400 до Start. Токен остаётся непрозрачным для HTTP: чужое
значение проверяется state machine, а не превращается в ошибку формата.
Существующий лимит всей внутренней команды 64 КиБ и точный JSON CAS сохранены.

Изменённые файлы:

- `server/kv.go`: валидация заголовка и тела lock/unlock, term в ответе.
- `server/http_atomic_test.go`: HTTP-гонки CAS/lock с одним победителем и одной
  записью на запрос; матрица методов с пустым/чужим/верным/старым токеном,
  lock отсутствующего ключа, удаление/пересоздание, fencing; строгие тела,
  ключи, заголовки, методы; 504 CAS/lock/unlock без кворума, последующий apply
  исходной записи без повтора; потеря ответа через ResponseWriter с
  io.ErrClosedPipe после apply, отсутствие повтора и сохранение результата;
  замена индекса другим term и ожидание consumer после commit для всех трёх
  операций. Таймеры виртуальные, память при изоляции сохраняется.
- `server/http_kv_test.go`: 307 с экранированным ключом/query и 503 без лидера
  дополнены CAS/lock/unlock с токеном.
- `server/http_test.go`: пять реальных Raft-узлов, Memory и HTTP/RPC listeners;
  гонка lock дополнительно к CAS, следование настоящему 307 для GET/CRUD/CAS/
  lock/unlock с сохранением метода/тела/токена; CAS владельца после failover.
  Остановленный узел не возвращается с пустым Storage в продолжающийся кластер.
- `README.md`: точные правила тел, заголовка, index/term, 307 и неизвестного
  исхода; `Kanban.md` и этот журнал — статус и результаты S4-API.

Go `go1.27.3-X:nodwarf5 linux/amd64`; версия go.mod сохранена,
`.golangci.yml` отсутствует. Фактические обязательные проверки, все exit=0:

```text
$ go build ./...
(без вывода)
$ go test -count=1 ./...
?   raftgo/cmd/raftgo [no test files]
ok  raftgo/kv        0.002s
ok  raftgo/raft      1.053s
ok  raftgo/server    6.628s
ok  raftgo/storage   0.003s
?   raftgo/transport [no test files]
$ go test -race -count=1 ./...
?   raftgo/cmd/raftgo [no test files]
ok  raftgo/kv        1.009s
ok  raftgo/raft      6.456s
ok  raftgo/server    8.457s
ok  raftgo/storage   1.016s
?   raftgo/transport [no test files]
$ go vet ./...
(без вывода)
$ gofmt -l .
(пусто)
$ git diff --check
(без вывода)
```

Дополнительные проверки карточки:

```text
$ go test ./server -run 'TestHTTP(Atomic|Cluster)' -count=1
ok  raftgo/server 5.849s
$ go test ./server -run 'TestHTTP(Atomic|KVFollower)' -count=1
ok  raftgo/server 0.055s
$ go test -race ./server -run 'TestHTTP(Atomic|KVFollower)' -count=20
ok  raftgo/server 7.766s
```

Все exit=0. Первый запуск новых тестов обнаружил несовпадение uint64 результата
KV и raft.Index в тестовых сравнениях; явные преобразования исправлены до
успешных прогонов. Docker и ручной UI относятся к приёмке S4-DEMO, здесь
не запускались. Ограничений выполнения S4-API нет. Следующий доступный ID —
S4-CLIENT (также доступна S4-UI); другие карточки не запускались.

Дополнительно после полного набора:

```text
$ go test -race ./server -run TestHTTPClusterCommitRedirectFailover -count=3
ok  raftgo/server 18.430s
```

Exit=0. После обновления журнала и канбана `git diff --check` и `gofmt -l .`
повторены успешно, вывод пуст. S4-API переведена в «Готово».

## S4-CLIENT — Клиентский Go API (2026-10-31)

Ветка `main`, база `6907d182f4305b4dd9837bcb2c9db4e0c62ac76b`.
Начальный checkout чистый, S4-API готова. Работа выполнена в существующем
checkout без переключения, новых веток, worktree, копий, агентов или push.
Снимки lab1–lab4, контроллер и его состояние не менялись.

Изменённые файлы и поведение:

- `client/client.go`: HTTP-only API Create/Get/Put/Delete/CAS/Lock/Unlock,
  context, копия настраиваемого http.Client, общий Timeout на все переходы.
  Только ограниченные 307 сохраняют метод, тело и явный X-Lock-Token;
  циклы/лимит отклоняются, остальные 3xx не выполняются. Тела не получают
  GetBody/idempotency headers; изменяющие запросы не повторяются после ошибки.
  Ключи кодируются одним сегментом, включая slash, Unicode, percent и dot-only.
  HTTPError сохраняет статус, результат и index/term; errors.Is различает
  404/409/423/503. UnknownOutcomeError для 504/сетевых ошибок записи и потери
  успешного ответа сохраняет исходную причину, включая context cancellation.
- `client/client_test.go`: httptest всех семи методов через follower 307,
  метод/тело/токен, escaping, лимит и цикл, отсутствие переходов по другим 3xx,
  статусы, отмена и http.Client.Timeout для GET и записи, разрыв соединения
  после чтения тела для всех шести изменяющих операций без повтора команды.
- `client/integration_test.go`: настоящий Raft с Memory и API на httptest,
  два клиента конкурируют за CAS и lock: один успех, один 409/423, по одной
  записи на запрос. Проверены доступ владельца, отсутствие доступа у конкурента,
  Create/Get/Put/Delete/CAS/Unlock, lock независимо от значения и старый токен.
  API сначала применяет CAS, затем соединение закрывается без ответа:
  клиент получает неизвестный исход, лог вырастает ровно на одну запись,
  другой клиент читает применённое значение.
- `client/example_test.go`: компилируемый пример двух клиентов, явный токен,
  отказ конкурента, CAS и явный Unlock; на неизвестном исходе нет повтора.
- `client/README.md`, `README.md`: сигнатуры, настройки, ошибки и пример,
  отсутствие exactly-once/дедупликации, ABA, потеря токена, отсутствие TTL,
  границы fencing в одной жизни кластера. Без CLI и внешних зависимостей.
- `Kanban.md`: S4-CLIENT переведена в «Готово» после успешных проверок.

Go `go1.27.3-X:nodwarf5 linux/amd64`, go.mod 1.27.1 не менялся;
`.golangci.yml` отсутствует. Фактические обязательные проверки, все exit=0:

```text
$ go build ./...
(без вывода)
$ go test -count=1 ./...
ok  raftgo/client   0.263s
?   raftgo/cmd/raftgo [no test files]
ok  raftgo/kv       0.003s
ok  raftgo/raft     1.097s
ok  raftgo/server   6.629s
ok  raftgo/storage  0.003s
?   raftgo/transport [no test files]
$ go test -race -count=1 ./...
ok  raftgo/client   1.304s
?   raftgo/cmd/raftgo [no test files]
ok  raftgo/kv       1.013s
ok  raftgo/raft     6.309s
ok  raftgo/server   8.426s
ok  raftgo/storage  1.015s
?   raftgo/transport [no test files]
$ go vet ./...
(без вывода)
$ gofmt -l .
(пусто)
$ git diff --check
(без вывода)
```

Дополнительные проверки критериев карточки:

```text
$ go test ./client -count=1
ok  raftgo/client 0.254s
$ go test -race ./client -count=20
ok  raftgo/client 6.376s
```

Оба exit=0. Реальные HTTP listeners и Memory Raft создаются самими тестами;
пример без Output компилируется, но не обращается к внешнему кластеру.
Docker и ручной UI относятся к S4-DEMO и здесь не требуются.
Ограничений выполнения нет. Следующий доступный ID — S4-UI, не запускался.

## S4-UI — CAS и владение lock (2026-10-31)

Ветка `main`, база `b3c75da1fcab96d18838fbdece80210a6072f2f2`.
Checkout перед работой чистый, зависимость S4-API готова. Работа только
в существующем checkout main, без переключений, новых веток, worktree,
копий проекта, агентов и push. Снимки lab1–lab4 и контроллер не менялись.
Следующий доступный ID — S4-DEMO; он не запускался, lab4 не создавалась.

Изменённые файлы и поведение:

- `server/ui/index.html`, `server/ui/kv.js`, `server/ui/election.css`:
  экран этапа 4 с expected/new CAS, Lock/Unlock, явным X-Lock-Token для
  CRUD/CAS/unlock. Полученный токен отображается в списке с ключом;
  отдельная кнопка подставляет его в форму. Автоматической подстановки нет,
  при ручной смене ключа поле очищается. Lock отправляется без токена.
  Map живёт только в памяти страницы: нет localStorage/sessionStorage,
  синхронизации вкладок и автоматического восстановления после перезагрузки.
  Только успешный lock добавляет токен, только успешный unlock удаляет его;
  после unlock явное поле сохранено для демонстрации stale-token 409.
  409/423 объяснены, 504/потеря ответа показывают неизвестный исход;
  для неопределённого захвата отдельно показано, что токен не получен.
  Нет автоповтора и auto-unlock. UI описывает потерю токена, отсутствие TTL,
  lock независимо от значения, нереентерабельность и границы жизни кластера.
- `server/ui_kv.go`: фиксированные same-origin маршруты CAS и lock/unlock;
  проверка и передача только явного X-Lock-Token. 307 сохраняет метод,
  тело и токен только для точного пути/ключа известных участников.
  Cookie не пересылаются. Тело DELETE тоже передаётся для проверки API;
  GetBody отключён, изменяющие запросы после потери ответа не повторяются.
- `server/ui_kv_test.go`: все семь операций через 307 с экранированным ключом,
  сохранение тела/токена/метаданных, 423, запрет смены ключа при redirect,
  отклонение неоднозначных/слишком длинных токенов, разрыв соединения для
  CRUD/CAS/lock/unlock с одним запросом и ответом 504.
- `server/ui_test.go`: номер этапа 4; `README.md`: инструкция по UI и токенам;
  `Kanban.md`: статус карточки после проверок; этот журнал: результаты.

Go `go1.27.3-X:nodwarf5 linux/amd64`, go.mod 1.27.1 сохранён,
`.golangci.yml` отсутствует. Фактические обязательные проверки, все exit=0:

```text
$ go build ./...
(без вывода)
$ go test -count=1 ./...
ok  raftgo/client    0.247s
?   raftgo/cmd/raftgo [no test files]
ok  raftgo/kv        0.002s
ok  raftgo/raft      0.927s
ok  raftgo/server    6.500s
ok  raftgo/storage   0.002s
?   raftgo/transport [no test files]
$ go test -race -count=1 ./...
ok  raftgo/client    1.293s
?   raftgo/cmd/raftgo [no test files]
ok  raftgo/kv        1.007s
ok  raftgo/raft      6.437s
ok  raftgo/server    7.890s
ok  raftgo/storage   1.018s
?   raftgo/transport [no test files]
$ go vet ./...
(без вывода)
$ gofmt -l .
(пусто)
$ git diff --check
(без вывода)
$ go test ./server -run 'Test(KVUI|ElectionUI)' -count=1
ok  raftgo/server 0.021s
$ go test -race ./server -run 'Test(KVUI|ElectionUI)' -count=20
ok  raftgo/server 1.686s
```

Дополнительно реальный Firefox 158.0.1 через Marionette на пяти Docker-узлах
проекта `raftgo-s4-ui`: сборка/up exit=0, итоговый браузерный harness exit=0.
Две вкладки одного origin: CAS одного expected даёт 200/409; lock отсутствующего
ключа 201/423; другой вкладке токен не подставляется и список владельца пуст.
GET без/с чужим токеном — 423; явная подстановка владельцем даёт 404 для
отсутствующего значения, затем Create/CAS/Delete/Create успешны под тем же lock.
Повторный Lock — 423. HTML-строка отображается текстом, скрипт не выполняется.
После pause лидера новый лидер принимает прежний токен и CAS; чужая вкладка
по-прежнему получает 423. После Unlock — 200, старый токен — 409.

Без кворума отдельно CAS, Lock и Unlock получили 504 с index/term.
Resource Timing для каждой операции показал ровно один запрос после ожидания:
нет повтора или автоматического Unlock. Сохранённый токен при 504 Unlock
остаётся в списке; при 504 Lock токен не выдумывается и явно показана его потеря.
После восстановления неизвестный Unlock допускает как выполненный, так и
невыполненный исход — проверка не трактует 504 как откат.
Перезагрузка страницы очищает поле/список токенов, но GET без токена остаётся
423; вручную сохранённый токен по-прежнему принят (404 без значения).
Ресурсы только same-origin, при ширине 390 px нет горизонтального переполнения;
скриншоты владельца и мобильного экрана просмотрены, форма и сообщения доступны.

Harness: `/tmp/raftgo-s4-ui-browser.py`, успешный лог:
`/tmp/raftgo-s4-ui-browser-final.log`, скриншоты:
`/tmp/raftgo-s4-ui-{owner,unknown,mobile}.png`.
Предыдущий расширенный прогон завершился на проверке harness, направившей
прямой HTTP-запрос к уже бывшему лидеру после серии выборов. В harness исправлен
повторный выбор лидера; продукт не менялся, полный повтор успешен.
При shutdown Python печатает предупреждения деструктора mozprofile
`sys.meta_path is None`; браузер явно закрыт, итоговый exit=0.

Cleanup harness снял все паузы. Собственный стенд удалён целиком через
`docker compose -p raftgo-s4-ui -f scenarios/compose.yaml down`, exit=0;
лог `/tmp/raftgo-s4-ui-down.log`. Существующий проект `rafting` не менялся.
Это проверка S4-UI; приёмка всех этапов/task1–4 остаётся отдельной S4-DEMO.
Ограничений выполнения карточки нет. После записи результатов S4-UI переведена
в «Готово»; финальные gofmt и git diff --check без вывода, exit=0.


## S4-DEMO — Итоговая приёмка четырёх веток (2026-10-31)

Рабочая ветка `main`, начальная база `95ddd3ba3ed836920ead8107e7d093e33cf2be88`.
Checkout перед карточкой чистый; S4-CLIENT и S4-UI готовы. Статус «В работе»
сохранён коммитом `e0960f9`. Источник снимка — main
`e0960f9d695cd582fbe3947c30e1f1266394bf6f`. Кандидат `snapshot/lab4` и принятая `lab4` указывают на один SHA:
`a4b1f2e8eebb39fe9e0cdff8b19e2dea094f30e1`.
Работа выполнена в одном checkout без копий проекта, worktree или других агентов.
Push ветки lab4 отдельно разрешён пользователем; main не отправляется.

В lab4 сохранён полный код этапов 1–4: выборы, строковый лог, KV CRUD,
CAS, lock/unlock, HTTP, Go-клиент и встроенный UI. Удалены `autokanban`,
`tools/autokanban`, `Kanban.md`, `docs/tasks.md`, `docs/delivery.md`, старые
отчёты `docs/demo2.md` и `docs/demo3.md`. AGENTS описывает снимок для сдачи;
README, docs/api.md, docs/onboarding.md и scenarios/README.md очищены от
старого процесса и ссылок на удалённые документы. Личные настройки в git-дерево
не включены. main сохраняет контроллер, полный код, канбан и журнал.

Проверены точные SHA, последовательно lab4 → lab1 → lab2 → lab3:

| Ветка | Проверенный SHA | Go-набор | Docker all | Firefox/UI и границы |
| --- | --- | --- | --- | --- |
| lab4 | `a4b1f2e8eebb39fe9e0cdff8b19e2dea094f30e1` | PASS | PASS task1–4 | PASS CAS/lock, две вкладки, Go-клиент |
| lab1 | `a2f486172f435f2e469802935ecec43048abee69` | PASS | PASS task1 | PASS выборы; нет лога/KV |
| lab2 | `0006d8422076f19336ad9ab8cdb5a9431fc2d4c6` | PASS | PASS task1–2 | PASS строки/лог; нет KV |
| lab3 | `d93c829d8de20cfccf92ee06cf0b577b66b46010` | PASS | PASS task1–3 | PASS CRUD; нет CAS/lock/клиента |

На каждом SHA выполнены следующие команды, все exit=0:

```sh
go build ./...
go test -count=1 ./...
go test -race -count=1 ./...
go vet ./...
gofmt -l .  # пустой вывод
```

Go `go1.27.3-X:nodwarf5 linux/amd64`, go.mod 1.27.1 сохранён;
`.golangci.yml` отсутствует. Фактический вывод пакета server в обычном/race
прогоне соответственно:

```text
lab4: ok raftgo/server 6.599s / ok raftgo/server 8.133s
lab1: ok raftgo/server 0.607s / ok raftgo/server 1.626s
lab2: ok raftgo/server 6.335s / ok raftgo/server 7.582s
lab3: ok raftgo/server 6.470s / ok raftgo/server 7.885s
```

Остальные пакеты также PASS либо [no test files]. В исходном main до выделения
кандидата обязательный Go-набор также прошёл; финальный прогон main после
обновления документации записан ниже.

Docker server 29.9.2. Каждый снимок собирался через
`docker compose -p raftgo-scenarios -f scenarios/compose.yaml up -d --build`,
затем `python3 scenarios/run.py all`. Runner печатал только доступные task1..N,
все PASS, exit=0. Перед каждым переключением все паузы сняты, весь собственный
стенд удалён через down, exit=0. Существующий проект `rafting` не менялся.
Проверены пять участников, Memory Storage, отсутствие файлового backend и
будущих исходников/маршрутов/сценариев; в Compose только read-only bind конфигов,
без data volumes. Для ранних этапов отсутствующие маршруты возвращают 404,
в lab1 нет entries/commit_index, с lab2 status содержит entries/consistency=local.
Принятые lab1–lab3 не перемещались и не изменялись.

Firefox 158.0.1 через Marionette: реальный браузер с кликами/вводом форм,
визуально просмотрены скриншоты каждого этапа. Проверены пять карточек, номер
этапа, пауза лидера/устаревание, same-origin ресурсы и ширина 390 px.
В lab2 — пустая строка, повтор Unicode/HTML-строки как текста, commit/apply и лог.
В lab3 — CRUD, 404/409, пустые/Unicode/HTML значения, экранированные и dot-ключи,
строковый add, чтение после failover, 504 без повтора и восстановление кворума.
В lab4 — две вкладки CAS 200/409, lock 201/423, явные/чужие/старые токены,
lock отсутствующего ключа, CRUD/CAS под lock и после смены лидера, unlock,
неизвестный исход CAS/lock/unlock 504 с ровно одним запросом, сохранение токена
после неизвестного unlock, потеря токена при reload без автоматического unlock.
Отдельный Go-прогон на Docker использовал два клиента через разные узлы:
ровно один победитель CAS, отказ конкурента под lock, доступ владельца с токеном,
чтение/удаление, unlock и stale-token конфликт через настоящие 307.

Артефакты текущей проверки (локальные, не часть снимков):
`/tmp/raftgo-s4-demo-{build,scenarios,browser,down}.log`,
`/tmp/raftgo-final-lab{1,2,3}-{build,scenarios,browser,down}.log`,
`/tmp/raftgo-final-browser.py`, `/tmp/raftgo-final-audit.py`,
`/tmp/raftgo-s4-demo-client.go`, `/tmp/raftgo-s4-demo-browser.py`.
Скриншоты: `/tmp/raftgo-final-lab{1,2}.png`,
`/tmp/raftgo-s3-demo-ui-{normal,failover,no-quorum,mobile}.png`,
`/tmp/raftgo-s4-demo-{owner,unknown,mobile}.png`.
Первый запуск общего UI harness на lab1 выявил отсутствие helper follower
в старом runner; исправлен только внешний harness, повтор exit=0.
При завершении Firefox mozprofile печатает предупреждение деструктора
`sys.meta_path is None`; браузеры явно закрыты, успешные harness exit=0.
Это предупреждение не является ошибкой продукта или незавершённой проверкой.

S4-DEMO готова. Ограничений выполнения нет; очередь канбана исчерпана,
следующего доступного ID нет. Временная snapshot/lab4 сохранена для аудита.

Финальная проверка main после записи отчёта, все команды exit=0:

```text
$ go build ./...
(без вывода)
$ go test -count=1 ./...
ok  raftgo/client  0.264s
?   raftgo/cmd/raftgo [no test files]
ok  raftgo/kv      0.003s
ok  raftgo/raft    1.027s
ok  raftgo/server  6.786s
ok  raftgo/storage 0.003s
?   raftgo/transport [no test files]
$ go test -race -count=1 ./...
ok  raftgo/client  1.296s
?   raftgo/cmd/raftgo [no test files]
ok  raftgo/kv      1.007s
ok  raftgo/raft    6.633s
ok  raftgo/server  8.339s
ok  raftgo/storage 1.017s
?   raftgo/transport [no test files]
$ go vet ./...
(без вывода)
$ gofmt -l .
(пусто)
$ git diff --check
(без вывода)
```


## Анализ упрощения lab1–lab4 и очередь R1–R4 (2026-10-31)

Ветка `main`, база `2680197fa41f5a5226839bd89d6b2f6f3d425b41`.
Checkout перед анализом чистый. Ветки исследованы через git show/ls-tree;
переключений, копий, агентов, реализации карточек и push не было.

Изменены `Kanban.md`, `docs/tasks.md`, `docs/refactoring.md` и этот журнал.
Созданы R1 (ожидание apply), R2 (пересылка UI), R3 (валидация ключа/токена),
R4 (выделение Config). Все в «К выполнению», зависимость S4-DEMO готова.
Анализ содержит SHA четырёх снимков, основания и границы применимости.
Поведение программы не менялось; refs lab1–lab4 сверены и остались прежними.

Go `go1.27.3-X:nodwarf5 linux/amd64`; go.mod не менялся, .golangci.yml нет.
Фактические проверки main, все exit=0:

```text
$ go build ./...
(без вывода)
$ go test -count=1 ./...
ok  raftgo/client   0.253s
?   raftgo/cmd/raftgo [no test files]
ok  raftgo/kv       0.002s
ok  raftgo/raft     0.934s
ok  raftgo/server   6.652s
ok  raftgo/storage  0.002s
?   raftgo/transport [no test files]
$ go test -race -count=1 ./...
ok  raftgo/client   1.294s
?   raftgo/cmd/raftgo [no test files]
ok  raftgo/kv       1.008s
ok  raftgo/raft     6.361s
ok  raftgo/server   8.314s
ok  raftgo/storage  1.020s
?   raftgo/transport [no test files]
$ go vet ./...
(без вывода)
$ gofmt -l .
(пусто)
$ git diff --check
(без вывода)
```

Дополнительно штатные парсеры runner.board/cards распознали четыре ID,
их статус и готовую зависимость; состав ID в канбане и каталоге согласован.
Ограничения: это статический анализ, Docker/UI и тесты самих снимков повторно
не запускались, производительность не измерялась. Autokanban пока запрещает
выполнение R-ID; карточки выполняются прямым поручением. Следующий доступный
ID — R1. Пересдача принятых снимков требует отдельного явного поручения.
