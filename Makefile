.PHONY: run test build clean

ROUTER_ADDR ?= http://192.168.119.1
PASSWORD ?= passw0rd

run:
	go run main.go --router=$(ROUTER_ADDR) --password=$(PASSWORD)

test:
	@echo "Starting geodesist service for testing..."
	go run main.go --router=$(ROUTER_ADDR) --password=$(PASSWORD) &
	@sleep 2
	@echo "\n--- Testing /metrics endpoint ---"
	@curl -s -o /dev/null -w "HTTP Status: %{http_code}\n" http://localhost:8080/metrics
	@curl -s http://localhost:8080/metrics | head -20
	@pkill -f "go run main.go" || true

build:
	go build -o geodesist

clean:
	rm -f geodesist

test-auth:
	@echo "Testing authentication only..."
	@curl -s -w "\nHTTP Status: %{http_code}\n" http://localhost:8080/metrics