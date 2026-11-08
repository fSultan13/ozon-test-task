# URL Shortener

## Тестовые стенды

Для удобного тестирования проект развёрнут на двух стендах — с хранением в памяти и в PostgreSQL.
API и Swagger UI доступны по следующим адресам:

| Режим      | API / `PUBLIC_BASE_URL`             | Swagger UI                                          | Readiness                                        |
|------------|-------------------------------------|-----------------------------------------------------|--------------------------------------------------|
| Memory     | [168.113.172.116:18080](http://168.113.172.116:18080) | [Swagger UI](http://168.113.172.116:18080/swagger/) | [Readiness](http://168.113.172.116:18080/readyz) |
| PostgreSQL | [168.113.172.116:18081](http://168.113.172.116:18081) | [Swagger UI](http://168.113.172.116:18081/swagger/) | [Readiness](http://168.113.172.116:18081/readyz) |

Swagger UI предназначен для отправки запросов к соответствующему API прямо из браузера. Данные memory-стенда теряются
при перезапуске; данные PostgreSQL сохраняются в постоянном томе. Лимит memory-стенда — 10 000 ссылок.
Конфигурация и команды управления описаны в [инструкции размещения](deploy/demo/README.md).
Для обновления через Docker Hub предусмотрен GitHub Actions workflow **Deploy demo**, запускаемый вручную через
**Run workflow** после настройки [секретов CI/CD](deploy/demo/README.md#cicd-через-github-actions).

Go-сервис для [тестового задания](TASK.md). Реализованы домен, application, генератор кодов, memory- и PostgreSQL- и
HTTP-адаптеры. Адаптер хранения выбирается в `app`. Доступны
`GET /healthz` и `GET /readyz`; в режиме PostgreSQL readiness проверяет соединение с БД.

## Альтернативная реализация хранения

Ветка `feat/alternative-storage-with-rom` сохраняет вариант с файловой БД bbolt и ограниченным кешем в оперативной
памяти. Она выделена для сравнения подходов: данные могут превышать размер ram и сохраняются после перезапуска, но
чтение при промахе кеша и запись требуют обращения к файловой БД.

Этот вариант отличается от требуемого в задании самостоятельного хранилища в памяти приложения: хранение данных и
транзакционную уникальность обеспечивает bbolt, самостоятельно реализованы адаптер и кеш. Он представлен как
дополнительный путь решения, а не как выполнение пункта про in-memory-хранилище.

Посмотреть реализацию после получения ветки:

```sh
git switch feat/alternative-storage-with-rom
```

## Запуск

Нужен Go 1.27.3 или новее. Для контейнерного запуска — Docker с Compose v2.

### Готовый образ из Docker Hub

Публичный образ: [fsultan13/ozon-url-shortener](https://hub.docker.com/r/fsultan13/ozon-url-shortener). Поддерживает
`linux/amd64` и `linux/arm64`; Docker выбирает архитектуру автоматически.

```sh
docker run --rm --name url-shortener \
  -p 127.0.0.1:8080:8080 \
  -e PUBLIC_BASE_URL=http://localhost:8080 \
  fsultan13/ozon-url-shortener:latest -storage=memory
```

Проверка: `curl http://localhost:8080/readyz`. Данные memory теряются при остановке контейнера. Для запуска готового
образа через Compose из клонированного репозитория:

Compose читает настройки из `.env`. Если файла ещё нет, скопируйте `.env.example` в `.env`.

```sh
docker compose --profile memory up --no-build --pull always -d
# Или PostgreSQL с автоматическим применением миграций:
docker compose --profile memory --profile postgres down
docker compose --profile postgres up --no-build --pull always -d
```

### Запуск из исходников

```sh
export PUBLIC_BASE_URL=http://localhost:8080
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

Профили запускаются по очереди: оба используют порт 8080. `make down` сохраняет том PostgreSQL; данные memory теряются
при остановке процесса. Для внешней БД сначала примените миграции:

```sh
export PUBLIC_BASE_URL=http://localhost:8080
export DATABASE_URL='postgres://user:password@localhost:5432/shortener?sslmode=disable'
make migrate-up
go run ./cmd/api -storage=postgres
```

Настройки описаны в [.env.example](.env.example). Приложение не загружает `.env` автоматически. Флаги `-storage` и
`-http-addr` имеют приоритет над переменными окружения; `-h` выводит справку.

## HTTP API

Обязательная переменная `PUBLIC_BASE_URL` задаёт публичный HTTP (S)-адрес без credentials, query и fragment. Завершающие
`/` удаляются. Host и proxy-заголовки запроса не используются для формирования ссылок. Если адрес содержит префикс пути
(например, `https://short.example/api`), reverse proxy должен удалять
`/api` перед передачей запроса приложению: внутренние маршруты остаются `/links` и `/links/{code}`.
`SHORTENER_OPERATION_TIMEOUT` ограничивает сценарий для обоих хранилищ (по умолчанию `3s`).

```sh
curl -X POST http://localhost:8080/links \
  -H 'Content-Type: application/json' \
  -d '{"url":"https://example.com"}'
# 200 {"short_url":"http://localhost:8080/links/Abc123_Xyz"}
curl http://localhost:8080/links/Abc123_Xyz
# 200 {"url":"https://example.com"}
```

Повторный POST возвращает ту же ссылку с `200`. GET возвращает JSON, без редиректа. POST принимает ровно один
JSON-объект с единственным строковым полем `url`; неизвестные и повторные поля, лишний JSON и невалидные URL
отклоняются. Поддерживается `application/json` с параметрами, включая `charset`. Размер тела ограничен
`HTTP_MAX_BODY_BYTES` (по умолчанию 1 MiB).

Ошибки бизнес-обработчиков имеют формат `{"code":"invalid_request","message":"Invalid request"}`:

| Статус | code                   | message                |
|--------|------------------------|------------------------|
| 400    | invalid_request        | Invalid request        |
| 404    | not_found              | Link not found         |
| 413    | payload_too_large      | Payload too large      |
| 415    | unsupported_media_type | Unsupported media type |
| 500    | internal_error         | Internal server error  |
| 503    | service_unavailable    | Service unavailable    |
| 504    | timeout                | Operation timed out    |

`503` возвращается при заполнении memory-хранилища или исчерпании попыток генерации кода. Неожиданные ошибки
зависимостей дают `500` без внутренних подробностей. Неизвестные маршруты и неподдерживаемые методы используют
стандартные ответы ServeMux (`404` и `405`). Health-маршруты сохраняют прежний формат. Swagger:
`docs/swagger/swagger.yaml` и `docs/swagger/swagger.json`; обновление — `make swagger`.

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
  adapters/http/                JSON API сокращения и разрешения ссылок
  adapters/random/              генератор кодов
  adapters/memory/              два индекса в RAM, общий sync.RWMutex, лимит записей
  adapters/postgres/            адаптер Storage и сгенерированный sqlc-код
db/migrations/                  миграции golang-migrate
db/query/shortener/              SQL-запросы для sqlc
```

## Проверки

### Нагрузочный тест

Python-утилита на Locust подключается к уже запущенному сервису. Установка и запуск из корня проекта:

```sh
python3 -m venv tools/loadtest/.venv
tools/loadtest/.venv/bin/python -m pip install -r tools/loadtest/requirements.txt
tools/loadtest/.venv/bin/python tools/loadtest/run.py --base-url http://localhost:8080 --label local-memory
tools/loadtest/.venv/bin/python tools/loadtest/run.py --base-url https://short.example/api --label hosted-postgres
```

Профиль — 95% GET и 5% повторных POST. Подготовка создаёт до 100 записей по умолчанию и переиспользует их при повторных
запусках; измеряемая фаза не добавляет новых адресов. Нужен Python 3.11+. Настройки: `--duration` (30 с),
`--concurrency` (10 пользователей Locust), `--timeout` (3 с на I/O), `--dataset-size` (100),
`--max-requests` (100000), `--label`. Любая ошибка означает провал; порогов RPS и задержки нет.

[Краткий отчёт последнего запуска](tools/loadtest/reports/latest.md) обновляется после каждого теста; история
сохраняется отдельными файлами. [Подробности запуска и метрик](tools/loadtest/README.md). Результат относится к
конкретному стенду, сети и Locust-клиенту и не оценивает число пользователей.

### E2E-сценарии в HTTP Client

Откройте файл в GoLand / IntelliJ IDEA и выполните **Run All Requests** по порядку:

- [`shortener.http`](tests/http/shortener.http) — создание и получение ссылок, повторный POST, уникальность, валидация и
  HTTP-ошибки. Работает с memory и PostgreSQL при `HTTP_MAX_BODY_BYTES=1048576`; каждый прогон сохраняет две новые
  связи.
- [`memory-capacity.http`](tests/http/memory-capacity.http) — заполнение хранилища, ошибка 503 и доступность
  существующих ссылок. Перед каждым прогоном нужен новый memory-процесс с `MEMORY_MAX_ENTRIES=1`.

В файлах `baseUrl` задаёт адрес API; `e2ePublicBaseUrl` в начальном скрипте `shortener.http` должен совпадать с
`PUBLIC_BASE_URL` без завершающего `/`. По умолчанию используется `http://localhost:8080`.

### Автоматические проверки

```sh
make check        # форматирование, vet, lint, race, checkptr, vuln, сборка
make fmt          # форматирование gofumpt и goimports; sqlc-код исключён
make fmt-check    # проверка форматирования без изменения файлов
make lint         # статический анализ без изменения файлов
make lint-fix     # автоматические исправления линтеров; проверить diff после запуска
make fuzz         # fuzzing конфигурации
make bench        # memory storage, валидация URL, генератор и middleware, время и аллокации
make profile      # CPU/heap-профили middleware в bin/
make profile-memory # CPU/heap-профили memory-адаптера в bin/
make test-docker  # проверка двух Compose-профилей, readiness и миграций
make test-demo    # оба стенда со Swagger, memory restart и сохранение PostgreSQL
make test-deploy  # сценарии ошибок размещения и отката приложения (Docker CLI заменён тестовой заглушкой)
make test-postgres # изолированная PostgreSQL 17: up/down/up, интеграционные тесты с race
make bench-postgres # параллельные resolve/save-existing с настоящей БД
make sqlc         # генерация SQL-кода, sqlc v1.30.0
make swagger      # спецификация из аннотаций работающих обработчиков
```

## Memory: хранение в оперативной памяти

`-storage=memory` использует самостоятельно реализованное хранилище: два `map` для поиска по исходному URL и коду.
Файловая БД и PostgreSQL в этом режиме не используются. Данные принадлежат одному процессу и теряются при перезапуске;
несколько экземпляров приложения имеют независимые хранилища.

```sh
MEMORY_MAX_ENTRIES=100000 go run ./cmd/api -storage=memory
```

- Оба индекса защищены одним `sync.RWMutex`: чтения могут выполняться параллельно, запись меняет оба индекса атомарно.
- Повторный URL возвращает прежний код, включая случай, когда предложенный код уже занят другой ссылкой.
- `MEMORY_MAX_ENTRIES` — положительный лимит связей, по умолчанию 100000. При заполнении новые ссылки отклоняются с
  `ports.ErrCapacity`; существующие доступны для чтения и повторного сохранения. Удаления, TTL и вытеснения нет.
- Карты растут по мере записи. Лимит записей не является лимитом RSS: расход зависит от длины URL (до 8000 байт),
  накладных расходов карт и сборщика мусора. Лимит следует подбирать под доступную память по замерам с запасом.
- Критические секции не выполняют I/O. Контекст проверяется до и после захвата блокировки; ожидание `RWMutex`
  не прерывается отменой контекста. Общий таймаут сценария задаётся в application.
- При остановке `Close` освобождает ссылки на данные и запрещает дальнейшие операции.

`make bench` измеряет параллельный поиск по обоим индексам, повторное сохранение, смесь 95% чтений и 5% повторных
сохранений, а также вставку пачки из 1000 новых связей восемью конкурентными работниками с чтением после вставки. Это
микробенчмарки адаптера, а не оценка HTTP RPS или числа пользователей. Для дальнейшей оптимизации нужны согласованный
профиль нагрузки, повторные замеры и CPU/heap-профили (`make profile-memory`).

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
