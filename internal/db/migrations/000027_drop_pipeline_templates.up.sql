-- 000027_drop_pipeline_templates.up.sql
-- 编排模板概念移除：pipeline 只是定时任务（ai_jobs.target_json）里的内联
-- Definition，AI 可直接生成复杂流程，不再需要独立模板与版本表。
-- 保留 000018/019/020/026 历史迁移以维持存量库的迁移链，这里只做运行时删除。
DROP TABLE IF EXISTS pipeline_template_versions;
DROP TABLE IF EXISTS pipeline_templates;

-- 模板触发规则已无对应目标，避免通用调度器每分钟报「目标不存在」
DELETE FROM schedule_triggers WHERE target_type = 'pipeline_template';
