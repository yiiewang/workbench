-- 000005_task_files.up.sql
-- todo ↔ file 联动：任务可关联文件路径（JSON 数组字符串，相对用户根目录）
ALTER TABLE tasks ADD COLUMN files TEXT NOT NULL DEFAULT '[]';
