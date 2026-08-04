// Package workbench 内置 UI 资源（embed），随 binary 发布，运行时只读。
// static_dir 仅服务用户文件，UI 资源走 embed，两者解耦。
package workbench

import "embed"

// UIFS 内置 UI 资源文件系统，包含 index.html / todo.html / css / js
//
//go:embed static
var UIFS embed.FS
