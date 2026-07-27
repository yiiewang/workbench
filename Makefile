# Workbench Makefile
# ============================================================

BINARY_NAME  := workbench
BUILD_DIR    := build
VERSION      := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS      := -s -w -X main.version=$(VERSION)

# 分发包包含的额外文件
ASSETS       := static/index.html static/todo.html config.yaml README.md

# ============================================================
# Local build
# ============================================================
.PHONY: build
build:
	@echo "Building $(BINARY_NAME)..."
	go build -ldflags="$(LDFLAGS)" -o $(BINARY_NAME) ./cmd/workbench/

.PHONY: run
run: build
	./$(BINARY_NAME)

# ============================================================
# Cross-platform build + package (requires xgo + Docker)
# 产出: build/workbench-<version>-<platform>.tar.gz / .zip
# 每个包包含: 二进制 + static/ + config.yaml + README.md
# ============================================================
.PHONY: build-all
build-all:
	@mkdir -p $(BUILD_DIR)
	@echo "Installing xgo..."
	go install src.techknowlogick.com/xgo@latest
	@echo "Running xgo..."
	xgo -ldflags="$(LDFLAGS)" \
		--targets=linux/*,windows/amd64,darwin/arm64 \
		-image techknowlogick/xgo:go-1.22.0 \
		-out $(BUILD_DIR)/$(BINARY_NAME)-$(VERSION) ./cmd/workbench/
	@# 重命名 xgo 生成的文件
	@cd $(BUILD_DIR) && for f in $(BINARY_NAME)-$(VERSION)-*; do \
		newname=$$(echo "$$f" | sed 's/darwin-[0-9.]*-/darwin-/g; s/windows-[0-9.]*-/win-/g; s/amd64/x86_64/g'); \
		if [ "$$f" != "$$newname" ]; then mv "$$f" "$$newname"; echo "Renamed $$f -> $$newname"; fi; \
	done
	@echo "Binaries built. Packaging..."
	@$(MAKE) package
	@echo "All packages created:"
	@ls -lh $(BUILD_DIR)/$(BINARY_NAME)-$(VERSION)-*.tar.gz $(BUILD_DIR)/$(BINARY_NAME)-$(VERSION)-*.zip 2>/dev/null

# ============================================================
# Package: 将每个二进制打包为完整分发包
# 依赖: build 目录下已有编译好的二进制
# ============================================================
.PHONY: package
package:
	@./scripts/package.sh $(BUILD_DIR) $(BINARY_NAME) $(VERSION)

# ============================================================
# Cleanup
# ============================================================
.PHONY: clean
clean:
	rm -rf $(BUILD_DIR) $(BINARY_NAME)
	@echo "Cleaned"
