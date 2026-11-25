# Changelog

本项目的所有显著变更记录在此文件中。
格式基于 [Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/)，版本号遵循 [SemVer](https://semver.org/)。

## [Unreleased]

### Fixed

- **工具审批卡片下方多出一条空白条**：`ToolCallList.vue` 的结果区 `<pre class="ai-tool-result">` 在卡片处于「待确认」时被 watch 预先展开，但此时工具尚未执行、`c.result === undefined`，`resultText()` 返回空串，于是渲染出只有 padding 与上边框的空白区域；改为 `v-show="open.has(i) && c.result !== undefined"`，结果到达后才显示。同时修正 watch 注释——原写「自动展开是为让批准/拒绝按钮可见」，实际按钮由独立的 `v-if="c.approval === 'pending'"` 控制，预先展开是为了批准后结果直接可见
- **编排流程图画布未铺满、圆角双层缝隙**：`PipelineGraph` 的 `.pg-scroll` 自带 `border` + `border-radius` 且高度只随内容，父级 `.pipe-board` 同样是圆角容器，导致左上角出现双重描边缝隙、点阵网格只占顶部一条、下方大片空白。改为 `.pg-wrap` / `.pg-scroll` 在 board 内 `flex:1` 撑满剩余高度，去掉内部边框与圆角（由父级 `overflow:hidden` 统一裁切），画布用 `flex + margin:auto` 在可用空间居中（超宽时仍可横向滚动且不裁左侧）
- **刷新后 AI 回复变成 `⚠ Error in input stream` 且不再续输出**：上一轮把 `streamReply` finally 里的 `setRunId(s, null)` 改为无条件执行，而 catch 又把任何异常（含浏览器刷新导致的 `ReadableStream` 中断）一律设为 `recoverable=false` —— pending run 记录被清、reader 的原生错误被当作业务错误写成 `⚠` 占位并落进会话快照，刷新后 `resumePendingRuns()` 无记录可恢复。实测服务端 `ai_runs` 里对应 run 已 `status=done` 且有完整回复，纯前端误判：
  - `api/ai.ts`：SSE `event: error` 抛出的错误打 `business=true`，与客户端流中断区分（`chatStream` 的重抛逻辑保留该标记）
  - `useAIChat.ts`：catch 里只有「用户主动停止（`s.resumeStopped`，不依赖 `AbortError` 名称）」或「服务端业务错误（`e.business`）」才判为不可恢复；刷新/网络中断且已拿到 runId 时保持 `recoverable`，不写 `⚠` 占位、不弹 toast、保留 pending 记录，由本 tab 的 `pollRun` 或刷新后的 `resumeOne` 按游标补齐
  - 局限：修复前已落库的 `⚠` 占位消息不会被自动纠正（当时 pending 记录已被清、消息本身也未存 runId），需点「重新生成」或删掉该条；之后的刷新均走恢复链路
- **邮件列表项里的 `**粗体**` 失效**：`mdToHTML` 原用 `strings.TrimLeft(t, "-*·• ")` 剥离列表符号，会一并吃掉内容前导的 `*`（`- **重点**` → `重点**`，粗体标记残缺）；改为按 `- ` / `* ` / `· ` / `• ` 具体前缀 `TrimPrefix`，只剥离列表符号本身
- **AI 对话面板左缘拖拽调宽失效**：消息目录刻度容器 `.ai-toc`（z-index:2，left:0 宽 48px）盖住了拖拽条 `.ai-resizer`（z-index:1，宽 8px），面板中部拖拽条不可点击、仅头尾狭窄区域可触发；将 `.ai-resizer` 提到 z-index:3 恢复整条可拖拽

### Changed

- **编排流程图组件归位到 `components/pipeline/`**：`PipelineGraph.vue` 原本放在 `components/ai/`，但它只服务编排模板详情（`PipelineView.vue` 的 DAG 画布），与 AI 对话无关；本轮 `ai/` 收敛成清一色的对话组件后，这个错位更明显：
  - `frontend/src/components/ai/PipelineGraph.vue` → `frontend/src/components/pipeline/PipelineGraph.vue`（新建 `components/pipeline/`），文件内容零改动：组件内部只 import `../../api/pipelines`，新旧目录同为 `components/<子目录>/`，相对层级不变
  - `PipelineView.vue` 的 import 路径同步更新；全仓仅此一处代码引用（`[Unreleased] Added` 的历史条目记录的是组件创建时路径，按事实保留不改）
  - 校验：`pnpm exec vue-tsc --noEmit` 通过，`PipelineView.vue` / `PipelineGraph.vue` lint 无告警
- **移动端历史会话改为左侧抽屉，不再整页替换消息区**：此前 `MAI.vue` 打开会话列表时用 `v-if` 渲染一个占满内容区的 `mai-sessions` 视图，同时用 `v-show="!sessionsOpen"` 把消息列表和输入区一起隐藏——翻会话时整个对话从眼前消失，且没有明确的「关闭」出口（只能再点头部图标或选一条会话）：
  - 改为 `.mai-mask` 遮罩 + `.mai-drawer` 左侧滑入抽屉（宽 `min(280px, 82%)`、`--sidebar-bg` 底色、0.18s 滑入动画），消息区与输入区始终可见；`.m-ai` 加 `position: relative` 提供定位上下文
  - 关闭路径补齐：点遮罩、抽屉标题栏新增 X 按钮、选中会话（`loadSession` 内部会置 `sessionsOpen=false`）、点新对话，均自动收起；刷新按钮保留，与 X 一起收进 `.mai-drawer-actions`
  - 与 PC 面板在 <=900px 视口下把 `.ai-rail` 退化为 absolute 覆盖抽屉的行为对齐：窄屏不做并排左栏（375px 屏宽下剩不下可读的消息区）
  - 校验：`pnpm exec vue-tsc --noEmit` 通过，`MAI.vue` lint 无告警
- **AI 对话输入区补上显式「@ 引用文件」入口**：此前引用文件只能靠键盘输入 `@` 触发 `FilePickerModal`，提示文字写着「@ 引用文件」却没有任何可点击入口，移动端触屏切符号键盘尤其不便：
  - PC `AiChat.vue`：底部工具行新增 `.ai-chip` 按钮（图标 + 文字），点击置 `showFilePicker = true`；提示文字缩为「Enter 发送 · Shift+Enter 换行」，不再与按钮和 placeholder 重复；顺带删掉 `.ai-chip` 里从未使用的 `.ai-chip-name` / `.active` / `:disabled` 规则
  - 移动端 `MAI.vue`：输入卡片顶部新增 `.mai-input-tools` 一行，引用按钮与已选文件 chips 合并显示（原 `mai-ctx-list` 带 `v-if`，只在有引用文件时才出现），触屏无需手动敲 `@`
  - 键盘 `@` 路径保留（`onInputKeydown` 仍打开选择器），两种入口并存；两端入口语义一致，外观各沿用原有卡片形态
  - 校验：`pnpm exec vue-tsc --noEmit` 通过，改动文件 lint 无告警
- **pipeline 与 schedule 解耦（pipeline 成为可被 schedule 执行的一个对象）**：此前 `internal/schedule` 里有 pipeline 专属 Job、`PipelineTemplateRunner` 签名直接带 `db.PipelineTemplateWithOwner`，模板表还混着调度列——依赖方向反了。现在 schedule 只认通用规则行 + handler，pipeline 在装配层注册自己：
  - 依赖方向：`server.go` 用 `schedule.NewTriggersJob(database, {"pipeline_template": srv.triggerPipelineTemplate}, loc)` 取代 `NewPipelineTemplatesJob`；`triggerPipelineTemplate(ctx, db.CronTriggerWithOwner)` 按 `target_id` 读模板 Definition → `runPipeline`，schedule 不需要知道 pipeline 是什么；`internal/schedule/pipelinetemplates.go` 及 `PipelineTemplateRunner` 删除
  - 迁移 000020 用 SQLite「建新表 → 复制 → 删旧表 → 改名」重建 `pipeline_templates`，去掉 `cron_expr/enabled/last_run_at/last_status`；`PipelineTemplate` 只剩 id/name/definition/时间戳，模板 CRUD 签名瘦身，删掉 `PipelineTemplateWithOwner` / `ListEnabledPipelineTemplates` / `MarkPipelineTemplateRun`；`000020.down` 加回 4 列并从 `schedule_triggers` 反向回填（多规则取最近一条），回滚不丢配置
  - 手动触发 `POST /api/pipeline/templates/{id}/run` 不再写模板的调度状态，过程与结果统一由 `pipeline_runs` 记录；模板提交不再校验 cron
  - 前端：`pipelines.ts` 去掉模板调度字段；`PipelineView.vue` 表单只留名称、顶部状态改由运行记录提供、模板启停按钮移除，cron/启用/最近执行全部交给独立组件 `PipelineTriggerCard` 通过通用 API 管理；`PipelineSidebar.vue` 不再显示启用圆点 / cron，改显示创建日期
  - 取代关系：`[Unreleased] Added` 中「编排能力第 2 步」条目关于迁移 000018 的调度列、`pipeline_templates_runner`、模板页 cron 预设 / 启停的描述，均以本条为准；`internal/pipeline` 引擎、`run_pipeline` 工具、`pipeline_runs`、DAG 流程图、`POST /api/pipeline/run`、手动触发端点路径不变
  - 单测覆盖：通用触发 CRUD / 过滤 / 隔离、000019 回填语义、000020 回滚反向回填、Job 按类型分发 / 防重 / 未知类型 / 错误回写、cron 校验与目标归属；`make test` + `pnpm build` 全绿
- **AI 对话消息区去气泡化 + 抽出 PC/移动端共用消息组件**：此前 `AiChat.vue` 与 `MAI.vue` 各维护一份近 70 行的消息模板和一套过程块折叠逻辑，样式又散在 `AiChat.vue` scoped 与全局 `ai-chat.css`，改一处要顾三处：
  - 新增 `components/ai/AiMessage.vue`：单条消息渲染（头像 / 时序分段 / 过程块折叠 / 工具调用 / RAG 引用 / token / 操作按钮），PC 面板与移动端模板各缩成一行 `<AiMessage :message :index />`；两侧重复的 `processManual` / `processOpen` / `processTitle` 等逻辑一并下沉，折叠 key 从「消息 idx:段 idx」简化为组件内段 idx
  - 上下文注入：`useAIChat.ts` 导出 `aiChatKey` / `AIChatContext`，两个父组件各自 `provide` 自己的 composable 实例，`AiMessage` 用 `inject` 复用；子组件若自行调用 `useAIChat` 会生成独立会话状态，导致审批 / 复制 / 重新生成 / 保存作用在错误实例上
  - assistant 回复去掉 `.ai-bubble.assistant` 灰底卡片，改为 `.ai-msg-col` 正文流（user 保留蓝色气泡）；过程块改为左侧细线的轻量折叠行（展开区去掉卡片边框），代码块 / 行内代码背景由 `--bg` 改 `--code-bg`，否则与页面背景融为一体
  - 新增统一「元信息行」`.ai-meta`：复制 / 保存 / 重新生成 + RAG 引用来源 + token 消耗收成一条 10px 轻量行，操作按钮 hover 显示（触屏常显）、token 右对齐；`RagCitations` 不改模板，靠 `.ai-meta .rag-cites { display: contents }` 让 toggle 成为行内项、展开列表 `flex: 1 1 100%` 独占一行
  - 顺带统一两端差异（保存按钮 title、user 引用文件 chip 图标、`data-idx` 供目录跳转），把 `.ai-msg-file-name` 从面板 scoped 迁入全局，删除未被引用的 `.ai-reasoning*` 死样式与旧 `.ai-usage` 定义
  - 校验：`pnpm exec vue-tsc --noEmit` 通过，改动文件 lint 无告警
- **AI 对话面板 UI 骨架分层（PC 浮动面板）**：功能叠加后头部挤了 5 个无文字图标、历史会话与文件索引状态各占一个覆盖消息区的互斥抽屉、消息目录刻度条常驻左缘（消息区被迫留 52px 左内边距）。本轮只重排骨架，消息内部与移动端不动：
  - `AiChat.vue` 新增可收起左栏 `.ai-rail`，用单一 `railOpen` + `railTab`（`sessions` / `index` / `toc`）把会话列表、索引状态、消息目录合成三个 tab；同一 tab 再点或 Esc 收起。头部只留侧栏开关 + 标题 + 模型 + 新建 + 收起，剧场模式入口下放到左栏底部
  - 消息区不再被抽屉覆盖：`.ai-body` 改用 `grid-template-columns: auto 1fr` + 消息/输入两行，左栏固定 216px（固定 px 避免百分比宽度与 `auto` 轨道互相依赖），侧栏收起时 auto 轨道塌缩为 0、消息区自动占满；`.ai-list` 去掉 `padding-left: 52px`
  - 消息目录由「左缘律动刻度条 + Teleport 悬浮摘要卡」改为左栏内的摘要列表：角色徽标 + 两行截断摘要、滚动高亮当前消息、点击跳转；相应删除 `tocHover` / `tocVisible` / `tocTipStyle` / `onTocEnter` / `onTocLeave` / `tocBarStyle` 及刻度条、悬浮卡样式与 Teleport
  - 抽屉状态不再依赖 composable 的 `sessionsOpen` / `toggleSessions`（`loadSession` 等内部仍会写该 ref，但 PC 面板不再读取），`watch(sessionsOpen)` 互斥逻辑删除；切到索引 tab 时按需拉取索引状态
  - 响应式：面板被拖到 <460px（`@container`）或 <=900px 视口时，左栏退化为 absolute 覆盖式抽屉（默认收起），不与消息区争宽度；剧场模式保持并排
  - 校验：`pnpm exec vue-tsc --noEmit` 通过，CSS lint 无告警
- **AI 对话消息区重设计（PC / 移动端共用 `AiMessage.vue`）**：此前 `AiChat.vue` 与 `MAI.vue` 各维护一整段消息模板（过程块、正文 markdown、RAG 引用、token、hover 操作），两端细节已分叉（灯泡图标、文件引用 chip 图标、保存按钮文案）：
  - 新增 `components/ai/AiMessage.vue`：单条消息统一渲染，包含时序分段（过程块折叠 + 正文段）、流式光标、头像、user 气泡与文件 chips、统一元信息行；两个父组件的消息循环各缩为一行 `<AiMessage :message="m" :index="i" />`
  - 状态注入：`useAIChat.ts` 导出 `aiChatKey`（`InjectionKey<ReturnType<typeof useAIChat>>`），父组件 `provide` 自己的实例；消息组件 `inject` 后直接调用 `copyMessage` / `regenerate` / `saveReplyAsFile` / `approveToolCall` / `isStreamingTail` 等，避免子组件再调 `useAIChat` 生成独立会话状态（审批、重新生成会作用到错误实例）。过程块折叠状态随组件实例保存，key 由 `消息idx:段idx` 简化为段 idx，父组件里的重复逻辑删除
  - 视觉：assistant 去掉 `--code-bg` 气泡底色与内边距，正文直接铺在 `.ai-msg-col`；user 保留右对齐蓝色气泡。复制 / 保存 / 重新生成、RAG 引用来源、token 消耗收进 `.ai-meta` 轻量行（hover 显示操作、触屏常显），`RagCitations` 以 `display: contents` 融入该行且展开列表独占一行；过程块改为左侧 2px 竖线的折叠行，展开区去掉边框底色
  - 配套：`.ai-md code/pre` 背景由 `--bg` 改为 `--code-bg`（正文无底色后仍需区分代码块）；`styles/ai-chat.css` 清理未使用的 `.ai-reasoning*` 规则，`.ai-msg-file-name` 的 ellipsis 规则从 `AiChat.vue` scoped 移到公共样式表
  - 校验：`pnpm exec vue-tsc --noEmit` 通过，相关文件 lint 无告警
- **邮件通知器装配简化**：原 `NewEmailNotifier` 在 `notify.email.enabled=false` 时仍构造一个未订阅任何事件的 `EmailNotifier` 并返回，调用方 `server.go` 用 `_` 丢弃实例（全仓仅此一处调用）——该对象无人持有、永不触发；且原顺序是先构造再校验 `host/user`，校验失败时构造白做。改为 `SubscribeEmailNotifier(...)` 只返回 `error`：`enabled=false` 直接返回，随后前置校验 `host/user` 再构造实例并订阅三类事件（事件总线持有方法值，无需返回实例）；`server.go` 直接判 error
- **邮件模板可读性与样式**：`mdToHTML` 输出由 `<br>` 连续拼接改为每项独立 `<div>`——列表项加 `padding:5px 0 5px 14px;text-indent:-14px` 悬挂缩进（长句折行与首行文字对齐）+ 项间留白，解决多个长 bullet 挤成一片文字墙；分节标题（`##` / 中文序号）加左侧 3px 蓝色色条与上间距，正文行距 1.9 → 1.8、段落自带下间距；新增行内代码（反引号）渲染为浅灰底小字，AI 日报里的 `done` / `CERTS_DELETE` 等内部标识不再露出反引号。行内标记统一由 `inlineRe` 正则（链接/粗体/代码）处理，用户内容仍经 `escHTML` 转义；`docs/email-template-preview.html` 同步更新
- **`ai` 配置结构嵌套化（破坏性变更）**：聊天配置收进 `ai.chat` 子段（`ai.base_url/api_key/model/tools` → `ai.chat.*`），与 `ai.embedding` 子段对称——两者常指向不同服务。旧平铺写法不再识别，存量 config.yaml 需手动迁移；环境变量（`AI_BASE_URL`/`AI_API_KEY`/`AI_MODEL`/`AI_TOOLS`）不受影响

### Added

- **通用定时触发规则（调度领域自己的持久化对象）**：`schedule_triggers` 表 + `GET/POST /api/schedule/triggers`、`GET/PUT/DELETE /{id}`，让任何领域对象都能成为「可被 schedule 执行的目标」：
  - 迁移 000019 建表（org/user、`target_type`、`target_id`、`cron_expr`、`enabled`、`last_run_at/last_status` + owner/enabled/target 三个索引），并从 000018 的 `pipeline_templates` **回填**已有 cron 配置（含停用状态与最近执行状态；纯手动模板不产生规则）；`internal/db/cron_triggers.go` 提供 CRUD、按类型/目标可选过滤、启用规则全库扫描、分钟键状态回写、按目标批量删除
  - `internal/schedule/triggers.go`：`TriggerHandler func(ctx, db.CronTriggerWithOwner) error` + handlers 注册表；`cron_triggers_runner` 每分钟只扫通用规则、按 `target_type` 分发、`YYYY-MM-DD HH:mm` 分钟键防重，非法 cron / 未注册类型各记状态互不阻塞，15min 超时 + 独立 ctx 回写；复用现有 `Job`/`Scheduler`，调度内核不动，**schedule 生产代码零 pipeline 标识符**
  - `internal/server/schedule_triggers.go`：CRUD handler；`schedule.ParseCron` 校验 cron，`supportedTriggerTargets` 白名单 + `validateTriggerTarget` 在装配层校验目标归属（跨用户 / 跨组织 / 不存在一律拒绝）；更新只允许改 cron/启用，目标绑定创建后不可变
  - 前端 `frontend/src/api/scheduleTriggers.ts` + 独立组件 `PipelineTriggerCard.vue`：模板页新增「定时触发」区块自管理新建 / 编辑 / 启停 / 删除，显示 cron 与最近执行状态；未保存的新模板禁用配置；第一版一个模板在界面上管理一条规则（后端支持多条）
  - 删除模板时 `DeleteCronTriggersForTarget` 级联清理指向它的规则，避免通用调度器每分钟报「目标不存在」
  - 边界：`ai_jobs` 仍是 schedule 的原生任务类型，未迁入通用表（避免把已有数据的迁移混进来）
- **AI 对话刷新恢复改为按游标增量续着输出**：此前 `pollRun` 每 2s 拉一次全量 `result` 并整段覆盖 `live.content`，断流期间看不到打字过程，刷新后要等下一次轮询才整块出现：
  - `api/ai.ts`：`AIRun` 增加 `delta`/`offset`，`getAIRun(id, offset=0)` 请求 `?offset=<rune 数>`；`useAIChat.ts` 的 `RESUME_POLL_MS` 2000 → 300（与后端 300ms 落库节流对齐）
  - `pollRun` 维护游标：首次以服务端累积正文覆盖 `live.content`（清掉本地半截或「（已停止）」占位），之后只 `live.content += run.delta`，并用服务端返回的 `run.offset` 前进；`applyRunSnapshot` 不再用 `run.result` 覆盖正文，只按 id 去重补工具卡片 / RAG 引用 / usage，正文与结构化事件两条通道互不干扰
  - 主动停止 / 业务错误时统一 `setRunId(s, null)` 清 pending 记录：否则 `resumeOne` 会把「用户已停止」的 run 当成待恢复任务、重置 `resumeStopped` 后又把内容补回来
  - 局限：仍是轮询而非真正附着到后台 run 的 SSE 重连，粒度受 300ms 落库节流约束；静默断流时服务端快照最多落后 300ms，首次覆盖可能让末尾几个字短暂回退、下一次轮询即补齐；reasoning 增量仍不落库
- **AI 后台 run 正文增量落库 + offset 游标查询（刷新续读的服务端基础）**：此前 `ai_runs.result` 只在每个工具轮结束与 run 收尾时写一次，纯文本流中途库里一直是空的，刷新恢复只能等跑完拿整段快照：
  - `parseAIDataLine` 返回每行正文增量，`forwardAIStream` 新增可选 `onDelta` 回调（3 处既有单测调用点传 `nil`）；`handleAIChat` 用回调把分片即时并入 `run.content` 并 `maybeFlush` 按 300ms 节流写 `running` 快照，删掉原先整轮结束后的 `WriteString(res.Content)`（`res.Content` 仍保留供 Agent 工具轮组消息用）
  - `GET /api/ai/runs/{id}` 支持 `offset=<rune 数>`：响应由 `db.AIRun` 扩为 `aiRunDetail`，多出 `delta`（游标之后的增量正文）与 `offset`（下次请求应带的游标）；`splitRunResult` 按 rune 而非字节切分（中文/emoji 口径一致），游标越界钳到两端、重复请求同一游标返回空增量，游标由服务端返回免去前端换算
  - 按是否带 `offset` 分流：不带游标返回原有 `db.AIRun` 全量快照（只查状态/归属的调用），带游标时 `result` 置空、只回 `delta`/`offset`/`events`/元信息，长回复每 300ms 轮询不再重复传输全量正文；前端 `getAIRun(id, offset?)` 不传游标时不带 query
  - 无迁移：`ai_runs` 表结构不变；不带游标的响应仍是全量文本，旧调用读 `result` 行为不变；文件头过时的「断连即取消上游」注释同步修正
  - 单测 `internal/server/ai_runs_test.go`：`TestSplitRunResult`（中文/emoji、空/负数/越界游标）、`TestForwardAIStream_DeltaCallback`（分片回调拼出的文本与整轮累积一致）；`make test`（`-tags sqlite_fts5`）全绿
  - 局限：仍是轮询而非 SSE 重连，增量可见性受 300ms 落库节流约束；reasoning 增量不落库（与原 `result` 语义一致）；前端已改为按 `delta` 增量追加，见上一条
- **AI 对话支持多个会话同时生成、生成中自由切换会话**：此前 `useAIChat` 只有一组 `messages`/`dbSessionId`/`streaming`，`send`/`askWith`/`regenerate`/`loadSession` 四处被单一 `streaming` 守卫拦截，生成中切会话被静默忽略：
  - 状态按会话隔离：`ChatSession`（`shallowReactive`）持有自己的消息数组（`reactive<ChatMessage[]>`）、`dbSessionId`、上游 `sessionId`、`streaming`、`abortCtrl`、runId/恢复标记与持久化防重入标记；`Map` + `currentKey` 决定当前展示会话，`streaming`/`dbSessionId` 改为依赖它的 `computed`。导出名与类型不变，`AiChat.vue` / `MAI.vue` 零改动
  - 流与会话绑定：`streamReply(userMsg, s)` 的 `live` 取自 `s.messages`（切到别的会话后增量仍写原会话的响应式代理），`AbortController`/runId/`completed`/`recoverable` 改为局部变量；`persistSession(s)`、`throttledPersist(s)`、`pollRun(..., s)`、`stopStreaming()` 都只作用于自己的会话，后台会话不抢当前会话的滚动、也不会把消息 `PUT` 到别的会话记录
  - 切换无阻：`loadSession` 去掉 `if (streaming) return`，命中内存态直接 `activate`；`clearChat` 改为新建空白会话而非清空数组，原会话的流继续跑完并落库；`removeSession` 只 abort 被删会话的流并清理它的 pending run
  - 恢复不串台：`sessionStorage` 的 `workbench-ai-active-run` 由单个 runId 改为 `PendingRun[]`（`{id, key, sessionId, dbSessionId}`），会话首次落库后 `backfillPendingRuns` 补归属；每条记录用 `GET /api/ai/runs/{id}` 的 `sessionId` 二次校验归属，删掉「按 org+user 找第一条 running」的兜底（多会话/多 tab 时那正是串台来源）；结束轮询按 runId 删记录，兼容刷新后会话 key 变化
  - 刷新后自动恢复所有会话：从「只恢复当前展示会话的第一条 run」升级为 `resumePendingRuns()` 遍历全部 pending run，每条交给独立异步任务 `resumeOne`（30min 过期保护 → `ensureSession(dbId)` 按 `dbSessionId` 重建会话内存态但不切换展示 → 尾部挂 live → `pollRun`），多个会话的轮询并行且不阻塞 UI，`resuming` 按 runId 防重复；`ensureSession` 并发载入时二次查 `findByDbId` 复用，避免同一会话两份状态，`loadSession` 也改用它；`maybeRestoreLastSession` 把扫描放到最前，面板每次打开都扫一遍，`workbench-ai-new-chat` 标记存在时也不再跳过其它会话的恢复；归属不符或过期的记录由 `forgetRun` 清理，`dbSessionId` 为空的先跳过等回填（不误删刚开跑的流）
  - 会话列表「生成中」标记：`chatSessions` 改为 `shallowReactive(Map)`（会话增删也进响应式依赖），新增导出 `streamingSessions`（聚合 `streaming && dbSessionId != null` 的会话 ID）；PC 面板与移动端会话列表项用它显示带脉冲圆点的「生成中」标签，切到别的会话也能看到哪些会话仍在后台跑
  - 局限：会话在首条消息落库前就刷新（run 尚无 dbSessionId）无法恢复；同一会话内仍串行（一轮未回完不能发下一轮）；跨 tab 写同一会话仍是 `PUT` 全量覆盖，未做版本/CAS；会话列表只在打开时拉取，新落库的会话需重新打开列表才会出现；恢复为按游标 300ms 增量追加，仍是轮询而非 SSE 重连
- **全量重建自动清理孤儿索引**：`POST /api/ai/reindex` / Agent 工具 `reindex_files` 触发的 `rag.ReindexTree` 在收集完待索引文件后新增一步清理，解决「文件已删但索引仍在、AI 仍检索并引用旧内容」：
  - 新增 `db.ListIndexedPaths(ctx, orgID, userID)`：只查 `file_index_state.path`、无 limit、按 org+user 隔离（现有 `ListFileIndexState` 有 500 条截断，不适合作全量比对）
  - `rag.orphanPaths` 把库中路径拼回用户根并 `os.Stat`，**仅 `os.IsNotExist` 判为孤儿**；刻意不用「本次遍历 `entries` 差集」——`collect` 遇 `os.ReadDir` 失败只记 warn 并跳过子树，差集会把整棵仍存在文件的索引误删
  - 复用 `DeleteFileIndex` 在事务内清 `file_chunk_vec` / `file_chunk_fts` / `file_chunks` / `file_index_state` 四表；清理失败只记 warn，不中断重建
  - 可观测：`ReindexStats` 与 `ReindexProgress` 新增 `pruned`，写入 `GET /api/ai/reindex/status` 快照（工具 `reindex_status` 同步可见）与 `rag: reindex done` 日志
  - 边界：只清「文件已不存在」；文件仍在但变成不可索引类型、隐藏/依赖目录里的旧索引、未跟随的 symlink、以及 `status='error'` 且文件仍存在的状态行都不清（分别由写钩子、`ragclean` 默认模式 / `--reset` 负责）
  - 单测 `internal/rag/service_test.go`：`TestOrphanPaths`（存在/嵌套/已删）、`TestPruneOrphans`（按 owner 隔离、他人索引不误删、error 状态行同判定）、`TestReindexTreePrunesOrphans`（空目录端到端验证清理接入与进度快照）；`make test` 全绿
  - 文档同步：`docs/05-operations.md` §5.3–§5.5/§9、`docs/02-functional-guide.md` §8.4/§14、`docs/03-design.md` §5.5/§10、`docs/README.md` 版本与更新记录
- **重建 `docs/` 文档体系（编号化 + 补齐构建/运维 + 基线追平）**：主文档改为两位序号命名并按阅读顺序组织——`00 README.md`（文档中心）、`01-getting-started.md`、`02-functional-guide.md`、`03-design.md`、`04-build-and-release.md`、`05-operations.md`，两份 2026-08-21 历史设计稿与邮件模板预览归拢 `archive/`（A1–A3），所有跨文档相对链接、归档文件头链接与根 `README.md` 引用同步改写；新增 `01`（环境/构建启动/首次超管初始化/SPA 路由注册/公网部署）、`04`（构建链路、Makefile target 全表及 `build: frontend` 每次重建前端的副作用、`CGO_ENABLED=1` / `sqlite_fts5` / embed / vec 头文件约束、质量门禁、xgo 五平台打包与 `package.sh`、发布流程、9 类构建故障）、`05`（配置非零值合并语义、启用矩阵、**RAG 索引运维**、备份、17 个迁移、日志、故障排查），并把根 `README.md` 的 Quick Start 与 Release Process 迁入这两份文档后改为链接，消除重复并顺带修正 D05（默认端口 `80` → `8080`）与 D06（`make build` 只编译 Go 的错误描述）；`02` / `03` 全量重写，追平 `000016_ai_runs` 后台运行（`runCtx` 15min + `aiRunWriter`）、1b 按 runId 2s 轮询恢复（15min 上限 / 30min 过期保护）、16 个内置工具（含 `reindex_files` / `reindex_status` / `run_pipeline`）、声明式编排（`internal/pipeline` 的 `Definition`/`Runner`/`RunContext.Interpolate`、`allowWrite` 预授权、三类 `uses`、`pipeline_runs`）、17 个迁移 / 21 项表 / 8 类 SSE 事件 / 4 条新路由；`05` §5 明确记录「删除文件会在事务内同步清 `file_chunk_vec` / `file_chunk_fts` / `file_chunks` / `file_index_state`，而 `ReindexTree` 只遍历现存文件、不做差集清理，孤儿索引需 `make ragclean ARGS=--reset` + 重启 + 全量 reindex」；`00` 建立编号规则、版本说明（`docs v2.0`，基线 `v1.3.0-25-g517f88c` + 工作树未提交的 000017 编排引擎）、更新记录、单一事实源 / 变更触发规则 / 提交前检查清单 / 一致性核对方法 / 归档规则，偏差清单复核到 17 项（D05/D06/D14/D15 标记为已处理）
- **补充三套项目文档（`docs/`）**：`docs/functional-guide.md`（功能说明，16 章：功能地图、快速开始与首次超管初始化、通用约定、各功能模块统一的「场景→操作流程→输入/输出→参数→限制」模板、PC/移动能力矩阵、配置默认值 vs `config.yaml` 示例值与环境变量表、限制条件汇总、FAQ、术语表）；`docs/design.md`（软件设计，12 章：分层架构与 3 个全局中间件链、后端 8 个包与前端目录的职责/依赖、`main.run` → `server.New` 装配顺序与失败语义、8 条关键流程时序、15 个迁移版本 + 19 项表的数据模型、全量路由与 7 类 SSE 自定义事件、安全模型、10 类扩展点、17 项设计取舍与已知约束）；`docs/README.md`（文档中心：目录结构与状态标记、文档版本规则、更新记录、单一事实源 + 变更触发规则 + 提交前检查清单 + 一致性核对方法、15 项已知文档偏差清单）。两份 2026-08-21 历史设计稿（`rbac-design.md` / `migration-strategy.md`）原位保留，文件头加归档提示与主要漂移说明（modernc/`CGO_ENABLED=0` → mattn + cgo + sqlite-vec、`org_features` → `user_org_feature` 等）；根 `README.md` 的过时描述按约定**仅登记不修改**。事实依据为 `Server.App()` 路由注册、`config.DefaultConfig` / `mergeConfig` / `ApplyEnv`、15 个迁移脚本、`aitools.go` 的 15 个工具注册表、`ai.go` 的 SSE 事件、`schedule.KnownTopics` 的逐项核对；文档内容基线锁定 `v1.3.0-23-g8f70235` + 迁移 000015；撰写期间 HEAD 前进到 `5d28238`（`000016_ai_runs` / `/api/ai/runs` / `run_id` 等），未纳入正文，登记在 `docs/README.md` §3.3「基线差距与待同步内容」
- **AI 对话面板 ESC 最小化**：`AiChat.vue` 原有 `onEsc` 只处理剧场模式退出，普通面板收起只能点右上角「收起」。扩展为分层优先级：有 `ModalShell` 弹窗（如引用文件选择器）时让路不响应 → 退出剧场模式 → 关闭历史会话 / 索引状态抽屉 → `panelOpen=false` 最小化回悬浮按钮；流式对话与 1a 的后台 run 不受影响。为让面板知道弹窗是否占用按键，把 `ModalShell.vue` 的模块级 `escStack` 抽到 `frontend/src/lib/modalEsc.ts` 并导出 `escStack` / `hasOpenModal()`，弹窗自身行为不变；移动端 `MAI.vue` 全屏页无悬浮按钮，不处理
- **编排模板页 UI 重设计（流程图即模板详情）**：上一版把 `.form-row` / `.form-input` 当成全局样式用（实际定义在 `AiJobsView.vue` 的 scoped CSS），输入框被压到约 150px、label 与输入挤成一行；流程图又藏在 Tab 后、只出现在执行记录里。重做后：
  - 信息架构：模板详情主体**默认就是流程图画布**；名称 / cron / 启用收成顶部紧凑设置栏；右侧节点 Inspector（uses / needs / when / retry / with / 该步运行输出）；JSON 降级为「查看 / 编辑 JSON」次级入口
  - 运行记录移到底部紧凑列表，点某次运行把 ok/failed/skipped + 耗时**叠加到同一张模板流程图**，不再在记录里另画一张图；「清除运行状态」可还原
  - 组件拆分：`PipelineGraph` 只管布局与渲染，选中节点 `emit('select', {step, result})` 交给父级 Inspector；共享类型 `PipelineGraphStep` 提到 `api/pipelines.ts`
  - 视觉：节点 208×76 卡片（tool 蓝 / api 紫 / llm 绿 类别色条、圆形状态徽标、id 与 uses 两行不截断、when/retry/耗时 chip）、点阵网格画布、细贝塞尔连线、hover 抬升；表单样式在本组件内完整定义（输入 100% 宽、label 块级、预设按钮 6px 间距）
  - Inspector 第一版只读，内容编辑仍走 JSON，避免双向同步复杂度
- **编排模板页渲染 DAG 流程图**：纯 JSON 文本框与长文本执行记录不直观，改为可视化：
  - 新组件 `frontend/src/components/ai/PipelineGraph.vue`：SVG 贝塞尔连线 + HTML 节点，不引入 mermaid/dagre（避免继续放大 bundle）；分层规则复刻 `internal/pipeline` 的 needs 语义（缺省=依赖前一步、`[]`=同层并行、id 列表=菱形 DAG），Kahn 分层并对未知依赖 / 自依赖 / 环给出可读错误
  - 节点显示 id / uses / when / retry / 耗时；`steps` 结果按 ok / failed / skipped 上色；点击节点展开该步 with 参数与输出/错误；画布可横向滚动，SVG marker id 按实例随机避免多图冲突
  - 模板编辑页 Definition 区加「定义 / 流程图」Tab，切到流程图时实时解析当前 JSON（解析失败给提示、不影响编辑）；名称 / cron / 启用表单始终可见
  - 运行记录展开时上部叠加状态流程图，逐步原始输出收进 `<details>` 默认折叠；`PipelineRun.definition` 类型改为 `unknown`，兼容后端 `json.RawMessage` 返回的对象
- **编排 DAG 依赖与同层并行（第 3 步前半）**：`Step` 新增 `needs`，引擎从顺序循环改为拓扑分层 + 层内并行：
  - 语义：`needs` 缺省 = 依赖数组前一步（保持原有顺序语义，旧模板零改动）；显式 `needs: []` = 无依赖可并行；`needs: ["a","b"]` = 等两者完成（菱形 DAG）。json tag 不带 `omitempty`，避免空数组在工具参数的 marshal→unmarshal 往返中被吞掉退化成 nil
  - `Definition.Validate` 增加未知依赖 / 自依赖 / 环检测；`Layers()` 用 Kahn 算法分层并对层内索引排序，保证结果顺序稳定
  - `Runner.Run` 按层启动 goroutine（并发上限 4，避免一次打满 LLM/embedding 上游），`sync.WaitGroup` 收齐一层后才写 `RunContext.Outputs` 并落一次全量快照；变量插值在启动前完成，层内并发不共享写。任一步失败即停，未执行步骤保持 skipped
  - 结果数组按 Definition 顺序输出（不再是完成顺序），`onStep` 改为每层一次
  - 工具描述与模板页提示同步 needs 语法；补菱形 DAG、环检测、并行 barrier 与跨步骤插值单测
  - 条件与重试（第 3 步后半）：`Step.When`（on_success 默认 / on_failure / always）——主流程失败即停，显式声明的补偿步骤仍执行，无失败时 on_failure 步骤自动 skipped；`Step.Retry`（0–5）按 200ms×次数固定退避重试，耗时计入单步 `durationMs`；`Validate` 校验取值与上限。失败后仍遍历剩余层做 when 判定，每层各落一次快照
  - 工具描述与模板页提示同步 needs / when / retry；补菱形 DAG、环检测、并行 barrier、跨步骤插值、失败补偿、重试次数、成功时跳过 on_failure 等单测
  - 局限：无表达式型条件（rules.if）与动态分支；Definition 仍用 JSON 文本框编辑（无可视化编排器）
- **编排能力第 2 步（后端）：HTTP 入口 / cron 触发 / 模板固化**：第 1 步的执行核心变成多入口共用能力，模板只是把 Definition 存库复用：
  - 共享核心：`execRunPipeline` 拆出 `runPipeline(ctx, orgID, userID, runID, def)`，与 `iris.Context` 完全解耦；AI 工具仍走 `aiToolRunCtx`（1a 后台上下文），HTTP / cron / 模板各自用 `context.Background()` + 15min 超时派生
  - `POST /api/pipeline/run`：直接提交一份 Definition，后台异步执行并立即返回 runId，逐步结果仍由 `GET /api/pipeline/runs/{id}` 查询
  - 模板：迁移 000018 `pipeline_templates`（definition JSON、cron_expr、enabled、last_run_at/last_status、org+user 隔离）；`GET/POST /api/pipeline/templates`、`PUT/DELETE /{id}`、`POST /{id}/run` 手动触发；提交时校验 Definition 与 cron 表达式
  - cron：`internal/schedule` 新增 `pipeline_templates_runner`，每分钟扫描启用模板、`YYYY-MM-DD HH:mm` 分钟键防重（同分钟重复 tick / 重启不重跑）、独立 goroutine + 15min 超时，状态回写用独立 ctx（沿用 `aijobs.go` 的异步 ctx 约定）；执行函数由 server 注入，schedule 不反向依赖 server
  - app 初始化时机：`New()` 内提前 `iris.New()` 并复用给 `App()`，避免 cron 早于路由装配触发时 `ContextPool` 为空导致工具步骤 panic
  - 前端：`frontend/src/api/pipelines.ts`（模板 CRUD / 手动触发 / Definition 直调 / 运行记录）、`PipelineView.vue`（Definition JSON 编辑、cron 预设、启停、立即执行、最近执行按 runId 展开逐步输出）、`PipelineSidebar.vue`（列表 + 新建）；补 `GET /api/pipeline/templates/{id}` 供编辑表单读取 Definition；路由 `/pipelines` + Activity Bar / PC 首页 / 移动端首页三处入口，移动端复用 DefaultLayout 的 ≤900px 适配
  - 局限：仍顺序执行；Definition 用 JSON 文本框编辑（无可视化编排器）；条件 / 并行 / 重试 / DAG 待第 3 步
- **声明式编排引擎（第 1 步：编排能力本身，模板后置）**：一次编排 = 一份纯数据 Definition 交给统一 Runner 按序执行；对话里 AI 自己构造步骤数组调 `run_pipeline` 即实现「自主规划多步」，无需改造 Agent 循环：
  - 新包 `internal/pipeline`（不依赖 HTTP/iris/db）：`Definition{name,allowWrite,steps}` + `Step{id,uses,with}`；`RunContext` 递归插值 `{{ steps.<id>.output }}`（字符串直嵌、其余 JSON 序列化，未产出引用原样保留）；`Runner` 每步独立超时（5min）、失败即停并把剩余步骤记 skipped，`onStep` 交出累积快照便于逐步落库；`Executor` 由调用方注入
  - 三类能力（`internal/server/pipeline.go`）：`tool/<name>` 复用现有 AI 工具、`api/<path>` 仅站内 `/api/*`（`GenerateToken` 自签短期 token + `X-Org-Id` 带当前用户身份，经 `app.ServeHTTP` 内部调用）、`llm` 走 `aiChatOnce` 非流式生成
  - 后台执行上下文：`withToolContext` 从 `app.ContextPool` 合成 `iris.Context` 预置 userID/orgID，`Request().Context()` 直接绑后台 run ctx，工具层零改动，与 1a 的 `aiToolRunCtx` 天然衔接
  - 审批：`run_pipeline` 一次界面确认覆盖整条流程；写类工具与非 GET 的 api 步骤必须在 `allowWrite` 显式列出，否则该步失败（工具层与 HTTP 写操作统一预授权）
  - 状态：迁移 000017 `pipeline_runs`（definition/status/steps/err_msg/时间戳，org+user 隔离），每步落库；`GET /api/pipeline/runs` 与 `/{id}` 查询；工具只回传步骤摘要，完整输出走查询接口
  - 初始化循环规避：`aiTools` 直接引用 `execRunPipeline` 会形成「aiTools → execRunPipeline → execAITool → aiToolByName → aiTools」包级变量初始化环；改为 `pipelineTool()` 由 `allTools()` 运行时合成，`aiToolByName` 统一查合并表
  - 形态取舍：保留 GitLab CI 的「声明式纯数据、逐步状态」，砍掉 stages/并行 job/artifacts/runner（单进程平台无构建集群）；步骤采用 GitHub Actions 式 `uses` + `with` + 插值，比 shell script 更贴合「每步是一种能力」
  - 局限：第一版仅顺序执行，无模板存库 / HTTP 直调入口 / cron 触发 / 条件与重试（第 2、3 步）；Definition 目前来自工具参数（JSON）
- **AI 对话后台运行结果前端恢复（1b）**：断连/刷新后自动按 runId 补齐后台跑完的回复，让 1a 落库的结果真正可见：
  - `chatStream` 新增 `onRunId` / `onDone` 回调并分发 `run_id` 事件；`onDone` 只在收到 `data: [DONE]` 时触发，用于区分「完整结束」与「静默断流」（后者 `reader.read()` 正常结束、不抛错）
  - `useAIChat` 把 runId 存入 `sessionStorage`；静默断流时保持 `streaming` 并启动 `pollRun`（2s 轮询 `/api/ai/runs/{id}`，上限 15min），不再补「（已中断）」占位；用户主动停止与业务错误仍走原有收尾
  - `applyRunSnapshot` 以后端 `result` 覆盖半截正文，按 id 去重补齐工具调用/结果、RAG 引用与 token 消耗；`steps` 置空交给 `legacySteps` 归一化重建，避免半截时序步骤与完整正文不一致
  - 刷新/打开面板时由 `maybeRestoreLastSession` 末尾触发 `resumePendingRun`：优先本地 runId（含已完成的断流遗留，30 分钟过期保护），本地记录丢失时用 `GET /api/ai/runs` 兜底找仍 running 的；PC 面板与移动端共用同一 composable，组件零改动
  - 轮询结束才清 runId 并 `persistSession()`；`stopStreaming` 同时终止轮询（服务端 run 仍会跑完，结果仍可查）
- **AI 对话与请求连接解耦（断连后继续跑完）**：一次 `/api/ai/chat` 抽象成后台 run，关闭面板/断网不再掐断 Agent 循环：
  - 运行上下文：`runCtx`（`context.Background()` + 15min 总超时）取代 `ctx.Request().Context()` 派生上游请求；前置 RAG 检索与全部工具执行经 `aiToolRunCtx` 取用同一上下文（`aitools.go` 内 11 处 `ctx.Request().Context()` 统一替换），断连后 DB/RAG 操作仍可继续
  - 镜像输出：`aiRunWriter` 包装 SSE ResponseWriter，首次写失败即标记 `disconnected` 并静默吞掉后续写入，`forwardAIStream` 继续解析上游分片；断连时不再 `return`，剩余工具轮与最终回复照常生成
  - 状态落库：迁移 000016 `ai_runs`（runId、org/user、status= running/done/failed、events、result、err_msg、时间戳）；开跑插一条 running，每个工具轮结束落一次快照，任一路径退出由 defer 落最终态；`events` 为 `run_id/rag_hits/tool_call/tool_result/usage` 结构化事件，`result` 累积各轮文本
  - 查询接口：`GET /api/ai/runs`（最近 20 条，不含 events）与 `GET /api/ai/runs/{id}`（含事件，按 org+user 隔离）；SSE header 就绪后先推 `run_id` 事件，供前端后续按 runId 轮询恢复（1b）
  - 审批策略：断连后写类工具无人确认，直接跳过并回传 `{error:"用户已断开连接，写操作已跳过"}` 让模型基于已有结果收尾；审批等待期间断开同样作废审批，不再阻塞循环
  - 局限：运行状态跨进程重启不恢复（循环仍绑定当前进程）；前端恢复 UI 待 1b
- **AI 可触发索引重建并查询进度**：新增内置工具 `reindex_files` / `reindex_status`，Agent 不再只能读索引、把重建推给界面或后台：
  - 共用启动逻辑：`handleAIReindex` 的「RAG 可用性检查 → org:user 防重 → 后台 `ReindexTree`」抽成 `startReindex`，返回 `reindexStarted / reindexUnavailable / reindexAlreadyRunning` 三态；`POST /api/ai/reindex` 映射为 200 / 50301 / 40902，工具映射为 `started` 或明确说明，HTTP 与 Agent 不再有两套防重/异步语义
  - `reindex_files`：写类工具、`NeedApproval=true`——全量 embed 有实际成本，复用 `runToolWithApproval` → `approval_request` SSE → 界面批准/拒绝（5 分钟超时）后才执行；适用于刚写入/上传的文件搜不到、或改过 `ai.embedding.dim` 后索引失效；后台异步执行，返回 `started` 并提示用 `reindex_status` 查进度
  - `reindex_status`：只读无审批，直接回传 `rag.ProgressOf` 的快照副本（`running/total/processed/indexed/skipped/failed/currentPath/startedAt/updatedAt`，`running=false` 即最近一次已完成记录），从未触发过时给出提示；`ProgressOf` 在锁内做值拷贝，重建 goroutine 并发更新不影响工具结果
  - 零前端改动：工具调用/结果/审批卡片沿用既有 `tool_call` / `tool_result` / `approval_*` SSE 事件；事件总线与 MCP 工具表未动
- **AI 接入 MCP 外部工具**（`ai.mcp.servers`）：让 Agent 能调用外部 MCP 服务的工具，范围限定为 **HTTP 型无状态服务**——不引入会话生命周期与权限模型：
  - 配置：`MCPConfig.Servers`（`name` / `url` / `headers`），headers 值支持 `${ENV_VAR}` 展开使凭据不落仓库；`mergeConfig` 按 slice 合并，`DefaultConfig` 保持零值（无服务 = 功能关闭）
  - 新包 `internal/mcpclient`：JSON-RPC 2.0 `initialize` → `notifications/initialized` → `tools/list` → `tools/call`，30s 超时；响应兼容 `application/json` 与 `text/event-stream`（取首条 `data:`）；`Mcp-Session-Id` 仅从握手响应透传，不维护会话表/重连/子进程
  - 工具桥接（`internal/server/aitools_mcp.go`）：启动时各服务拉一次 `tools/list` 转成与 `aiTools` 同构的静态表，`mcp_<server>_<tool>` 命名空间防冲突、`inputSchema → parameters`、结果以 `{"result": text}` 回传并沿用 32KB 限长；`aiToolByName` 改为 `Server` 方法、`aiToolsPayload` 改为接受合并表，`execAITool` / `runToolWithApproval` 链路原样复用，前端 SSE 事件零改动
  - 安全：外部工具一律 `NeedApproval=true`（复用 `runToolWithApproval` → `approval_request` SSE → `aiApprovalHub`），可信 endpoint 由配置白名单决定；启动握手 10s 超时，配置缺 `name`/`url` fail fast，服务不可达只记日志并跳过
- **邮件跳转链接**：新增顶层 `site_url` 全局配置（环境变量 `WORKBENCH_SITE_URL` 覆盖；由 `Server` 装配时传给 `EmailNotifier`，notify 包不耦合整个 `Config`）——站点对外地址不只服务邮件，分享链接/AI 内容等场景亦可复用；邮件通知运行在 schedule 事件总线、无 HTTP 请求上下文，站点根地址无法像分享链接那样从 `ctx.Host()` 推断，只能由部署环境提供。配置后三类邮件正文下方渲染蓝色 CTA 按钮（table + `bgcolor` 内联样式，兼容邮件客户端），纯文本兜底同步附上 URL；未配置或地址非 http(s) 时不渲染任何链接，避免发出 `localhost` 死链。跳转目标：任务到期/精确时间提醒 → `/todo?task=<id>`（`url.QueryEscape` 编码任务 ID，前端 `TodoView` 的 `route.query.task` watcher 自动打开编辑弹窗；未登录由路由守卫带 `redirect` 回跳），AI 定时任务报告 → `/jobs`（管理该定时任务），普通 `user.notify` → `/todo`。`mdToHTML` 新增 `[文字](url)` 渲染，`safeHref` 只允许 http(s) 且与 site_url 同 host，外域 / `javascript:` / 站点未配置时降级为纯文字，防 AI 编造链接与 href 属性注入
- **AI 定时任务注入 RAG 检索**：`LLMCaller` 签名扩展任务归属用户身份（全局公共任务展开后的实际执行者），执行 prompt 前以该用户身份做 RAG 前置检索（top-3），命中片段（`ReferencePrompt`，无编号标注要求）拼进 prompt——日报/总结类定时任务可自动引用个人文件（周报文档、项目笔记）；检索失败静默降级
- **文件索引状态可视化**：`GET /api/ai/index-status`（汇总 files/chunks/errors + 明细 500 条）；AI 面板头部新增数据库图标按钮，抽屉式展示（绿点=ok 红点=失败、路径/块数/错误悬浮提示，与历史会话抽屉互斥，可刷新）
- **AI 对话 token 消耗显示**：请求上游时携带 `stream_options.include_usage`（流式响应末尾携带 usage 分片），后端解析并**聚合 Agent 多轮**消耗后在 [DONE] 前以自定义 SSE 事件 `usage` 推给前端；assistant 气泡底部右对齐小字显示 `prompt 1.2K · completion 800 · 共 2.1K tokens`（>1000 缩写 K）。usage 随消息持久化到会话快照（历史回看保留）；上游网关不支持 include_usage 时分片缺失，不显示（无害降级）。PC 面板与移动端同步
- **混合检索（FTS5 关键词路 + RRF 融合）**：向量 KNN（语义路）之外增加 FTS5 trigram 全文检索（关键词路），双路召回（各 topK×4）经 Reciprocal Rank Fusion（`score = Σ 1/(60+rank)`）融合取 topK——**专有名词/函数名/错误码的精确匹配不再受向量距离阈值误杀**（阈值只作用于语义路候选，FTS 命中按 bm25 直接入围），两路同时命中的块显著加权；query <3 字符（trigram 无 gram）自然降级纯向量；FTS 失败降级纯向量。迁移 000015 建 `file_chunk_fts`（trigram 分词，中英文通用子串匹配；unicode61 对中文是整段单 token 不可用）+ 存量回填；写删路径事务内同步维护；mattn 驱动需 build tag `sqlite_fts5`（Makefile 全部构建入口统一携带）；ragclean --reset 同步清 FTS

### Added

- **RAG 引用卡片（编号引用 + 行级定位跳转）**：AI 回答基于检索片段作答时，底部展示可折叠的"引用来源"列表——每条含编号、文件路径（可点击）、起始行号、命中的原文片段（引用了哪句话直接可见，不依赖模型转述）：
  - **编号引用制**：注入 prompt 的片段按 `[1]…[n]` 编号，要求模型以 `[n]` 标注结论出处（比让模型复述路径稳定）；命中数据经 SSE 自定义事件 `rag_hits` 一次性推给前端，挂在当前回答上
  - **行号链路**：迁移 000014 `file_chunks` 加 `start_line` 列；chunker 切分时记录每块原文起始行（1-based，重叠块锚定新正文）；**行号定位是粘性的**——文件内容异步加载完成前到达的定位请求会保留重试
  - **跳转**：点击路径 → PC 端 `#/路径#L12` hash（`useIndexApp` 新增 `route.hash` 活页 watcher，顺带修复 markdown 内链在已打开页面不生效的问题）→ 展开目录树 + 打开 tab + `CodePreview` 滚动到目标行居中并闪烁高亮（自动展开覆盖该行的折叠区；超大文件降级时仅打开）；移动端走 `/m/file?path=&line=` query
  - 前端新组件 `RagCitations`（PC 面板与移动端共用）；`ChatMessage` 类型加 `ragHits` 字段（会话历史快照一并持久化）
- **RAG 文件语义检索**（`ai.embedding` 配置段启用；迁移 000013）：AI 对话自动检索用户个人文件——提问时以最新一条 user 消息做向量检索，命中片段以独立 system 消息注入上下文（含来源路径，模型按相关性取舍，与前端"引用文件"机制并存），全程零手动 `@` 引用：
  - **向量基建**：SQLite 驱动从 modernc（纯 Go）换 mattn/go-sqlite3（cgo）+ sqlite-vec v0.1.6 静态扩展（源码 vendor 于 `internal/sqlitevec/`，配套 sqlite3.h 复制自 mattn 的 sqlite3-binding.h，与实际链接的 sqlite 版本严格一致，升级 mattn 后 `make sync-vec-headers` 刷新）；vec0 虚拟表按 org:user 分区（partition key），KNN 查询只扫本人分区（无跨用户泄漏）；构建需 `CGO_ENABLED=1`（Makefile 已内建），交叉编译走既有 xgo 流程
  - **索引链路**（`internal/rag`）：文本类扩展名白名单（md/txt/json/yaml/go/py 等，≤2MB）→ SHA-256 整文件幂等判重（内容未变零 embed 成本）→ 按空行段落边界切分（目标 ~800 字 + 相邻块 100 字重叠，超长段按句末标点硬切）→ OpenAI 兼容 `/embeddings` 批量向量化（`dimensions` 参数下发 Matryoshka 降维，批 16 条）→ 替换式事务写表（`file_chunks` 存原文 + `file_chunk_vec` 存向量 + `file_index_state` 存状态）
  - **写钩子自动索引**：上传（编辑器保存统一走 /api/upload）、AI `write_file`、删除/重命名/移动（fsops，含目录前缀递归）三处钩子维护索引；写钩子异步执行不阻塞主流程；embedding 失败记 error 状态行，下次钩子或全量重建时重试；非文本/超限文件若已有索引自动清理（文件被替换场景）
  - **检索与重建**：对话前置检索 top-5，8s 超时静默降级（对话可用性优先于检索完整性）；`POST /api/ai/reindex` 手动全量重建（幂等，hash 未变跳过；同用户并发触发 409）
  - **配置**：`ai.embedding.{base_url, api_key, model, dim}` 独立于聊天网关（Qwen3-Embedding-8B 原生 4096 维，默认降 1024——精度损失很小，存储与 KNN 扫描量降 1/4）；dim 与库中记录不一致时自动 DROP 向量表并作废全部索引状态（等全量重建）；环境变量 `AI_EMBEDDING_BASE_URL / AI_EMBEDDING_API_KEY / AI_EMBEDDING_MODEL / AI_EMBEDDING_DIM` 同名覆盖

### Removed

- **访问统计（visit_logs）**（迁移 000012 drop 表）：移除 `GET /api/stats` 接口与每请求的 visit_logs INSERT（无谓写放大）；文本访问日志（access log 文件）保留。历史统计数据随表删除，需要时可经 down 迁移恢复表结构重新启用

### Added

- **任务精确时间提醒**（迁移 000009：tasks 加 `scheduled_time`/`due_time` 列，HH:mm 可选）：编辑弹窗时间范围选择器升级为 `datetimerange`（一个控件选日期 + 时刻；约定时刻 `00:00` = 未指定，归一为空，避免无时间任务触发凌晨提醒风暴）；每分钟扫描 job（`todo_time_reminder`）——`scheduled_time` 到点发"计划开始"提醒、`due_time` 到点且未完成发"即将截止"提醒，经 `task.reminder` 事件 → 邮件投递（含任务状态/优先级/进度）；去重键含时刻（`taskID:kind:HH:mm`），同分钟重跑不重发。PATCH 缺省保留语义扩展至 `scheduledTime`/`dueTime`（omitempty 空值省略，快捷修改不清空时刻）；AI 工具 `create_task`/`update_task` 支持 `dueTime`/`scheduledTime` 参数（HH:mm 校验，空串清除）
- **AI 定时任务**（迁移 000009/000010/000011；功能栏「定时」入口 `/jobs`，首页功能卡片）：用户自建"到点让 AI 执行 prompt"的任务——
  - **全局公共任务模型**（000010）：`user_id IS NULL` = 全局任务只存一条（预置"每日工作总结"），执行时对所有有邮箱的用户逐个以本人身份展开（各自任务数据/投递），避免按用户复制记录导致数据量膨胀；私有任务（user_id 归属）用户自建自管；前端「公共」徽标 + 只读
  - 管理：侧栏任务列表（名称/cron/启用状态点/新建）+ 主区域左右双卡片（左详情表单含 cron 频率预设，右执行记录）；CRUD API `/api/ai/jobs`（cron 创建时校验 fail fast）
  - **执行记录**（000011 `ai_job_runs` 表）：每次触发留痕（时间/状态/输出前 4KB），`GET /api/ai/jobs/{id}/runs` 查看（全局任务的记录所有用户可查）
  - 执行引擎：`ai_jobs_runner` 每分钟读库（增删改下一分钟生效，无需重载调度器）→ cron 命中且 `last_run_at` 非本分钟（幂等，重启不重跑）→ 组 prompt（平台角色 + 该用户任务数据精简 ≤50 条 + 指令）→ 非流式 LLM 调用（`aiChatOnce`，复用上游 AI 配置，2 分钟超时）→ 结果经 `user.notify` 事件邮件投递；失败也通知；单任务独立 goroutine 慢任务不阻塞 tick
  - **Agent 工具接入**：`list_ai_jobs`（只读）/ `create_ai_job` / `update_ai_job`（先读后合并，公共任务不可改）/ `delete_ai_job`（写类走审批）——对话里一句话即可安排定时任务
  - 典型用法：每天 18:00 AI 总结任务发日报；每周一 09:00 生成周报与本周优先级建议

### Added

- **定时调度系统**（`internal/schedule`，`schedule.enabled` 开启，默认关闭）：cron 引擎 + 进程内事件总线（ebus）+ 订阅式通知渠道，三层解耦——定时触发、事件分发、投递通道各自独立，未来 webhook/站内信只需新增订阅者：
  - **手写 cron 引擎**（零依赖）：标准 5 段表达式（分 时 日 月 周），支持 `*`、`*/n` 步进、`a-b` 区间、逗号列表与英文别名（`wed`/`nov`）；预编译为 5 个 bitset，每分钟整点对齐 tick 后 O(1) 匹配；日/周均受限时取 Vixie cron 并集语义；表达式配置错误启动即失败（fail fast）
  - **EventBus**：topic → handlers 发布订阅；Publish 为每个 handler 起独立 goroutine（发布方不阻塞），handler panic recover 不影响总线与其他订阅者
  - **TodoDueScan 内置 job**（`todo_due_cron` 默认每 5 分钟）：全库扫描 `due <= today && status != done` 的任务 → 逐条经 `notify_log` 表去重（同任务同天只提醒一次：截止日当天 + 逾期每天各一次，`INSERT OR IGNORE` + 受影响行数判断，无竞态窗口）→ 发布 `task.due` 事件；任务完成后自然停止提醒
  - **邮件通知渠道**（`internal/notify`，`notify.email.enabled` 开启）：`EmailNotifier` 订阅 `task.due`，向**任务归属用户**的邮箱投递（含任务标题/截止日/逾期天数/优先级/进度）；SMTP 587 STARTTLS（`net/smtp`，465 出站常被云厂商干预故不使用），授权码走环境变量 `EMAIL_PASS`；UTF-8 base64 编码正文/主题（RFC 2045 76 字符折行）；用户未设邮箱跳过记日志，投递失败不影响其他事件
  - 时区支持（`schedule.timezone`，IANA 名，默认 Asia/Shanghai）；单测覆盖表达式解析/匹配/非法输入、总线分发与 panic 隔离、去重扫描（当天去重/次日重发/逾期天数）、邮件内容与报文组装
- **AI 事件总线工具**（Agent 与事件系统打通）：
  - `list_events`（只读）：总线内省——`KnownTopics` 注册表（topic → 用途说明）+ 实时订阅者数，AI 据此了解可触发什么
  - `notify_user(subject, body)`（写类，走审批）：向当前用户发布 `user.notify` 自定义通知事件 → EmailNotifier 订阅并按用户邮箱投递；发布异步（受理即返回），用户未配置邮箱时跳过记日志
- **用户邮箱字段**（迁移 000008：users 加 `email` 列 + `notify_log` 去重表）：通知投递目标。admin 用户管理创建/编辑支持邮箱（格式校验）；用户自助修改接口 `POST /api/profile/email`（空串 = 清空不再接收提醒），移动端「我的」页提供编辑入口；`userinfo` 聚合接口携带 email
- **AI 工具调用（Agent 模式）**（`config.yaml` `ai.tools: true` 或环境变量 `AI_TOOLS=true` 开启，默认关闭保持纯对话；要求上游模型支持 function calling）：`/api/ai/chat` 从透传代理升级为服务端 Agent 循环——模型可调用 4 个内置工具（`list_dir` 列目录 / `read_file` 读文件 / `list_tasks` 查任务 / `create_task` 建任务），后端执行并把结果以 tool 消息回传模型继续决策，最多 5 轮（防无限循环）：
  - 安全：工具一律在后端执行，文件路径复用 `resolveUserPathSafe` 沙箱校验（safeJoin + EvalSymlinks），前端只做展示；工具结果限长 32KB（防撑爆上下文）、执行 panic 兜底转为错误结果回传模型
  - 流式解析：`tool_calls` 分片按 index 累积（id/name 首片携带、arguments 逐片拼接）；中间轮上游 `data: [DONE]` 抑制、最终轮统一补发（否则前端提前断流）；流已开启后上游再失败改走 SSE `error` 事件（HTTP 状态码已不可改）
  - SSE 协议扩展：新增 `event: tool_call` / `tool_result` / `error` 自定义事件
  - 前端：消息气泡内可折叠工具调用卡片（工具名/参数摘要/执行结果 JSON，执行中闪烁、出错红框），PC 面板与移动端共用新组件 `ToolCallList`；会话历史保留工具上下文（`toPayloadMessages` 展开为 assistant(tool_calls)+tool(结果) 序列回传，多轮对话模型可见自己调过什么）；`/api/ai/status` 新增 `tools` 字段
  - 消息校验扩展：role 支持 `tool`，assistant 带 `tool_calls` 时 content 允许为空；新增流式分片拼接/[DONE] 抑制/断开/校验单测
- **AI 写类工具与审批机制（Human-in-the-loop）**：Agent 模式新增 2 个写工具——`write_file`（写/覆盖文件，父目录须已存在，内容限 256KB）与 `update_task`（更新已有任务，先读现有任务再合并改动字段——db 层 UpdateTask 为全字段覆盖写，防未提供字段被清空）；写工具执行前必须经用户确认：
  - 跨请求协调：SSE 对话流发 `approval_request` 事件后阻塞等待，用户经 `POST /api/ai/approval {id, approved}`（独立 HTTP 连接）提交决定，`aiApprovalHub`（channel + 一次性条目 + 用户绑定）接通两条连接；审批 ID 直接复用上游 tool_call ID
  - 安全：审批绑定发起用户（归属校验通过才消费/删除条目——陌生人请求无法作废他人审批）、一次性语义（重复提交失败）、60s 超时视为拒绝、客户端断开（关面板/停止生成）自动作废不执行
  - SSE 新增 `approval_request` / `approval_resolved` 事件；拒绝/超时以说明性结果作为 tool 消息回传模型，由模型向用户解释原因后继续对话
  - 前端：工具卡片待确认态显示「批准/拒绝」按钮与状态徽标（待确认/已批准/已拒绝/已超时），本地先更新防重复点击、提交失败回滚可重试；中断生成时未决审批定格为已拒绝；PC 面板与移动端共用 `ToolCallList`
  - 新增审批协调器单测（决定传递/归属校验/一次性/作废语义），首版实现"先删后校验归属"的缺陷由测试暴露后修复
- **回复时序渲染与过程折叠**：Agent 多步回答从"类别堆叠"（工具卡片全在顶部）改为按发生顺序的时间线——思考 → 调工具 → 分析结果 → 继续调工具 → 最终回答。消息新增 `steps` 时序数组（SSE 回调按序追加，相邻同类增量合并），渲染时连续过程步骤归入可折叠"思考与工具调用"块（标题显示工具步数，流式执行中默认展开、完成后折叠，手动 toggle 覆盖），正文片段独立渲染（中间轮说明文本自然穿插在过程块之间）；旧会话快照无 steps 时从扁平字段归一化重建，历史回看不受影响；原思考过程独立折叠条移除（并入过程块）。PC 面板与移动端同步
- **RAG 引用编号悬浮预览**：正文中模型标注的 `[n]` 引用编号渲染为可悬浮的角标元素——hover 显示浮动卡片（文件路径可点击跳转 + 起始行号 + 命中原文片段，可滚动），不用展开底部引用列表即可核对出处。实现：`linkRagRefs` 对渲染后 HTML 做 DOM 后处理注入 `data-*`（跳过代码块、编号前字符边界检查排除 `a[1]` 数组下标误伤、超编号的幻觉引用不处理）；全局单例 `RagRefTooltip` 组件 document 级委托（PC/移动共用，z-index 高于剧场模式）；底部"引用来源"列表同步简化为仅文件清单（编号/路径/行号可点击），原文详情收敛到悬浮卡片一处；跳转逻辑提取共用 `lib/ragNav.ts`
- **AI 面板剧场模式**：面板头部新增切换按钮——开启后面板脱离右下角锚定，视口居中放大（宽 `min(1100px, 100vw-80px)`、高 `min(820px, 100vh-60px)`），背景可见不遮挡全屏；Esc 或再点按钮退出；移动端不提供（面板本就近乎全宽），窗口缩到移动断点自动退出剧场态；宽度拖拽在剧场模式下隐藏（尺寸由视口决定）
- **Agent 工具 `search_files`（语义检索）**：模型可按自然语言问题主动检索用户文件（query + topK≤10），返回带路径/起始行号/原文的片段列表——检索片段不足以回答时模型换关键词多轮检索，替代此前"猜路径盲调 read_file"的失败模式（RAG 未启用时返回明确错误）
- **Agent 系统提示补检索纪律**：文件内容类问题优先用语义检索片段；不够时 search_files 换关键词，read_file 只用于已确认存在的具体文件
- **Agent 系统提示加停止准则（用户实测：多轮对话中反复 search_files"确认细节"）**：新增规则——本会话已检索到/已读取过的内容视为已知，足以回答时直接总结作答，不为确认细节反复检索；只有关键内容确有缺口才再检索，同一问题换关键词不超过两次
- **RAG 全量重建进度可观测**：`GET /api/ai/reindex/status` 返回进度快照（running/total/processed/indexed/skipped/failed/currentPath/startedAt/updatedAt，保留最近一次已完成记录）；`ReindexTree` 重构为"先收集后处理"两阶段——预扫描全部可索引文件得到准确 total（进度分母），处理阶段逐文件更新快照并每 20 个输出周期日志（`rag: reindex progress`，tail 日志可观测）。快照按 org:user 维度互斥存取，多用户并发互不覆盖
- **RAG 文件级摘要卡（两层索引，可选）**：`ai.embedding.digest_model` 配置 flash 级小模型——索引时为每个文件生成语义摘要卡（是什么/核心内容/关键点，80~150 字），作为特殊块（`chunk_index=-1`）与原文块**同表共存、同等参与 KNN 检索**（表结构零迁移）：宏观问题（"这个项目怎么做的"）命中摘要卡，token 极省且无源码噪音；细节问题（"那行配置具体怎么写"）继续走原文块 + 引用行号定位。摘要只在文件内容变化时重新生成（hash 幂等保证），生成失败降级只索引原文块；输入截 3000 字控制成本。Env: `AI_EMBEDDING_DIGEST_MODEL`
- **RAG LLM 相关性预判（可选）**：`ai.embedding.judge_model` 配置 flash 级小模型（如 deepseek-flash，复用 embedding 网关的 base_url/api_key 走 /chat/completions）——检索命中过完距离阈值后，把问题 + 候选片段（各截 300 字）一次调用批量判断实际相关性（结构化输出相关编号），拦住阈值拦不住的语义无关命中（中文 embedding 空间各向异性）。判断标准写明"仅主题相近、词面重合但答非所问的不算"；失败/超时降级保留阈值结果（预判不拖垮对话可用性）；模型明确判定全不相关返回空（有效结论）；留空 = 关闭。Env: `AI_EMBEDDING_JUDGE_MODEL`

### Fixed

- **AI 对话刷新后不自动恢复上一次会话**：点「新对话」设置的 sessionStorage 标记（`NEW_CHAT_FLAG`，语义"用户明确要空白"）在后续新提问落库后从不清除——同一浏览器 tab 内只要点过一次"新对话"，之后刷新页面就永远不自动恢复历史（尽管库里有最新会话）。修复：`persistSession` 成功保存后清除标记（新内容已产生，空白语义失效；标记只在"明确开了新会话且还没说话"时有效）
- **已完成任务的"计划开始"提醒仍发邮件**：`ScanTimeReminders` 的 due 条件早有 `status != 'done'` 过滤，但 start（scheduled_time 到点）条件没有——已完成任务到了计划开始时刻照样投递提醒。改为整查询统一 `t.status != 'done'`；顺带修正注释（两条件同时命中实际只返回一行、start 优先，原注释称"返回两行"与实现不符）
- **刷新后 token 消耗丢失**：`persistSession` 的防重入竞态——流末的最终保存（含 usage）恰逢节流保存进行中时被 `if (persisting) return` 直接丢弃，库停留在无 usage 的中途快照。改为排队补一轮：被挡时置队列标记，当前保存完成后自动重存拿到最新数据（含 usage 与最后的增量）
- **中断的回复没有重新生成按钮（用户实测：中断发生在思考/工具阶段时操作行消失）**：操作行显示条件依赖 `m.content`，中断在思考/工具阶段时正文为空 → 连保存/复制也没有，用户只能重新手打问题。修复：① 新增 `hasReplyContent`（正文/思考/工具/时序步骤任一存在即算有产出），重新生成按钮条件放宽——中断消息可直接点 ↻ 重问；② 流结束兜底放宽为"无正文即补"（此前有工具调用时不补"（已中断）"，中断发生在工具阶段时气泡只有一个思考折叠条、看不出被打断）
- **流式中断后对话卡死（用户实测：中断的空 assistant 消息导致后续提问整体 400 "content is empty"）**：两条路径漏兜底——① 上游静默断流（EOF 无内容不抛错）时 content 为空落库（`（已停止）`兜底只在 catch 分支）；② 流式中途的节流持久化已把空 assistant 写进会话，中断后残留。修复：finally 统一兜底（content 空且无工具调用 → 补"（已中断）"文本步）；`toPayloadMessages` 跳过空 assistant（历史脏数据不再发给上游，存量会话无需手动清理即恢复可用）
- **ragclean 直连 go run 报 no such module: fts5**：FTS5 是 mattn 驱动的编译期 build tag，Makefile 的 build/test 已统一携带但独立 go run 入口天然缺 tag。加 `make ragclean [ARGS=--reset]` target 统一携带（tag 管理收敛 Makefile 一处），工具头注释同步更新
- **FTS5 段式存储惰性删除（与 vec0 同款，用户实测 --reset 后仍占 18.5MB）**：FTS5 的 DELETE 是标记式删除，VACUUM 只回收空闲页、影子表内已删数据仍占页——`--reset` 改为整表 DROP；启动时 `EnsureFTS` 自动重建（表缺失即建 + 回填存量，与 EnsureVecIndex 双信号思路一致），FTS 重建失败仅降级纯向量不挡 RAG。实测 --reset 后 18.5MB → 404KB
- **短问题跨话题歧义命中（用户实测："v5 版本怎么使用"命中另一项目的 v5 报告）**：短 query 的指代（"v5"指 minibridge 的 v5）依赖对话上下文，孤立检索会命中库中其他项目的话题相近内容，且向量/关键词两路同时命中被 RRF 加权放大。修复：当前问题 <30 字时拼接上一条 user 消息前缀（80 字）补充语境（"minibridge 是什么 v5 版本怎么使用"可精确命中 minibridge/README）；引用来源列表改为**只展示模型实际引用的片段**（正文出现对应 `[n]` 才显示）——检索命中 ≠ 被引用，模型判定无关忽略的命中不再上墙
- **RAG 索引清理工具 `scripts/ragclean.go`**（`go run` 单文件，build tag ignore 不参与常规构建）：系统 sqlite3 CLI 缺 vec0 模块无法直接操作虚拟表，本工具复用项目驱动（mattn + sqlite-vec）执行。两种模式：默认按排除规则清垃圾；`--reset` 全量重置——**vec0 v0.1.6 的 DELETE 是标记失效位的惰性删除，向量 blob 空间不回收**（实测 3.3 万垃圾块删除后 196MB blob 残留、VACUUM 无效），只有 DROP 整表重建才能真正释放（实测 251MB → 352KB）。重置后服务启动自动重建空表（EnsureVecIndex），reindex 重新灌入
- **ragclean --reset 后 reindex 全部失败（用户实测：`file_index_state` 全空）**：`--reset` 只 DROP 了 vec0 表但残留 `rag_meta` 的维度记录，`EnsureVecIndex` 见"维度一致"误判表已存在而跳过建表，后续写入全部 no such table。修复：一致性判定改为**双信号**（meta 记录与表实际存在缺一即重建），表缺失重建时顺带作废 `file_index_state`（防 hash 命中跳过导致向量永远缺失）；工具 `--reset` 同步清 meta。新增回归用例（DROP 表 + meta 残留 → 自动重建可写）
- **RAG 误索引依赖/隐藏目录**（用户实测：reindex 逐个向量化 `.venv` 里的 site-packages）：遍历排除规则——隐藏目录（`.` 前缀，覆盖 .venv/.git/.idea）与依赖/构建目录（node_modules/\_\_pycache\_\_/vendor/target）整个子树跳过；隐藏文件（.env/.gitignore 等）同步排除（Indexable 层，上传钩子一致生效）。依赖产物无知识价值且数量庞大（一个 .venv 上千文件，纯浪费 embed 费用）
- **RAG HTML 索引正文提取（去标签噪音）**：`.html`/`.htm` 此前按源码原样切分——`<div>`/`<style>` 等高频标签污染 embedding 向量（检索质量下降）且浪费注入 token。索引前经 `golang.org/x/net/html` 提取可见正文：跳过 script/style/svg 等无语义子树，`<head>` 只取 `<title>`（页面主题锚点），块级元素空行分段（与 chunker 段落边界天然对齐）、行内文本空格拼接；提取后文本与源码行号无法映射，HTML 命中的引用行号记 0（跳转只打开文件不定位行），代码/markdown 类行号定位不受影响；文件变更检测仍用原始内容 hash（提取消耗不影响幂等）。顺带修复 chunk_test 一处 vet 格式错误；依赖锁定 go 1.25 兼容版本（x/net v0.43.0）
- **RAG 全量索引不覆盖符号链接**（用户实测：软链目录整个子树零索引）：`filepath.WalkDir` 默认不跟随符号链接，链接到别处的目录（如 blog_example）被静默跳过，而文件浏览器/AI read_file 在 `allow_symlink=true` 下能正常访问——同一文件"能读却搜不到"。`ReindexTree` 改为自写递归遍历：跟随开关与 `server.allow_symlink` 语义一致（关闭时严格限定根内、链接条目跳过），跟随时解析真实路径去重防循环链接（A→B→A 死循环），索引路径记用户视角的链接侧路径，内容读真实文件
- **RAG 检索相关度阈值过滤**：KNN 没有"不相关"概念——问天气这类库中完全没有相关内容的提问，也会返回"距离最近的 topK 个垃圾片段"（截图案例：天气问题引用了 5 个测试报告）。`ai.embedding.max_distance`（默认 0.9，归一化向量 L2 距离 ≈ 余弦相似度 0.6）以上的命中直接丢弃，KNN 按距离升序返回故首个超阈值即可截断；`rag_hits` 事件与注入 prompt 同源过滤。中文 embedding 空间各向异性（无关文本距离普遍 <1.0），首版默认 1.1 实测拦不住无关命中，收紧至 0.9；`rag: context injected` 日志同步输出每条命中的实际 distance（阈值调参依据）。Env: `AI_EMBEDDING_MAX_DISTANCE`
- **RAG 全链路自查优化（6 项）**：① chunker 超长段切分的行号统计 O(n²)——无空行的 MB 级单行文件（minified js）索引卡死，改为增量计数 O(n)（含句子边界回退时 [cut, i] 换行的补偿计算）；② CodePreview 行定位用 scrollIntoView 会连带滚动整个页面，改为仅滚代码容器自身（rect 差值计算居中）；③ 定位请求 pendingLocate 在切换 tab 时不丢弃，旧文件的定位会错误落到新文件上；④ RagRefTooltip 状态/委托从实例级闭包提升为模块级单例（双实例共存时后挂载实例的 handler 未注册、tooltip 失效），实例卸载改引用计数；⑤ search_files 工具 topK=10 全中文长块逼近 32KB 工具结果上限（超限整个结果变错误提示），片段截断 500 字；⑥ RAG 检索 query 无长度限制（超长问题直发 embedding API 可能 4xx），截断 2000 字
- **Agent 工具轮次用尽后"只有工具卡片没有回答"**：达到 5 轮上限时模型往往仍在请求调工具，被掐断后直接补发 [DONE]，整次回答零文本。改为上限轮不再携带 tools（模型只能文本输出），并在进入上限轮前注入 system 提示"轮次已达上限，基于已获得的工具结果直接回答，不足部分明确说明"

- **AI 对话流式期间"思考过程"无法滚动**：每个 reasoning/正文增量回调都无条件把消息列表拉到底部（每秒数十次），用户展开思考过程往上滚立即被拉回底部。改为 stick-to-bottom——scroll 事件感知用户上滚（距底 >40px 即暂停跟随，滚回底部自动恢复；程序拉底自带 120ms 事件窗口防误判）；发消息/载入会话/流结束等用户主动场景仍无条件拉底并恢复跟随（`scrollBottom(force)` 分级）。首版纯距离阈值方案（距底 >80px 才暂停）在高频增量拉底下用户单次滚轮滚不出逃逸距离，表现为"完全滚不动"，故废弃

- **邮件主题显示原始编码（`=?UTF-8?B?...`）**：`b64()` 对最后一段也补 `\r\n`，主题 encoded-word 被裸换行截断且无 `?=` 闭合，收件方无法解码原样显示。新增 `encodeWord`（RFC 2047）：按 ~40 字节切段（回退 UTF-8 字符边界防切断中文）、每段独立闭合、段间 CRLF+空格续行折叠；显示名同样处理（支持中文）
- **AI 定时任务执行成功但邮件查询失败（context canceled）**：`runAIJob` 的 `runCtx` 在函数 return 时被 `defer cancel()` 取消，而 `bus.Publish` 的邮件 handler 是异步 goroutine——LLM 完成后主流程毫秒级返回并取消 ctx，handler 的查库请求被打断（竞态）。发布事件改用独立长生命周期 ctx（事件分发不绑定发布方生命周期）
- **AI 写工具审批按钮不显示（用户反馈超时取消的根因）**：`useAIChat.streamReply` 中 `reply` 对象 push 进响应式数组后，流式回调仍在修改**原始对象**（raw）——Vue 深层代理不为 raw 修改触发视图更新，`approval: 'pending'` 状态变化无法到达 `ToolCallList`，批准/拒绝按钮永不渲染（60s 超时必然发生）。改为 push 后取 `messages.value[len-1]` 响应式代理操作；顺带：审批超时 60s → 5 分钟（人工操作场景），待确认的工具卡片自动展开（不依赖用户点开折叠卡片）
- **长会话报错 too many messages (max 30)**：Agent 对话中一条"AI 回复"在 payload 展开为 assistant(tool_calls) + N 条 tool 消息，原 `HISTORY_LIMIT=20`（按显示条数）展开后突破后端 30 条上限。历史截断改为**按"轮"预算**（user + 其回复为原子单位，展开后总条数 ≤28，从尾部往前取）——既不超限，也不把 assistant 与其 tool 消息拆开（OpenAI 语义非法）
- **TodoView 打开即崩（TypeError: e.showModal is undefined）**：useTodoApp 拆分重构时编辑弹窗 store refs（`showModal`/`isCreating`/`editingTask`/`editingDateRange`/`activeTaskId`）经 ctx 传入各域模块后不再经域模块转发，主 return 导出面遗漏——TodoView 的 `watch(todoApp.showModal.value)` 与模板 `v-if="showModal"` 全部失效；导出面补齐

### Added

- **HTML 邮件模板**（简约版，设计稿 `docs/email-template-preview.html`）：三封邮件共用骨架（类型灰字/加粗标题/meta 信息行/正文/细分隔线/底部说明），`multipart/alternative`（HTML 优先 + 纯文本兜底，老客户端自动降级）；`mdToHTML` 极简渲染——markdown `##/-/**`（AI 执行 prompt 强制要求输出标准 markdown）+ 中文序号小标题（"一、二、"）+ `·/•` 列表兜底识别，用户内容经 `html/template` 转义防 XSS；AI 报告标题去掉 `[定时任务]` 前缀（类型标识移至顶部灰字）
- **Agent 系统提示**：Agent 模式注入 system prompt——平台角色 + 当前日期时间（修复模型无时间感知、任务日期靠猜）+ 写操作礼仪（先说明计划再请求确认、一次一个写操作）+ 创建任务给全时间范围（只说截止则起止相同、涉及钟点给 Time 参数）
- **任务时间范围参数补全**：`create_task`/`update_task` 新增 `scheduled`（计划开始日期）；创建默认值兜底（due 未给 → 当天、scheduled 未给 → 与 due 相同），AI 创建的任务永远有时间范围

### Changed

- **AI 对话 UI**：操作按钮布局重做——user 提问 hover 悬浮显示、AI 回答常驻，均在气泡下方流内排列（对齐文字列、避开头像列），轻量线性图标（无边框、hover 主题色）；长会话渲染窗口（打开只渲染最近 5 条，顶部"加载更早"逐批 +10 展开）
- **AI 对话目录刻度**（PC 面板左缘，消息 >6 条时出现）：条宽随内容长度变化、点击平滑跳转、滚动律动（活跃条拉长变深、相邻按距离衰减，随滚动实时跟随）；摘要卡 `Teleport` 到 body 修复被 toc 容器 overflow 裁剪不显示的问题；滚动监听改模板 `@scroll` 声明式绑定（原 `onMounted` 手动绑定在面板未打开时绑了个空——律动失效的根因）；刻度条垂直居中/左对齐向右伸展/活跃条 28px；hover 期间摘要卡常驻（水平固定于目录列右缘、垂直平滑跟随选中条居中、内容原地更新不闪烁）、出现时淡入、两段式内容（粗体角色标题 + 灰色摘要）
- **Todo 编辑弹窗时间选择**：`datetimerange` 一体选择（日期 + 时刻），宽度撑满表单列；约定时刻 `00:00` = 未指定（归一为空，避免无时间任务触发凌晨提醒风暴）
- **AI 定时任务执行**：LLM 调用超时 2 → 5 分钟（推理模型生成日报类长文实测需要数分钟）；执行 prompt 明确输出格式要求（Markdown 分节/列表/粗体，禁用中文序号与 `·` 符号）
- **移动端**：MHome 新增「AI 定时任务」入口（/jobs 响应式单列可用）；README 更新（Agent 工具 13 个清单、schedule/notify 配置段、8 个新 API 端点、Features）


### Performance

- **任务列表/详情分离（content 瘦身）**：`GET /api/tasks` 列表不再携带任务正文（content）全文，以 80 字截断的 `contentPreview`（按 rune 截，防中文乱码）替代；新增 `GET /api/tasks/{id}` 详情接口返回完整任务（含 content）。配套防丢数据设计：
  - **PATCH content 缺省语义**：`db.UpdateTask` 为全字段覆盖写，列表瘦身后看板拖拽/勾选完成/移动端快捷操作等路径的任务对象没有 content 字段——patchTask 解析请求体探测 `task.content` 字段是否存在，缺失时回填服务端现值（快捷修改零改动且不再清空正文）；显式传空串 = 用户主动清空
  - `TaskItem.Content` 加 `omitempty`（空正文序列化省略，与旧版响应兼容）+ 新增 `ContentPreview` 字段
  - 前端：PC 编辑弹窗改为**先拉详情成功再弹窗**（失败不弹，杜绝无正文对象进入保存链路）；移动端任务详情点击时拉取（失败降级）；看板卡片预览用 `contentPreview`；冲突对比卡两侧（编辑对象/服务端副本）均含 content，对比逻辑不变
  - AI 的 `update_task` 工具本就先读后合并全量写，不受影响
- **API/静态资源响应压缩**：全局压缩中间件（iris `CompressWriter`，gzip/deflate/br 按客户端 Accept-Encoding 协商，不声明则自动回退不压缩）。排查 `/api/tasks` 公网访问慢（0.59s）定位：服务端直连仅 2.2ms，慢在网络传输——56KB 未压缩 JSON 在公网 RTT 下需多轮传输。压缩后 JSON/前端静态资源（embed 的 JS/CSS）预计缩减 70-85%；流式接口显式排除：`/api/media`（`http.ServeContent` 的 Range 字节区间与压缩语义冲突，视频拖动会失效）、`/api/ai/chat`（SSE 逐行透传，压缩徒增小 gzip 块）。压缩中间件与访问日志的 `Record` 顺序兼容（记录未压缩数据）；附压缩行为单测（声明/不声明 Accept-Encoding、流式路径跳过三例）

## [1.3.0] - 2026-11-10

### Changed（Breaking：任务同步机制重设计）

- **服务端权威版本号（revision）+ 任务级乐观锁**，彻底替代旧版 md5/version_json 对账体系（users 表迁移 000007：新增 revision 列、删除 version_json 列）：
  - 所有任务写入（增/改/删）revision 单调 +1；增量接口 PATCH/POST/DELETE `/api/tasks/{id}` 携带 `baseRevision`（必填），与服务端不一致返回 409 + 服务端任务副本
  - 全量 PUT `/api/tasks` 端点废弃；任务 id 生成改为 UUID（消除多设备并发重复 id）
  - GET `/api/tasks` 响应改为 `{revision, tasks}`；新增轻量端点 GET `/api/tasks/revision`
- **冲突处理重做**：旧版三层冲突弹窗链（选择→逐项 radio→汇总确认，中途关闭丢选择）与整套 md5 推断逻辑删除；409 时同字段碰撞才弹**单任务对比卡**（本地 vs 服务端并排、差异字段逐项选择，一次解决），无碰撞静默合并，新增/删除冲突自动重拉
- **多设备自动对账**：30s revision 轮询 + 页面切回前台立即对账，他端写入后本端视图自动刷新（编辑弹窗打开/冲突待裁决/离线时自动跳过）
- ⚠️ 升级注意：旧客户端的 PUT 全量同步不再可用，前后端需同版本部署

### Fixed

- Todo 页运行时崩溃（批次手术残留）：return 导出残留已删除的 `save`（ReferenceError）、`conflictCard` 未导入（冲突卡永不显示）
- `importJSON` 调用已删除的 unwrapData；改为合并导入语义（同 id 跳过、新任务逐条上传）
- `clearAll` 此前只改本地不同步服务器（刷新即复活）；改为逐条 DELETE

### Refactored

- `useTodoApp.ts` 拆分（1275→1005 行）：同步层抽 `composables/todoSync.ts`（全新类型化实现）、纯工具抽 `lib/todoUtils.ts`（日期/逾期判定/标签映射，类型化）

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
