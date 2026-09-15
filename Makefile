.PHONY: all build run test clean migrate help

ifeq ($(OS),Windows_NT)
SHELL := powershell.exe
.SHELLFLAGS := -NoProfile -ExecutionPolicy Bypass -Command
LOCAL_GOCACHE := $(CURDIR)\.gocache
else
LOCAL_GOCACHE := $(CURDIR)/.gocache
endif

# 变量定义
APP_NAME=narra-api
BUILD_DIR=bin
MAIN_PATH=cmd/server/main.go

# Go 参数
GOCMD=go
GOBUILD=$(GOCMD) build
GORUN=$(GOCMD) run
GOTEST=$(GOCMD) test
GOMOD=$(GOCMD) mod
GOGET=$(GOCMD) get

all: clean build

## build: 编译项目
build:
ifeq ($(OS),Windows_NT)
	@Write-Host "Building $(APP_NAME)..."
	@if (!(Test-Path '$(BUILD_DIR)')) { New-Item -ItemType Directory -Force '$(BUILD_DIR)' | Out-Null }
	$(GOBUILD) -o $(BUILD_DIR)/$(APP_NAME).exe $(MAIN_PATH)
	@Write-Host "Build complete: $(BUILD_DIR)/$(APP_NAME).exe"
else
	@echo "Building $(APP_NAME)..."
	@mkdir -p $(BUILD_DIR)
	$(GOBUILD) -o $(BUILD_DIR)/$(APP_NAME).exe $(MAIN_PATH)
	@echo "Build complete: $(BUILD_DIR)/$(APP_NAME).exe"
endif

## run: 运行项目
run:
ifeq ($(OS),Windows_NT)
	@Write-Host "Running $(APP_NAME)..."
	$$env:GOCACHE='$(LOCAL_GOCACHE)'; $$env:GOTELEMETRY='off'; $(GORUN) $(MAIN_PATH) -c configs/config.yaml
else
	@echo "Running $(APP_NAME)..."
	GOCACHE='$(LOCAL_GOCACHE)' GOTELEMETRY=off $(GORUN) $(MAIN_PATH) -c configs/config.yaml
endif

## test: 运行测试
test:
ifeq ($(OS),Windows_NT)
	@Write-Host "Running tests..."
	$$env:GOCACHE='$(LOCAL_GOCACHE)'; $$env:GOTELEMETRY='off'; $(GOTEST) -v ./...
else
	@echo "Running tests..."
	GOCACHE='$(LOCAL_GOCACHE)' GOTELEMETRY=off $(GOTEST) -v ./...
endif

## test-coverage: 运行测试并生成覆盖率报告
test-coverage:
ifeq ($(OS),Windows_NT)
	@Write-Host "Running tests with coverage..."
	$$env:GOCACHE='$(LOCAL_GOCACHE)'; $$env:GOTELEMETRY='off'; $(GOTEST) -v -coverprofile=coverage.out ./...
	$(GOCMD) tool cover -html=coverage.out -o coverage.html
	@Write-Host "Coverage report generated: coverage.html"
else
	@echo "Running tests with coverage..."
	GOCACHE='$(LOCAL_GOCACHE)' GOTELEMETRY=off $(GOTEST) -v -coverprofile=coverage.out ./...
	$(GOCMD) tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report generated: coverage.html"
endif

## clean: 清理构建文件
clean:
ifeq ($(OS),Windows_NT)
	@Write-Host "Cleaning..."
	@if (Test-Path '$(BUILD_DIR)') { Remove-Item -LiteralPath '$(BUILD_DIR)' -Recurse -Force }
	@if (Test-Path '$(LOCAL_GOCACHE)') { Remove-Item -LiteralPath '$(LOCAL_GOCACHE)' -Recurse -Force }
	@if (Test-Path 'coverage.out') { Remove-Item -LiteralPath 'coverage.out' -Force }
	@if (Test-Path 'coverage.html') { Remove-Item -LiteralPath 'coverage.html' -Force }
	@Write-Host "Clean complete"
else
	@echo "Cleaning..."
	@rm -rf $(BUILD_DIR)
	@rm -rf $(LOCAL_GOCACHE)
	@rm -f coverage.out coverage.html
	@echo "Clean complete"
endif

## deps: 下载依赖
deps:
	@echo "Downloading dependencies..."
	$(GOMOD) tidy
	$(GOMOD) download

## fmt: 格式化代码
fmt:
	@echo "Formatting code..."
	$(GOCMD) fmt ./...

## vet: 代码静态检查
vet:
	@echo "Running go vet..."
	$(GOCMD) vet ./...

## lint: 代码检查（需要安装 golangci-lint）
lint:
	@echo "Running golangci-lint..."
	@golangci-lint run

## help: 显示帮助信息
help:
ifeq ($(OS),Windows_NT)
	@Write-Host "可用命令: build run test test-coverage clean deps fmt vet lint help"
else
	@echo "可用命令:"
	@sed -n 's/^##//p' $(MAKEFILE_LIST) | column -t -s ':' | sed -e 's/^/ /'
endif
