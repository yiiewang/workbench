# Changelog

本项目的所有显著变更记录在此文件中。
格式基于 [Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/)，版本号遵循 [SemVer](https://semver.org/)。

## [Unreleased]

## [1.1.2] - 2026-10-22

### Added

- **文件上传**（`POST /api/upload`）：multipart 多文件上传，沙箱限定在当前用户根目录（`{org}/{user}/`）内；同名文件跳过不覆盖（`O_EXCL` 防并发竞态）；单请求 512MB / 100 文件上限；命中 hidden 过滤规则的文件单独提示。前端侧栏新增 Upload 按钮（目标目录 = 当前选中目录，选中文件取父目录，未选中传根）
- **文件管理操作**：新建目录（`POST /api/mkdir`）、删除（`DELETE /api/file`，目录递归，拒绝删除用户根）、重命名（`POST /api/rename`）、移动（`POST /api/move`，环检测防止目录移进自身）。前端新增文件树右键菜单（新建文件夹 / 分享 / 重命名 / 移动 / 删除），移动采用"标记 → 移动到此处"剪贴模式；操作后自动刷新目录树并联动迁移/关闭受影响的 tab
- **软链接支持**：目录树与分享页展示链接标识（`treeItem.isLink`，图标右下角徽章 + title 提示）；文件管理操作（移动/重命名/删除）对符号链接**只操作链接本身**，不触及真实目标（父目录真实路径仍做沙箱校验，内容访问的 EvalSymlinks 校验不变）
- **树根目录行**：侧栏渲染根目录条目，支持右键（移动到根 / 在根新建文件夹），根目录菜单不含分享/重命名/移动/删除
- **预览页下载按钮**：TabBar 右端新增当前激活文件的下载入口，覆盖全部文件类型（原来仅二进制详情页有 Download），分享模式同样可用
- **查看成员日程端点**（`GET /api/tasks/user/{id}`）：TodoView"查看成员日程"改走专用端点，服务端校验目标成员与当前用户同 org 且关系 active 后返回该成员任务；不走前端 3 秒缓存

### Fixed

- **Markdown Mermaid 图不渲染**：`mermaid.run` 在 Vue DOM 更新前执行导致 `querySelector` 匹配不到容器（第二次起打开必现）。改为 `loading` 解除后 `await nextTick()` 再渲染，且限定组件作用域、按需加载、失败不再完全静默
- **`/file`、`/admin` 刷新 401**：两条 SPA 路由未在 `config.yaml routes` 注册为 `__spa__`，浏览器刷新直接 GET 不带 Authorization 头被静态兜底拦截。已补注册（部署需同步配置并重启）
- **身份切换状态残留**：SPA 内登出→换账号 / 切换组织不刷新页面，内存单例（树缓存 dirCache、打开的 tab、todo 任务列表、查看成员状态、`/api/tasks` 请求缓存）残留上一身份数据——新用户会看到旧用户的目录树，todo 场景甚至会把旧账号任务全量写入新账号存储。新增 `resetIdentityScopedState()` 统一清理，接入登出 / 登录成功 / 切换组织三个调用点
- **查看成员日程显示"暂无任务"**：RBAC 收紧后 `GET /api/tasks` 仅返回当前登录用户自己的任务，前端仍从中读取 `org[memberId]` 必然落空。改调新增的 `/api/tasks/user/{id}`
- **move 后目录树变空**：`refreshDir` 只对 `expanded` 集合内的目录重载，而根目录永远不在其中——对根目录的任何刷新（移动到根、根下删除/重命名、上传到根）都会清缓存不重载导致整树空白。重载条件扩展为"有缓存（=树上正显示）或已展开"
- **登出残留**：`clearAuthState` 顺带清理 `share_pwd_*` 分享密码缓存

### Security

- 上传与文件管理操作全部复用 `resolveUserPathSafe`/`safeJoin` 用户沙箱（防路径穿越与跨用户/跨组织越权）、`validEntryName`/`sanitizeUploadName` 文件名校验，每操作记录 slog 审计日志（user/org/path）
- 软链接操作安全模型明确：操作链接条目不访问内容；非链接条目保持 EvalSymlinks 全路径沙箱（防中间目录链接逃逸）；内容访问（预览/下载/分享）沙箱不变，指向用户根外的链接依旧 404

## [1.1.1] - 2026-08-28

- 跨平台打包（xgo 五平台）、前端 embed、发布流程文档化。

## [1.1.0]

- RBAC 多组织支持 + 迁移 + UI 改进（owner/admin/member 角色、组织切换、per-user-per-org 功能开关）。
- `GET /api/tasks` 按 org+user 收紧为仅返回当前用户任务（防跨用户数据泄露）。
- Todo 增量任务接口（PATCH/POST/DELETE `/api/tasks/{id}`）、任务看板与用户看板 UI 改版。

## [1.0.0]

- 初始发布：文件浏览器、Markdown/JSON/XML 预览、分享链接（密码/有效期/访问次数）、Todo 看板、Token 认证、访问统计。
