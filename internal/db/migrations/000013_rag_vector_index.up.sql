-- 000013_rag_vector_index.up.sql
-- RAG 文件向量索引（语义检索）：普通表部分。
-- 向量本身存 vec0 虚拟表（file_chunk_vec），因建表维度取决于 ai.embedding.dim
-- 配置（运行时值），由 db.EnsureVecIndex 按需创建/重建，不在迁移里写死。
--
-- file_index_state 文件级索引状态：content_hash 未变则跳过重切分/重 embed；
-- file_chunks 切分后的文本块（content 存原文，供检索命中后直接拼 prompt）；
-- rag_meta RAG 元数据（当前仅 vec0 维度，dim 变更 → 全量重建）。
CREATE TABLE IF NOT EXISTS file_index_state (
    org_id      INTEGER NOT NULL,
    user_id     INTEGER NOT NULL,
    path        TEXT    NOT NULL,               -- 用户根目录内相对路径（以 / 开头）
    content_hash TEXT   NOT NULL,               -- 整文件 SHA-256，索引幂等键
    chunk_count INTEGER NOT NULL DEFAULT 0,
    status      TEXT    NOT NULL DEFAULT 'ok',  -- ok | error
    error       TEXT,
    indexed_at  TEXT    NOT NULL,
    PRIMARY KEY (org_id, user_id, path)
);

CREATE TABLE IF NOT EXISTS file_chunks (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    org_id      INTEGER NOT NULL,
    user_id     INTEGER NOT NULL,
    path        TEXT    NOT NULL,
    chunk_index INTEGER NOT NULL,
    content     TEXT    NOT NULL,
    UNIQUE (org_id, user_id, path, chunk_index)
);

CREATE INDEX IF NOT EXISTS idx_file_chunks_owner ON file_chunks (org_id, user_id, path);

CREATE TABLE IF NOT EXISTS rag_meta (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);
