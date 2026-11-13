package db

import (
	"context"
	"testing"

	"github.com/yiiewang/workbench/internal/sqlitevec"
)

func serVec(t *testing.T, v []float32) []byte {
	t.Helper()
	b, err := sqlitevec.SerializeFloat32(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// vec0 冒烟：partition key 建表、替换式写索引、分区隔离 KNN、
// 删除/重命名、维度变更触发全量作废。全链路不依赖真实 embedding 模型
// （向量用固定常量模拟）。
func TestVec0RAG(t *testing.T) {
	ctx := context.Background()
	d, err := Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	if err := d.EnsureVecIndex(4); err != nil {
		t.Fatalf("ensure vec: %v", err)
	}

	// 用户 1/10 两个文件各 2 块；用户 2/20 一个文件（验证分区隔离）
	f1 := [][]float32{{1, 0, 0, 0}, {0, 1, 0, 0}}
	f2 := [][]float32{{0.9, 0.1, 0, 0}}
	if err := d.ReplaceFileIndex(ctx, 1, 10, "/a.md", "h1",
		[]FileChunk{{Content: "块A", StartLine: 1, ChunkIndex: 0}, {Content: "块B", StartLine: 40, ChunkIndex: 1}},
		[][]byte{serVec(t, f1[0]), serVec(t, f1[1])}); err != nil {
		t.Fatalf("index f1: %v", err)
	}
	if err := d.ReplaceFileIndex(ctx, 1, 10, "/b.md", "h2",
		[]FileChunk{{Content: "块C", StartLine: 1, ChunkIndex: 0}},
		[][]byte{serVec(t, f2[0])}); err != nil {
		t.Fatalf("index f2: %v", err)
	}
	if err := d.ReplaceFileIndex(ctx, 2, 20, "/x.md", "h3",
		[]FileChunk{{Content: "他人内容", StartLine: 1, ChunkIndex: 0}},
		[][]byte{serVec(t, []float32{1, 0, 0, 0})}); err != nil {
		t.Fatalf("index other user: %v", err)
	}

	// KNN：用户 1/10 查 [1,0,0,0] → 最近邻应是 /a.md 的块A（含起始行号），且查不到他人内容
	hits, err := d.SearchChunks(ctx, 1, 10, serVec(t, []float32{1, 0, 0, 0}), 2)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(hits) != 2 || hits[0].Path != "/a.md" || hits[0].Content != "块A" || hits[0].StartLine != 1 || hits[0].Distance > 1e-6 {
		t.Fatalf("unexpected hits: %+v", hits)
	}
	// 用户 2/20 的分区里只有 1 块，k=2 也只应返回 1 块
	hits2, err := d.SearchChunks(ctx, 2, 20, serVec(t, []float32{1, 0, 0, 0}), 2)
	if err != nil {
		t.Fatalf("search other: %v", err)
	}
	if len(hits2) != 1 || hits2[0].Content != "他人内容" {
		t.Fatalf("partition isolation broken: %+v", hits2)
	}

	// 替换式重建：同文件新 hash 新内容（含摘要块 -1），旧块应消失
	if err := d.ReplaceFileIndex(ctx, 1, 10, "/a.md", "h1v2",
		[]FileChunk{{Content: "摘要卡", StartLine: 0, ChunkIndex: -1}, {Content: "块A2", StartLine: 1, ChunkIndex: 0}},
		[][]byte{serVec(t, []float32{0.5, 0.5, 0, 0}), serVec(t, []float32{0.6, 0.4, 0, 0})}); err != nil {
		t.Fatalf("reindex: %v", err)
	}
	hits3, err := d.SearchChunks(ctx, 1, 10, serVec(t, []float32{0.5, 0.5, 0, 0}), 10)
	if err != nil {
		t.Fatalf("search after reindex: %v", err)
	}
	if len(hits3) != 3 { // a.md 摘要块 + a.md 新块 + b.md 块C
		t.Fatalf("want 3 chunks after reindex, got %d: %+v", len(hits3), hits3)
	}
	// 最近邻应为 a.md 的摘要块（与 query 向量相同）
	if hits3[0].Content != "摘要卡" {
		t.Fatalf("nearest should be digest chunk, got %+v", hits3[0])
	}
	for _, h := range hits3 {
		if h.Content == "块A" || h.Content == "块B" {
			t.Fatalf("stale chunk survived reindex: %+v", h)
		}
	}

	// 重命名：路径改写，内容保留
	if err := d.RenameFileIndex(ctx, 1, 10, "/a.md", "/renamed.md"); err != nil {
		t.Fatalf("rename: %v", err)
	}
	hits4, err := d.SearchChunks(ctx, 1, 10, serVec(t, []float32{0.5, 0.5, 0, 0}), 10)
	if err != nil {
		t.Fatalf("search after rename: %v", err)
	}
	found := false
	for _, h := range hits4 {
		if h.Path == "/renamed.md" {
			found = true
		}
	}
	if !found {
		t.Fatalf("renamed path not found: %+v", hits4)
	}

	// 删除：单文件
	if err := d.DeleteFileIndex(ctx, 1, 10, "/renamed.md"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	hits5, err := d.SearchChunks(ctx, 1, 10, serVec(t, []float32{0.5, 0.5, 0, 0}), 10)
	if err != nil {
		t.Fatalf("search after delete: %v", err)
	}
	if len(hits5) != 1 || hits5[0].Path != "/b.md" {
		t.Fatalf("want only /b.md left, got %+v", hits5)
	}

	// 维度变更：vec 表重建、索引状态清空（全量待重建）
	if err := d.EnsureVecIndex(8); err != nil {
		t.Fatalf("ensure vec dim=8: %v", err)
	}
	hits6, err := d.SearchChunks(ctx, 1, 10, serVec(t, []float32{1, 0, 0, 0, 0, 0, 0, 0}), 10)
	if err != nil {
		t.Fatalf("search after dim change: %v", err)
	}
	if len(hits6) != 0 {
		t.Fatalf("dim change should invalidate all vectors, got %+v", hits6)
	}
	if _, exists, err := d.GetFileIndexState(ctx, 1, 10, "/b.md"); err != nil || exists {
		t.Fatalf("index state should be cleared after dim change (exists=%v err=%v)", exists, err)
	}

	// 手工 DROP 表 + meta 残留（ragclean --reset 场景）：EnsureVecIndex 须
	// 重建表而非误判"维度一致跳过"——否则后续写入全部 no such table
	if err := d.EnsureVecIndex(4); err != nil {
		t.Fatalf("ensure before drop: %v", err)
	}
	if _, err := d.conn.Exec(`DROP TABLE file_chunk_vec`); err != nil {
		t.Fatalf("manual drop: %v", err)
	}
	if err := d.EnsureVecIndex(4); err != nil {
		t.Fatalf("ensure after drop (meta残留): %v", err)
	}
	if err := d.ReplaceFileIndex(ctx, 1, 10, "/rebuilt.md", "h9",
		[]FileChunk{{Content: "重建后可写", StartLine: 1, ChunkIndex: 0}},
		[][]byte{serVec(t, []float32{1, 0, 0, 0})}); err != nil {
		t.Fatalf("write after rebuild: %v", err)
	}

	// FTS 关键词路：子串精确命中（与向量无关），删除文件后 FTS 同步清理
	fts, err := d.SearchFTS(ctx, 1, 10, `"重建后可写"`, 5)
	if err != nil {
		t.Fatalf("fts search: %v", err)
	}
	if len(fts) != 1 || fts[0].Path != "/rebuilt.md" {
		t.Fatalf("fts should hit /rebuilt.md exactly, got %+v", fts)
	}
	if err := d.DeleteFileIndex(ctx, 1, 10, "/rebuilt.md"); err != nil {
		t.Fatalf("delete rebuilt: %v", err)
	}
	fts2, err := d.SearchFTS(ctx, 1, 10, `"重建后可写"`, 5)
	if err != nil {
		t.Fatalf("fts search after delete: %v", err)
	}
	if len(fts2) != 0 {
		t.Fatalf("fts should be cleaned after delete, got %+v", fts2)
	}
}
