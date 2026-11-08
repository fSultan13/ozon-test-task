# Swagger

`make swagger` генерирует `swagger.json` и `swagger.yaml` из аннотаций `cmd/api/main.go` и HTTP-обработчиков. Версия генератора закреплена в Makefile. Генерируемые файлы хранятся в Git и проверяются на актуальность в CI.
Описаны `/healthz`, `/readyz`, `POST /links` и `GET /links/{code}`. Swagger UI в API не встраивается;
для двух тестовых стендов его раздаёт Nginx на `/swagger/`, см. [инструкцию размещения](../../deploy/demo/README.md).
