package rag

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/yiiewang/workbench/internal/config"
	"github.com/yiiewang/workbench/internal/db"
)

// indexTimeout 单文件索引的整体超时（含 embedding 网络调用）
const indexTimeout = 5 * time.Minute

// Service RAG 索引与检索服务。由 server 在 ai.embedding 配置启用时创建；
// nil 服务（未启用）时所有钩子静默跳过（调用方判 nil，方法内部不再防御）。
type Service struct {
	db      *db.DB
	emb     *Embedder
	maxDist float64         // 相关度阈值：KNN 命中距离超过它视为无关丢弃
	judge   *RelevanceJudge // LLM 相关性预判（nil = 关闭）
	digest  *Digester       // 文件级摘要生成（nil = 关闭）
	// 全量重建进度快照（owner → 进度）；读写经 mu 互斥
	mu       sync.Mutex
	progress map[string]*ReindexProgress
}

// ownerKeyOf vec0 分区键同款格式（db 包内为私有，rag 侧独立实现）
func ownerKeyOf(orgID, userID int64) string {
	return fmt.Sprintf("%d:%d", orgID, userID)
}

// NewService 构造服务；vec0 表的建立（EnsureVecIndex）由调用方在启动时完成
func NewService(database *db.DB, cfg config.EmbeddingConfig) *Service {
	maxDist := cfg.MaxDistance
	if maxDist <= 0 {
		maxDist = 0.9
	}
	return &Service{db: database, emb: NewEmbedder(cfg), maxDist: maxDist, judge: newRelevanceJudge(cfg), digest: newDigester(cfg)}
}

// normRel 归一化相对路径形态：以 / 开头、去尾部 /（目录前缀场景由调用方拼）
func normRel(p string) string {
	p = strings.ReplaceAll(p, "\\", "/")
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return strings.TrimSuffix(p, "/")
}

// ============================================================
// 索引钩子（server 写路径调用）
// ============================================================

// HandleWrite 文件写入完成（上传 / AI 写文件 / 编辑保存）：异步重建该文件索引。
// 不阻塞写请求主流程；失败记日志与状态行，不影响文件操作本身。
func (s *Service) HandleWrite(orgID, userID int64, fsPath, relPath string) {
	go s.indexFile(orgID, userID, fsPath, normRel(relPath))
}

// HandleDelete 文件/目录被删除：同步清理索引（纯本地 SQL，微秒级，无需异步）
func (s *Service) HandleDelete(orgID, userID int64, relPath string, isDir bool) {
	rel := normRel(relPath)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var err error
	if isDir {
		err = s.db.DeleteFileIndexPrefix(ctx, orgID, userID, rel+"/")
	} else {
		err = s.db.DeleteFileIndex(ctx, orgID, userID, rel)
	}
	if err != nil {
		slog.Warn("rag: delete index failed", "err", err, "path", rel)
	}
}

// HandleRename 重命名/移动：改写索引中的路径，内容与向量不变
func (s *Service) HandleRename(orgID, userID int64, oldRel, newRel string, isDir bool) {
	oldPath, newPath := normRel(oldRel), normRel(newRel)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var err error
	if isDir {
		err = s.db.RenameFileIndexPrefix(ctx, orgID, userID, oldPath+"/", newPath+"/")
	} else {
		err = s.db.RenameFileIndex(ctx, orgID, userID, oldPath, newPath)
	}
	if err != nil {
		slog.Warn("rag: rename index failed", "err", err, "path", oldPath)
	}
}

// indexFile 单文件索引：读文件 → hash 判重 → 切分 → embed → 替换式写表。
func (s *Service) indexFile(orgID, userID int64, fsPath, rel string) {
	ctx, cancel := context.WithTimeout(context.Background(), indexTimeout)
	defer cancel()
	if err := s.IndexFile(ctx, orgID, userID, fsPath, rel); err != nil {
		slog.Warn("rag: index file failed", "err", err, "path", rel)
	}
}

// IndexFile 同步索引单文件（导出供全量重建复用）：
//   - 目录 / 非文本类 / 超限：若有旧索引（文件被替换）则清理后返回
//   - content_hash 未变且上次成功：跳过（幂等，重复钩子零成本）
//   - embedding 失败：记 error 状态行，下次钩子或全量重建时重试
func (s *Service) IndexFile(ctx context.Context, orgID, userID int64, fsPath, rel string) error {
	info, err := os.Stat(fsPath)
	if err != nil {
		return err
	}
	if info.IsDir() || !Indexable(filepath.Base(fsPath), info.Size()) {
		return s.db.DeleteFileIndex(ctx, orgID, userID, rel)
	}

	data, err := os.ReadFile(fsPath)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(data)
	hash := hex.EncodeToString(sum[:])

	if st, exists, err := s.db.GetFileIndexState(ctx, orgID, userID, rel); err == nil &&
		exists && st.Status == "ok" && st.ContentHash == hash {
		return nil // 内容未变，跳过
	}

	// 内容提取：HTML 索引正文而非源码（标签噪音污染向量与注入 token）。
	// 提取后文本与源码行号无法映射——行号置 0（引用跳转只打开文件不定位行）。
	text := string(data)
	isHTML := false
	switch strings.ToLower(filepath.Ext(fsPath)) {
	case ".html", ".htm":
		text = extractHTMLText(text)
		isHTML = true
	}

	chunks := SplitChunks(text)
	if isHTML {
		for i := range chunks {
			chunks[i].StartLine = 0
		}
	}
	return s.indexChunks(ctx, orgID, userID, rel, hash, s.withDigest(ctx, rel, text, chunks))
}

// withDigest 文件级摘要块（可选）：digest_model 配置时为文件生成语义卡
// （chunk_index=-1），置于原文块之前一起向量化——宏观问题命中摘要卡
// （token 极省、无噪音），细节问题继续走原文块。生成失败降级只返回原文块。
func (s *Service) withDigest(ctx context.Context, rel, text string, chunks []Chunk) []Chunk {
	// HTML 已做正文提取，摘要输入用提取后 text（无标签噪音）
	for i := range chunks {
		chunks[i].ChunkIndex = i
	}
	if s.digest == nil {
		return chunks
	}
	digest, err := s.digest.Generate(ctx, rel, text)
	if err != nil {
		slog.Warn("rag: digest failed, fallback to raw chunks", "err", err, "path", rel)
		return chunks
	}
	all := make([]Chunk, 0, len(chunks)+1)
	all = append(all, Chunk{Content: digest, StartLine: 0, ChunkIndex: -1})
	return append(all, chunks...)
}

// indexChunks 共用写库路径：embed → 替换式写索引
func (s *Service) indexChunks(ctx context.Context, orgID, userID int64, rel, hash string, chunks []Chunk) error {
	if len(chunks) == 0 {
		// 空文件：只清状态，不留块
		return s.db.DeleteFileIndex(ctx, orgID, userID, rel)
	}
	texts := make([]string, len(chunks))
	dbChunks := make([]db.FileChunk, len(chunks))
	for i, c := range chunks {
		texts[i] = c.Content
		dbChunks[i] = db.FileChunk{Content: c.Content, StartLine: c.StartLine, ChunkIndex: c.ChunkIndex}
	}
	vecs, err := s.emb.Embed(ctx, texts)
	if err != nil {
		if serr := s.db.SetFileIndexError(ctx, orgID, userID, rel, hash, err.Error()); serr != nil {
			slog.Warn("rag: record index error failed", "err", serr)
		}
		return err
	}
	blobs := make([][]byte, len(vecs))
	for i, v := range vecs {
		blobs[i], err = db.SerializeVector(v)
		if err != nil {
			return err
		}
	}
	return s.db.ReplaceFileIndex(ctx, orgID, userID, rel, hash, dbChunks, blobs)
}

// ============================================================
// 全量重建（手动触发）
// ============================================================

// ReindexStats 全量重建结果
type ReindexStats struct {
	Indexed int `json:"indexed"` // 实际（重新）向量化写入
	Skipped int `json:"skipped"` // hash 未变跳过
	Failed  int `json:"failed"`  // 失败（多为 embedding 上游异常）
}

// ReindexProgress 全量重建进度（GET /api/ai/reindex/status 查询；
// 保留最近一次结果，Running=false 时即已完成的历史记录）
type ReindexProgress struct {
	Owner       string `json:"owner"` // org:user
	Running     bool   `json:"running"`
	Total       int    `json:"total"`     // 预收集的可索引文件数
	Processed   int    `json:"processed"` // 已处理（= indexed + skipped + failed）
	Indexed     int    `json:"indexed"`
	Skipped     int    `json:"skipped"`
	Failed      int    `json:"failed"`
	CurrentPath string `json:"currentPath"` // 正在处理的文件
	StartedAt   string `json:"startedAt"`
	UpdatedAt   string `json:"updatedAt"`
}

// reindexEntry 全量重建的待处理文件（收集阶段产出）
type reindexEntry struct {
	fsPath string
	rel    string
	info   fs.FileInfo
}

// reindexIgnoreDirs 依赖/构建目录：整个子树跳过——机器产物无知识价值，
// 且数量庞大（一个 .venv/node_modules 就是上千个文件，纯浪费 embed 费用）。
// 隐藏目录（.venv/.git/.idea）由 "." 前缀规则统一覆盖。
var reindexIgnoreDirs = map[string]bool{
	"node_modules": true, "__pycache__": true, "vendor": true, "target": true,
}

// ReindexTree 全量索引（幂等：hash 未变的文件零 embed 成本）。
// 供 POST /api/ai/reindex 后台执行。两阶段：先收集全部可索引文件
// （得到准确的 Total 进度分母），再逐个索引并更新进度快照。
//
// followSymlink：跟随符号链接（与 server.allow_symlink 语义一致——
// false 时严格限定真实路径在根内，符号链接条目整体跳过）。
// filepath.WalkDir 不跟随符号链接，故自写递归遍历；跟随时以真实路径
// 去重防循环链接（A→B→A 会死循环），同一真实文件只在首个链接路径下索引。
func (s *Service) ReindexTree(ctx context.Context, orgID, userID int64, root string, followSymlink bool) (ReindexStats, error) {
	var stats ReindexStats
	visited := make(map[string]bool) // 已收集真实路径（符号链接去重/防环）
	var entries []reindexEntry

	var collect func(dir, relPrefix string) error
	collect = func(dir, relPrefix string) error {
		dirEntries, err := os.ReadDir(dir)
		if err != nil {
			slog.Warn("rag: reindex walk error", "path", dir, "err", err)
			return nil // 单个目录错误不中断全量
		}
		for _, e := range dirEntries {
			// 隐藏目录与依赖/构建目录整个子树跳过（见 reindexIgnoreDirs）
			isDir := e.IsDir()
			if strings.HasPrefix(e.Name(), ".") || (isDir && reindexIgnoreDirs[e.Name()]) {
				continue
			}
			fsPath := filepath.Join(dir, e.Name())
			rel := normRel(relPrefix + "/" + e.Name())

			// 符号链接条目：不跟随模式整体跳过（沙箱语义）；跟随则解析真实路径
			if e.Type()&os.ModeSymlink != 0 {
				if !followSymlink {
					continue
				}
				real, rerr := filepath.EvalSymlinks(fsPath)
				if rerr != nil || visited[real] {
					continue
				}
				visited[real] = true
				info, serr := os.Stat(real)
				if serr != nil {
					continue
				}
				if info.IsDir() {
					// 索引路径记链接侧 rel（用户视角路径），内容读真实路径
					if cerr := collect(real, rel); cerr != nil {
						return cerr
					}
				} else if Indexable(filepath.Base(real), info.Size()) {
					entries = append(entries, reindexEntry{fsPath: real, rel: rel, info: info})
				}
				continue
			}

			info, ierr := e.Info()
			if ierr != nil {
				continue
			}
			if info.IsDir() {
				if cerr := collect(fsPath, rel); cerr != nil {
					return cerr
				}
				continue
			}
			if Indexable(filepath.Base(fsPath), info.Size()) {
				entries = append(entries, reindexEntry{fsPath: fsPath, rel: rel, info: info})
			}
		}
		return nil
	}

	if err := collect(root, ""); err != nil {
		return stats, err
	}

	// 进度快照：处理阶段持续更新（同用户并发 reindex 已被 409 挡，
	// 快照按 owner 记录互不覆盖）
	now := time.Now().Format(time.RFC3339)
	p := &ReindexProgress{
		Owner: ownerKeyOf(orgID, userID), Running: true,
		Total: len(entries), StartedAt: now, UpdatedAt: now,
	}
	s.setProgress(p)
	defer func() {
		p.Running = false
		s.touchProgress(p)
	}()

	for i, ent := range entries {
		s.updateProgress(p.Owner, func(pp *ReindexProgress) { pp.CurrentPath = ent.rel })
		// 跳过判重在 IndexFile 内（读文件算 hash 后），此处只区分"内容变了/没变"
		before, _, _ := s.db.GetFileIndexState(ctx, orgID, userID, ent.rel)
		var ierr error
		if ierr = s.IndexFile(ctx, orgID, userID, ent.fsPath, ent.rel); ierr != nil {
			stats.Failed++
		} else if after, exists, _ := s.db.GetFileIndexState(ctx, orgID, userID, ent.rel); exists && after.ContentHash == before.ContentHash && after.Status == "ok" {
			stats.Skipped++
		} else {
			stats.Indexed++
		}
		p.Indexed, p.Skipped, p.Failed = stats.Indexed, stats.Skipped, stats.Failed
		p.Processed = i + 1
		s.touchProgress(p)
		// 周期进度日志（每 20 个文件）：tail 日志可观测
		if p.Processed%20 == 0 || p.Processed == p.Total {
			slog.Info("rag: reindex progress", "owner", p.Owner,
				"processed", p.Processed, "total", p.Total,
				"indexed", stats.Indexed, "skipped", stats.Skipped, "failed", stats.Failed)
		}
	}
	return stats, nil
}

// ============================================================
// 重建进度快照（互斥保护；GET /api/ai/reindex/status 读取）
// ============================================================

// setProgress 记录该 owner 的进度快照（覆盖旧值）
func (s *Service) setProgress(p *ReindexProgress) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.progress == nil {
		s.progress = make(map[string]*ReindexProgress)
	}
	s.progress[p.Owner] = p
}

// touchProgress 快照内容更新后刷新时间戳
func (s *Service) touchProgress(p *ReindexProgress) {
	s.mu.Lock()
	p.UpdatedAt = time.Now().Format(time.RFC3339)
	s.mu.Unlock()
}

// updateProgress 原子修改该 owner 的快照字段
func (s *Service) updateProgress(owner string, fn func(p *ReindexProgress)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p, ok := s.progress[owner]; ok {
		fn(p)
	}
}

// ProgressOf 返回指定用户的进度快照副本；无记录返回 nil
func (s *Service) ProgressOf(orgID, userID int64) *ReindexProgress {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.progress[ownerKeyOf(orgID, userID)]
	if !ok {
		return nil
	}
	cp := *p
	return &cp
}

// ============================================================
// 在线检索（AI 对话前置）
// ============================================================

// Hit 检索命中（含来源路径与起始行号，供 prompt 引用出处与前端跳转定位）
type Hit struct {
	Path      string  `json:"path"`
	Content   string  `json:"content"`
	StartLine int     `json:"startLine"`
	Distance  float64 `json:"distance"`
}

// searchTimeout 检索链路超时：embedding 网络调用 + KNN 查询的总预算。
// 检索是锦上添花，超时/失败一律降级为无检索结果，不拖慢对话首字节。
const searchTimeout = 8 * time.Second

// searchQueryMaxRunes 检索 query 长度上限：embedding 模型输入有限
// （Qwen3-Embedding 32K token），超长问题截断头部（语义集中在开头）
const searchQueryMaxRunes = 2000

// Search 混合检索：向量 KNN（语义路）+ FTS5 trigram（关键词路）双路召回，
// RRF 融合取 topK。关键词精确命中（专有名词/函数名/错误码）不受向量距离
// 阈值误杀——阈值只作用于向量路候选，FTS 命中按 bm25 直接入围。
// 短 query（<3 字符，trigram 无 gram）自然降级为纯向量。
func (s *Service) Search(ctx context.Context, orgID, userID int64, query string, topK int) ([]Hit, error) {
	if strings.TrimSpace(query) == "" {
		return nil, nil
	}
	if r := []rune(query); len(r) > searchQueryMaxRunes {
		query = string(r[:searchQueryMaxRunes])
	}
	ctx, cancel := context.WithTimeout(ctx, searchTimeout)
	defer cancel()

	// 语义路：embed → KNN（召回扩大 4 倍给 RRF 留融合空间）→ 距离阈值
	vecs, err := s.emb.Embed(ctx, []string{query})
	if err != nil {
		return nil, err
	}
	blob, err := db.SerializeVector(vecs[0])
	if err != nil {
		return nil, err
	}
	rows, err := s.db.SearchChunks(ctx, orgID, userID, blob, topK*4)
	if err != nil {
		return nil, err
	}
	// 相关度阈值过滤：KNN 没有"不相关"概念，库中无相关内容时也会返回
	// 距离最近的垃圾片段——超过阈值（归一化下 L2≈1 即正交无关）丢弃
	vecHits := make([]db.ChunkHit, 0, len(rows))
	for _, r := range rows {
		if r.Distance > s.maxDist {
			break // KNN 按距离升序返回，首个超阈值后全是垃圾
		}
		vecHits = append(vecHits, r)
	}

	// 关键词路：FTS5 trigram（query ≥3 字符才有 gram），bm25 排序
	var ftsHits []db.ChunkHit
	if len([]rune(query)) >= 3 {
		// phrase 转义：双引号包裹为短语匹配（子串语义），内部引号翻倍
		phrase := `"` + strings.ReplaceAll(query, `"`, `""`) + `"`
		ftsHits, err = s.db.SearchFTS(ctx, orgID, userID, phrase, topK*4)
		if err != nil {
			slog.Warn("rag: fts search failed, vector-only", "err", err)
			ftsHits = nil // FTS 失败降级纯向量
		}
	}

	// RRF 融合 → topK
	fused := rrfFuse(vecHits, ftsHits, topK)
	hits := make([]Hit, 0, len(fused))
	for _, r := range fused {
		hits = append(hits, Hit{Path: r.Path, Content: r.Content, StartLine: r.StartLine, Distance: r.Distance})
	}

	// LLM 相关性预判（可选）：阈值/关键词路都拦不住的语义无关命中
	// 交由 flash 级小模型批量判断；失败降级保留融合结果
	if s.judge != nil && len(hits) > 0 {
		hits = s.judge.Filter(ctx, query, hits)
	}
	return hits, nil
}

// rrfK RRF 融合常数（业界标准值）：score = Σ 1/(k + rank)，k 越大
// 单路排名差异的影响越平缓
const rrfK = 60

// rrfFuse Reciprocal Rank Fusion：按 chunk id 对齐两路候选，
// rank 从 1 计。只出现在一路的块也得该路分（保留精确命中的单路结果），
// 两路同时命中的块显著加权。
func rrfFuse(vec, fts []db.ChunkHit, topK int) []db.ChunkHit {
	type entry struct {
		hit   db.ChunkHit
		score float64
	}
	m := make(map[int64]*entry, len(vec)+len(fts))
	add := func(list []db.ChunkHit) {
		for i, h := range list {
			e, ok := m[h.ID]
			if !ok {
				e = &entry{hit: h}
				m[h.ID] = e
			}
			e.score += 1.0 / float64(rrfK+i+1)
		}
	}
	add(vec)
	add(fts)

	list := make([]*entry, 0, len(m))
	for _, e := range m {
		list = append(list, e)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].score > list[j].score })
	if len(list) > topK {
		list = list[:topK]
	}
	out := make([]db.ChunkHit, len(list))
	for i, e := range list {
		out[i] = e.hit
	}
	return out
}

// ContextPrompt 把命中片段拼成注入 AI 对话的上下文消息（编号引用制）。
// 作为独立 system 消息追加在消息序列末尾：与前端"引用文件"机制并存，
// 模型按相关性自行取舍。模型只需输出稳定的 [n] 编号，路径/行号由前端
// 引用卡片展示（rag_hits SSE 事件），不依赖模型转述。
func ContextPrompt(hits []Hit) string {
	var b strings.Builder
	b.WriteString("以下是根据用户最新提问从其个人文件中语义检索到的编号片段（[n] 路径 第N行起）。")
	b.WriteString("回答时优先依据这些片段；片段与问题无关时忽略，不要提及检索过程。")
	b.WriteString("回答确实用到了片段内容时，在相应结论后标注片段编号，如 [1]。\n")
	for i, h := range hits {
		fmt.Fprintf(&b, "\n[%d] %s 第%d行起\n%s\n", i+1, h.Path, h.StartLine, h.Content)
	}
	return b.String()
}
