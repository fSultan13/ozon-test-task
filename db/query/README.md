# SQL-запросы

Запросы модуля — `shortener/*.sql`, схема — `db/migrations`.
`make sqlc` генерирует pgx/v5-код в `internal/shortener/adapters/postgres/sqlc`.
Версия генератора закреплена в Makefile; сгенерированный код не редактируется вручную.
