GO ?= go

.PHONY: build run test vet docker clean

build:
	$(GO) build -o bin/neabbs ./cmd/neabbs

run: build
	./bin/neabbs

test:
	$(GO) test ./...

vet:
	$(GO) vet ./...

docker:
	docker build -t neabbs .

clean:
	rm -rf bin
