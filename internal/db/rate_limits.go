package db

import (
	"context"
	"database/sql"
	"time"
)

// RateLimitCheck 返回 key 在当前窗口内的失败次数；窗口过期或无记录返回 0。
func (d *DB) RateLimitCheck(ctx context.Context, key string, window time.Duration) (int, error) {
	var count int
	var expiresAt int64
	err := d.conn.QueryRowContext(ctx, `SELECT count, expires_at FROM rate_limits WHERE key = ?`, key).Scan(&count, &expiresAt)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if time.Now().Unix() >= expiresAt {
		return 0, nil // 窗口已过期
	}
	return count, nil
}

// RateLimitRecord 记录一次失败：无记录或窗口过期则重置为 1 并开启新窗口，否则计数 +1。返回更新后的计数。
func (d *DB) RateLimitRecord(ctx context.Context, key string, window time.Duration) (int, error) {
	now := time.Now().Unix()
	expiresAt := time.Now().Add(window).Unix()

	var count int
	var exp int64
	err := d.conn.QueryRowContext(ctx, `SELECT count, expires_at FROM rate_limits WHERE key = ?`, key).Scan(&count, &exp)
	if err == sql.ErrNoRows || (err == nil && now >= exp) {
		// 不存在或已过期：重置为 1
		_, err = d.conn.ExecContext(ctx, `INSERT INTO rate_limits (key, count, expires_at) VALUES (?, 1, ?)
			ON CONFLICT(key) DO UPDATE SET count = 1, expires_at = excluded.expires_at`, key, expiresAt)
		if err != nil {
			return 0, err
		}
		return 1, nil
	}
	if err != nil {
		return 0, err
	}
	// 未过期：计数 +1
	if _, err = d.conn.ExecContext(ctx, `UPDATE rate_limits SET count = count + 1 WHERE key = ?`, key); err != nil {
		return 0, err
	}
	return count + 1, nil
}

// RateLimitClear 清除 key 的失败计数（成功后调用）。
func (d *DB) RateLimitClear(ctx context.Context, key string) error {
	_, err := d.conn.ExecContext(ctx, `DELETE FROM rate_limits WHERE key = ?`, key)
	return err
}
