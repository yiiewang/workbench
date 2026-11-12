# Changelog

本项目的所有显著变更记录在此文件中。
格式基于 [Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/)，版本号遵循 [SemVer](https://semver.org/)。

## [Unreleased]

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

### Fixed

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
