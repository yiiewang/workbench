-- 000016_ai_runs.up.sql
-- AI 对话后台运行：一次 /api/ai/chat 一条记录。客户端断开后 Agent 循环
-- 继续跑完，状态、结构化事件与最终回复落库供 GET /api/ai/runs 查询。
CREATE TABLE ai_runs (
    id TEXT PRIMARY KEY,
    org_id INTEGER NOT NULL,
    user_id INTEGER NOT NULL,
    qa_id TEXT NOT NULL DEFAULT '',
    session_id TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL,               -- running / done / failed
    events TEXT NOT NULL DEFAULT '[]',  -- 结构化 SSE 事件 JSON 数组
    result TEXT NOT NULL DEFAULT '',    -- 累积的助手回复文本
    err_msg TEXT NOT NULL DEFAULT '',
    started_at TEXT DEFAULT (datetime('now')),
    updated_at TEXT DEFAULT (datetime('now'))
);
CREATE INDEX idx_ai_runs_owner ON ai_runs(org_id, user_id, started_at DESC);
