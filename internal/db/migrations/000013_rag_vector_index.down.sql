-- 000013_rag_vector_index.down.sql
-- vec0 虚拟表由代码按 dim 创建（见 EnsureVecIndex），此处一并清理其影子表
DROP TABLE IF EXISTS file_chunk_vec_rowids;
DROP TABLE IF EXISTS file_chunk_vec_chunk_vectors;
DROP TABLE IF EXISTS file_chunk_vec;
DROP TABLE IF EXISTS rag_meta;
DROP TABLE IF EXISTS file_chunks;
DROP TABLE IF EXISTS file_index_state;
