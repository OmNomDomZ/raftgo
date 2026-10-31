# Как читать lab3

Начните с README.md и docs/demo3.md. Raft зависит от интерфейсов Storage,
Transport и Clock. Memory сохраняет term, голос и лог в безопасной RAM.

PUT/POST/DELETE кодируются как `raftgo:kv:v1:` + JSON и предлагаются лидеру.
Raft реплицирует непрозрачные байты; машина KV меняется только через Consume
после commit. Ответ ждёт apply своей пары index/term. Чужой неподтверждённый
хвост может быть заменён; статус и лог локальны.

GET выполняет ReadIndex, подтверждает свежий кворум и ждёт apply барьера.
Проверки роли Leader недостаточно для чтения при изоляции старого лидера.
No-op текущего term открывает барьер чтений после выборов.

Читайте raft/handlers.go, raft/replication.go, raft/read.go, server/http.go,
server/kv.go, kv/store.go. Core-тесты используют fake-транспорт и виртуальное
время. server/kv_safety_test.go проверяет apply, изоляцию, конкурентные чтения,
replay подтверждённого префикса и запрет подделки внутренних команд /entries.
