# Тестовые стенды

VPS `168.113.172.116`, Compose-проект `ozon-shortener-demo`, каталог `/opt/ozon-shortener-demo`.

| Хранилище  | API                          | Swagger UI                            |
|------------|------------------------------|---------------------------------------|
| Memory     | http://168.113.172.116:18080 | http://168.113.172.116:18080/swagger/ |
| PostgreSQL | http://168.113.172.116:18081 | http://168.113.172.116:18081/swagger/ |

Readiness — `/readyz`. Swagger UI включён в gateway; версия и SHA-256 закреплены в `images.env`.

## CI/CD через GitHub Actions

1. Публичные Docker Hub репозитории: `fsultan13/ozon-url-shortener` и `fsultan13/ozon-url-shortener-gateway`.
2. GitHub Environment `demo`: разрешить `master`, добавить `DOCKERHUB_TOKEN` (Read/Write)
   и `DEPLOY_SSH_KEY` из `~/.ssh/id_ed25519_ozon_demo_actions`.
3. После обновления `master`: **Actions → Deploy demo → Run workflow**.

Push/PR запускают только проверки. Размещение: CI → образы по digest → backup БД → миграции → обновление → проверка
API/Swagger. Тег `latest` не обновляется. SSH-ключ даёт root-доступ; ключ и пароль БД (`deploy/demo/.env` на VPS) не
добавлять в Git.

## Данные и откат

- Memory: 10 000 ссылок, очищается при рестарте. PostgreSQL: постоянный том; `down -v` удаляет данные.
- При ошибке запуска/серверной проверки скрипт пытается вернуть API/gateway. Миграции не откатываются и должны быть
  совместимы с предыдущим релизом.
- Ошибка финальной внешней проверки оставляет релиз работающим, workflow — неуспешным.
- `backups/` и `releases/` не очищаются автоматически; нехватка диска останавливает обновление до миграций.

## Проверки

`make test-demo` — локальные стенды; `make test-deploy` — ошибки размещения и откат с заглушкой Docker. Проверка VPS
создаёт по одной ссылке в каждом хранилище:

```sh
python3 scripts/check-demo.py --memory http://168.113.172.116:18080 --postgres http://168.113.172.116:18081
```
