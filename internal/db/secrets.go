package db

import (
	"context"
	"crypto/rand"
	"database/sql"
	"fmt"
	"os"
)

// LoadOrCreateSecret 从数据库加载指定密钥；不存在时优先迁移旧文件，再无则生成新密钥。
// legacyFilePath 非空且 db 无记录时，尝试读取旧文件并迁移入库、删除旧文件。
func (d *DB) LoadOrCreateSecret(ctx context.Context, key, legacyFilePath string) ([]byte, error) {
	// 1. 查 db
	var val []byte
	err := d.conn.QueryRowContext(ctx, `SELECT value FROM app_secrets WHERE key = ?`, key).Scan(&val)
	if err == nil {
		return val, nil
	}
	if err != sql.ErrNoRows {
		return nil, fmt.Errorf("query secret %s: %w", key, err)
	}
	// 2. db 无记录，尝试迁移旧文件
	if legacyFilePath != "" {
		if data, err := os.ReadFile(legacyFilePath); err == nil {
			if _, err := d.conn.ExecContext(ctx, `INSERT INTO app_secrets (key, value) VALUES (?, ?)`, key, data); err != nil {
				return nil, fmt.Errorf("migrate secret %s: %w", key, err)
			}
			os.Remove(legacyFilePath) // 迁移成功删除旧文件
			return data, nil
		}
	}
	// 3. 都没有，生成新密钥
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return nil, fmt.Errorf("generate secret %s: %w", key, err)
	}
	if _, err := d.conn.ExecContext(ctx, `INSERT INTO app_secrets (key, value) VALUES (?, ?)`, key, secret); err != nil {
		return nil, fmt.Errorf("insert secret %s: %w", key, err)
	}
	return secret, nil
}
