BINARY := bin/flinternet
ADDR ?= :8080

.PHONY: build run test vet clean

build:
	go build -o $(BINARY) ./cmd/flinternet

run: build
	./$(BINARY) -addr $(ADDR)

test:
	go test -v -race ./...

vet:
	go vet ./...

clean:
	rm -rf bin
