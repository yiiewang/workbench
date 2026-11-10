# Changelog

本项目的所有显著变更记录在此文件中。
格式基于 [Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/)，版本号遵循 [SemVer](https://semver.org/)。

## [Unreleased]

## [1.2.0] - 2026-11-10

### Added

- **代码高亮折叠预览**：代码文件 hljs 高亮 + 区块折叠，超大文件降级纯文本；修复二进制文件预览与下载
- **音视频在线预览**：媒体短时效 ticket 流式播放（分享模式 blob 整文件回退）
- **AI 对话助手**（OpenAI 兼容协议代理，`config.yaml` `ai:` 段或 `AI_BASE_URL`/`AI_API_KEY`/`AI_MODEL` 环境变量）：右下角浮动面板（fab-dock 统一悬浮容器），SSE 流式输出、markdown 渲染（复用文件预览高亮链路）、推理模型思考过程折叠展示（`reasoning_content`）、`@` 引用文件（文件树选择器，多选，内容并入当轮 user 消息）、停止生成、面板拖拽调宽（持久化）；API key 只留后端，前端经 `/api/ai/chat` 代理访问，透传 `X-Model-Request-Id`/`X-QA-Id`/`X-Session-Id` 链路排障头
- **AI 会话历史**（SQLite `ai_sessions` 表，迁移 000006）：每轮对话自动落库（流式中 3s 节流，刷新/断网保住半截回复），历史列表可恢复继续沟通，刷新后自动接上最近会话（页签内主动新开除外），支持删除；修复详情接口 `json.RawMessage` Scan 500（TEXT 需经 string 中转，附回归测试）
- **AI 消息操作**：hover 显示工具条——复制任意消息、重新生成末尾回复（复用原提问与引用文件）、回复保存为文件（`/ai/YYYY-MM-DD-主题.md`，自动建目录）
- **文档 AI 摘要**：文件 tab 工具栏新增「AI 摘要」按钮（仅 markdown 文件显示），一键引用当前文档并让 AI 总结（`workbench:ai-ask` 全局事件，其他场景可复用）
- **Todo ↔ File 关联**（任务 `files` 字段，迁移 000005）：todo 编辑弹窗经文件树选择器（`FilePickerModal`，支持上传）关联文件，chips 点击直达文件；文件浏览顶部「关联任务」条可跳转定位任务（关闭弹窗自动回跳）、「挂到任务」反向建立关联（`TaskPickerModal` 未完成任务多选）；版本 md5 与旧数据兼容（空关联归一为 nil + omitempty）
- **markdown 编辑器视图模式切换**：分屏 / 仅编辑 / 仅预览三态循环切换
- **统一弹窗外壳 `ModalShell`**：遮罩/ESC（层叠栈只关最上层）/层级管理收敛，PromptModal、ShareModal、SharePasswordModal、用户菜单弹窗迁移；全局 z-index 层级变量表（`--z-*`）替代散落魔法数
- **全局悬浮按钮区 fab-dock**：Markdown 目录与 AI 按钮统一容器叠放（order 控制），组件间零感知

### Security

- **首个管理员创建需一次性初始化 token**：启动时生成 setup token，防止裸奔部署被抢注管理员
- **AI api_key 不落 config**：仅 `AI_API_KEY` 环境变量传入；README 增补部署安全指南（admin 初始化、nginx HTTPS、secrets 环境变量化）

### Fixed

- **全局 `.modal-mask` 遮挡 Element Plus 弹层**：z-index 9000 高于 EP 弹层（2000+），弹窗内下拉/日期选择被遮罩盖住；统一为 1000（`--z-modal`）
- **fab-dock 浮于移动端抽屉遮罩之上**：抽屉展开时悬浮按钮仍可点击；层级降为 898（`--z-fab-dock`）
- **关联任务跳转后二次点击无响应**：TodoView 定位 watcher 首次成功即停，KeepAlive 缓存下 setup 不再执行；改为持续监听 `?task=`
- **路由切换后 Markdown 目录悬浮按钮残留**：IndexView 被 KeepAlive 缓存不卸载，Teleport 到 fab-dock 的按钮残留到其他路由；`onDeactivated` 隐藏、`onActivated` 恢复

### Changed

- AI 面板输入区改 DeepSeek 风格卡片（textarea 融入、圆形发送钮、引用 chips 在卡片上方）
- todo 编辑弹窗「关联文件」UI 重做：label 行选择入口 + 文件名 chips + 虚线空态占位，移除手动路径输入
- 后端自然排序工具（章节号路径感知）从 server.go 独立到 sortutil.go；移动视图公共样式抽 `styles/mobile.css`

### Added（独立移动端 UI）

- **完全两套 UI**：移动端走 `/m/*` 独立路由（MobileLayout + `views/mobile/*`），PC 端零改动；窄视口（≤900px）访问 PC 路径自动 redirect 到移动版，桌面打开 `/m` 回 PC；登录页不分流，分享页有移动专属视图（`/m/s/:token`）。共用 api/stores/lib 与纯展示组件（FileViewer、ModalShell 等），仅视图层独立
- **MobileLayout**：底部导航（首页/文件/AI/Todo/我的），100dvh + safe-area
- **MHome / MProfile**：时段问候 + 功能入口卡片；用户信息 + 组织切换 + 登出
- **MFiles / MFileView**：单层目录下钻（query 驱动可回退）；文件预览复用 FileViewer（markdown/代码/媒体全类型），内链拦截走移动路由
- **MAI**：全屏 AI 对话页（进入自动恢复最近会话、@ 引用、思考过程、历史会话、消息操作）——对话核心逻辑抽 `composables/useAIChat`、消息样式抽全局 `styles/ai-chat.css`，与 PC 面板共用
- **MTodo**：按状态分组的任务列表（轻编辑：勾选/状态切换，失败回滚），详情 markdown 渲染、关联文件可跳移动预览；四象限/甘特等复杂视图留在 PC
- **ModalShell ≤900px 全屏化 + 触屏适配**：FilePickerModal 行高/热区加大、树占满可用高度（移动端弹窗共用）

## [1.1.4] - 2026-10-26

### Fixed

- **Markdown 预览中 mermaid 渲染失败**（报 `Syntax error in text`）：mermaid 代码块此前会落入 highlight.js 的 `highlightAuto`，被插入 `<span>` 标签；mermaid 的 `run()` 以节点 `innerHTML` 作为源码，这些标签被当成源码文本导致解析失败。现 mermaid 代码块原样输出（仅转义 `&`、`<`、`>`），文件预览与 todo 编辑器预览一致
- **侧栏折叠按钮位置**：`left` 加 `Math.max(0, ...)` 下限钳制，分享模式下折叠时不再算出 `-6px` 导致按钮左半边被视口左缘裁掉

### Changed

- todo 编辑器分栏预览开启 mermaid 渲染，与文件预览行为一致；mermaid 渲染加 300ms 防抖，避免边输入边重排图形

## [1.1.3] - 2026-10-23

### Added

- **移动端适配**（≤768px 断点）：侧栏变抽屉（transform 滑入滑出，`collapsed` 类驱动显隐），遮罩点击关闭、抽屉头关闭按钮、打开文件自动收起；隐藏 resizer，移动端默认收起不影响桌面宽度偏好；PromptModal 加 max-width 防小屏溢出
- **Markdown 预览宽屏展示**：`.markdown-body` 去掉 860px 固定宽度与居中留白，内容铺满预览区（窄屏保留 16px 内边距）
- **Markdown 表格增强**（文件预览与 todo 编辑器预览一致）：宽表格在容器内左右滚动、左右边缘渐变阴影提示可滚动、表头 sticky 常驻、首列 sticky 常驻；观感改为细横线 + 表头底色 + 斑马纹 + 行 hover + 圆角卡片
- **统一 Markdown 预览组件**（`components/markdown/MarkdownPreview.vue`）：文件预览与 todo 编辑器预览共用同一渲染链路（`marked` → `DOMPurify` → `v-html`），均支持 mermaid（CDN 按需懒加载）与表格滚动容器；`compact` prop 保留 todo 侧小尺寸
- **HTML 预览页内锚点可跳转**：改用 `srcdoc` 渲染并注入锚点拦截脚本，TOC 类 `#hash` 链接正常滚动定位
- **Markdown 标题折叠与目录**：标题可折叠（VSCode 式：折父标题时子标题与内容一并隐藏，不持久化）；文件预览右下角新增悬浮目录按钮，点击展开标题列表、层级缩进、滚动高亮当前标题、点击平滑定位；目标处于折叠区时自动展开。标题 `id` 在 DOM 上补齐（marked 默认不生成），同名标题自动加 `-1`/`-2`
- **正文内页内锚点统一处理**：`[链接](#标题)` 改为 `preventDefault` + `scrollIntoView`，不再往浏览器历史里塞 hash；目标被折叠时同样自动展开

### Changed

- **Tab 栏右上角按钮改为 flex 右对齐**：Share/Download 不再用绝对定位硬编码 `right`，tab 区也不再靠 `padding-right: 150px` 预留位置；分享模式下 Share 隐藏时 Download 自动贴右，tab 再多也不会压到按钮下方
- Markdown 渲染依赖统一走 npm（`marked` 既有，新增 `dompurify`），文件预览不再依赖 CDN 加载 marked/DOMPurify
- **Markdown 渲染配置收敛到 `lib/markdownRenderer.ts`**：`==高亮==` 语法与 highlight.js 代码高亮原先是 `useTodoApp` setup 里的全局副作用（隐式影响文件预览），现改为幂等的 `configureMarked()`，由 `MarkdownPreview` 通过动态 import 按需装配——highlight.js 拆成独立 chunk，首屏不加载
- todo 编辑器预览关闭 mermaid（分栏空间小，且避免每次输入都触发 CDN 检查）
- 前端 `tsconfig.json` 开启 `noUnusedLocals`，未使用的 import/变量会被类型检查拦下

### Removed

- 清理多轮迭代残留的死代码：`passwordRequired` 整条 emit 链路（FileViewer 声明但从不 emit、EditorArea 转发、IndexView 空处理）、`useTabs` 中零调用的 `closeTab`/`activateTab`/`closeAndActivatePrev`、`indexStore` 的 `storeTabCount`/`dsGetFile`/`b64`、CSS 中无对应逻辑的 `.tab-share.copied`
- 合并表格样式的重复定义（`.md-table-scroll` 分散的两块、`thead th` 的重复定义）

### Fixed

- **HTML 预览锚点点击无反应**：原 `blob:` URL 在无 `allow-same-origin` 的 sandbox iframe 中被 Chrome 拒绝（`Not allowed to load local resource`）；srcdoc 方案下 `href="#id"` 又会被解析为导航到 `about:srcdoc#id` 触发整页重载。现用 srcdoc + 注入脚本拦截锚点点击改为 `scrollIntoView`
- **右上角 Download 按钮重复渲染**：同一按钮在 `TabBar.vue` 与 `IndexView.vue` 各有一份且位置重叠，现统一收敛到 TabBar
- **移动端抽屉无法收起**：媒体查询的 `width: 280px !important` 覆盖了折叠态的内联 `width: 0`，导致抽屉常驻、遮罩不渲染、toggle 被隐藏。改为 `transform` 驱动显隐，并补充抽屉头关闭按钮
- **分享弹窗无限递归（栈溢出）**：`SharesSidebar` 把 `lib/shareModal` 的包装函数又写回 `window.openShareModal`，而该包装函数内部正是调用 `window.openShareModal` —— 访问过 `/shares` 后再点任意 Share 都会 `Maximum call stack size exceeded`。删除该覆盖，注册统一由 `App.vue` 挂载的 `ShareModal` 完成

### Security

- **todo Markdown 预览此前未做 HTML 净化**（`v-html` 直接渲染 marked 输出，存在 XSS 风险）；统一组件后两处预览均经 `DOMPurify` 清理
- HTML 预览 iframe 的 sandbox 仍不含 `allow-same-origin`：注入的锚点脚本运行在独立源，无法访问父页面 DOM 与 `localStorage`（登录 token）

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
