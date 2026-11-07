# URL Shortener

Стартовый каркас Go-сервиса для [тестового задания](TASK.md). В режиме PostgreSQL приложение открывает пул и проверяет
соединение; в режиме memory база не требуется.

## Запуск

Нужен Go 1.27.3 или новее (исправления безопасности). При включённом `GOTOOLCHAIN=auto` Go загрузит требуемый toolchain
из `go.mod`. Для контейнерного запуска — Docker с Compose v2.

```sh
go run ./cmd/api -storage=memory
curl http://localhost:8080/healthz
curl http://localhost:8080/readyz
```

Оба endpoint возвращают `200` и `{"status":"ok"}`. Readiness в режиме PostgreSQL проверяет БД и возвращает `503`, если
она недоступна. `GET` также поддерживает `HEAD` по правилам `http.ServeMux`.

```sh
make up-memory
# Остановить перед запуском другого профиля: оба используют порт 8080.
make down
make up-postgres
make down
```

Compose запускает PostgreSQL только в профиле `postgres`. Данные сохраняются в named volume; `make down` их не удаляет.
Учётные данные в Compose предназначены для локальной разработки. База не публикует порт на хост. HTTP доступен на
`127.0.0.1:8080`; порт хоста меняется через `HTTP_PORT`.

При внешней БД:

```sh
DATABASE_URL='postgres://user:password@localhost:5432/shortener?sslmode=disable' \
  go run ./cmd/api -storage=postgres
```

Dockerfile использует BuildKit cache mounts для модулей Go и кеша компиляции. В builder копируются только `go.mod`,
`go.sum`, `cmd` и `internal`: изменения документации и SQL-файлов не запускают компиляцию заново. Кеш сохраняется у
текущего Docker builder; на новом CI-runner он изначально пустой. Сохранение кеша между запусками GitHub Actions
отдельно не настроено.

Параметры командной строки имеют приоритет над переменными окружения. Доступны `-storage` и `-http-addr`; `-h` выводит
справку. `.env` автоматически не загружается Go-приложением. [.env.example](.env.example) содержит пример переменных,
которые нужно передать процессу или контейнеру. Compose использует явно описанные в нём параметры.

| Переменная                 | По умолчанию | Назначение                         |
|----------------------------|--------------|------------------------------------|
| `STORAGE`                  | `memory`     | `memory` или `postgres`            |
| `HTTP_ADDR`                | `:8080`      | Адрес HTTP-сервера                 |
| `HTTP_READ_HEADER_TIMEOUT` | `5s`         | Чтение заголовков                  |
| `HTTP_READ_TIMEOUT`        | `10s`        | Чтение запроса                     |
| `HTTP_WRITE_TIMEOUT`       | `15s`        | Запись ответа                      |
| `HTTP_IDLE_TIMEOUT`        | `60s`        | Простой keep-alive                 |
| `HTTP_SHUTDOWN_TIMEOUT`    | `10s`        | Ожидание завершения запросов       |
| `HTTP_MAX_BODY_BYTES`      | `1048576`    | Лимит чтения тела запроса          |
| `LOG_LEVEL`                | `info`       | `debug`, `info`, `warn`, `error`   |
| `LOG_FORMAT`               | `json`       | `json` или `text`                  |
| `DATABASE_URL`             | —            | Обязательна для PostgreSQL         |
| `DATABASE_MAX_CONNS`       | `10`         | Максимальный размер пула           |
| `DATABASE_CONNECT_TIMEOUT` | `5s`         | Таймаут подключения и startup ping |

## Архитектура

Разделение пакетов. API использует `net/http`, без других HTTP-фреймворков.

```text
cmd/api/                         точка входа, сигналы завершения
internal/app/                    сборка зависимостей, владение ресурсами
  resources.go                   создание, readiness и закрытие зависимостей
  server.go                      настройка, запуск и остановка HTTP-сервера
internal/platform/
  config/                        конфигурация и валидация
  logger/                        структурированные slog-логи
  postgres/                      создание и проверка pgx-пула
internal/transport/http/
  router.go                      создание mux и подключение middleware
  health_routes.go               регистрация маршрутов liveness и readiness
  handler/                       общие health-обработчики
  middleware/                    request ID, access log, recovery, лимит тела
internal/shortener/
  domain/                        сущности и инварианты
  ports/                         интерфейсы зависимостей
  application/                   сценарии использования
  adapters/http/                 HTTP-обработчики
  adapters/memory/               конкурентное хранилище
  adapters/postgres/             PostgreSQL-репозиторий
db/migrations/                   миграции
db/query/shortener/              sqlc-запросы
docs/swagger/                    генерируемая API-спецификация
```

Каждый запрос получает серверный `X-Request-ID`, запись о статусе и длительности в stdout. URL и query string в access
log не попадают. Recovery возвращает `500` без внутренних деталей; если ответ уже начат, соединение прерывается.
Middleware ограничивает чтение тела через `http.MaxBytesReader`; будущие обработчики должны преобразовывать
`http.MaxBytesError` в `413`. CORS, авторизация и rate limiting требуют отдельных требований.

При SIGINT/SIGTERM сервер перестаёт принимать соединения, закрывает простаивающие соединения и ждёт завершения активных
запросов в пределах shutdown timeout. Если время истекло, оставшиеся HTTP-соединения принудительно закрываются, а
контексты их запросов отменяются. Затем освобождается пул БД. Такой же порядок остановки выполняется при ошибке
listener. Принудительное закрытие соединения не останавливает произвольный Go-код: обработчики и операции БД должны
учитывать context. 

## Разработка

```sh
make fmt
make vet
make test
make build
make swagger
```

`make swagger` использует закреплённую версию Swag и генерирует Swagger 2.0 JSON/YAML из Go-аннотаций. В спецификации
присутствуют только работающие health-endpoint. Для первого запуска генератора нужен доступ к Go proxy. Swagger UI в
runtime не подключён.

Тесты проверяют ошибки конфигурации, приоритет параметров, health/readiness и поведение middleware, включая recovery и
ограничение тела.

## Качество и производительность

```sh
make check     # форматирование, vet, lint, race, checkptr, vuln, build
make fuzz      # 10 секунд fuzzing конфигурации
make bench     # конкурентный middleware benchmark, время и аллокации, 5 прогонов
make profile   # CPU и heap профили в bin/
go tool pprof bin/cpu.pprof
```

golangci-lint запускает `govet`, `staticcheck`, `errcheck`, `ineffassign`, `unused`, `gosec`, `bodyclose`,
`durationcheck` и `nilerr`. Версии golangci-lint и govulncheck закреплены в Makefile. Линтер собирается текущим Go,
чтобы избежать несовместимости готового бинарника с Go проекта. Для первого запуска инструментов и проверки базы
уязвимостей требуется сеть.

Race detector выявляет гонки на выполненных тестовых путях, `checkptr` проверяет корректность преобразований указателей.
ASan/MSan не включены: проект работает без cgo; эти инструменты требуют отдельных поддерживаемых платформ и C-toolchain
и становятся полезны при появлении нативных зависимостей. Ни одна из проверок не доказывает отсутствие всех ошибок.

CI выполняет `make check`, fuzzing, проверяет актуальность Swagger, валидирует Compose и собирает Docker-образ.

`make test-docker` проверяет оба профиля в отдельном Compose-проекте: readiness, liveness, потерю и восстановление
PostgreSQL и завершение по SIGTERM. Тест удаляет свои контейнеры и тестовый volume при выходе. Порт хоста должен быть
свободен; при необходимости задайте `HTTP_PORT=18080`. Docker CLI можно указать через
`make DOCKER=/path/to/docker test-docker` (каталог CLI и credential helper должен быть в `PATH`). Эти же контейнерные
тесты выполняются в CI.

Ориентир — высокая пропускная способность и предсказуемое потребление ресурсов.