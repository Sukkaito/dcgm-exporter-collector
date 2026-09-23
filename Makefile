BIN_DIR := bin
AGENT_BIN := $(BIN_DIR)/dcgm-compute-agent
CONTROL_BIN := $(BIN_DIR)/dcgm-control-service
SRC := $(shell find . -name '*.go')

.PHONY: all build build-agent build-control test clean vet docker docker-agent docker-control

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

docker: docker-agent docker-control

docker-agent:
	docker build -t dcgm-compute-agent:latest -f Dockerfile.compute-agent .

docker-control:
	docker build -t dcgm-control-service:latest -f Dockerfile.control-service .

