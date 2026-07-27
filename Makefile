# Workbench Makefile
# ============================================================

BINARY_NAME  := workbench
BUILD_DIR    := build
VERSION      := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS      := -s -w -X main.version=$(VERSION)

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
# Cross-platform build (requires xgo + Docker)
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
	@echo "Build completed"
	@ls -lh $(BUILD_DIR)/

# ============================================================
# Cleanup
# ============================================================
.PHONY: clean
clean:
	rm -rf $(BUILD_DIR) $(BINARY_NAME)
	@echo "Cleaned"
