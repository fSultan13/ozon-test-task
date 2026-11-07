# URL Shortener

Go-сервис для [тестового задания](TASK.md). Реализованы домен, application и генератор кодов.
HTTP API сокращения ссылок и адаптеры хранения пока не подключены. Доступны `GET /healthz` и `GET /readyz`;
в режиме PostgreSQL readiness проверяет соединение с БД.

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

Профили запускаются по очереди: оба используют порт 8080. `make down` сохраняет данные PostgreSQL.
Для внешней БД:

```sh
DATABASE_URL='postgres://user:password@localhost:5432/shortener?sslmode=disable' \
  go run ./cmd/api -storage=postgres
```

Настройки описаны в [.env.example](.env.example). Приложение не загружает `.env` автоматически.
Флаги `-storage` и `-http-addr` имеют приоритет над переменными окружения; `-h` выводит справку.

## Домен и генерация

- Исходный URL — абсолютный `http`/`https` с непустым хостом, не больше 8000 байт; без неэкранированных пробелов,
  управляющих символов и некорректного экранирования. Сохраняется без нормализации; DNS и доступность не проверяются.
- Доменный код — от 1 до 10 символов `[a-zA-Z0-9_]`; генератор выдаёт ровно 10.
- Потокобезопасный генератор использует `crypto/rand`, выбирая все 63 символа равновероятно.
- `Shorten` возвращает существующую связь или создаёт новую; `Resolve` находит исходную ссылку по коду.
- Хранилище обязано атомарно обеспечивать уникальность URL и кода. При коллизии кода — до 10 попыток генерации.
- Application передаёт context в зависимости и ограничивает сценарий общим таймаутом.

8000 байт — выбранный лимит сервиса на основе рекомендации [RFC 9110 §4.1](https://www.rfc-editor.org/rfc/rfc9110.html#section-4.1),
а не максимальная длина URL, установленная стандартом. Memory- и PostgreSQL-адаптеры хранения ещё предстоит реализовать.

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
```

## Проверки

```sh
make check        # форматирование, vet, lint, race, checkptr, vuln, сборка
make fuzz         # fuzzing конфигурации
make bench        # валидация URL, генератор и middleware, время и аллокации
make profile      # CPU/heap-профили middleware в bin/
make test-docker  # проверка обоих Compose-профилей
make swagger      # спецификация из аннотаций работающих обработчиков
```
