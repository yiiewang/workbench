# 快速开始

> 面向：使用者、部署运维 ｜ 代码基线：`v1.3.0-26-gc0577c4` + 工作树迁移 000021–000024
> 构建链、Makefile target 与发布流程见 [`04-build-and-release.md`](04-build-and-release.md)；配置与运维见 [`05-operations.md`](05-operations.md)；功能与参数见 [`02-functional-guide.md`](02-functional-guide.md)。

## 1. 前置条件

| 项 | 要求 | 用途 |
|---|---|---|
| Go | 1.25+ | 编译后端 |
| C 工具链 | `CGO_ENABLED=1` | `mattn/go-sqlite3` + 静态链接的 sqlite-vec 扩展 |
| Node / pnpm | Node 18+ / pnpm | 构建 Vue3 前端（`frontend/dist`） |
| Docker + xgo | 仅跨平台发布需要 | `make build-all` 五平台交叉编译 |
| `gh` CLI | 仅发布需要 | 创建 GitHub Release 与上传产物 |

> SQLite 驱动带 FTS5 全文检索，必须携带 build tag `sqlite_fts5`。`Makefile` 已在所有构建/检查入口统一携带，**不要直接 `go build` / `go run`**，否则会报 `no such module: fts5`。

## 2. 产物形态

一个 Go 二进制承载全部能力：

- **UI**：`frontend/dist` 经 `//go:embed`（`embed.go` 的 `UIFS`）编译进二进制，运行时只读。
- **用户文件**：磁盘上的 `server.static_dir`（默认 `./static`，仓库 `config.yaml` 为 `./preview`），按 `{orgName}/{userName}/` 分根。
- **业务数据**：单个 SQLite 文件 `database.path`（默认 `./data/workbench.db`），WAL 模式。

因此分发包只需要：二进制 + `config.yaml` + 空的数据目录；运行时不需要 Node 或 pnpm。

## 3. 构建与启动

| 命令 | 实际行为 |
|---|---|
| `make all` | 等价于 `make build`：先 `pnpm build` 再 `go build` |
| `make build` | **依赖 `frontend` 这个 phony target，每次都会重新构建前端**（不是「只编译 Go」）；产物 `build/workbench` |
| `make run` | 构建后以 `ALLOW_SYMLINK=true` 启动 |
| `make preview ARGS="--config /path/to/config.yaml"` | 构建后带指定配置启动 |
| `cd frontend && pnpm dev` | Vite 开发服务器（`0.0.0.0:4000`），`/api` 代理到 `VITE_API_TARGET`；不 embed、不编译 Go |
| `make check` | `go vet` + `golangci-lint` + `go test`，全部带 `sqlite_fts5` |
| `PORT=3000 STATIC_DIR=/var/www make run` | 环境变量覆盖配置 |

**端口**：由 `server.port` 决定，**代码默认值 `8080`**，仓库 `config.yaml` 示例为 `8001`，`PORT` 环境变量可覆盖。启动后访问 `http://<host>:<port>/`。开发时 Vite 的后端代理默认指向 `http://localhost:80`，若后端不在 80 端口，需设置 `VITE_API_TARGET`。

## 4. 首次初始化（创建第一个超管）

`users` 表为空时，**第一个成功调用 `/api/set-password` 的人自动成为平台超管**。部署后先设置一次性引导码，否则任何能访问服务的人都能抢占超管：

```bash
export ADMIN_SETUP_TOKEN=$(openssl rand -hex 16)   # 记住该值
make run

curl -s -X POST http://localhost:8001/api/set-password \
  -H "Content-Type: application/json" \
  -d '{"orgId":"cm","userId":"yiiewang","newPassword":"your-password","setupToken":"<上一步的值>"}'
```

| 字段 | 必填 | 说明 |
|---|---|---|
| `orgId` | 是 | 组织标识，不存在时自动创建 |
| `userId` | 是 | 用户名，全局唯一 |
| `newPassword` | 是 | 密码，服务端用 bcrypt 存储 |
| `setupToken` | 仅首次 | 仅在系统无任何用户时校验，与 `ADMIN_SETUP_TOKEN` 常量时间比较；有用户后无效 |
| `oldPassword` | 改密时 | 已有密码的账号改密必须提供 |

**限制**：前端**没有「设置密码」页面**（登录页注明不支持自助注册），首用户创建必须走上述 HTTP 调用；后续账号由管理员在 `/admin` 创建，重置密码也在该页面。已有账号但密码为空时可直接用 `newPassword` 激活，无需 `oldPassword`。

## 5. 登录与组织切换

浏览器打开 `/login`，输入 User ID + Password（用户名全局唯一，`orgId` 可省略）。登录返回 Bearer token，前端随后调 `/api/userinfo` 拉取全部组织、角色与功能开关，之后每个请求自动带 `Authorization: Bearer <token>` 与 `X-Org-Id`。Account 弹窗（移动端「我的」页）可切换组织、修改通知邮箱、登出。

## 6. SPA 路由注册（部署必做）

前端用 `createWebHistory()`，**所有 SPA 路径必须在 `config.yaml` 的 `routes` 注册为 `__spa__`**：`/login`、`/file`、`/todo`、`/jobs`、`/shares`、`/admin`、`/m`、`/m/files`、`/m/file`、`/m/ai`、`/m/todo`、`/m/profile` 等。未注册时浏览器直接刷新该 URL 会被静态兜底当作资源请求，返回 401。新增前端页面时同步加键，否则同样问题。`/jobs` 不在 PC→移动映射表中，窄视口下以响应式 PC 布局呈现；编排流程已并入该页，无独立 `/pipelines` 路由。

## 7. 公网部署

```
client ──HTTPS──> nginx (443, TLS) ──HTTP──> workbench (127.0.0.1:8001)
```

- 后端只绑定回环地址，不要直接暴露明文端口。
- nginx：`client_max_body_size 512m`（对齐上传上限）、`proxy_buffering off`（SSE 流式对话必需）、`proxy_read_timeout 3600s`。
- 无域名时可用 `acme.sh --issue -d <ip> --cert-profile shortlived` 申请 IP 证书；国内未备案主机需确认 443 可用性。
- 密钥一律环境变量注入，不写进 `config.yaml`：`AI_API_KEY`、`AI_EMBEDDING_API_KEY`、`EMAIL_PASS`、`ADMIN_SETUP_TOKEN`、MCP header 的 `${ENV_VAR}` 对应变量。误入 git 历史视为泄漏，先到提供方吊销。
- 配置 `site_url`（或 `WORKBENCH_SITE_URL`）为对外 https 地址，邮件才会带跳转按钮。

## 8. 验证与下一步

启动后依次确认：首页能打开 → 登录成功 → `/api/ai/status` 返回预期 `enabled` → `GET /api/ai/index-status` 可用（RAG 启用时）→ 上传一个文本文件后能被 `search_files` 检索到 → 打开 `/jobs` 新建任务、类型选「编排流程」并看到 DAG 流程图。

| 需求 | 文档 |
|---|---|
| 各功能怎么操作、参数与限制 | [`02-functional-guide.md`](02-functional-guide.md) |
| 架构、接口、数据模型 | [`03-design.md`](03-design.md) |
| 配置、索引运维、备份、故障排查 | [`05-operations.md`](05-operations.md) |
| 构建、测试、打包、发布 | [`04-build-and-release.md`](04-build-and-release.md) |
