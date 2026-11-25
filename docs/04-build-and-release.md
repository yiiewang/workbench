# 构建与发布

> 面向：开发者、发布者 ｜ 代码基线：`v1.3.0-25-g517f88c`
> 本文件是**构建、质量门禁、打包与发布流程的唯一权威说明**（根 `README.md` 对应章节已迁移至此）。
> 首次启动与部署配置见 [`01-getting-started.md`](01-getting-started.md)；运行期配置与运维见 [`05-operations.md`](05-operations.md)。

## 1. 工具链

| 项 | 要求 | 说明 |
|---|---|---|
| Go | 1.25+（`go.mod` 为 `go 1.25.0`） | 模块 `github.com/yiiewang/workbench` |
| C 工具链 | `CGO_ENABLED=1` | `mattn/go-sqlite3` 是 cgo 驱动，且 sqlite-vec 以静态扩展编译进进程 |
| build tag | `sqlite_fts5` | FTS5 trigram 全文检索必需，`Makefile` 的 `GO_TAGS` 统一注入 |
| Node / pnpm | Node 18+ / pnpm | `frontend/` 的 Vite + Vue3 + TypeScript 构建 |
| Docker + xgo | 仅 `make build-all` 需要 | 五平台交叉编译 |
| `gh` CLI | 仅发布需要 | 创建 GitHub Release、上传产物 |

核心依赖见 `go.mod`：iris v12.2.11、mattn/go-sqlite3 v1.14.53、golang-migrate v4.19.0、yaml.v3、`golang.org/x/crypto`、`golang.org/x/net`、`google/uuid`。

## 2. 构建链路

```
frontend/ (Vue3 + TS)
   │ pnpm build  = vue-tsc --noEmit && vite build
   ▼
frontend/dist/ (index.html + assets/*，content-hash 命名)
   │ //go:embed frontend/dist  →  embed.go 的 UIFS
   ▼
go build -tags sqlite_fts5 -ldflags "-X main.version=$(git describe ...)"
   │
   ▼
build/workbench  （单二进制：HTTP 服务 + UI + SQLite 驱动 + vec0 扩展）
```

**关键点**：UI 资源是**编译期**嵌入的，所以任何前端改动都必须重新执行 `go build`；`frontend/dist` 在编译时必须存在。`static_dir` 只放用户文件，与 UI 资源完全解耦。

`VERSION` 由 `git describe --tags --always --dirty` 自动推导（无 tag 时为 `dev`），经 `-X main.version` 注入，不需要手工改代码里的版本号。

## 3. Makefile target 全表

| Target | 命令 / 依赖 | 说明与副作用 |
|---|---|---|
| `make all` | `all: build` | 与 `make build` 等价：前端 + Go |
| `make build` | `build: frontend`，`CGO_ENABLED=1 go build -tags "$(GO_TAGS)" -ldflags=... -o build/workbench ./cmd/workbench/` | **`frontend` 是 phony，每次都会跑 `pnpm build`**，即使只改了 Go 代码 |
| `make frontend` | 依赖 `frontend/node_modules` → `cd frontend && pnpm build` | 生成 `frontend/dist`；`package.json`/lock 变化时先 `pnpm install` |
| `make run` | 依赖 `build`，`ALLOW_SYMLINK=true ./build/workbench` | 开发启动，**强制开启符号链接支持**（与 `config.yaml` 的 `false` 不同） |
| `make preview` | 依赖 `build`，`./build/workbench $(ARGS)` | 可传 `ARGS="--config /path/to/config.yaml"` |
| `make vet` | `CGO_ENABLED=1 go vet -tags "$(GO_TAGS)" ./...` | 静态检查 |
| `make lint` | `golangci-lint run ./...` | 需本机安装 golangci-lint |
| `make test` | `CGO_ENABLED=1 go test -tags "$(GO_TAGS)" ./...` | 单测 |
| `make check` | `vet lint test` | 发布前门禁 |
| `make sync-vec-headers` | 复制 `$(go list -m -f '{{.Dir}}' github.com/mattn/go-sqlite3)/sqlite3-binding.h` → `internal/sqlitevec/sqlite3.h` | **升级 mattn/go-sqlite3 后必须执行**，保证 vendor 头文件与实际链接的 SQLite 版本一致 |
| `make ragclean [ARGS=--reset]` | `CGO_ENABLED=1 go run -tags "$(GO_TAGS)" scripts/ragclean.go` | RAG 索引清理的唯一入口（独立 `go run` 缺 build tag 会失败） |
| `make build-all` | 依赖 `frontend`，`go install xgo@latest` → `xgo -tags ... --targets=linux/amd64,linux/arm64,darwin/amd64,darwin/arm64,windows/amd64` → 重命名 → `make package` | 五平台交叉编译 + 打包 |
| `make package` | `./scripts/package.sh build workbench $(VERSION)` | 从已有二进制生成分发包 |
| `make clean` | `rm -rf build frontend/dist` | 清理产物（之后必须重新构建前端才能 `go build`） |
| `make help` | — | 打印帮助 |

## 4. 关键构建约束

| 约束 | 原因 | 违反后的现象 |
|---|---|---|
| 必须 `CGO_ENABLED=1` | mattn/go-sqlite3 + `internal/sqlitevec` 的 C 源码静态链接 | 驱动不可用 / vec0 注册失败 |
| 所有入口携带 `sqlite_fts5` | `file_chunk_fts` 是 FTS5 trigram 虚拟表 | `no such module: fts5` |
| 编译前 `frontend/dist` 必须存在 | `//go:embed frontend/dist` | `pattern frontend/dist: no matching files found` |
| 前端改动后必须重编 Go 二进制 | UI 在编译期嵌入 | 页面仍是旧版 |
| 升级 mattn 后同步 `sqlite3.h` | vec0 扩展与 SQLite 版本强绑定 | 编译错误或运行时异常 |
| `pnpm build` 含 `vue-tsc --noEmit` | `tsconfig.json` 开 `strict` + `noUnusedLocals` | 未使用的 import/变量会中断构建；`pnpm build:nocheck` 可临时跳过类型检查（不建议用于发布） |

`vite.config.ts` 当前是**单入口**（`rollupOptions.input.index = index.html`）；`embed.go` 注释、`server.isUIAsset` 白名单与 `config.yaml` 中的 `todo.html` 属历史多入口遗留。`manualChunks` 固定分出 `vue` / `element-plus` / `marked` / `highlight.js` / `spark-md5` 五个 vendor 分片，其中 `spark-md5` 当前无 import，产物是 1 B 空占位分片（已知现状，非错误）。

## 5. 前端构建与开发服务器

| 脚本 | 行为 |
|---|---|
| `pnpm dev` | Vite dev server，`0.0.0.0:4000`，HMR |
| `pnpm build` | `vue-tsc --noEmit && vite build` → `dist/`，`emptyOutDir: true` |
| `pnpm build:nocheck` | 仅 `vite build` |
| `pnpm preview` | 预览构建产物 |

`vite.config.ts` 的 dev proxy **只代理 `/api`** 到 `VITE_API_TARGET`（默认 `http://localhost:80`）。`/s/{token}` 故意不代理：若代理到 Go 会返回引用 hashed assets 的 built `index.html`，而 Vite 无这些文件会回退成 `text/html`，导致 CSS/JS MIME 不匹配。分享数据仍走已代理的 `/api/share/{token}`。产物 `base: '/'` 使用绝对路径 `/assets/...`，Go 端 `isUIAsset` 放行 `assets/*` 前缀。

## 6. 质量门禁

发布前执行 `make check`（`vet` + `golangci-lint` + `test`），三者都带 `sqlite_fts5` 与 `CGO_ENABLED=1`。单测覆盖：cron 解析/匹配、事件总线分发与 panic 隔离、到期扫描去重、邮件内容与报文组装、`safeJoin` 沙箱、审批协调器、AI 会话持久化、vec0 KNN 冒烟、存量库基线迁移、压缩行为、RAG 切块与 HTML 提取、后台运行状态机等。

## 7. 跨平台打包

```bash
make build-all
```

- 依赖 `frontend`（先构建前端），再 `go install src.techknowlogick.com/xgo@latest`，用 Docker 交叉编译五个 target。
- xgo 产物经 `sed` 归一化命名（`darwin-*` / `win-*` / `x86_64`），再由 `scripts/package.sh` 打包。
- `package.sh` 为每个二进制建临时目录：复制二进制（Windows 重命名为 `workbench.exe`）、`config.yaml`、`README.md`，生成 `static/` 空目录；非 Windows 出 `.tar.gz`，Windows 出 `.zip`；最后不负责上传。
- 产物形如 `build/workbench-<version>-linux-x86_64.tar.gz`、`-linux-arm64.tar.gz`、`-darwin-x86_64.tar.gz`、`-darwin-arm64.tar.gz`、`-win-x86_64.zip`。

**分发包内容**：`workbench`（或 `workbench.exe`，已内嵌前端）、`config.yaml`、`README.md`；运行时会自行创建 `data/` 与 `static_dir`。

## 8. 发布流程

### 8.1 前置

Go 1.25+、Node 18+ / pnpm、Docker（xgo）、已认证的 `gh` CLI。

### 8.2 版本与变更记录

1. 版本号从 git tag 自动推导，**通常无需改代码**。
2. 把 `CHANGELOG.md` 的 `[Unreleased]` 条目移入新版本段并标注日期。
3. 提交所有改动，确保工作树干净。
4. 按 [`README.md`](README.md) §5 的维护规范同步文档（路由、配置、迁移、工具清单、文档基线）。

### 8.3 打 tag 与创建 Release

```bash
git tag -a v1.x.x -m "Release v1.x.x - brief description"
git push origin v1.x.x
gh release create v1.x.x --title "v1.x.x" --notes "Release notes..."
```

### 8.4 构建并上传

```bash
make build-all                                        # 前端 + 五平台二进制 + 打包
gh release upload v1.x.x build/workbench-v1.x.x-* --clobber
```

### 8.5 检查清单

- [ ] 所有改动已提交并推送，工作树干净
- [ ] `make check` 通过
- [ ] `CHANGELOG.md` 已生成新版本段
- [ ] 文档基线、迁移编号、工具数量已核对（见 `docs/README.md` §5）
- [ ] `git tag -a v1.x.x` 已创建并推送
- [ ] GitHub Release 已创建
- [ ] `make build-all` 成功，5 个平台包齐全
- [ ] 全部产物已上传到 Release

## 9. 常见构建故障

| 现象 | 原因与处理 |
|---|---|
| `no such module: fts5` | 直接 `go build` / `go run` 未带 tag；统一改用 `make` 入口（清理索引用 `make ragclean`） |
| `pattern frontend/dist: no matching files found` | 未构建前端或执行过 `make clean`；先 `make frontend` 或 `make all` |
| cgo / sqlite-vec 链接失败 | 检查 `CGO_ENABLED=1` 与本机 C 工具链 |
| 升级 `mattn/go-sqlite3` 后构建或 vec0 异常 | 执行 `make sync-vec-headers` 重同步头文件 |
| `pnpm build` 因未使用变量中断 | `tsconfig.json` 开启 `noUnusedLocals`；清理无用 import（或用 `build:nocheck` 临时绕过） |
| dev 模式打开分享链接白屏 / MIME 报错 | `/s/` 故意不代理，属预期行为；用生产构建验证分享页 |
| Vite 连不上后端 | 后端不在 80 端口时设置 `VITE_API_TARGET=http://localhost:8001` |
| `make build-all` 失败 | 确认 Docker 可用、`xgo` 可安装；交叉编译依赖 CGO 工具链由 xgo 镜像提供 |
| 改了前端但页面没变化 | UI 是编译期 embed，必须重新 `make build` 并重启进程 |

## 10. 文档关系

| 内容 | 位置 |
|---|---|
| 环境、首次启动、管理员初始化、部署 | [`01-getting-started.md`](01-getting-started.md) |
| 运行期配置、索引运维、备份、故障排查 | [`05-operations.md`](05-operations.md) |
| 架构与接口 | [`03-design.md`](03-design.md) |
| 功能与参数 | [`02-functional-guide.md`](02-functional-guide.md) |
| 文档维护规范与偏差清单 | [`README.md`](README.md) |

根 `README.md` 的 Quick Start 与 Release Process 已精简为指向本文件与 `01-getting-started.md` 的链接，避免两份说明各自漂移。
