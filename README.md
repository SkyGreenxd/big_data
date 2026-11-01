# Практические работы по большим данным

Один Go-модуль `github.com/SkyGreenxd/bigdata` для обеих работ. Реализация находится в `internal`,
точки входа — в `cmd`, общие пакеты — в `pkg`.

```text
go.mod
cmd/
  pr1/main.go       WebHDFS CLI
  pr2/main.go       генерация, MapReduce и рекомендации
internal/
  pr1/              первая работа: код, документация и Docker Compose
  pr2/              вторая работа: код, тесты, данные и окружение Hadoop
pkg/
  webhdfs/          общий клиент HDFS
```

Из корня проекта:

```bash
make test
make build linux
make pr1
./bin/pr2 generate --out internal/pr2/data
```

Без предварительной сборки доступны `go run ./cmd/pr1` и `go run ./cmd/pr2`
с соответствующими аргументами. Все пакеты проверяются командой `go test ./...`.

Документация: [первая работа](internal/pr1/README.md),
[вторая работа](internal/pr2/README.md), [план второй работы](internal/pr2/plan.md).

Полная демонстрация второй работы:

```bash
make demo
```

Для неё нужен работающий Docker. Результаты сборки находятся в `bin/`;
локальные данные и логи демонстрации — в `internal/pr2/runs/`.
