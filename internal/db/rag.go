// Package db 内 RAG 向量索引数据访问（file_index_state / file_chunks / vec0 虚拟表）
package db

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/yiiewang/workbench/internal/sqlitevec"
)

// ragMetaVecDim rag_meta 中记录 vec0 维度的键。dim 是建 vec0 表的固定参数，
// 配置变更后旧表无法复用：检测到不一致时 DROP 重建并作废全部文件索引状态，
// 等待下次索引（文件内容仍在 file_chunks，重建仅需重新 embed）。
const ragMetaVecDim = "vec_dim"

// ownerKey vec0 partition key：同一用户的文件隔离在独立分区，
// KNN 查询只在分区内扫（既正确——不泄漏他人内容，又高效——扫描量随分区裁剪）。
func ownerKey(orgID, userID int64) string {
	return fmt.Sprintf("%d:%d", orgID, userID)
}

// EnsureVecIndex 确保 file_chunk_vec 虚拟表存在且维度与配置一致。
// 首次调用建表；dim 变更时 DROP 重建并清空 file_index_state（触发全量重建）。
// 幂等，进程启动时调用一次即可。
//
// 一致性判定不信任单一信号（meta 记录或表存在任一缺失即重建）：
// ragclean --reset 等手工 DROP 表后 meta 残留会跳过建表，后续写入
// 全部 no such table。表缺失重建时顺带作废 file_index_state——
// chunks 的 hash 记录若保留，reindex 会"内容未变"跳过，向量永远缺失。
func (d *DB) EnsureVecIndex(dim int) error {
	if dim <= 0 {
		return fmt.Errorf("invalid vec dim %d", dim)
	}
	dimStr := fmt.Sprintf("%d", dim)

	tableExists, err := hasTable(d.conn, "file_chunk_vec")
	if err != nil {
		return err
	}
	var cur string
	metaErr := d.conn.QueryRow(`SELECT value FROM rag_meta WHERE key = ?`, ragMetaVecDim).Scan(&cur)
	if metaErr != nil && metaErr != sql.ErrNoRows {
		return metaErr
	}
	if metaErr == nil && cur == dimStr && tableExists {
		return nil // 记录一致且表实际存在：无需动作
	}

	// 维度变更：旧向量不可比，全部作废
	if tableExists {
		if _, err := d.conn.Exec(`DROP TABLE IF EXISTS file_chunk_vec`); err != nil {
			return fmt.Errorf("drop stale vec table: %w", err)
		}
	}
	// 表缺失（首次建库或手工清除）也要作废状态：防止 hash 命中跳过重建
	if _, err := d.conn.Exec(`DELETE FROM file_index_state`); err != nil {
		return fmt.Errorf("clear index state: %w", err)
	}

	if _, err := d.conn.Exec(fmt.Sprintf(
		`CREATE VIRTUAL TABLE IF NOT EXISTS file_chunk_vec USING vec0(
			chunk_id INTEGER PRIMARY KEY,
			owner TEXT PARTITION KEY,
			embedding float[%d]
		)`, dim)); err != nil {
		return fmt.Errorf("create vec0 table: %w", err)
	}
	if _, err := d.conn.Exec(`INSERT INTO rag_meta(key, value) VALUES(?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, ragMetaVecDim, dimStr); err != nil {
		return err
	}
	return nil
}

// EnsureFTS 确保 file_chunk_fts 存在（ragclean --reset DROP 后启动重建；
// FTS5 段式存储的 DELETE 是标记式惰性删除，空间回收只能整表重建——
// 与 vec0 同款）。表存在时零动作；重建时回填存量 chunks。
func (d *DB) EnsureFTS() error {
	ok, err := hasTable(d.conn, "file_chunk_fts")
	if err != nil {
		return err
	}
	if ok {
		return nil
	}
	if _, err := d.conn.Exec(`CREATE VIRTUAL TABLE IF NOT EXISTS file_chunk_fts USING fts5(
		content,
		chunk_id UNINDEXED,
		tokenize = 'trigram'
	)`); err != nil {
		return fmt.Errorf("create fts table: %w", err)
	}
	if _, err := d.conn.Exec(`INSERT INTO file_chunk_fts(rowid, content, chunk_id)
		SELECT id, content, id FROM file_chunks`); err != nil {
		return fmt.Errorf("backfill fts: %w", err)
	}
	return nil
}

// deleteFTSByChunkIDs 删全文索引行（FTS5 支持任意 WHERE 的 DELETE）
func deleteFTSByChunkIDs(ctx context.Context, tx *sql.Tx, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	for _, id := range ids {
		if _, err := tx.ExecContext(ctx, `DELETE FROM file_chunk_fts WHERE chunk_id = ?`, id); err != nil {
			return err
		}
	}
	return nil
}

// deleteVecByChunkIDs 删 vec0 向量行。vec0 的 DELETE 只认主键点删，
// 不支持 IN (子查询)，故先从 file_chunks 查出 id 再批量删（每批 500）。
func deleteVecByChunkIDs(ctx context.Context, tx *sql.Tx, ids []int64) error {
	for len(ids) > 0 {
		n := len(ids)
		if n > 500 {
			n = 500
		}
		batch := ids[:n]
		ph := strings.TrimRight(strings.Repeat("?,", len(batch)), ",")
		args := make([]any, len(batch))
		for i, id := range batch {
			args[i] = id
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM file_chunk_vec WHERE chunk_id IN (`+ph+`)`, args...); err != nil {
			return err
		}
		ids = ids[n:]
	}
	return nil
}

// chunkIDsByPath 事务内查某文件（或 LIKE 前缀）的全部 chunk id
func chunkIDsByPath(ctx context.Context, tx *sql.Tx, orgID, userID int64, where string, args ...any) ([]int64, error) {
	rows, err := tx.QueryContext(ctx,
		`SELECT id FROM file_chunks WHERE org_id = ? AND user_id = ? AND `+where,
		append([]any{orgID, userID}, args...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// FileIndexState 文件级索引状态
type FileIndexState struct {
	ContentHash string
	ChunkCount  int
	Status      string
	Error       string
	IndexedAt   string
}

// FileIndexItem 索引状态明细（索引状态页展示）
type FileIndexItem struct {
	Path       string `json:"path"`
	Status     string `json:"status"`
	ChunkCount int    `json:"chunkCount"`
	Error      string `json:"error,omitempty"`
	IndexedAt  string `json:"indexedAt"`
}

// FileIndexSummary 索引状态汇总
type FileIndexSummary struct {
	Files  int `json:"files"`  // 已索引文件数
	Chunks int `json:"chunks"` // 文本块总数（含摘要卡）
	Errors int `json:"errors"` // 失败文件数
}

// ListFileIndexState 索引状态明细（按路径排序，limit 截断）
func (d *DB) ListFileIndexState(ctx context.Context, orgID, userID int64, limit int) ([]FileIndexItem, error) {
	rows, err := d.conn.QueryContext(ctx,
		`SELECT path, status, chunk_count, error, indexed_at
		 FROM file_index_state WHERE org_id = ? AND user_id = ? ORDER BY path LIMIT ?`,
		orgID, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []FileIndexItem
	for rows.Next() {
		var it FileIndexItem
		if err := rows.Scan(&it.Path, &it.Status, &it.ChunkCount, &it.Error, &it.IndexedAt); err != nil {
			return nil, err
		}
		items = append(items, it)
	}
	return items, rows.Err()
}

// FileIndexSummaryOf 索引状态汇总（文件数/块数/失败数）
func (d *DB) FileIndexSummaryOf(ctx context.Context, orgID, userID int64) (FileIndexSummary, error) {
	var s FileIndexSummary
	err := d.conn.QueryRowContext(ctx,
		`SELECT COUNT(*), COALESCE(SUM(chunk_count),0),
		        COALESCE(SUM(CASE WHEN status != 'ok' THEN 1 ELSE 0 END),0)
		 FROM file_index_state WHERE org_id = ? AND user_id = ?`,
		orgID, userID).Scan(&s.Files, &s.Chunks, &s.Errors)
	return s, err
}

// GetFileIndexState 查文件索引状态；不存在时 exists=false
func (d *DB) GetFileIndexState(ctx context.Context, orgID, userID int64, path string) (FileIndexState, bool, error) {
	var st FileIndexState
	err := d.conn.QueryRowContext(ctx,
		`SELECT content_hash, chunk_count, status, error, indexed_at
		 FROM file_index_state WHERE org_id = ? AND user_id = ? AND path = ?`,
		orgID, userID, path).
		Scan(&st.ContentHash, &st.ChunkCount, &st.Status, &st.Error, &st.IndexedAt)
	if err == sql.ErrNoRows {
		return st, false, nil
	}
	if err != nil {
		return st, false, err
	}
	return st, true, nil
}

// FileChunk 待写入的文本块（含原文起始行号，1-based，0 = 未知）。
// ChunkIndex 为 -1 时表示文件级摘要块（LLM 生成的语义卡，与原文块同表共存，
// 检索时同等参与 KNN）；普通原文块从 0 递增。
type FileChunk struct {
	Content    string
	StartLine  int
	ChunkIndex int
}

// ReplaceFileIndex 整文件替换式写入索引（事务）：删旧 chunks+向量，插新的，
// upsert 状态行。chunks[i] 与 vecs[i] 一一对应；vec 为 nil 的块跳过向量写入
// （embedding 失败的降级策略：文本块保留、不参与向量检索）。
func (d *DB) ReplaceFileIndex(ctx context.Context, orgID, userID int64, path, contentHash string, chunks []FileChunk, vecs [][]byte) error {
	if len(chunks) != len(vecs) {
		return fmt.Errorf("chunks(%d) and vecs(%d) length mismatch", len(chunks), len(vecs))
	}
	tx, err := d.conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	// 删旧：先查 chunk id 再点删向量/全文索引（vec0 不支持子查询删除），
	// 再删文本行——FTS 删除必须在 file_chunks 删除前（依赖子查询）
	ids, err := chunkIDsByPath(ctx, tx, orgID, userID, `path = ?`, path)
	if err != nil {
		return err
	}
	if err := deleteVecByChunkIDs(ctx, tx, ids); err != nil {
		return err
	}
	if err := deleteFTSByChunkIDs(ctx, tx, ids); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM file_chunks WHERE org_id = ? AND user_id = ? AND path = ?`,
		orgID, userID, path); err != nil {
		return err
	}

	owner := ownerKey(orgID, userID)
	for i := range chunks {
		res, err := tx.ExecContext(ctx,
			`INSERT INTO file_chunks(org_id, user_id, path, chunk_index, content, start_line) VALUES(?, ?, ?, ?, ?, ?)`,
			orgID, userID, path, chunks[i].ChunkIndex, chunks[i].Content, chunks[i].StartLine)
		if err != nil {
			return err
		}
		id, err := res.LastInsertId()
		if err != nil {
			return err
		}
		// 全文索引（混合检索关键词路）同步写入
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO file_chunk_fts(rowid, content, chunk_id) VALUES(?, ?, ?)`,
			id, chunks[i].Content, id); err != nil {
			return err
		}
		if vecs[i] != nil {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO file_chunk_vec(chunk_id, owner, embedding) VALUES(?, ?, ?)`,
				id, owner, vecs[i]); err != nil {
				return err
			}
		}
	}

	_, err = tx.ExecContext(ctx,
		`INSERT INTO file_index_state(org_id, user_id, path, content_hash, chunk_count, status, error, indexed_at)
		 VALUES(?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(org_id, user_id, path) DO UPDATE SET
			content_hash = excluded.content_hash,
			chunk_count = excluded.chunk_count,
			status = excluded.status,
			error = excluded.error,
			indexed_at = excluded.indexed_at`,
		orgID, userID, path, contentHash, len(chunks), "ok", "", time.Now().Format(time.RFC3339))
	if err != nil {
		return err
	}
	return tx.Commit()
}

// SetFileIndexError 记录单文件索引失败（状态行 upsert，保留旧 hash 供重试判断）
func (d *DB) SetFileIndexError(ctx context.Context, orgID, userID int64, path, contentHash, errMsg string) error {
	_, err := d.conn.ExecContext(ctx,
		`INSERT INTO file_index_state(org_id, user_id, path, content_hash, chunk_count, status, error, indexed_at)
		 VALUES(?, ?, ?, ?, 0, 'error', ?, ?)
		 ON CONFLICT(org_id, user_id, path) DO UPDATE SET status = 'error', error = excluded.error, indexed_at = excluded.indexed_at`,
		orgID, userID, path, contentHash, errMsg, time.Now().Format(time.RFC3339))
	return err
}

// DeleteFileIndex 删除单文件的索引（文件被删除时）
func (d *DB) DeleteFileIndex(ctx context.Context, orgID, userID int64, path string) error {
	tx, err := d.conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	ids, err := chunkIDsByPath(ctx, tx, orgID, userID, `path = ?`, path)
	if err != nil {
		return err
	}
	if err := deleteVecByChunkIDs(ctx, tx, ids); err != nil {
		return err
	}
	if err := deleteFTSByChunkIDs(ctx, tx, ids); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM file_chunks WHERE org_id = ? AND user_id = ? AND path = ?`,
		orgID, userID, path); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM file_index_state WHERE org_id = ? AND user_id = ? AND path = ?`,
		orgID, userID, path); err != nil {
		return err
	}
	return tx.Commit()
}

// DeleteFileIndexPrefix 删除路径前缀下全部索引（目录被删除时，pathPrefix 含尾部 /）
func (d *DB) DeleteFileIndexPrefix(ctx context.Context, orgID, userID int64, pathPrefix string) error {
	tx, err := d.conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	like := likeEscape(pathPrefix) + "%"
	ids, err := chunkIDsByPath(ctx, tx, orgID, userID, `path LIKE ? ESCAPE '\'`, like)
	if err != nil {
		return err
	}
	if err := deleteVecByChunkIDs(ctx, tx, ids); err != nil {
		return err
	}
	if err := deleteFTSByChunkIDs(ctx, tx, ids); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM file_chunks WHERE org_id = ? AND user_id = ? AND path LIKE ? ESCAPE '\'`,
		orgID, userID, like); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM file_index_state WHERE org_id = ? AND user_id = ? AND path LIKE ? ESCAPE '\'`,
		orgID, userID, like); err != nil {
		return err
	}
	return tx.Commit()
}

// RenameFileIndex 路径变更（重命名/移动）：chunks 保留（内容与向量不变），
// 只改 path 列与状态行。
func (d *DB) RenameFileIndex(ctx context.Context, orgID, userID int64, oldPath, newPath string) error {
	tx, err := d.conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx,
		`UPDATE file_chunks SET path = ? WHERE org_id = ? AND user_id = ? AND path = ?`,
		newPath, orgID, userID, oldPath); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE file_index_state SET path = ? WHERE org_id = ? AND user_id = ? AND path = ?`,
		newPath, orgID, userID, oldPath); err != nil {
		return err
	}
	return tx.Commit()
}

// RenameFileIndexPrefix 目录移动：前缀路径整体改写（oldPrefix 含尾部 /）。
// 偏移量用 SQL length()（按字符计），避免 Go len()（按字节）在中文路径下错位。
func (d *DB) RenameFileIndexPrefix(ctx context.Context, orgID, userID int64, oldPrefix, newPrefix string) error {
	tx, err := d.conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	like := likeEscape(oldPrefix) + "%"
	if _, err := tx.ExecContext(ctx,
		`UPDATE file_chunks SET path = ? || substr(path, length(?) + 1)
		 WHERE org_id = ? AND user_id = ? AND path LIKE ? ESCAPE '\'`,
		newPrefix, oldPrefix, orgID, userID, like); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE file_index_state SET path = ? || substr(path, length(?) + 1)
		 WHERE org_id = ? AND user_id = ? AND path LIKE ? ESCAPE '\'`,
		newPrefix, oldPrefix, orgID, userID, like); err != nil {
		return err
	}
	return tx.Commit()
}

// ChunkHit 检索命中项
type ChunkHit struct {
	ID        int64 // file_chunks.id（RRF 融合的对齐键）
	Path      string
	Content   string
	StartLine int
	Distance  float64 // 向量路：L2 距离；FTS 路：bm25 分（越小越相关）
}

// SearchChunks 向量 KNN 检索：query 向量在指定用户的分区内找 topK 最近邻，
// JOIN file_chunks 取回文本。只扫该分区（partition key 裁剪），无跨用户泄漏。
func (d *DB) SearchChunks(ctx context.Context, orgID, userID int64, queryVec []byte, topK int) ([]ChunkHit, error) {
	rows, err := d.conn.QueryContext(ctx,
		`SELECT c.id, c.path, c.content, c.start_line, v.distance
		 FROM file_chunk_vec v
		 JOIN file_chunks c ON c.id = v.chunk_id
		 WHERE v.owner = ? AND v.embedding MATCH ? AND k = ?
		 ORDER BY v.distance`,
		ownerKey(orgID, userID), queryVec, topK)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var hits []ChunkHit
	for rows.Next() {
		var h ChunkHit
		if err := rows.Scan(&h.ID, &h.Path, &h.Content, &h.StartLine, &h.Distance); err != nil {
			return nil, err
		}
		hits = append(hits, h)
	}
	return hits, rows.Err()
}

// SearchFTS 全文检索（混合检索的关键词路）：trigram 子串匹配 + bm25 排序。
// query 以 phrase 形式传入（调用方转义），限指定用户（JOIN 过滤）。
func (d *DB) SearchFTS(ctx context.Context, orgID, userID int64, phrase string, topK int) ([]ChunkHit, error) {
	rows, err := d.conn.QueryContext(ctx,
		`SELECT c.id, c.path, c.content, c.start_line, bm25(file_chunk_fts) AS rank
		 FROM file_chunk_fts f
		 JOIN file_chunks c ON c.id = f.chunk_id
		 WHERE file_chunk_fts MATCH ? AND c.org_id = ? AND c.user_id = ?
		 ORDER BY rank
		 LIMIT ?`,
		phrase, orgID, userID, topK)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var hits []ChunkHit
	for rows.Next() {
		var h ChunkHit
		if err := rows.Scan(&h.ID, &h.Path, &h.Content, &h.StartLine, &h.Distance); err != nil {
			return nil, err
		}
		hits = append(hits, h)
	}
	return hits, rows.Err()
}

// likeEscape 转义 LIKE 通配符（% _ \），配合 ESCAPE '\' 使用
func likeEscape(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '%', '_', '\\':
			out = append(out, '\\')
		}
		out = append(out, s[i])
	}
	return string(out)
}

// SerializeVector float32 切片 → vec0 BLOB（小端），转调 sqlitevec 包
func SerializeVector(v []float32) ([]byte, error) {
	return sqlitevec.SerializeFloat32(v)
}
