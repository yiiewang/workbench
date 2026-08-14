package db

import (
	"context"
	"database/sql"
	"fmt"
)

// Share 分享记录
type Share struct {
	ID             string `json:"id"`
	Token          string `json:"token"`
	OwnerUserID    string `json:"ownerUserId"`
	OwnerOrgID     string `json:"ownerOrgId"`
	ResourcePath   string `json:"resourcePath"`
	ResourceType   string `json:"resourceType"` // "file" | "dir"
	MaxAccessCount int    `json:"maxAccessCount"`
	AccessCount    int    `json:"accessCount"`
	PasswordHash   string `json:"-"` // 不序列化
	HasPassword    bool   `json:"hasPassword"`
	Remark         string `json:"remark"` // 分享备注（复制链接时附带）
	EffectiveAt    string `json:"effectiveAt"`
	ExpiresAt      string `json:"expiresAt"`
	CreatedAt      string `json:"createdAt"`
}

// CreateShare 创建分享记录
func (d *DB) CreateShare(ctx context.Context, s *Share) error {
	const q = `INSERT INTO shares (id, token, owner_user_id, owner_org_id, resource_path, resource_type, max_access_count, access_count, password_hash, remark, effective_at, expires_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, 0, ?, ?, ?, ?)`
	_, err := d.conn.ExecContext(ctx, q, s.ID, s.Token, s.OwnerUserID, s.OwnerOrgID, s.ResourcePath, s.ResourceType,
		s.MaxAccessCount, s.PasswordHash, s.Remark, s.EffectiveAt, s.ExpiresAt)
	return err
}

// ListSharesByOwner 查询用户的所有分享
func (d *DB) ListSharesByOwner(ctx context.Context, orgID, userID string) ([]Share, error) {
	rows, err := d.conn.QueryContext(ctx, `SELECT id, token, owner_user_id, owner_org_id, resource_path, resource_type,
		max_access_count, access_count, password_hash, remark, effective_at, expires_at, created_at
		FROM shares WHERE owner_org_id = ? AND owner_user_id = ? ORDER BY created_at DESC`, orgID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var shares []Share
	for rows.Next() {
		var s Share
		if err := rows.Scan(&s.ID, &s.Token, &s.OwnerUserID, &s.OwnerOrgID, &s.ResourcePath, &s.ResourceType,
			&s.MaxAccessCount, &s.AccessCount, &s.PasswordHash, &s.Remark, &s.EffectiveAt, &s.ExpiresAt, &s.CreatedAt); err != nil {
			return nil, err
		}
		s.HasPassword = s.PasswordHash != ""
		shares = append(shares, s)
	}
	return shares, nil
}

// GetShareByToken 通过 token 查询分享（用于公开访问）
func (d *DB) GetShareByToken(ctx context.Context, token string) (*Share, error) {
	var s Share
	err := d.conn.QueryRowContext(ctx, `SELECT id, token, owner_user_id, owner_org_id, resource_path, resource_type,
		max_access_count, access_count, password_hash, remark, effective_at, expires_at, created_at
		FROM shares WHERE token = ?`, token).Scan(
		&s.ID, &s.Token, &s.OwnerUserID, &s.OwnerOrgID, &s.ResourcePath, &s.ResourceType,
		&s.MaxAccessCount, &s.AccessCount, &s.PasswordHash, &s.Remark, &s.EffectiveAt, &s.ExpiresAt, &s.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	s.HasPassword = s.PasswordHash != ""
	return &s, nil
}

// DeleteShare 删除分享（仅 owner 可删）
func (d *DB) DeleteShare(ctx context.Context, orgID, userID, shareID string) error {
	const q = `DELETE FROM shares WHERE id = ? AND owner_org_id = ? AND owner_user_id = ?`
	res, err := d.conn.ExecContext(ctx, q, shareID, orgID, userID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("share not found or no permission")
	}
	return nil
}

// IncrementShareAccessCount 增加访问计数，返回更新后的值；达到上限返回 false
func (d *DB) IncrementShareAccessCount(ctx context.Context, token string) (newCount int, limitReached bool, err error) {
	tx, err := d.conn.BeginTx(ctx, nil)
	if err != nil {
		return 0, false, err
	}
	defer func() { _ = tx.Rollback() }()

	var accessCount, maxCount int
	err = tx.QueryRowContext(ctx, `SELECT access_count, max_access_count FROM shares WHERE token = ?`, token).Scan(&accessCount, &maxCount)
	if err == sql.ErrNoRows {
		return 0, false, fmt.Errorf("share not found")
	}
	if err != nil {
		return 0, false, err
	}

	accessCount++
	if maxCount > 0 && accessCount > maxCount {
		return accessCount, true, nil
	}

	_, err = tx.ExecContext(ctx, `UPDATE shares SET access_count = ?, updated_at = datetime('now') WHERE token = ?`, accessCount, token)
	if err != nil {
		return 0, false, err
	}
	return accessCount, false, tx.Commit()
}
