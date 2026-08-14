// Package workbench 内置 UI 资源（embed），随 binary 发布，运行时只读。
// static_dir 仅服务用户文件，UI 资源走 embed，两者解耦。
package workbench

import "embed"

// UIFS 内置 UI 资源文件系统，由 frontend/ 的 Vite 构建产物 frontend/dist 提供
// （index.html / todo.html / assets/*）。构建命令：make frontend 或 make all。
//
//go:embed frontend/dist
var UIFS embed.FS
