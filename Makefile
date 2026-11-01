.PHONY: build build-pr1 build-pr2 linux test up up-pr1 pr1 ready demo

build: build-pr1 build-pr2

build-pr1:
	mkdir -p bin
	go build -o bin/pr1 ./cmd/pr1

build-pr2:
	mkdir -p bin
	go build -o bin/pr2 ./cmd/pr2

linux:
	mkdir -p bin
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o bin/pr2-linux ./cmd/pr2

test:
	go test ./...

up:
	docker compose -f internal/pr2/compose.yaml up -d

up-pr1:
	docker compose -f internal/pr1/docker-compose.yaml up -d

pr1: build-pr1
	$(MAKE) up-pr1
	bash internal/pr1/scripts/wait-ready.sh
	./bin/pr1 localhost 9870 root

ready:
	bash internal/pr2/scripts/wait-ready.sh

demo: build linux
	bash internal/pr2/scripts/demo.sh
