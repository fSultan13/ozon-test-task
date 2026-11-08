# URL Shortener

Go-сервис для [тестового задания](TASK.md): создание коротких ссылок и получение исходных URL. Хранилище выбирается при
запуске: Memory или PostgreSQL. HTTP — стандартный `net/http`.

## Тестовые стенды

| Хранилище  | API                                                   | Swagger UI                                       |
|------------|-------------------------------------------------------|--------------------------------------------------|
| Memory     | [168.113.172.116:18080](http://168.113.172.116:18080) | [Swagger](http://168.113.172.116:18080/swagger/) |
| PostgreSQL | [168.113.172.116:18081](http://168.113.172.116:18081) | [Swagger](http://168.113.172.116:18081/swagger/) |

Readiness — `/readyz`. Memory: до 10 000 ссылок, данные теряются при рестарте. PostgreSQL сохраняет данные в
томе. [Размещение и CI/CD](deploy/demo/README.md).

## Запуск

Нужен Go 1.27.3+ или Docker с Compose v2. Настройки — [.env.example](.env.example). Приложение не читает `.env`; Compose
читает. Флаги `-storage` и `-http-addr` имеют приоритет над окружением.

Из исходников:

```sh
PUBLIC_BASE_URL=http://localhost:8080 go run ./cmd/api -storage=memory
```

Готовый образ (`linux/amd64`, `linux/arm64`):

```sh
docker run --rm -p 127.0.0.1:8080:8080 \
  -e PUBLIC_BASE_URL=http://localhost:8080 \
  fsultan13/ozon-url-shortener:latest -storage=memory
```

Compose из исходников: скопируйте `.env.example` в `.env`, затем выберите один профиль:

```sh
make up-memory
make down
make up-postgres
```

Для готового образа: `docker compose --profile memory up --no-build --pull always -d`
или профиль `postgres`. Профили используют один порт 8080; `make down` сохраняет том БД. Для внешней БД задайте
`DATABASE_URL`, выполните `make migrate-up` и запустите с `-storage=postgres`.

## HTTP API

`PUBLIC_BASE_URL` обязателен: HTTP (S)-адрес без credentials, query и fragment. Завершающие `/` удаляются; префикс пути
должен снимать reverse proxy.

```sh
curl -X POST http://localhost:8080/links \
  -H 'Content-Type: application/json' -d '{"url":"https://example.com"}'
# 200 {"short_url":"http://localhost:8080/links/Abc123_Xyz"}
curl http://localhost:8080/links/Abc123_Xyz
# 200 {"url":"https://example.com"}
```

Повторный POST возвращает ту же ссылку. GET возвращает JSON без редиректа. POST принимает один JSON-объект с
единственным полем `url`; лишние/повторные поля, некорректный JSON, Unicode и URL отклоняются. Требуется
`application/json`, параметры допустимы. Лимит тела — `HTTP_MAX_BODY_BYTES` (по умолчанию 1 MiB).

Ошибки: `{"code":"invalid_request","message":"Invalid request"}`.

| HTTP | code                   | message                |
|------|------------------------|------------------------|
| 400  | invalid_request        | Invalid request        |
| 404  | not_found              | Link not found         |
| 413  | payload_too_large      | Payload too large      |
| 415  | unsupported_media_type | Unsupported media type |
| 500  | internal_error         | Internal server error  |
| 503  | service_unavailable    | Service unavailable    |
| 504  | timeout                | Operation timed out    |

503 — заполнение Memory или исчерпание попыток генерации кода. Неизвестные маршруты и методы — стандартные 404/405
ServeMux. Проверки состояния: `/healthz`, `/readyz` (в PostgreSQL проверяет БД). Swagger генерируется командой
`make swagger` в `docs/swagger`.

## Хранение и генерация

- URL: абсолютный HTTP (S), непустой хост, до 8000 байт; без нормализации и проверки DNS.
- Генератор: `crypto/rand`, 10 равновероятных символов `[a-zA-Z0-9_]`; до 10 попыток при коллизии.
- URL и код уникальны; конкурентное сохранение атомарно.
- `SHORTENER_OPERATION_TIMEOUT` ограничивает сценарий целиком (по умолчанию 3 с).
- Memory: два `map` под `sync.RWMutex`, данные одного процесса. `MEMORY_MAX_ENTRIES` — 100 000 по умолчанию. При
  заполнении существующие ссылки доступны; TTL, удаления и вытеснения нет. Лимит записей не ограничивает RSS.
- PostgreSQL: `links`, PRIMARY KEY кода, уникальный SHA-256 URL и HASH-индексы поиска. При совпадении хеша проверяется
  полный URL. `Save` использует `ON CONFLICT DO NOTHING`.
  `DATABASE_MAX_CONNS` — 10, `DATABASE_OPERATION_TIMEOUT` — 3 с, включая ожидание соединения и блокировок.

## Архитектура

```text
cmd/api/                         точка входа
internal/app/                    зависимости и lifecycle
internal/platform/               конфигурация, логирование, пул БД
internal/transport/http/          роутер, health, middleware
internal/shortener/domain/       типы и инварианты
internal/shortener/ports/        контракты зависимостей
internal/shortener/application/  сценарии Shorten и Resolve
internal/shortener/adapters/     HTTP, random, memory, postgres
db/migrations/                   миграции
db/query/shortener/              SQL для sqlc
```

Ветка `feat/alternative-storage-with-rom` содержит дополнительный вариант bbolt с кешем. Он сохраняет данные на диске и
не заменяет требуемое заданием хранилище в памяти.

## Нагрузочные тесты

[Установка и запуск Locust](tools/loadtest/README.md). Профиль: 95% GET / 5% повторных POST. Отчёты VPS от 8 ноября 2026
года:

- 30 с, 10 клиентов: [Memory](tools/loadtest/reports/2026-11-08-vps-memory.md),
  [PostgreSQL](tools/loadtest/reports/2026-11-08-vps-postgres.md).
- [Ступени 10 → 25 → 50 → 100 клиентов и 10 минут при 50 клиентах](tools/loadtest/reports/2026-11-08-vps-step-endurance/summary.md).
- [Последний одиночный запуск](tools/loadtest/reports/latest.md).

## Проверки

| Команда                                | Проверка                                                       |
|----------------------------------------|----------------------------------------------------------------|
| `make check`                           | форматирование, vet, lint, race, checkptr, govulncheck, сборка |
| `make fuzz`                            | fuzzing конфигурации                                           |
| `make bench` / `make bench-postgres`   | микробенчмарки с `-benchmem`, 5 повторов                       |
| `make profile` / `make profile-memory` | CPU/heap-профили в `bin/`                                      |
| `make test-postgres`                   | миграции и интеграционные тесты в отдельной PostgreSQL         |
| `make test-docker` / `make test-demo`  | Compose-профили и тестовые стенды                              |
| `make test-deploy`                     | ошибки размещения и откат с заглушкой Docker                   |
| `make fmt` / `make lint-fix`           | исправление форматирования / замечаний линтера                 |
| `make sqlc` / `make swagger`           | генерация SQL-кода / спецификации API                          |

HTTP Client: [основные сценарии](tests/http/shortener.http) и [лимит Memory](tests/http/memory-capacity.http).
Выполняйте Run All Requests; задайте `baseUrl` и `e2ePublicBaseUrl`. Для проверки лимита нужен новый Memory-процесс с
`MEMORY_MAX_ENTRIES=1`.

Ручные интеграционные тесты требуют `TEST_DATABASE_URL` отдельной БД с миграциями: тесты очищают `links`. Без переменной
они пропускаются. Микробенчмарки не определяют HTTP RPS или число пользователей.
