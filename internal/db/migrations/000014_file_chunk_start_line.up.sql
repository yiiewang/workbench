-- 000014_file_chunk_start_line.up.sql
-- file_chunks 加起始行号（1-based）：RAG 引用跳转定位用。
-- 存量行为 0（未知行号），重新索引后填充。
ALTER TABLE file_chunks ADD COLUMN start_line INTEGER NOT NULL DEFAULT 0;
