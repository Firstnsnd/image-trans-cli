# 定义变量
BINARY_NAME=image-trans-cli
GO=go

# 默认目标
.PHONY: all
all: build build-linux build-windows

# 构建目标
.PHONY: build
build:
	@echo "Building the binary for the current OS..."
	$(GO) build -o ./bin/$(BINARY_NAME)

# Linux 目标
.PHONY: build-linux
build-linux:
	@echo "Building the binary for the current OS..."
	GOOS=linux GOARCH=amd64 $(GO) build -o ./bin/$(BINARY_NAME)

# Linux 目标
.PHONY: build-mac
build-mac:
	@echo "Building the binary for the current OS..."
	GOOS=darwin GOARCH=amd64 $(GO) build -o ./bin/$(BINARY_NAME)

# Windows 目标
.PHONY: build-windows
build-windows:
	@echo "Building the binary for Windows..."
	GOOS=windows GOARCH=amd64 $(GO) build -o ./bin/$(BINARY_NAME).exe

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
