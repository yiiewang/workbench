// Package pipeline 声明式编排执行引擎。
//
// 一份 Definition（纯数据，可存库复用即"模板"）交给 Runner，按序执行步骤、
// 维护步骤输出与变量插值，失败即停。具体能力（AI 工具 / 站内 API / LLM）
// 由调用方以 Executor 注入，本包不依赖 HTTP / iris / db，可独立测试。

package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// Step 单个声明式步骤。Uses 形如 tool/<name>、api/<path> 或 llm；
// Needs 缺省时依赖数组中的前一步（顺序），显式 [] 表示无依赖可并行。
type Step struct {
	ID    string         `json:"id" yaml:"id"`
	Uses  string         `json:"uses" yaml:"uses"`
	Needs []string       `json:"needs" yaml:"needs,omitempty"`
	When  string         `json:"when,omitempty" yaml:"when,omitempty"` // 空/on_success | on_failure | always
	Retry int            `json:"retry,omitempty" yaml:"retry,omitempty"`
	With  map[string]any `json:"with,omitempty" yaml:"with,omitempty"`
}

// Definition 一次编排的定义
type Definition struct {
	Name       string   `json:"name,omitempty" yaml:"name"`
	AllowWrite []string `json:"allowWrite,omitempty" yaml:"allowWrite"`
	Steps      []Step   `json:"steps" yaml:"steps"`
}

// StepResult 单步执行结果
type StepResult struct {
	ID         string `json:"id"`
	Uses       string `json:"uses"`
	Status     string `json:"status"` // ok | failed | skipped
	Output     any    `json:"output,omitempty"`
	Error      string `json:"error,omitempty"`
	DurationMs int64  `json:"durationMs"`
}

var stepIDRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// Validate 校验定义：步骤非空、id 唯一合法、uses 形态正确、needs 引用存在且无环
func (d Definition) Validate() error {
	if len(d.Steps) == 0 {
		return fmt.Errorf("steps is required")
	}
	if len(d.Steps) > 50 {
		return fmt.Errorf("too many steps (max 50)")
	}
	seen := make(map[string]bool, len(d.Steps))
	for i, s := range d.Steps {
		if !stepIDRe.MatchString(s.ID) {
			return fmt.Errorf("steps[%d].id invalid (letters/digits/_/- only)", i)
		}
		if seen[s.ID] {
			return fmt.Errorf("duplicate step id %q", s.ID)
		}
		seen[s.ID] = true
		if _, _, ok := parseUses(s.Uses); !ok {
			return fmt.Errorf("steps[%d].uses invalid: %q", i, s.Uses)
		}
		switch s.When {
		case "", "on_success", "on_failure", "always":
		default:
			return fmt.Errorf("steps[%d].when invalid: %q", i, s.When)
		}
		if s.Retry < 0 || s.Retry > 5 {
			return fmt.Errorf("steps[%d].retry out of range (0-5)", i)
		}
	}
	_, err := d.layers()
	return err
}

// dependencies 返回步骤 i 的依赖：needs 缺省（nil）时依赖数组中的前一步，
// 保持「顺序执行」的默认语义；显式 needs: [] 表示无依赖，可并行。
// Needs 的 json tag 不能带 omitempty，否则空数组会在
// execRunPipeline 的 marshal→unmarshal 往返中被吞掉、退化成 nil。
func (d Definition) dependencies(i int) []string {
	if d.Steps[i].Needs != nil {
		return d.Steps[i].Needs
	}
	if i > 0 {
		return []string{d.Steps[i-1].ID}
	}
	return nil
}

// Layers 按依赖拓扑分层：同层步骤互不依赖、可并行；层内按 Definition 顺序稳定排列。
func (d Definition) Layers() ([][]int, error) {
	return d.layers()
}

func (d Definition) layers() ([][]int, error) {
	index := make(map[string]int, len(d.Steps))
	for i, s := range d.Steps {
		index[s.ID] = i
	}
	indeg := make([]int, len(d.Steps))
	children := make(map[int][]int, len(d.Steps))
	for i := range d.Steps {
		for _, dep := range d.dependencies(i) {
			j, ok := index[dep]
			if !ok {
				return nil, fmt.Errorf("step %q needs unknown step %q", d.Steps[i].ID, dep)
			}
			if j == i {
				return nil, fmt.Errorf("step %q cannot depend on itself", d.Steps[i].ID)
			}
			indeg[i]++
			children[j] = append(children[j], i)
		}
	}
	var layers [][]int
	processed := 0
	current := make([]int, 0, len(d.Steps))
	for i := range d.Steps {
		if indeg[i] == 0 {
			current = append(current, i)
		}
	}
	for len(current) > 0 {
		layers = append(layers, current)
		processed += len(current)
		var next []int
		for _, i := range current {
			for _, child := range children[i] {
				indeg[child]--
				if indeg[child] == 0 {
					next = append(next, child)
				}
			}
		}
		sort.Ints(next)
		current = next
	}
	if processed != len(d.Steps) {
		return nil, fmt.Errorf("pipeline definition contains a dependency cycle")
	}
	return layers, nil
}

// parseUses 拆出能力类别与名称；llm 这类无名称的类别 name 为空
func parseUses(uses string) (kind, name string, ok bool) {
	if uses == "" {
		return "", "", false
	}
	if i := strings.IndexByte(uses, '/'); i >= 0 {
		if i == 0 || i == len(uses)-1 {
			return "", "", false
		}
		return uses[:i], uses[i+1:], true
	}
	return uses, "", true
}

// RunContext 一次运行的上下文：身份 + 已完成步骤的输出
type RunContext struct {
	OrgID   int64
	UserID  int64
	Outputs map[string]any
}

func NewRunContext(orgID, userID int64) *RunContext {
	return &RunContext{OrgID: orgID, UserID: userID, Outputs: map[string]any{}}
}

var outputRefRe = regexp.MustCompile(`\{\{\s*steps\.([A-Za-z0-9_-]+)\.output\s*\}\}`)

// Interpolate 递归替换 with 里的 {{ steps.<id>.output }}。
// 输出为字符串直接嵌入，其余类型 JSON 序列化，便于拼进 prompt / 参数；
// 引用了尚未产出的步骤时原样保留，由执行器/模型看到未解析变量。
func (r *RunContext) Interpolate(v any) any {
	switch x := v.(type) {
	case string:
		return outputRefRe.ReplaceAllStringFunc(x, func(m string) string {
			id := outputRefRe.FindStringSubmatch(m)[1]
			out, ok := r.Outputs[id]
			if !ok {
				return m
			}
			if s, ok := out.(string); ok {
				return s
			}
			b, err := json.Marshal(out)
			if err != nil {
				return fmt.Sprint(out)
			}
			return string(b)
		})
	case map[string]any:
		cp := make(map[string]any, len(x))
		for k, vv := range x {
			cp[k] = r.Interpolate(vv)
		}
		return cp
	case []any:
		cp := make([]any, len(x))
		for i, vv := range x {
			cp[i] = r.Interpolate(vv)
		}
		return cp
	default:
		return v
	}
}

// Executor 执行一个步骤；kind/name 来自 uses，args 已完成变量插值。
// 由 server 装配注入（工具层/HTTP/LLM），本包不关心具体实现。
type Executor func(ctx context.Context, kind, name string, args map[string]any, rctx *RunContext) (any, error)

// maxParallelSteps 同层并发上限：LLM / embedding 调用成本高，避免一次打满上游
const maxParallelSteps = 4

// Runner DAG 执行器：按依赖分层、同层并行；任一步失败即停，未执行步骤保持 skipped。
type Runner struct {
	StepTimeout time.Duration
	exec        Executor
	onStep      func([]StepResult)
}

func NewRunner(exec Executor, onStep func([]StepResult)) *Runner {
	return &Runner{StepTimeout: 5 * time.Minute, exec: exec, onStep: onStep}
}

type stepOutcome struct {
	idx int
	res StepResult
	out any
	err error
}

// Run 返回与 Definition 顺序一致的全部步骤结果（含 skipped）；遇错时返回首个错误。
func (r *Runner) Run(ctx context.Context, def Definition, rctx *RunContext) ([]StepResult, error) {
	if err := def.Validate(); err != nil {
		return nil, err
	}
	layers, err := def.Layers()
	if err != nil {
		return nil, err
	}
	results := make([]StepResult, len(def.Steps))
	for i, s := range def.Steps {
		results[i] = StepResult{ID: s.ID, Uses: s.Uses, Status: "skipped"}
	}
	emit := func() {
		if r.onStep != nil {
			r.onStep(results)
		}
	}

	sem := make(chan struct{}, maxParallelSteps)
	var firstErr error
	failed := false
	for _, layer := range layers {
		// when 判定：主流程（on_success）在出现失败后跳过，补偿步骤
		// （on_failure/always）仍可执行；失败即停对主流程依旧成立。
		runnable := make([]int, 0, len(layer))
		for _, idx := range layer {
			when := def.Steps[idx].When
			if when == "" {
				when = "on_success"
			}
			if (when == "on_success" && failed) || (when == "on_failure" && !failed) {
				continue // 保持预填的 skipped
			}
			runnable = append(runnable, idx)
		}
		if len(runnable) > 0 {
			outCh := make(chan stepOutcome, len(runnable))
			var wg sync.WaitGroup
			for _, idx := range runnable {
				s := def.Steps[idx]
				kind, name, _ := parseUses(s.Uses)
				// 插值与 Outputs 读取都在启动 goroutine 前完成：层内并发不共享写
				args, _ := rctx.Interpolate(s.With).(map[string]any)
				wg.Add(1)
				go func(idx int, s Step, kind, name string, args map[string]any) {
					defer wg.Done()
					sem <- struct{}{}
					defer func() { <-sem }()
					out, err, dur := r.execWithRetry(ctx, s, kind, name, args, rctx)
					outCh <- stepOutcome{
						idx: idx,
						res: StepResult{ID: s.ID, Uses: s.Uses, DurationMs: dur.Milliseconds()},
						out: out, err: err,
					}
				}(idx, s, kind, name, args)
			}
			wg.Wait()
			close(outCh)
			for o := range outCh {
				if o.err != nil {
					o.res.Status, o.res.Error = "failed", o.err.Error()
					failed = true
					if firstErr == nil {
						firstErr = fmt.Errorf("step %q failed: %w", o.res.ID, o.err)
					}
				} else {
					o.res.Status, o.res.Output = "ok", o.out
					rctx.Outputs[o.res.ID] = o.out
				}
				results[o.idx] = o.res
			}
		}
		emit() // 每层结束落一次全量快照
	}
	return results, firstErr
}

// execWithRetry 单步执行 + 固定退避重试；返回总耗时（含重试等待）。
func (r *Runner) execWithRetry(ctx context.Context, s Step, kind, name string, args map[string]any, rctx *RunContext) (any, error, time.Duration) {
	start := time.Now()
	var out any
	var err error
	for attempt := 0; attempt <= s.Retry; attempt++ {
		if attempt > 0 {
			select {
			case <-time.After(time.Duration(attempt) * 200 * time.Millisecond):
			case <-ctx.Done():
				return nil, ctx.Err(), time.Since(start)
			}
		}
		stepCtx, cancel := context.WithTimeout(ctx, r.StepTimeout)
		out, err = r.exec(stepCtx, kind, name, args, rctx)
		cancel()
		if err == nil {
			return out, nil, time.Since(start)
		}
	}
	return nil, err, time.Since(start)
}
