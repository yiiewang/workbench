//go:build ignore

// RAG 索引清理工具（经 Makefile 入口执行，须携带 sqlite_fts5 build tag）：
//
//	make ragclean              # 按排除规则清垃圾
//	make ragclean ARGS=--reset # 全量重置 RAG 索引
//
// （直接 go run 须自带 -tags sqlite_fts5，否则 file_chunk_fts 报 no such module: fts5）
//
// 背景：系统 sqlite3 CLI 缺 vec0 模块，无法直接 DELETE file_chunk_vec
// （vec0 虚拟表）；本工具用项目同款驱动（mattn + sqlite-vec 静态注册）执行。
// 仅清理 RAG 三表（全部可经 reindex 重建），不触碰任何业务数据。
//
// --reset 直接 DROP vec0 表：vec0 v0.1.6 的 DELETE 是标记失效位的惰性删除，
// 向量 blob 空间不回收（实测 3.3 万垃圾块删除后 196MB blob 残留）——
// 只有整表重建才能真正释放。重置后启动服务（EnsureVecIndex 自动建空表）
// 并 reindex 重新灌入。排除规则清理模式同样受此限制，建议优先 --reset。
package main

import (
	"database/sql"
	"fmt"
	"os"

	_ "github.com/mattn/go-sqlite3"
	vec "github.com/yiiewang/workbench/internal/sqlitevec"
)

// junkPath 与 internal/rag 排除规则保持同款（改一处需同步另一处）
const junkPath = `path LIKE '%/.%' OR path LIKE '%/node_modules/%' OR path LIKE '%/__pycache__/%' OR path LIKE '%/vendor/%' OR path LIKE '%/target/%'`

func main() {
	dbPath := "data/workbench.db"
	reset := false
	args := os.Args[1:]
	for _, a := range args {
		if a == "--reset" {
			reset = true
		} else {
			dbPath = a
		}
	}

	// 进程级注册 sqlite-vec（与 db.Open 同机制），vec0 表即可读写
	vec.Auto()

	db, err := sql.Open("sqlite3", dbPath+"?_busy_timeout=10000")
	if err != nil {
		fatal("open db: %v", err)
	}
	defer db.Close()

	if reset {
		// 全量重置：DROP vec0（释放惰性删除残留的向量 blob）+ 清数据与维度
		// 记录（meta 残留会让 EnsureVecIndex 误判"表已存在"跳过建表）。
		// 服务启动时 EnsureVecIndex 自动重建空表
		steps := []string{
			`DROP TABLE IF EXISTS file_chunk_vec`,
			// FTS5 段式存储惰性删除：DELETE 不回收空间，必须整表 DROP
			`DROP TABLE IF EXISTS file_chunk_fts`,
			`DELETE FROM file_chunks`,
			`DELETE FROM file_index_state`,
			`DELETE FROM rag_meta WHERE key = 'vec_dim'`,
		}
		for _, q := range steps {
			if _, err := db.Exec(q); err != nil {
				fatal("reset %q: %v", q, err)
			}
		}
	} else {
		// 按排除规则清垃圾（注意：vec0 惰性删除不回收 blob，大清理建议 --reset）
		tx, err := db.Begin()
		if err != nil {
			fatal("begin: %v", err)
		}
		n1, err := tx.Exec(`DELETE FROM file_chunk_vec WHERE chunk_id IN (SELECT id FROM file_chunks WHERE ` + junkPath + `)`)
		if err != nil {
			fatal("delete vec: %v", err)
		}
		n2, err := tx.Exec(`DELETE FROM file_chunks WHERE ` + junkPath)
		if err != nil {
			fatal("delete chunks: %v", err)
		}
		n3, err := tx.Exec(`DELETE FROM file_index_state WHERE ` + junkPath)
		if err != nil {
			fatal("delete state: %v", err)
		}
		if err := tx.Commit(); err != nil {
			fatal("commit: %v", err)
		}
		v1, _ := n1.RowsAffected()
		v2, _ := n2.RowsAffected()
		v3, _ := n3.RowsAffected()
		fmt.Printf("清理：向量 %d 行、文本块 %d 行、状态 %d 行\n", v1, v2, v3)
	}

	// VACUUM 必须在事务外；删除/DROP 不缩文件，靠它回收磁盘空间
	if _, err := db.Exec(`VACUUM`); err != nil {
		fatal("vacuum: %v", err)
	}
	fmt.Println("已 VACUUM 回收空间")
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "ragclean: "+format+"\n", args...)
	os.Exit(1)
}
