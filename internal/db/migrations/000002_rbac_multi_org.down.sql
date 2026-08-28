-- 000002_rbac_multi_org.down.sql
-- 回滚阶段 A：删除 user_orgs / org_features 表与 users.is_platform_admin 列。

DROP TABLE IF EXISTS org_features;
DROP TABLE IF EXISTS user_orgs;
ALTER TABLE users DROP COLUMN is_platform_admin;
