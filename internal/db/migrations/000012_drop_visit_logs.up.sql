-- 000012_drop_visit_logs.up.sql
-- 移除访问统计：visit_logs 表停用（每个请求一条 INSERT 也是无谓写放大；
-- 文本访问日志 logging 仍保留在 access log 文件，需要统计时可重新启用）。
DROP TABLE IF EXISTS visit_logs;
