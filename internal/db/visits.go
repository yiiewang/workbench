package db

import (
	"context"
	"fmt"
	"time"
)

// LogVisit 记录一次访问
func (d *DB) LogVisit(ctx context.Context, visitorID, ip, orgID, userAgent, path string, statusCode int) error {
	const q = `INSERT INTO visit_logs (visitor_id, ip, org_id, user_agent, path, status_code) VALUES (?, ?, ?, ?, ?, ?)`
	_, err := d.conn.ExecContext(ctx, q, visitorID, ip, orgID, userAgent, path, statusCode)
	return err
}

// GetStatsByOrg 查询指定组织的访问统计
func (d *DB) GetStatsByOrg(ctx context.Context, orgID string) (*VisitStats, error) {
	stats := &VisitStats{
		Visitors: make(map[string]VisitorStat),
		TopPages: make(map[string]int),
	}

	row := d.conn.QueryRowContext(ctx, `SELECT COUNT(DISTINCT visitor_id), COUNT(*) FROM visit_logs WHERE org_id = ?`, orgID)
	if err := row.Scan(&stats.TotalVisitors, &stats.TotalPageViews); err != nil {
		return nil, fmt.Errorf("count visitors: %w", err)
	}

	vr, err := d.conn.QueryContext(ctx, `
		SELECT visitor_id, COUNT(*), COUNT(DISTINCT path),
			MIN(created_at), MAX(created_at)
		FROM visit_logs WHERE org_id = ? GROUP BY visitor_id ORDER BY MAX(created_at) DESC`, orgID)
	if err != nil {
		return nil, fmt.Errorf("query visitors: %w", err)
	}
	defer vr.Close()
	for vr.Next() {
		var v VisitorStat
		var first, last string
		if err := vr.Scan(&v.VisitorID, &v.Visits, &v.PagesVisited, &first, &last); err != nil {
			return nil, fmt.Errorf("scan visitor: %w", err)
		}
		v.FirstVisit = first
		v.LastVisit = last
		v.DurationSecs = durationSeconds(first, last)
		stats.Visitors[v.VisitorID] = v
	}

	pr, err := d.conn.QueryContext(ctx, `SELECT path, COUNT(*) FROM visit_logs WHERE org_id = ? GROUP BY path ORDER BY COUNT(*) DESC LIMIT 10`, orgID)
	if err != nil {
		return nil, fmt.Errorf("query top pages: %w", err)
	}
	defer pr.Close()
	for pr.Next() {
		var path string
		var count int
		if err := pr.Scan(&path, &count); err != nil {
			return nil, fmt.Errorf("scan page: %w", err)
		}
		stats.TopPages[path] = count
	}

	return stats, nil
}

func durationSeconds(start, end string) int {
	parse := func(s string) time.Time {
		for _, l := range []string{"2006-01-02 15:04:05", "2006-01-02T15:04:05Z", "2006-01-02T15:04:05-07:00"} {
			if t, err := time.Parse(l, s); err == nil {
				return t
			}
		}
		return time.Time{}
	}
	t1, t2 := parse(start), parse(end)
	if t1.IsZero() || t2.IsZero() {
		return 0
	}
	return int(t2.Sub(t1).Seconds())
}
