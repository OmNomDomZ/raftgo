# Сценарии этапов 1–2

Docker Compose и Python 3.9+, только стандартная библиотека Python.

```sh
docker compose -p raftgo-scenarios -f scenarios/compose.yaml up -d --build
python3 scenarios/run.py all
# Или отдельно:
bash scenarios/task1/run.sh
bash scenarios/task2/run.sh
```

Пять узлов с Memory на портах 18001–18005; имя проекта фиксировано.
[Task1](task1/README.md) проверяет выборы, смену лидера и отсутствие лидера
у меньшинства. [Task2](task2/README.md) проверяет add, догоняющую репликацию,
сохранение подтверждённого префикса и замену конфликтного хвоста.
[Лист показа](../docs/demo2.md) включает ручной браузерный прогон UI.

Runner ожидает условия с дедлайном, печатает HTTP-коды, завершает ошибку
ненулевым кодом и выводит диагностику доступных узлов. PASS печатается после
проверок. Не управлять контейнерами параллельно. После успеха/ошибки finally
снимает паузы и восстанавливает отсоединённые сети с прежними IP/алиасами.
Task2.5 отсоединяет три приостановленные реплики перед записью хвоста,
чтобы RPC из TCP-буферов не доставили его при возврате. Новые процессы не создаются.
После SIGKILL runner восстановление сети и снятие пауз может потребовать ручного
вмешательства; в остальных случаях cleanup выполняется автоматически.

```sh
docker compose -p raftgo-scenarios -f scenarios/compose.yaml logs -f --tail=100
curl -sS http://localhost:18001/log | python3 -m json.tool
docker compose -p raftgo-scenarios -f scenarios/compose.yaml unpause
docker compose -p raftgo-scenarios -f scenarios/compose.yaml down
```

504 — неизвестный исход записи без автоматического повтора. Диагностика
локальна, статусы неатомарны. Term/голос/лог в RAM сохраняются при pause/unpause
и изоляции, но теряются при завершении процесса. Новый запуск всех процессов —
новый пустой кластер; stop/start одной ноды прежнего ID в живом кластере небезопасен.
