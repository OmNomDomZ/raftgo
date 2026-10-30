# Сценарий этапа 1

Пять реальных процессов, выборы и heartbeat. Стенд отделён от основного:
проект `raftgo-scenarios`, API `localhost:18001`–`localhost:18005`.

```sh
docker compose -p raftgo-scenarios -f scenarios/compose.yaml up -d --build
python3 scenarios/run.py all
# То же: bash scenarios/task1/run.sh
```

Runner проверяет согласие пяти узлов о лидере и term, новые выборы после
паузы лидера, возвращение прежнего лидера и невозможность выборов в меньшинстве
из двух узлов. Наблюдения локальны и не доказывают Election Safety для всех
возможных исполнений: это свойство дополнительно проверяют тесты ядра.
`all` запускает только доступный task1. `PASS task1` означает успешные проверки
runner; при ошибке выводится диагностика, код завершения ненулевой.

Отказы используют pause/unpause с сохранением RAM. Runner снимает паузы
в finally, включая частично выполненную Docker-команду. После SIGKILL снимите
паузы вручную. Не управляйте узлами параллельно runner.

```sh
docker compose -p raftgo-scenarios -f scenarios/compose.yaml unpause
docker compose -p raftgo-scenarios -f scenarios/compose.yaml down
```

`down` всех процессов уничтожает память; следующий `up` — пустой кластер.
Stop/start отдельного узла с пустым Storage не является безопасным возвратом.
UI: http://localhost:18001/ (либо порт любого доступного узла).
Лист ручного показа — [task1/README.md](task1/README.md).
