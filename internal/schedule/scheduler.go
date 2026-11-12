package schedule

// 手写 cron 表达式引擎：标准 5 段（分 时 日 月 周），支持
//   *        任意值
//   */n      步进（0, n, 2n, …）
//   a-b      区间（含端点）
//   a,b,c    列表（可与区间/步进组合：1-10/2,20,30）
// 周字段 0/7 均为周日。
//
// 实现方式：表达式预编译为 5 个 bitset（uint64，分钟/小时 0-59、
// 日 1-31、月 1-12、周 0-7），匹配 O(1)——每分钟 tick 一次，
// 逐个 job 查 bitset 即可，无需外部库。

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"time"
)

// CronSpec 预编译的 cron 表达式（5 个 bitset）
type CronSpec struct {
	min, hour, dom, mon, dow uint64
}

// 各字段的 bit 序号即取值本身（dom 从 1 开始仍用 bit 位序号对应）
func bit(v uint) uint64 { return 1 << v }

// parseField 解析单字段表达式为 bitset；lo/hi 为合法取值范围，names 为别名映射
func parseField(field string, lo, hi uint, names map[string]uint) (uint64, error) {
	var set uint64
	for _, part := range strings.Split(field, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			return 0, fmt.Errorf("empty list item")
		}
		// 步进：range/step 或 */step
		rangePart, stepPart := part, ""
		if i := strings.IndexByte(part, '/'); i >= 0 {
			rangePart, stepPart = part[:i], part[i+1:]
		}
		var blo, bhi uint
		switch {
		case rangePart == "*" || rangePart == "":
			blo, bhi = lo, hi
		case strings.Contains(rangePart, "-"):
			ab := strings.SplitN(rangePart, "-", 2)
			var err error
			if blo, err = parseValue(ab[0], names); err != nil {
				return 0, err
			}
			if bhi, err = parseValue(ab[1], names); err != nil {
				return 0, err
			}
		default:
			v, err := parseValue(rangePart, names)
			if err != nil {
				return 0, err
			}
			if stepPart != "" {
				blo, bhi = v, hi // "n/step" = 从 n 到上限按步进
			} else {
				blo, bhi = v, v
			}
		}
		if blo < lo || bhi > hi || blo > bhi {
			return 0, fmt.Errorf("value out of range [%d,%d]: %q", lo, hi, part)
		}
		step := uint(1)
		if stepPart != "" {
			s, err := strconv.ParseUint(stepPart, 10, 32)
			if err != nil || s == 0 {
				return 0, fmt.Errorf("invalid step %q", stepPart)
			}
			step = uint(s)
		}
		for v := blo; v <= bhi; v += step {
			set |= bit(v)
		}
	}
	if set == 0 {
		return 0, fmt.Errorf("empty field")
	}
	return set, nil
}

// parseValue 解析单值：数字或英文别名（周字段的 sun/mon/…）
func parseValue(s string, names map[string]uint) (uint, error) {
	if v, ok := names[s]; ok {
		return v, nil
	}
	n, err := strconv.ParseUint(s, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("invalid value %q", s)
	}
	return uint(n), nil
}

var dowNames = map[string]uint{"sun": 0, "mon": 1, "tue": 2, "wed": 3, "thu": 4, "fri": 5, "sat": 6}
var monNames = map[string]uint{"jan": 1, "feb": 2, "mar": 3, "apr": 4, "may": 5, "jun": 6,
	"jul": 7, "aug": 8, "sep": 9, "oct": 10, "nov": 11, "dec": 12}

// ParseCron 解析 5 段表达式："分 时 日 月 周"
func ParseCron(expr string) (*CronSpec, error) {
	fields := strings.Fields(expr)
	if len(fields) != 5 {
		return nil, fmt.Errorf("cron expression must have 5 fields (min hour dom mon dow), got %d", len(fields))
	}
	s := &CronSpec{}
	var err error
	if s.min, err = parseField(fields[0], 0, 59, nil); err != nil {
		return nil, fmt.Errorf("minute: %w", err)
	}
	if s.hour, err = parseField(fields[1], 0, 23, nil); err != nil {
		return nil, fmt.Errorf("hour: %w", err)
	}
	if s.dom, err = parseField(fields[2], 1, 31, nil); err != nil {
		return nil, fmt.Errorf("day-of-month: %w", err)
	}
	if s.mon, err = parseField(fields[3], 1, 12, monNames); err != nil {
		return nil, fmt.Errorf("month: %w", err)
	}
	if s.dow, err = parseField(fields[4], 0, 7, dowNames); err != nil {
		return nil, fmt.Errorf("day-of-week: %w", err)
	}
	// 7 与 0 同为周日
	if s.dow&bit(7) != 0 {
		s.dow |= bit(0)
	}
	return s, nil
}

// Matches 判断 t（在指定时区语义下取值）是否命中表达式。
// 采用 Vixie cron 语义：日与周均受限时取并集（任一命中即触发）。
func (s *CronSpec) Matches(t time.Time) bool {
	if s.min&bit(uint(t.Minute())) == 0 {
		return false
	}
	if s.hour&bit(uint(t.Hour())) == 0 {
		return false
	}
	if s.mon&bit(uint(t.Month())) == 0 {
		return false
	}
	domHit := s.dom&bit(uint(t.Day())) != 0
	dowHit := s.dow&bit(uint(t.Weekday())) != 0
	domStar := s.dom == allBits(1, 31)
	dowStar := s.dow == allBits(0, 7) // 已归一 7→0
	switch {
	case domStar && dowStar:
		return true
	case domStar:
		return dowHit
	case dowStar:
		return domHit
	default:
		return domHit || dowHit
	}
}

// allBits [lo,hi] 全 1 的 bitset
func allBits(lo, hi uint) uint64 {
	var set uint64
	for v := lo; v <= hi; v++ {
		set |= bit(v)
	}
	return set
}

// Next 返回 t 之后的下一次触发时刻（向后逐分钟探测，上限 4 年——
// 覆盖 2/29 这类极端日期；仅供调试/展示，调度器本身用 Matches 逐分钟对齐）。
func (s *CronSpec) Next(t time.Time) time.Time {
	cur := t.Truncate(time.Minute).Add(time.Minute)
	limit := t.Add(4 * 365 * 24 * time.Hour)
	for cur.Before(limit) {
		if s.Matches(cur) {
			return cur
		}
		cur = cur.Add(time.Minute)
	}
	return time.Time{}
}

// ============================================================
// Scheduler：每分钟 tick + 表达式匹配的调度器
// ============================================================

// Job 定时任务：表达式 + 触发动作（拿到触发时刻与总线）
type Job struct {
	Name string
	Spec *CronSpec
	Run  func(ctx context.Context, fireAt time.Time, bus *EventBus)
}

// Scheduler 调度器：持有 job 列表与事件总线，Start 后每分钟对齐 tick。
// tick 时刻与整分对齐（如 10:00:00+n 偏移启动），漂移由 ticker 本身保证。
type Scheduler struct {
	jobs []*Job
	bus  *EventBus
	loc  *time.Location

	cancel context.CancelFunc
	done   chan struct{}
}

// NewScheduler 创建调度器（loc 为表达式求值时区，nil = UTC）
func NewScheduler(bus *EventBus, loc *time.Location) *Scheduler {
	if loc == nil {
		loc = time.UTC
	}
	return &Scheduler{bus: bus, loc: loc, done: make(chan struct{})}
}

// AddJob 注册任务（须在 Start 前调用）
func (s *Scheduler) AddJob(job *Job) {
	s.jobs = append(s.jobs, job)
}

// Start 启动调度循环（goroutine）。首个 tick 对齐到下一整分钟，
// 随后每分钟一次；服务停止由 ctx 取消退出。
func (s *Scheduler) Start(ctx context.Context) {
	ctx, s.cancel = context.WithCancel(ctx)
	go func() {
		defer close(s.done)
		// 对齐下一整分钟
		now := time.Now()
		next := now.Truncate(time.Minute).Add(time.Minute)
		select {
		case <-time.After(next.Sub(now)):
		case <-ctx.Done():
			return
		}
		s.tick(ctx, next)
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case t := <-ticker.C:
				s.tick(ctx, t.Truncate(time.Minute))
			case <-ctx.Done():
				return
			}
		}
	}()
}

// Stop 停止调度器并等待循环退出
func (s *Scheduler) Stop() {
	if s.cancel != nil {
		s.cancel()
		<-s.done
	}
}

// tick 单次调度：遍历 job，命中表达式的在独立 goroutine 执行
// （单 job 慢不阻塞其他 job 与下一 tick）
func (s *Scheduler) tick(ctx context.Context, fireAt time.Time) {
	local := fireAt.In(s.loc)
	for _, job := range s.jobs {
		if !job.Spec.Matches(local) {
			continue
		}
		j := job
		go func() {
			defer func() {
				if r := recover(); r != nil {
					// 已导入 log/slog（bus.go 同包）
					slog.Error("schedule job panic", "job", j.Name, "panic", r)
				}
			}()
			j.Run(ctx, fireAt, s.bus)
		}()
	}
}

// Jobs 返回已注册任务名（按注册序），调试/状态展示用
func (s *Scheduler) Jobs() []string {
	names := make([]string, 0, len(s.jobs))
	for _, j := range s.jobs {
		names = append(names, j.Name)
	}
	sort.Strings(names)
	return names
}
