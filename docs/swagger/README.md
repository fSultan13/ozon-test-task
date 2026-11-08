# Swagger

`make swagger` генерирует `swagger.json` / `swagger.yaml` из аннотаций `cmd/api/main.go` и HTTP-обработчиков. Файлы
хранятся в Git; актуальность проверяет CI. Маршруты: `/healthz`, `/readyz`, `POST /links`, `GET /links/{code}`. Swagger
UI раздаёт gateway на `/swagger/`: [размещение](../../deploy/demo/README.md).
