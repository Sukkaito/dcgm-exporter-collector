BIN_DIR := bin
AGENT_BIN := $(BIN_DIR)/dcgm-compute-agent
CONTROL_BIN := $(BIN_DIR)/dcgm-control-service
SRC := $(shell find . -name '*.go')

.PHONY: all build build-agent build-control test clean vet

all: test build

build: build-agent build-control

build-agent: $(AGENT_BIN)

build-control: $(CONTROL_BIN)

$(AGENT_BIN): $(SRC)
	@mkdir -p $(BIN_DIR)
	go build -ldflags "-s -w" -o $(AGENT_BIN) ./cmd/dcgm-compute-agent

$(CONTROL_BIN): $(SRC)
	@mkdir -p $(BIN_DIR)
	go build -ldflags "-s -w" -o $(CONTROL_BIN) ./cmd/dcgm-control-service

test:
	go test -v -race ./...

vet:
	go vet ./...

clean:
	rm -rf $(BIN_DIR)

