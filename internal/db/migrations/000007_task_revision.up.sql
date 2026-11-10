-- 000007_task_revision.up.sql
-- 任务同步改用服务端权威版本号（单调递增整数）：
--   - 每次任务写入（增/改/删/全量导入）revision + 1
--   - 增量接口携带 baseRevision 做乐观锁，不等则 409 并返回服务端任务
--   - 替代建立在 JS/Go 序列化巧合上的 version_json md5 概率体系（旧字段保留兼容）
ALTER TABLE users ADD COLUMN revision INTEGER NOT NULL DEFAULT 1;
