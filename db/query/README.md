# SQL-запросы

Запросы принадлежат модулю: `shortener/*.sql`. При реализации PostgreSQL-адаптера добавить `sqlc.yaml` с `sql_package: pgx/v5`, схемой из `db/migrations` и выходом в `internal/shortener/adapters/postgres/sqlc`.
До появления схемы и запросов генерация sqlc не подключается.
