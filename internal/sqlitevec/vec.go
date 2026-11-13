// Package sqlitevec 进程内注册 sqlite-vec 扩展（vec0 虚拟表，向量 KNN 检索）。
//
// sqlite-vec.c/h 以源码形式 vendor 在本包目录（上游 MIT，取自
// github.com/asg017/sqlite-vec-go-bindings），其依赖的 sqlite3.h 同目录
// ——复制自 mattn/go-sqlite3 的 sqlite3-binding.h，与实际链接进二进制的
// sqlite 版本严格一致；升级 mattn 版本时须同步刷新（make sync-vec-headers）。
//
// -DSQLITE_CORE：sqlite-vec 以静态扩展编进宿主 sqlite（非 loadable extension），
// 经 sqlite3_auto_extension 注册后所有新连接自动可用，无需每连接初始化。
package sqlitevec

/*
#cgo CFLAGS: -DSQLITE_CORE
#cgo linux LDFLAGS: -lm
#include <stdlib.h>
#include "sqlite-vec.h"
*/
import "C"

import (
	"bytes"
	"encoding/binary"
)

// Auto 进程级注册 sqlite-vec：此后进程内所有新建 SQLite 连接自动加载 vec0。
// 不可撤销（进程生命周期内有效），应在打开首个连接前调用一次。
func Auto() {
	C.sqlite3_auto_extension((*[0]byte)(C.sqlite3_vec_init))
}

// SerializeFloat32 把 float32 向量序列化为 sqlite-vec 接受的 BLOB（小端）。
func SerializeFloat32(vector []float32) ([]byte, error) {
	var buf bytes.Buffer
	if err := binary.Write(&buf, binary.LittleEndian, vector); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
