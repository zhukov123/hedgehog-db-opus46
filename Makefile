.PHONY: build clean test run dev lint

BUILD_DIR = ./bin

build:
	@mkdir -p $(BUILD_DIR)
	go build -o $(BUILD_DIR)/hedgehogdb ./cmd/hedgehogdb
	go build -o $(BUILD_DIR)/hedgehogctl ./cmd/hedgehogctl
	go build -o $(BUILD_DIR)/trafficgen ./cmd/trafficgen
	@echo "Built binaries in $(BUILD_DIR)/"

clean:
	rm -rf $(BUILD_DIR) data/

test:
	go test -v -count=1 ./...

test-race:
	go test -v -race -count=1 ./...

run: build
	$(BUILD_DIR)/hedgehogdb

dev:
	go run ./cmd/hedgehogdb -node-id node-1 -bind 0.0.0.0:8080 -data-dir ./data/node1

cluster: build
	@bash scripts/start-cluster.sh

lint:
	go vet ./...

bench:
	go test -bench=. -benchmem ./internal/storage/...

seed: build
	@bash scripts/seed-data.sh
