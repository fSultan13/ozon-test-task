# Миграции

Файлы: `000001_<name>.up.sql` / `.down.sql`. Runner — golang-migrate v4.18.3.
`DATABASE_URL=... make migrate-up`; `make migrate-down` удаляет таблицу с данными. Compose применяет миграции до запуска
API.

Первая миграция создаёт `links` и `pgcrypto` (нужны права CREATE); откат оставляет расширение. PRIMARY KEY кода и
уникальный SHA-256 URL обеспечивают уникальность, HASH-индексы — поиск. Коллизия SHA-256 возвращает ошибку сохранения.
