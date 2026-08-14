# Workbench Makefile
# ============================================================

BINARY  := workbench
OUT_DIR := build
LOCAL   := $(OUT_DIR)/$(BINARY)
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

# Go 源码（排除测试文件）
GO_SRC := $(shell find cmd internal -name '*.go' ! -name '*_test.go' 2>/dev/null)

# ============================================================
# 本地构建（每次都重新构建前端 + Go，确保 dist 是最新的）
# ============================================================
# //go:embed frontend/dist 在编译时打包，必须保证 dist 是最新的。
# build 依赖 frontend（phony），每次 make build 都会触发 pnpm build + go build。
.PHONY: build
build: frontend
	@mkdir -p $(OUT_DIR)
	go build -ldflags="$(LDFLAGS)" -o $(LOCAL) ./cmd/workbench/
	@echo "$(BINARY) built -> $(LOCAL)"

.PHONY: run
run: build
	ALLOW_SYMLINK=true ./$(LOCAL)

.PHONY: preview
preview: build
	./$(LOCAL) $(ARGS)

# ============================================================
# 前端构建（Vite + Vue3 + TypeScript）
# ============================================================
frontend/node_modules: frontend/package.json frontend/pnpm-lock.yaml
	cd frontend && pnpm install

.PHONY: frontend
frontend: frontend/node_modules
	cd frontend && pnpm build
	@echo "frontend built -> frontend/dist"

# 一键构建：前端 + Go（前端改动后使用此命令）
.PHONY: all
all: build

# ============================================================
# 质量检查
# ============================================================
.PHONY: vet
vet:
	go vet ./...

.PHONY: lint
lint:
	golangci-lint run ./...

.PHONY: test
test:
	go test ./...

.PHONY: check
check: vet lint test

# ============================================================
# 跨平台构建 + 打包（需要 xgo + Docker）
# ============================================================
.PHONY: build-all
build-all:
	@mkdir -p $(OUT_DIR)
	go install src.techknowlogick.com/xgo@latest
	xgo -ldflags="$(LDFLAGS)" \
		--targets=linux/*,windows/amd64,darwin/arm64 \
		-image techknowlogick/xgo:go-1.22.0 \
		-out $(OUT_DIR)/$(BINARY)-$(VERSION) ./cmd/workbench/
	@cd $(OUT_DIR) && for f in $(BINARY)-$(VERSION)-*; do \
		newname=$$(echo "$$f" | sed 's/darwin-[0-9.]*-/darwin-/g; s/windows-[0-9.]*-/win-/g; s/amd64/x86_64/g'); \
		[ "$$f" != "$$newname" ] && mv "$$f" "$$newname" && echo "  $$f -> $$newname"; \
	done
	$(MAKE) package
	@echo "packages:"
	@ls -lh $(OUT_DIR)/$(BINARY)-$(VERSION)-*.tar.gz $(OUT_DIR)/$(BINARY)-$(VERSION)-*.zip 2>/dev/null || true

.PHONY: package
package:
	./scripts/package.sh $(OUT_DIR) $(BINARY) $(VERSION)

# ============================================================
# 清理
# ============================================================
.PHONY: clean
clean:
	rm -rf $(OUT_DIR) frontend/dist
	@echo "cleaned $(OUT_DIR)/ frontend/dist/"

.PHONY: help
help:
	@echo "Usage: make [target]"
	@echo ""
	@echo "Build:"
	@echo "  build        编译 Go 二进制（仅 .go 或 frontend/dist 变化时）"
	@echo "  frontend     编译前端"
	@echo "  all          一键构建：前端 + Go"
	@echo ""
	@echo "Run:"
	@echo "  run          构建并启动（ALLOW_SYMLINK=true）"
	@echo "  preview      构建并启动（可传 ARGS=\"--config ...\"）"
	@echo ""
	@echo "Quality:"
	@echo "  vet          运行 go vet"
	@echo "  lint         运行 golangci-lint"
	@echo "  test         运行 go test"
	@echo "  check        一键检查：vet + lint + test"
	@echo ""
	@echo "Release:"
	@echo "  build-all    跨平台编译 + 打包"
	@echo "  package      仅打包已有二进制"
	@echo ""
	@echo "Other:"
	@echo "  clean        删除 build/ 目录"
	@echo "  help         显示此帮助"
