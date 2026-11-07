# Swagger

`make swagger` генерирует `swagger.json` и `swagger.yaml` из аннотаций `cmd/api/main.go` и HTTP-обработчиков. Версия генератора закреплена в Makefile. Генерируемые файлы хранятся в Git и проверяются на актуальность в CI.
В каркасе описаны только `/healthz` и `/readyz`. Swagger UI в API не встраивается.
