Swagger UI v5.34.1: `swagger-ui-bundle.js`, `swagger-ui.css`, LICENSE и NOTICE
из https://github.com/swagger-api/swagger-ui/tree/v5.34.1/dist
(лицензия и NOTICE — из корня репозитория). Apache-2.0.
Ресурсы встроены в бинарник; CDN, npm и внешний validator не используются.
`openapi.json` описывает только возможности текущего этапа. В lab1 это
диагностика выборов, в lab2 также строковый лог, в lab3 KV CRUD,
в lab4/main CRUD, CAS и lock. Файл статический: возможности последующих этапов
не включаются в ранние снимки. Номер этапа указан в `x-raftgo-stage`.
