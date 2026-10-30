# Как выполнять задачи и сдавать этапы

## Источники и ветки

Полный исходник для выборочного переноса:
`70782f23fe695d184981dff932c47181f9eadd21` (HEAD при составлении плана).
В момент планирования checkout — `chore/obsidian-vault`, README изменён,
Kanban/docs/.obsidian ещё не отслеживаются. Не считать их чужим мусором и
не удалять. В B0 созданы `reference/full` и подготовительная ветка (см. запись ниже).
Сдаваемые stage-ветки создаются только в последующих карточках.

```text
reference/full (исходный полный код, не изменять)
       |
prepare/in-memory       B0 → B1 → B2
       |
stage/1-election        S1-CORE → S1-API → S1-UI → S1-DEMO
       |
stage/2-log             S2-CORE → S2-API → S2-UI → S2-DEMO
       |
stage/3-kv              S3-CORE → S3-API → S3-UI → S3-DEMO
       |
stage/4-cas-lock        S4-CORE → S4-API → {S4-CLIENT, S4-UI} → S4-DEMO
```

Четыре stage-ветки — четыре сдаваемых результата; reference/prepare/task —
служебные. Раннюю ветку нельзя впоследствии fast-forward до поздней.
Следующий этап создаётся **после** приёмки предыдущего, от принятого SHA.
Исходный полный код не мержится целиком в ранние этапы: переносится только
нужная реализация с тестами и адаптацией под память. Не переписывать историю
main и существующих веток `bogdan/3a-leader-election`, `bogdan/3b-log-replication`.

Каждая карточка выполняется в `task/<ID>-<slug>` от своей базы. Если stage-ветки
ещё нет, первая CORE-карточка готовит её из указанной базы. Поручение «Выполни S2-API» включает подготовку результата для целевой ветки
и обновление журнала. Локальная интеграция допустима, если целевая ветка чистая
и не занята другой работой; при этом сохранить отдельную task-ветку и SHA.
Если интеграция невозможна без затрагивания чужой работы, оставить результат
в task-ветке и назвать конкретное препятствие. Не пушить без поручения.

План и канбан — общий журнал в ветке документации/подготовки. Stage-ветки
получают их актуальную копию при создании; статусы в старой ветке остаются
снимком на тот момент. После работы в task-ветке перенести в общий журнал
только статус своей карточки и ссылку/SHA результата, отдельным изменением
документации; не мержить код этапа ради обновления доски. В отчёте явно указать,
если общий журнал ещё не обновлён. Готовая карточка может ожидать интеграции;
её зависимые задачи начинаются лишь после попадания результата в целевую базу.

## Задание агенту

```text
Прочитай AGENTS.md, docs/stages.md, docs/delivery.md и карточку S2-API
в docs/tasks.md. Выполни только S2-API от принятой stage/2-log в отдельной
task-ветке. Сохрани чужие изменения. Проверь критерии и обязательный race.
Обнови статус карточки, подготовь результат для review, укажи SHA и проверки.
```

Заменить ID; не нужно заново объяснять контекст. Если база ещё не содержит
зависимость, агент должен назвать её ID, а не строить весь проект в одной задаче.
Для первых запусков порядок: B0, B1, B2, S1-CORE, S1-API, S1-UI, S1-DEMO.
S4-CLIENT и S4-UI независимы после S4-API, но автоматический запуск других
агентов не требуется; при параллельной работе использовать разные worktree.

## Определение готовности

Карточка готова, когда её объём реализован, критерии проверены и отчёт содержит
фактические проверки. Этап принят только после DEMO-карточки и интеграции в
stage-ветку. Не скрывать провалившиеся тесты удалением safety-проверок; удаление
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
| stage/1-election | — | — | — | — | Не начат |
| stage/2-log | — | — | — | — | Не начат |
| stage/3-kv | — | — | — | — | Не начат |
| stage/4-cas-lock | — | — | — | — | Не начат |

В строке результата записывать реальный SHA проверенного кода. При изменениях
после приёмки проверки повторяются для нового SHA. Если исправление раннего
этапа требуется поздним, переносить отдельный исправляющий коммит вперёд,
проверяя каждую затронутую ветку, не переносить будущий функционал назад.

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
