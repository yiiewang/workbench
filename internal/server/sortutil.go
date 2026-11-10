package server

// 自然排序工具（章节号路径感知）：供目录列表/文件树排序使用，
// 与 HTTP 处理逻辑无关，独立成文件便于维护与测试。

// naturalLess 按章节号路径排序：1.1.2 < 1.2 < 2 < 10 < 10.1；非章节号文件名回退到自然排序。
func naturalLess(a, b string) bool {
	pa, pOK := leadingChapterPath(a)
	pb, pOKb := leadingChapterPath(b)
	if pOK && pOKb {
		if c := chapterPathCmp(pa, pb); c != 0 {
			return c < 0
		}
	}
	return naturalLessImpl(a, b)
}

// leadingChapterPath 抽取开头的章节号路径，如 "1.2.3 xxx.md" → [1,2,3]。
// 章节号格式：连续的数字+分隔符（.或-或_或空），数字后面必须跟分隔符或字符串结束。
// 数字段后紧跟 alnum 字母（如 "10xxx"）视为非法章节号。
func leadingChapterPath(s string) ([]int64, bool) {
	var path []int64
	i := 0
	for {
		// 跳过分隔符（空格、点、横线、下划线等非 alnum）
		for i < len(s) && !isDigit(s[i]) {
			if isAlnum(s[i]) {
				// 字母开头（如 "abc.md"）→ 不是章节号
				return nil, false
			}
			i++
		}
		if i >= len(s) {
			if len(path) == 0 {
				return nil, false
			}
			return path, true
		}
		// 没有跳过分隔符就遇到数字（字符串以数字开头或单段），仍算章节号，直接进入读数字。
		// 读数字
		n := int64(0)
		for i < len(s) && isDigit(s[i]) {
			n = n*10 + int64(s[i]-'0')
			i++
		}
		path = append(path, n)
		if i >= len(s) {
			break
		}
		if isAlnum(s[i]) {
			// 数字后接字母（如 "10xxx"）→ 不是章节号
			return nil, false
		}
		// 是分隔符，继续
		if len(path) == 0 {
			return nil, false
		}
		// 跳到下一个分隔符后看是否是数字
		j := i
		for j < len(s) && !isAlnum(s[j]) {
			j++
		}
		if j >= len(s) || !isDigit(s[j]) {
			break
		}
		i = j
	}
	if len(path) == 0 {
		return nil, false
	}
	return path, true
}

// chapterPathCmp 比较章节号路径：[1,2] vs [1,2,1] → 短前缀更小 → 返回 -1
func chapterPathCmp(a, b []int64) int {
	for k := 0; k < len(a) && k < len(b); k++ {
		if a[k] != b[k] {
			if a[k] < b[k] {
				return -1
			}
			return 1
		}
	}
	switch {
	case len(a) < len(b):
		return -1
	case len(a) > len(b):
		return 1
	default:
		return 0
	}
}

func naturalLessImpl(a, b string) bool {
	for i, j := 0, 0; ; {
		for i < len(a) && !isAlnum(a[i]) {
			i++
		}
		for j < len(b) && !isAlnum(b[j]) {
			j++
		}
		if i == len(a) && j == len(b) {
			return len(a) < len(b)
		}
		if i == len(a) {
			return true
		}
		if j == len(b) {
			return false
		}
		ni, nj := i, j
		for ni < len(a) && isAlnum(a[ni]) {
			ni++
		}
		for nj < len(b) && isAlnum(b[nj]) {
			nj++
		}
		// 整段是否为纯数字
		aDigit := true
		for k := i; k < ni; k++ {
			if a[k] < '0' || a[k] > '9' {
				aDigit = false
				break
			}
		}
		bDigit := true
		for k := j; k < nj; k++ {
			if b[k] < '0' || b[k] > '9' {
				bDigit = false
				break
			}
		}
		if aDigit && bDigit {
			ai, aj := i, j
			for ai < ni-1 && a[ai] == '0' {
				ai++
			}
			for aj < nj-1 && b[aj] == '0' {
				aj++
			}
			if ni-ai != nj-aj {
				return ni-ai < nj-aj
			}
			for k := 0; k < ni-ai; k++ {
				if a[ai+k] != b[aj+k] {
					return a[ai+k] < b[aj+k]
				}
			}
			if ni-i != nj-j {
				return ni-i < nj-j
			}
		} else {
			// 至少一段含字母：按字符逐个比较，遇数字按数字段比
			if c := mixedLess(a, i, ni, b, j, nj); c != 0 {
				return c < 0
			}
		}
		i, j = ni, nj
	}
}

// mixedLess 比较 a[i1:i2] 与 b[j1:j2]：逐段比较，段内是连续同类型字符（数字或非数字）
// 数字段按整数比，非数字段按小写字典比。
func mixedLess(a string, i1, i2 int, b string, j1, j2 int) int {
	for i1 < i2 && j1 < j2 {
		// 跳过分隔符
		for i1 < i2 && !isAlnum(a[i1]) {
			i1++
		}
		for j1 < j2 && !isAlnum(b[j1]) {
			j1++
		}
		if i1 == i2 || j1 == j2 {
			break
		}
		// 判断 a 段类型
		ai := i1
		aDigit := a[i1] >= '0' && a[i1] <= '9'
		if aDigit {
			for ai < i2 && a[ai] >= '0' && a[ai] <= '9' {
				ai++
			}
		} else {
			for ai < i2 && isAlnum(a[ai]) && !(a[ai] >= '0' && a[ai] <= '9') {
				ai++
			}
		}
		bj := j1
		bDigit := b[j1] >= '0' && b[j1] <= '9'
		if bDigit {
			for bj < j2 && b[bj] >= '0' && b[bj] <= '9' {
				bj++
			}
		} else {
			for bj < j2 && isAlnum(b[bj]) && !(b[bj] >= '0' && b[bj] <= '9') {
				bj++
			}
		}
		// 比较 a[i1:ai] 和 b[j1:bj]
		switch {
		case aDigit && bDigit:
			na, nb := int64(0), int64(0)
			for k := i1; k < ai; k++ {
				na = na*10 + int64(a[k]-'0')
			}
			for k := j1; k < bj; k++ {
				nb = nb*10 + int64(b[k]-'0')
			}
			if na != nb {
				if na < nb {
					return -1
				}
				return 1
			}
			if ai-i1 != bj-j1 {
				if ai-i1 < bj-j1 {
					return -1
				}
				return 1
			}
		case !aDigit && !bDigit:
			// 两段都是字母段：字符逐个小写比
			k := 0
			for ; i1+k < ai && j1+k < bj; k++ {
				ca, cb := a[i1+k], b[j1+k]
				if ca >= 'A' && ca <= 'Z' {
					ca += 32
				}
				if cb >= 'A' && cb <= 'Z' {
					cb += 32
				}
				if ca != cb {
					if ca < cb {
						return -1
					}
					return 1
				}
			}
			// 一方多出的字符决定胜负：数字字符 < 字母字符？这里都已是字母段
			if ai-i1 != bj-j1 {
				if ai-i1 < bj-j1 {
					return -1
				}
				return 1
			}
		default:
			// 一方数字一方字母：按 ASCII 比较首字符等价
			// 约定：数字段 < 字母段
			if aDigit {
				return -1
			}
			return 1
		}
		i1, j1 = ai, bj
	}
	if i1 == i2 && j1 == j2 {
		return 0
	}
	if i1 == i2 {
		return -1
	}
	return 1
}

func isAlnum(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

func isDigit(c byte) bool {
	return c >= '0' && c <= '9'
}
