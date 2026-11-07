# URL Shortener

Go-сервис для [тестового задания](TASK.md). Реализованы домен, application, генератор кодов и PostgreSQL-адаптер. HTTP
API сокращения ссылок и memory-адаптер пока не реализованы. В режиме PostgreSQL адаптер хранения собирается в `app`.
Доступны `GET /healthz` и `GET /readyz`; в режиме PostgreSQL readiness проверяет соединение с БД.

## Запуск

Нужен Go 1.27.3 или новее. Для контейнерного запуска — Docker с Compose v2.

```sh
go run ./cmd/api -storage=memory
curl http://localhost:8080/healthz
curl http://localhost:8080/readyz
```

Через Docker Compose:

```sh
make up-memory
make down
make up-postgres
make down
```

Профили запускаются по очереди: оба используют порт 8080. `make down` сохраняет данные PostgreSQL. Для внешней БД
сначала примените миграции:

```sh
export DATABASE_URL='postgres://user:password@localhost:5432/shortener?sslmode=disable'
make migrate-up
go run ./cmd/api -storage=postgres
```

Настройки описаны в [.env.example](.env.example). Приложение не загружает `.env` автоматически. Флаги `-storage` и
`-http-addr` имеют приоритет над переменными окружения; `-h` выводит справку.

## Домен и генерация

- Исходный URL — абсолютный `http`/`https` с непустым хостом, не больше 8000 байт; без неэкранированных пробелов,
  управляющих символов и некорректного экранирования. Сохраняется без нормализации; DNS и доступность не проверяются.
- Доменный код — от 1 до 10 символов `[a-zA-Z0-9_]`; генератор выдаёт ровно 10.
- Потокобезопасный генератор использует `crypto/rand`, выбирая все 63 символа равновероятно.
- `Shorten` возвращает существующую связь или создаёт новую; `Resolve` находит исходную ссылку по коду.
- Хранилище обязано атомарно обеспечивать уникальность URL и кода. При коллизии кода — до 10 попыток генерации.
- Application передаёт context в зависимости и ограничивает сценарий общим таймаутом.

8000 байт — выбранный лимит сервиса на основе
рекомендации [RFC 9110 §4.1](https://www.rfc-editor.org/rfc/rfc9110.html#section-4.1), а не максимальная длина URL,
установленная стандартом.

## Архитектура

HTTP — стандартный `net/http`. Зависимости связываются явно в `app`.

```text
cmd/api/                        точка входа
internal/app/                   сборка зависимостей и жизненный цикл
internal/platform/              конфигурация, логирование, пул PostgreSQL
internal/transport/http/        роутер, health, middleware
internal/shortener/
  domain/                       сущности и инварианты
  ports/                        контракты хранения и генерации
  application/                  Shorten и Resolve
  adapters/random/              генератор кодов
  adapters/postgres/            адаптер Storage и сгенерированный sqlc-код
db/migrations/                  миграции golang-migrate
db/query/shortener/              SQL-запросы для sqlc
```

## Проверки

```sh
make check        # форматирование, vet, lint, race, checkptr, vuln, сборка
make fuzz         # fuzzing конфигурации
make bench        # валидация URL, генератор и middleware, время и аллокации
make profile      # CPU/heap-профили middleware в bin/
make test-docker  # проверка обоих Compose-профилей и запуска миграций
make test-postgres # изолированная PostgreSQL 17: up/down/up, интеграционные тесты с race
make bench-postgres # параллельные resolve/save-existing с настоящей БД
make sqlc         # генерация SQL-кода, sqlc v1.30.0
make swagger      # спецификация из аннотаций работающих обработчиков
```

## PostgreSQL

Таблица `links` хранит исходный URL и код. На обоих полях созданы HASH-индексы для поиска. Уникальность обеспечивают
PRIMARY KEY кода и B-tree-индекс SHA-256 URL через `pgcrypto`. Хеш позволяет индексировать длинные URL; при его
совпадении адаптер проверяет полную строку.

`Save` атомарно вставляет связь через `ON CONFLICT DO NOTHING`. При конфликте возвращает существующую связь для URL либо
ошибку занятого кода. Конкурентные записи защищены ограничениями БД. Изменение и удаление связей не предусмотрены.

`DATABASE_MAX_CONNS` ограничивает пул соединений (по умолчанию 10), `DATABASE_OPERATION_TIMEOUT` — время операции вместе
с ожиданием соединения и блокировок (3s). Более ранний deadline контекста сохраняется. Пул закрывается после остановки
HTTP-сервера.

`make test-postgres` запускает тесты в отдельном временном контейнере. Для ручного запуска нужен `TEST_DATABASE_URL`
отдельной БД с применёнными миграциями: тесты очищают `links`. Без переменной интеграционные тесты пропускаются.
Бенчмарки измеряют работу адаптера, а не пропускную способность HTTP API.