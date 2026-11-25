package rag

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"github.com/yiiewang/workbench/internal/db"
)

func openTestDB(t *testing.T) *db.DB {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "rag.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d
}

// 孤儿判定只看磁盘文件是否真的不存在：存在的文件（含嵌套目录）一律保留。
func TestOrphanPaths(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "sub"), 0755); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"keep.md", filepath.Join("sub", "nested.md")} {
		if err := os.WriteFile(filepath.Join(root, p), []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	got := orphanPaths(root, []string{
		"/keep.md", "/sub/nested.md", "/deleted.md", "/sub/gone.md",
	})
	want := []string{"/deleted.md", "/sub/gone.md"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("orphanPaths = %v, want %v", got, want)
	}
}

// 清理只作用于当前 owner，且 error 状态行同样按文件存在性判定。
func TestPruneOrphans(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "keep.md"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	d := openTestDB(t)

	const org, user int64 = 1, 10
	for _, p := range []string{"/keep.md", "/gone-a.md", "/gone-b.md"} {
		if err := d.SetFileIndexError(ctx, org, user, p, "h", "embedding failed"); err != nil {
			t.Fatal(err)
		}
	}
	if err := d.SetFileIndexError(ctx, 2, 20, "/other-gone.md", "h", "err"); err != nil {
		t.Fatal(err)
	}

	s := &Service{db: d}
	n, err := s.pruneOrphans(ctx, org, user, root)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("pruned = %d, want 2", n)
	}

	got, err := d.ListIndexedPaths(ctx, org, user)
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(got)
	if !reflect.DeepEqual(got, []string{"/keep.md"}) {
		t.Fatalf("remaining paths = %v, want [/keep.md]", got)
	}

	other, err := d.ListIndexedPaths(ctx, 2, 20)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(other, []string{"/other-gone.md"}) {
		t.Fatalf("other owner paths = %v, want [/other-gone.md]", other)
	}
}

// 空目录（无待索引文件）下直接验证 ReindexTree 会执行清理并写入计数与进度快照。
func TestReindexTreePrunesOrphans(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	d := openTestDB(t)

	const org, user int64 = 1, 10
	for _, p := range []string{"/gone-a.md", "/gone-b.md"} {
		if err := d.SetFileIndexError(ctx, org, user, p, "h", "err"); err != nil {
			t.Fatal(err)
		}
	}

	s := &Service{db: d}
	stats, err := s.ReindexTree(ctx, org, user, root, false)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Pruned != 2 {
		t.Fatalf("stats.Pruned = %d, want 2", stats.Pruned)
	}

	remaining, err := d.ListIndexedPaths(ctx, org, user)
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 0 {
		t.Fatalf("orphan indexes remained: %v", remaining)
	}

	p := s.ProgressOf(org, user)
	if p == nil {
		t.Fatal("progress snapshot missing")
	}
	if p.Running {
		t.Fatal("progress should be finished")
	}
	if p.Pruned != 2 {
		t.Fatalf("progress.Pruned = %d, want 2", p.Pruned)
	}
}
