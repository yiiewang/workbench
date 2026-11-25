# 定时任务可执行对象设计

> 面向：开发者 ｜ 状态：**现行（P0-a / P0-b / P1 已实现）** ｜ 代码基线：`v1.3.0-26-gc0577c4` + 工作树 000021–000024（`docs v2.3`）
> 功能说明见 `02-functional-guide.md`、软件设计见 `03-design.md`、文档维护规范见 `README.md`（00 号入口）。
> 本文已纳入现行文档基线；后续按 `README.md` §5.2 随代码变更同步。

## 1. 结论与范围

两条结论：

1. **执行对象多态**：把 `ai_jobs` 的执行对象从硬编码 `prompt` 抽象成多态 `target`，四类 target 全部归一化为 `pipeline.Definition`，复用同一个 `runPipeline` 执行核心。不为 schedule 写四套执行器，并天然继承编排已有的 `tool/<name>`、`api/<path>`、`llm` 三类能力。
2. **调度收敛**：`cron` / 启停 / 最近执行状态统一归 `schedule_triggers`；`ai_jobs` 退化为只描述"做什么"的纯执行定义，与 `pipeline_templates` 平级；删除 `ai_jobs_runner`，全仓只剩一个每分钟扫描器 `cron_triggers_runner`。

也就是说：现在"几点跑"和"跑什么"混在 `ai_jobs` 一条记录里、两个闹钟各扫各的表；改完"几点跑"只在 `schedule_triggers` 一行，"跑什么"只在 `ai_jobs` 一行，闹钟扫到 `target_type="job"` 就去取 job 内容，扫到 `pipeline_template` 就去取模板内容，两者最后都进 `runPipeline`。

`cron` 是调度领域的**通用能力，不限于 AI**：`ai` 只是 job 内部四种 `target` 之一，`pipeline_template` 已经是另一种被触发对象。新增领域对象只需在装配层注册 handler，不需要新的扫描器（§4 / §12）。

已确认：`interface` 指**站内 `/api/*`**，不引入外部 HTTP 外访边界。外部 MCP 定时执行 P0 禁用，P1 再议；外部 HTTP 接口整体移出范围，将来需要时另立安全边界与议题。

P0 全部落在编排已有能力内：站内接口链路、自签短期 token + `X-Org-Id` + `app.ServeHTTP` 内部调用、结果限长、每步超时、逐步落库，均不改 Executor。

## 2. 现状与差距

`ai_jobs_runner`（`internal/schedule/aijobs.go`）链路写死：`buildAIJobPrompt`（平台角色 + ≤50 条任务 + `job.Prompt`）→ `LLMCaller`（RAG + `aiChatOnce`，`aiJobTimeout` 5min）→ `MarkAIJobRun`（`output` 前 4KB）→ `bus.Publish(user.notify)`。失败也发通知。全局公共任务（`user_id IS NULL`）按 `ListUsersWithEmail` 展开，每用户以本人身份取任务数据、各写一条 `ai_job_runs`。

编排侧（`internal/pipeline`、`internal/server/pipeline.go`）已具备三类分派：`tool/<name>` 复用 `execPipelineTool`、`api/<path>` 走 `execPipelineAPI`、`llm` 走 `aiChatOnce`；`Definition.Validate` 校验 ≤50 步、id 唯一合法、`uses` 形态、`when`/`retry` 与 DAG 无环；`Runner` 按 `Layers()` 分层、同层并发上限 4、每步 5min、`onStep` 落 `pipeline_runs` 全量快照、主流程失败即停。`execRunPipeline` 已拆出与 `iris.Context` 解耦的 `runPipeline(ctx, orgID, userID, runID, def)`，HTTP 直调、手动触发、模板触发共用。

两者能力重叠，缺的是一层"派发 + 校验"。另外三处必须一并处理：

- `03-design.md` 权衡表仍有一行写「第一版仅顺序执行；无模板存库 / HTTP 直调 / cron / 条件 / 重试」，而 `run_pipeline` 已支持 `needs`/`when`/`retry`，模板、HTTP 直调、通用定时触发均已落地。`02` §9.3 已更新为「模板与触发规则是两个领域的对象」，`README.md` §6 的 D18 已在 `docs v2.2` 关闭。落地时只需修正 `03` 那一行。
- `runAIJobs` 与 `runCronTriggers` 都用分钟键 `YYYY-MM-DD HH:mm` 判重（`LastRunAt == nowKey`）。跑超过 1 分钟的 pipeline 会在下一分钟 tick 被**重复触发**（跑了 3 分钟，下一分钟闹钟以为没跑过，又跑一遍）。收敛后只剩一条扫描链路，租约只需在一处实现。
- 写类工具在对话侧依赖 SSE 界面的 HITL 审批（`run_pipeline` 是写类工具，一次确认覆盖整条流程）。定时入口直接调 `runPipeline`，不经过工具层审批分支，因此不存在"无人确认挂死"，但也意味着安全模型必须由静态预授权承担。

## 3. 与现有代码的对齐（核对结论）

| 方案早期假设 | 代码 / 文档事实 | 本设计采用 |
|---|---|---|
| 工作树有未提交的 000018 模板（含 cron 触发） | 000018–000020 已随 `c0577c4` 提交，`docs v2.2` 已同步、D18 已关闭、迁移基线 000020；000020 删掉 `pipeline_templates` 的 `cron_expr/enabled/last_run_at/last_status`，模板只存 Definition | 不再有「模板 cron 收敛为预设值」这一动作 |
| 迁移 000019 `ai_jobs_target` | 000019 是 `schedule_triggers`，000020 是模板纯化，000021 已被 AI 会话消息占用（工作树未提交） | 两步各一个迁移：**000022**（target 列）、**000023**（调度收敛） |
| cron 唯一归属 `ai_jobs` | cron 已归 `schedule_triggers`，`cron_triggers_runner` 按 `target_type` 分发；`server.go` 的 `supportedTriggerTargets` 目前只有 `pipeline_template`；CHANGELOG 明确 `ai_jobs` 未迁入通用表 | 反向收敛：cron 统一归 `schedule_triggers`，`ai_jobs` 不再持有调度列（§12） |
| 两个扫描器并存 | `ai_jobs_runner` 与 `cron_triggers_runner` 各自每分钟扫表 | 只保留 `cron_triggers_runner`，`job` 注册为新的 `target_type` |
| `target_type` 是 AI 专属 | `000019_schedule_triggers.up.sql` 的 `target_type TEXT` 注释写「由装配层注册 handler」，`target_id INTEGER`；`supportedTriggerTargets` 白名单 + `validateTriggerTarget` 是唯一扩展点，`pipeline_template` 已注册 | 用中性 `job` 命名，`job` 与 `pipeline_template` 平级；将来 `task` / `file` 沿用同一扩展点 |
| 创建 job 时「直接用编排既有的 `allowWrite` 判定函数」 | 判定内嵌在 `execPipelineTool`（`tool.NeedApproval && !contains(def.AllowWrite, name)`）与 `execPipelineAPI`（`method != GET && !contains(def.AllowWrite, "api/"+name)`），是运行期执行判定，无独立可复用函数 | 抽一个共享判定函数供创建时与执行时共用，避免两份逻辑漂移；不改判定语义 |
| 响应 `code!=0` 判失败，可选 `allowNonZero` 放行 | `execPipelineAPI` 已判 `w.status` 非 2xx 或 `env.Code != CodeOK` 即失败并返回 `env.Data`，**没有 `allowNonZero` 开关** | P0 不放行；`allowNonZero` 降级为后续扩展，不写进 P0 |
| 「校验直接调 `Validate`，不另写一份」 | `Definition.Validate` 不含路径 / 工具黑名单，也不做 `allowWrite` 预检 | 装配层补一层 `ValidateJobTarget`（黑名单 + 预授权预检），再调既有 `Definition.Validate`；结构校验不重复实现 |

## 4. 统一执行对象模型

`ai_jobs`（语义上是**通用定时执行对象定义**，历史表名暂留）只描述"做什么"（`target`），"何时做"由 `schedule_triggers` 的规则行描述（`cron_expr` / `enabled`）。四类 target 归一化成 `pipeline.Definition` 后交给共享核心：

| `target.type` | 含义 | 归一化后的 Definition |
|---|---|---|
| `ai` | 纯 LLM prompt（现状，默认） | 单 `llm` 步 + 派发前 prelude 展开上下文 |
| `tool` | 单个内置工具（`tool/<name>`） | 单 `{id, uses:"tool/<name>"}` 步 |
| `interface` | 单个站内接口（`api/<path>`） | 单 `{id, uses:"api/<path>"}` 步 |
| `pipeline` | 编排 Definition 或模板引用 | 原样 Definition（支持 DAG / `when` / `retry`） |

`ai`/`tool`/`interface` 都是 1 步 Definition，只有 `pipeline` 是多步 / DAG，派发层因此可以很薄。

**两层 `target` 不能混**：

| 层 | 表达什么 | 落在哪 |
|---|---|---|
| 被触发的领域对象 | `schedule_triggers.target_type` | `job`、`pipeline_template`、将来的 `task` / `file` |
| 触发后执行什么 | 对象内部的 `target` | `ai` / `tool` / `interface` / `pipeline` |

`ai`/`tool`/`interface`/`pipeline` 是**执行内容**，不拆成四种 `target_type`；否则注册表与 `validateTriggerTarget` 会随能力类型膨胀。新增领域对象的成本固定为：`server.go` 注册一个 `TriggerHandler` + `supportedTriggerTargets` 白名单一行 + `validateTriggerTarget` 一个 case，扫描器与执行核心不动。

以 id 9「A股收盘总结」为例，改完是两行数据。`ai_jobs` 一行只有内容：

```json
{
  "name": "A股收盘总结",
  "target": {
    "type": "pipeline",
    "templateId": "tpl_market_close",
    "params": { "date": "{{run.date}}" },
    "notify": { "on": ["success", "failure"], "render": "steps" }
  },
  "context": { "tasks": false, "ragTopK": 0 },
  "allowWrite": ["notify_user"]
}
```

`schedule_triggers` 一行只有时间：

```json
{ "targetType": "job", "targetId": 9, "cronExpr": "10 15 * * 1-5", "enabled": true }
```

**上下文渲染保持不变但可配置**：`{{run.date}}`、`{{tasks}}`（≤50 条）、`{{rag.topK}}` 以及 `params`/`query`/`body` 中的同类占位符，由 prelude 在派发前展开，不塞隐藏步骤，避免污染步骤摘要。

## 5. `interface` target 约定

`interface` 归一化为单步 `{uses:"api/<path>"}`，复用 `execPipelineAPI` 的站内调用链路（自签短期 token + `X-Org-Id` + `app.ServeHTTP` 内部调用），执行器零改动。

**path 约定**：`02` §9 写 `api/<站内 /api/* 路径>`，工具参数示例写 `api/ai/reindex/status`；代码事实是 `execPipelineAPI` 用 `"/api/" + strings.TrimPrefix(name, "/")` 自己补前缀。因此：

- target 与 `uses` 里的 `<path>` 存**相对路径**（不含 `/api/`），如 `tasks/open`、`ai/reindex/status`；
- `ResolveTarget` 只负责产出 `api/<rel>` 并校验路径沙箱，**不补 `/api/` 前缀**，避免双重前缀；
- 路径沙箱（`..`、`://`、空白字符）与身份绑定沿用 `execPipelineAPI`，不另写一份。

```json
{
  "type": "interface",
  "method": "GET",
  "path": "tasks/open",
  "query": { "date": "{{run.date}}" },
  "notify": { "on": ["success", "failure"] }
}
```

归一化为 `{ "id":"call", "uses":"api/tasks/open", "with": {"method":"GET","query":{...}} }`。响应信封 `{code,msg,data}` 由 `execPipelineAPI` 判定：非 2xx 或 `code!=0` 该步失败，成功返回 `data`；结果沿用编排现有结果限长并写入 `pipeline_runs.steps`，每步 5 分钟超时、失败即停。`allowNonZero` 不在 P0。

## 6. 执行架构

```
cron_triggers_runner 整分钟 tick（唯一扫描器）
  → ClaimCronTrigger（CAS 加租约）→ 按 target_type 分发
      ├─ job                → triggerJob（读 ai_jobs.target）
      └─ pipeline_template  → triggerPipelineTemplate（读模板 Definition）
  → ResolveTarget
      ├─ ai        → prelude 渲染 prompt + llm 单步
      ├─ tool      → tool/<name> 单步
      ├─ interface → api/<path> 单步
      └─ pipeline  → templateId → Definition（或内联）
  → ValidateJobTarget（黑名单 + allowWrite 预检）→ Definition.Validate
  → 共享 runPipeline 核心
       withToolContext 合成 iris.Context，预置 job 归属 userID/orgID
       Executor 分派 tool/api/llm；with 插值 {{ steps.<id>.output }}
  → onStep 落 pipeline_runs + ai_job_runs
  → 归一化 output（前 4KB）→ bus.Publish(context.Background(), user.notify)
```

三条入口——对话里的 `run_pipeline` 工具、HTTP 直调 / 模板手动触发、定时触发——只保留 `runPipeline` 一个执行核心。`triggerPipelineTemplate`（按 `target_id` 读模板 Definition → `runPipeline`）是可直接照搬到 `triggerJob` 的注册 handler 范式；`ResolveTarget` / `ValidateJobTarget` 都放在 server 装配层，`internal/schedule` 不含任何业务标识符。发布事件使用独立长生命周期 `ctx`，不绑定 `runCtx`。

全局公共任务仍按 `ListUsersWithEmail` 展开（该逻辑从 `runAIJobs` 迁入 `triggerJob` handler），每用户以本人身份解析路径 / 任务数据、各写一条 `ai_job_runs`。

## 7. 数据模型（迁移 000021 / 000022）

| 对象 | 变更 |
|---|---|
| `ai_jobs`（000022） | 新增 `target_type`（`TEXT NOT NULL DEFAULT 'ai'`）、`target_json`（target 完整 JSON，含 prompt / templateId / definition / method / path）、`allow_write`（静态预授权清单 JSON）、`context_json` / `notify_json`；`prompt` 列保留不删 |
| `ai_jobs`（000023） | 用 SQLite「建新表 → 复制 → 删旧表 → 改名」重建，删掉 `cron_expr` / `enabled` / `last_run_at` / `last_status`；`.down` 加回 4 列并从 `schedule_triggers` 反向回填（多规则取最近一条），回滚不丢配置（照搬 000020 的做法） |
| `schedule_triggers`（000023） | 新增 `lease_until`（租约）；`target_type` 白名单增加中性类型 `job`；`validateTriggerTarget` 增加 `job` 归属 case 与全局公共任务分支 |
| `ai_job_runs` | 000011 原表仅 `id/job_id/ran_at/status/output`，增列 `target_type`、`pipeline_run_id`、`steps_summary`、`status`、`err_msg`、`duration_ms`、`finished_at` |

**兼容回填**（000022 / 000023 各做一半；均已实现）：

- `target_json = {"type":"ai","prompt":prompt}`，`target_type='ai'`；现有 id 6「每日工作总结」、id 9「A股收盘总结」自动成为 `type=ai`，行为不变。
- 从 `ai_jobs` 中 `cron_expr <> ''` 的任务回填 `schedule_triggers`（`target_type='job'`、`target_id=ai_jobs.id`、cron、启停状态、最近执行状态），照搬 000019 从模板回填的做法；纯手动 / 无 cron 的任务不产生规则。
- 全局公共任务（`user_id IS NULL`）的规则行用 `org_id=0 / user_id=0` 标记，执行时由 `triggerJob` 按 `ListUsersWithEmail` 展开；`validateTriggerTarget` 只允许归属用户管理私有 job 规则。
- 删除任务时经 `DeleteCronTriggersForTarget` 级联清理指向它的规则，避免调度器每分钟报「目标不存在」。

**通用性边界与命名**：

- `target_id INTEGER NOT NULL`：想「直接定时跑某个工具 / 站内接口」，字符串目标装不进这个字段，得先给它们建持久化对象，或给 `schedule_triggers` 增 `target_ref TEXT`。P0 只保留 `job` / `pipeline_template` 两类整数 id 目标，其余列为后续扩展点。
- `org_id/user_id NOT NULL`：全局公共任务用 `0/0` 标记（见上方回填说明）。
- 命名：`ai_jobs` 表名、`/api/ai/jobs` 路径、`ai_job_runs` 属历史命名，000023 不改（老客户端零改动、避免破坏性重命名）；它们在语义上是通用对象。重命名为 `scheduled_jobs` / `/api/schedule/jobs` 单独排期，不改行为，不计入本次改造。
- `task.due` / `task.reminder` 是独立扫描器、不读 cron 表达式，本次不纳入统一定时对象。

## 8. 安全模型

定时任务没有人在场，改成「创建时一次性静态授权 + 执行时不可提权」：

| 规则 | 说明 |
|---|---|
| 创建即校验 | `POST/PUT /api/ai/jobs` 时，Definition 内所有写类工具与非 GET `api` 步骤必须在 `allowWrite` 中，否则 400；判定函数与执行期共用（见 §3）；同时校验触发规则的目标归属（复用 `validateTriggerTarget`） |
| `ExecMode=Scheduled` | 在执行入口注入执行上下文；命中 `allowWrite` → 直接执行并写审计日志（job_id / tool / 参数摘要），未命中 → 该步失败 |
| 不发审批请求 | 定时入口直接调 `runPipeline`，不进入工具层的 `approval_request` 分支；无无人确认挂死问题 |
| 非 GET 授权 | POST/PUT/PATCH/DELETE 的 `api/<path>` 必须在 `allowWrite` 显式列出，GET 免授权 |
| 递归 / 自修改黑名单 | 拒绝 `api/ai/chat`（会再起 SSE 后台 run）、`api/ai/jobs` 的非 GET（运行时改 cron/prompt 会绕过静态授权）、`api/pipeline/*`（触发执行），与 `tool/run_pipeline`、`tool/create_ai_job`/`update_ai_job` 合并成同一张黑名单 |
| 外部 MCP | 对话里一律需审批；定时模式 P0 默认禁用，P1 再按 `allowWrite` 白名单放开 |
| 不变边界 | `api/<path>` 仅限站内、身份绑定归属用户、路径沙箱、结果限长、`withToolContext` 合成 `iris.Context` |

## 9. 调度语义

- **Claim 租约**：`UPDATE schedule_triggers SET last_run_at=?, lease_until=?, last_status='running' WHERE ... AND (lease_until IS NULL OR lease_until < now)`；完成后清租约、写 `finished_at`。解决长 pipeline 跨分钟重复触发；进程重启后的残留 `running` 由 `lease_until` 过期回收（现有后台 run 不跨进程恢复）。扫描器只剩一个，租约只需在此一处实现。
- **超时**：单 tool/api/llm 5min 不变；pipeline 每步 5min、总 15min（对齐 `cronTriggerTimeout` 与后台 run 总超时）；job 级 `context.WithTimeout` 可取消。
- **去重**：`user.notify` 不去重；`task.due`/`task.reminder` 链路完全不动。

## 10. 结果与通知

| target | 邮件正文 |
|---|---|
| `ai` | Markdown 正文（保持现状） |
| `tool` / `interface` | 单步结果代码块 + 状态 |
| `pipeline` | 每步状态表 + 失败步骤详情 + `site_url` 跳转到 `/api/pipeline/runs/{id}`（未配置 `site_url` 则无按钮） |

`ai_jobs/{id}/runs` 返回 `target_type` 与 `pipeline_run_id`，前端可从执行历史直接跳编排详情。失败同样发 `user.notify`（现状成功 / 失败都发）。

## 11. 接口 / 工具 / 前端

- `POST|PUT /api/ai/jobs`：老字段 `prompt` / `cronExpr` / `enabled` 继续接受；服务端自动包装 `target={type:"ai",prompt}`，并把 `cronExpr` / `enabled` 拆成 `ai_jobs` 一行 + `schedule_triggers` 一行；响应把触发规则的 cron / 启停映射回 `cronExpr` / `enabled`，同时返回 `target` 与 `prompt`，兼容旧客户端。`GET /api/ai/jobs/{id}/runs` 增 `target_type`/`pipeline_run_id`。
- 复用 `/api/pipeline/templates` 与 `/api/schedule/triggers`，不另起一套模板或触发规则 CRUD。
- `create_ai_job` / `update_ai_job` / `list_ai_jobs` 增 `target` 字段，审批策略不变。
- `AiJobsView`：拆成「内容」与「时间」两块——内容用类型选择器（AI 提示词 / 工具 / 接口 / 编排），编排可选模板或 JSON 编辑器；时间复用 `PipelineTriggerCard`（必要时抽成通用组件）配触发规则。移动端 `/jobs` 沿用响应式 PC 布局，只加只读展示。
- P0-a 已把 `LLMCaller` / `buildAIJobPrompt` / RAG 注入迁到装配层 `execAIJob`；P0-b 删除 `ai_jobs_runner`（`internal/schedule/aijobs.go`）后，同一段执行逻辑搬进 `triggerJob`，`internal/schedule` 不再含 `ai_jobs` 标识符。

## 12. 调度收敛（已定方案）

现状：`schedule_triggers` 是调度领域自己的持久化对象（`target_type`/`target_id`/`cron_expr`/`enabled`），`cron_triggers_runner` 每分钟扫描并按 `target_type` 分发给装配层注册的 `TriggerHandler`；`pipeline_templates` 只存 Definition；`ai_jobs` 仍是唯一自带 cron 的领域对象，CHANGELOG 写明「未迁入通用表（避免把已有数据的迁移混进来）」，属有意推迟而非设计上要长期保留两套。

**决策**：

- **D1（已定：迁入）**：`ai_jobs` 以中性 `target_type="job"` 迁入通用触发表，不再自带 cron / 启停 / 状态列；`job` 与 `pipeline_template` 成为两个平级的对象类型。命名不贴 AI 标签，因为四种 `target` 里三种不是 AI，且注册表是给任意领域对象用的。
- **D2（已定：只修一个闹钟）**：Claim 租约只在 `cron_triggers_runner` 一条链路实现，`ai_jobs_runner` 删除。

理由：只需修一处重复触发；`/jobs` 与 `/pipelines` 的定时配置变成同一张表、同一套组件；以后给新领域对象加定时只注册一个 handler，不用再写第三个扫描器。代价是 `/jobs` 前端与老 API 兼容改造比原 P0 大，故拆成两步落地（§13）。

cron 的对象不限于 AI：`pipeline_template` 已是非 AI 目标，`job` 只是另一种；四种 `target` 是「执行内容」而非「对象类型」（§4）。`schedule_triggers` 的扩展点是开放的，硬约束与命名遗留见 §7。

终态是一个扫描器、一张规则表、两种 `target_type`、一个执行核心 `runPipeline`；cron 判定与执行核心保持分离。

## 13. 分期

两步各自可独立上线验证，因此各带一个迁移（000022 / 000023）。

| 阶段 | 范围 |
|---|---|
| P0-a（执行对象归一化，迁移 000022；已实现） | 加 target 列 + `prompt` 回填、`ResolveTarget`、`ValidateJobTarget`、共享 `allowWrite` 判定、接入 `runPipeline`；此时 cron 仍在 `ai_jobs`、`ai_jobs_runner` 照常扫 |
| P0-b（调度收敛，迁移 000023；已实现） | 重建 `ai_jobs` 删 4 个调度列 + 回填 `schedule_triggers`（`target_type='job'`）+ `lease_until` + `triggerJob` handler + 老 API 拆两行（前端零改动，`/jobs` 复用触发卡片留到 P1）+ 删除 `ai_jobs_runner` |
| P1（已实现） | 模板引用、执行历史联动 `pipeline_runs`（迁移 000024）、`AiJobsView` 类型选择器、邮件分类型渲染、外部 MCP 按 `allowWrite` 白名单放开 |
| P2 | job 级失败重试、模板版本化、外部 MCP 定时放开后的审计增强 |
| 后续议题 | 更多领域对象（`task` / `file`）接入 `schedule_triggers`、字符串目标（`target_ref`）、表名 / API 中性化重命名 |

外部 HTTP 接口已移出分期：当前编排明确禁止外访，将来真要外访需另立安全边界。

**A股例子**：id 9「A股收盘总结」升级后可做成 `interface GET tasks/open → llm 汇总 → tool/notify_user`，或 `tool/list_tasks → llm → tool/notify_user`，`allowWrite=["notify_user"]`。但站内 `/api/*` 没有行情源，15:10 拿实时收盘价仍做不到；那需要 P1 放开外部 MCP 网页搜索，不是 `interface` 能解决的。

## 14. 待确认细节与文档同步计划

待确认（不影响架构）：`allowNonZero` 是否进入后续阶段、`ExecMode` 的注入形式。D1 / D2 已定，见 §12；全局任务规则归属已定为 `org_id=0 / user_id=0`（§7）。

已按 `README.md` §5.2 同步（`docs v2.3`）：

- `02-functional-guide.md` §9 / §10 / §13：新增 target 类型、创建时预授权、执行历史字段、触发规则与任务定义分离、通用触发对象；
- `03-design.md` §5.8 / §5.9 / §6 / §7.2：派发层、表结构、安全模型、单一扫描器、通用触发对象，并修正权衡表中「仅顺序执行、无模板存库 / HTTP 直调 / cron / 条件 / 重试」那一行；
- `README.md` §3.2 迁移基线 000020 → 000024（跨过工作树的 000021）、§2/§3/§4 目录清单新增 06；§6 的 D18 已在 `docs v2.2` 关闭，无需改动；
- 根 `CHANGELOG.md [Unreleased]`（按既有中文 Keep a Changelog 格式）；
- 本文件已随 `docs v2.3` 转为现行，`02` / `03` / `README` / 根 `CHANGELOG.md` 已同批更新。
