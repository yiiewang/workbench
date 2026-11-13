package rag

import (
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// 切分参数：目标块长与相邻块重叠（rune 数）。
// 800 字 ≈ Qwen3-Embedding 上下文的安全区间，块太大语义稀释、太小丢失上下文；
// 重叠 100 保证跨块的连续语义（如列表项、跨段论述）至少在一侧完整出现。
const (
	chunkTargetRunes  = 800
	chunkOverlapRunes = 100
	// chunkMinRunes 低于此长度的尾块并入前一块（不值得单独成块）
	chunkMinRunes = 50
)

// indexableMaxBytes 单文件索引上限：超限文件内容过大，切分后块数爆炸
// （embed 调用与向量存储线性增长），直接跳过。
const indexableMaxBytes = 2 << 20

// textExts 可索引的文本类扩展名。二进制/媒体文件无文本语义，白名单收敛。
var textExts = map[string]bool{
	".md": true, ".markdown": true, ".txt": true, ".log": true,
	".json": true, ".yaml": true, ".yml": true, ".toml": true, ".ini": true,
	".conf": true, ".cfg": true, ".csv": true, ".tsv": true, ".xml": true,
	".html": true, ".css": true, ".js": true, ".ts": true, ".go": true,
	".py": true, ".sh": true, ".sql": true, ".rs": true, ".java": true,
	".c": true, ".h": true, ".cpp": true, ".hpp": true,
}

// Indexable 判断文件是否可索引（文本类 + 未超大小上限 + 非隐藏文件）。
// 隐藏文件（.env/.gitignore 等）不索引：配置/缓存类内容，索引只会引入噪音。
func Indexable(name string, size int64) bool {
	if size > indexableMaxBytes {
		return false
	}
	if strings.HasPrefix(name, ".") {
		return false
	}
	return textExts[strings.ToLower(filepath.Ext(name))]
}

// Chunk 文本块（含原文起始行号，1-based——引用跳转定位用）。
// ChunkIndex 为 -1 表示文件级摘要块（withDigest 生成）；普通块由 SplitChunks
// 顺序编号（withDigest 统一赋值）。
type Chunk struct {
	Content    string
	StartLine  int
	ChunkIndex int
}

// para 段落（含起始行号）
type para struct {
	text string
	line int
}

// SplitChunks 文本切分：按空行分段落，段落贪心合并至 ~800 字；
// 超长段落按句子边界（。！？.!?）硬切；相邻块共享尾部 ~100 字重叠。
// StartLine 取块首段的行号（重叠块取新增正文的行号，跳转定位更有意义）。
// 设计取向：块边界优先落在自然段落边界，语义完整度优于几何均匀。
func SplitChunks(text string) []Chunk {
	paras := splitParagraphs(text)

	// 先把超长段落按句子边界展开为 ≤target 的段
	var units []para
	for _, p := range paras {
		if utf8.RuneCountInString(p.text) <= chunkTargetRunes {
			units = append(units, p)
			continue
		}
		units = append(units, splitLongParagraph(p)...)
	}

	// 贪心合并 + 尾部重叠
	var chunks []Chunk
	var cur para
	for _, u := range units {
		switch {
		case cur.text == "":
			cur = u
		case utf8.RuneCountInString(cur.text)+utf8.RuneCountInString(u.text)+1 <= chunkTargetRunes:
			cur.text += "\n\n" + u.text
		default:
			chunks = append(chunks, Chunk{Content: cur.text, StartLine: cur.line})
			// 新块行号锚定新正文（重叠尾巴只是上下文）
			cur = para{text: tailRunes(cur.text, chunkOverlapRunes) + "\n\n" + u.text, line: u.line}
		}
	}
	if cur.text != "" {
		// 过短的尾块并入前一块（不额外成块）
		if utf8.RuneCountInString(cur.text) < chunkMinRunes && len(chunks) > 0 {
			chunks[len(chunks)-1].Content += "\n\n" + cur.text
		} else {
			chunks = append(chunks, Chunk{Content: cur.text, StartLine: cur.line})
		}
	}
	return chunks
}

// splitParagraphs 按空行分段（记录各段在原文中的起始行号，1-based），
// 段内连续换行折叠为单个（软换行不构成语义边界）
func splitParagraphs(text string) []para {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	var out []para
	var cur []string
	start := 1
	flush := func() {
		if len(cur) > 0 {
			p := strings.TrimSpace(strings.Join(cur, "\n"))
			if p != "" {
				out = append(out, para{text: p, line: start})
			}
			cur = nil
		}
	}
	for i, ln := range lines {
		if strings.TrimSpace(ln) == "" {
			flush()
			continue
		}
		if len(cur) == 0 {
			start = i + 1
		}
		cur = append(cur, ln)
	}
	flush()
	return out
}

// splitLongParagraph 超长段落按句子边界切为 ≤target 的片段（带段内行号）。
// 行号增量计数（nl = 当前片段 [start, i) 内的换行数），O(n) 一次扫描——
// 不能按需重扫：无空行的 MB 级单行文件（如 minified js）会退化成 O(n²)。
func splitLongParagraph(p para) []para {
	runes := []rune(p.text)
	var parts []para
	start := 0
	startLine := p.line // 片段起始行号
	lastSentenceEnd := -1
	nl := 0 // [start, i) 内换行数
	for i := 0; i < len(runes); i++ {
		if runes[i] == '\n' {
			nl++
		}
		if isSentenceEnd(runes[i]) {
			lastSentenceEnd = i + 1
		}
		if i-start+1 >= chunkTargetRunes {
			cut := i + 1
			if lastSentenceEnd > start {
				cut = lastSentenceEnd
			}
			parts = append(parts, para{text: strings.TrimSpace(string(runes[start:cut])), line: startLine})
			// startLine 增量 = [start, cut) 的换行数。nl 此刻覆盖 [start, i]，
			// 句子边界回退（cut ≤ i）时须扣除 [cut, i] 部分（该区间 i 回退后会重扫重计）
			nAfterCut := 0
			for j := cut; j <= i; j++ {
				if runes[j] == '\n' {
					nAfterCut++
				}
			}
			startLine += nl - nAfterCut
			start = cut
			nl, lastSentenceEnd = 0, -1
			i = start - 1
		}
	}
	if start < len(runes) {
		rest := strings.TrimSpace(string(runes[start:]))
		if rest != "" {
			parts = append(parts, para{text: rest, line: startLine})
		}
	}
	return parts
}

// isSentenceEnd 中英文句末标点
func isSentenceEnd(r rune) bool {
	switch r {
	case '。', '！', '？', '.', '!', '?', ';', '；':
		return true
	}
	return false
}

// tailRunes 取尾部 n 个 rune（不足则全取）
func tailRunes(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[len(runes)-n:])
}
