-- 000001_baseline.down.sql
-- 回滚 baseline：逆序删除阶段 A 前的全部表（回到空库）。

DROP TABLE IF EXISTS rate_limits;
DROP TABLE IF EXISTS shares;
DROP TABLE IF EXISTS app_secrets;
DROP TABLE IF EXISTS tasks;
DROP TABLE IF EXISTS users;
DROP TABLE IF EXISTS roles;
DROP TABLE IF EXISTS orgs;
DROP TABLE IF EXISTS visit_logs;
