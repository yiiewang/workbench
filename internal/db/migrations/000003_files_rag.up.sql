-- 000003_files_rag.up.sql
-- 文件索引与 RAG：索引状态 / chunk / FTS5（trigram）。
-- 向量表 file_chunk_vec 由 rag.EnsureVecIndex 在服务启动时创建（依赖 sqlite-vec 扩展），
-- 不在迁移里建，避免空库迁移阶段扩展未加载导致失败。
CREATE TABLE file_index_state (
    org_id INTEGER NOT NULL,
    user_id INTEGER NOT NULL,
    path TEXT NOT NULL,
    content_hash TEXT NOT NULL,
    chunk_count INTEGER NOT NULL DEFAULT 0,
    status TEXT NOT NULL DEFAULT 'ok',
    error TEXT,
    indexed_at TEXT NOT NULL,
    PRIMARY KEY (org_id, user_id, path)
);

CREATE TABLE file_chunks (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    org_id INTEGER NOT NULL,
    user_id INTEGER NOT NULL,
    path TEXT NOT NULL,
    chunk_index INTEGER NOT NULL,
    content TEXT NOT NULL,
    start_line INTEGER NOT NULL DEFAULT 0,
    UNIQUE (org_id, user_id, path, chunk_index)
);

CREATE INDEX idx_file_chunks_owner ON file_chunks (org_id, user_id, path);

CREATE TABLE rag_meta (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

CREATE VIRTUAL TABLE file_chunk_fts USING fts5(
    content,
    chunk_id UNINDEXED,
    tokenize = 'trigram'
);
