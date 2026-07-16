# 定义变量
BINARY_NAME=image-trans-cli
VERSION?=dev
GO=go
LDFLAGS=-ldflags "-X main.version=$(VERSION)"

# 默认目标
.PHONY: all
all: build build-linux build-mac build-mac-arm build-windows

# 构建目标
.PHONY: build
build:
	@echo "Building the binary for the current OS..."
	$(GO) build $(LDFLAGS) -o ./bin/$(BINARY_NAME)

# Linux 目标
.PHONY: build-linux
build-linux:
	@echo "Building the binary for Linux amd64..."
	GOOS=linux GOARCH=amd64 $(GO) build $(LDFLAGS) -o ./bin/$(BINARY_NAME)-linux-amd64

# macOS Intel 目标
.PHONY: build-mac
build-mac:
	@echo "Building the binary for macOS amd64..."
	GOOS=darwin GOARCH=amd64 $(GO) build $(LDFLAGS) -o ./bin/$(BINARY_NAME)-darwin-amd64

# macOS Apple Silicon 目标
.PHONY: build-mac-arm
build-mac-arm:
	@echo "Building the binary for macOS arm64..."
	GOOS=darwin GOARCH=arm64 $(GO) build $(LDFLAGS) -o ./bin/$(BINARY_NAME)-darwin-arm64

# Windows 目标
.PHONY: build-windows
build-windows:
	@echo "Building the binary for Windows amd64..."
	GOOS=windows GOARCH=amd64 $(GO) build $(LDFLAGS) -o ./bin/$(BINARY_NAME)-windows-amd64.exe

# 清理目标
.PHONY: clean
clean:
	@echo "Cleaning up..."
	rm -f $(BINARY_NAME) $(BINARY_NAME).exe

# 帮助目标
.PHONY: help
help:
	@echo "Makefile for $(BINARY_NAME)"
	@echo ""
	@echo "Usage:"
	@echo "  make build          - Build the binary for the current OS"
	@echo "  make build-linux    - Build the binary for the Linux"
	@echo "  make build-windows  - Build the binary for Windows"
	@echo "  make clean          - Remove the binary"
	@echo "  make help           - Show this help message"
