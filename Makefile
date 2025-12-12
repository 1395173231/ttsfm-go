.PHONY: build run test clean docker docker-build docker-run docker-stop docker-dev

# 变量
APP_NAME := ttsfm-server
DOCKER_IMAGE := ttsfm
DOCKER_TAG := latest

# Go 构建
build:
	@echo "Building $(APP_NAME)..."
	go build -o bin/$(APP_NAME) ./cmd/main.go

run:
	@echo "Running $(APP_NAME)..."
	go run ./cmd/main.go

test:
	@echo "Running tests..."
	go test -v ./...

test-coverage:
	@echo "Running tests with coverage..."
	go test -v -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html

clean:
	@echo "Cleaning..."
	rm -rf bin/ tmp/ coverage.out coverage.html

# Docker 命令
docker-build:
	@echo "Building Docker image..."
	docker build -t $(DOCKER_IMAGE):$(DOCKER_TAG) .

docker-run:
	@echo "Running Docker container..."
	docker run -d -p 8080:8080 --name $(APP_NAME) $(DOCKER_IMAGE):$(DOCKER_TAG)

docker-stop:
	@echo "Stopping Docker container..."
	docker stop $(APP_NAME) || true
	docker rm $(APP_NAME) || true

docker-dev:
	@echo "Starting development environment..."
	docker-compose --profile dev up --build

docker-compose-up:
	@echo "Starting with docker-compose..."
	docker-compose up -d --build

docker-compose-down:
	@echo "Stopping docker-compose..."
	docker-compose down

docker-compose-logs:
	@echo "Viewing logs..."
	docker-compose logs -f

# 生产环境
docker-prod:
	@echo "Starting production environment..."
	docker-compose -f docker-compose.prod.yml up -d --build

docker-prod-down:
	@echo "Stopping production environment..."
	docker-compose -f docker-compose.prod.yml down

# 工具
lint:
	@echo "Running linter..."
	golangci-lint run

fmt:
	@echo "Formatting code..."
	go fmt ./...

mod-tidy:
	@echo "Tidying modules..."
	go mod tidy
