package server

import "testing"

func TestNaturalLess(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"1. 项目概述.md", "1.1. 工程结构.md", true},
		{"1.1. 工程结构.md", "1.2. 系统架构与模块化设计.md", true},
		{"1.2. 系统架构与模块化设计.md", "2. 节点入口与命令行.md", true},
		{"2. 节点入口与命令行.md", "10. 访问控制.md", true},
		{"10. 访问控制.md", "10.1. foo.md", true},
		{"file2.txt", "file10.txt", true},
		{"file10.txt", "file20.txt", true},
		{"a.md", "b.md", true},
		{"b.md", "a.md", false},
		{"", "a", true},
		{"a", "", false},
		{"1.1.md", "1.1.md", false},
		{"01.md", "1.md", false}, // 1 < 01 (无前导零更小)
		{"2.md", "10.md", true},
		{"11.md", "100.md", true},
		{"index.html", "todo.html", true},
		{"z.md", "Z.md", false}, // 大小写不敏感，Z < z 视为相等
		// 真实章节文件名
		{"1. 项目概述.md", "2. 节点入口与命令行.md", true},
		{"1.1. 工程结构.md", "1.2. 系统架构与模块化设计.md", true},
		{"15. 部署运维与可观测.md", "1. 项目概述.md", false},
	}
	for _, c := range cases {
		got := naturalLess(c.a, c.b)
		if got != c.want {
			t.Errorf("naturalLess(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}
