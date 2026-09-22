BIN_DIR := bin
BINARY := $(BIN_DIR)/dcgm-compute-agent
SRC := $(shell find . -name '*.go')

.PHONY: all build test clean vet

all: test build

build: $(BINARY)

$(BINARY): $(SRC)
	@mkdir -p $(BIN_DIR)
	go build -ldflags "-s -w" -o $(BINARY) ./cmd/dcgm-compute-agent

test:
	go test -v -race ./...

vet:
	go vet ./...

clean:
	rm -rf $(BIN_DIR)

