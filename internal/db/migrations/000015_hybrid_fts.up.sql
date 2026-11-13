-- 000015_hybrid_fts.up.sql
-- 混合检索（FTS5 关键词路）：file_chunk_fts 与向量路（file_chunk_vec）双路召回，
-- rag 层 RRF 融合——专有名词/精确匹配（如 "阿司匹林"、函数名、错误码）不再
-- 受向量距离阈值误杀，两路都命中的块显著加权。
-- tokenize=trigram：3 字符滑窗子串匹配，中英文通用（unicode61 对中文是整段
-- 单 token，不可用）；查询 <3 字符时 FTS 无 gram，自然降级为纯向量。
CREATE VIRTUAL TABLE IF NOT EXISTS file_chunk_fts USING fts5(
  content,
  chunk_id UNINDEXED,
  tokenize = 'trigram'
);

-- 存量回填（块原文进全文索引）
INSERT INTO file_chunk_fts(rowid, content, chunk_id)
  SELECT id, content, id FROM file_chunks;
