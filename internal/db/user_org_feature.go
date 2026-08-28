package db

import (
	"context"
	"fmt"
	"strings"
)

// 功能标识常量（与 user_org_feature.feature_code 取值对应）
const (
	FeatureFile  = "file"  // 文件浏览
	FeatureShare = "share" // 分享
	FeatureTodo  = "todo"  // 待办看板
	FeatureAdmin = "admin" // 用户管理（组织内成员管理）
)

// AllFeatures 全部功能标识（预置 user_org_feature 用）
var AllFeatures = []string{FeatureFile, FeatureShare, FeatureTodo, FeatureAdmin}

// UserOrgFeature 用户-组织功能映射（per-user-per-org）
type UserOrgFeature struct {
	ID          int64  `json:"id"`
	UserID      int64  `json:"userId"`
	OrgID       int64  `json:"orgId"`
	FeatureCode string `json:"featureCode"`
	Enabled     bool   `json:"enabled"`
}

// SeedUserOrgFeatures 为用户在某组织预置全部默认功能（幂等，已存在不覆盖 enabled）。
// 用户加入组织时（AddUserOrg / EnsureUserOrg）调用。
func (d *DB) SeedUserOrgFeatures(ctx context.Context, userID, orgID int64) error {
	const q = `INSERT OR IGNORE INTO user_org_feature (user_id, org_id, feature_code, enabled)
		SELECT ?, ?, code, 1 FROM (
			SELECT 'file' AS code UNION ALL SELECT 'share' UNION ALL SELECT 'todo' UNION ALL SELECT 'admin'
		)`
	if _, err := d.conn.ExecContext(ctx, q, userID, orgID); err != nil {
		return err
	}
	return nil
}

// ListUserOrgFeatures 列出用户在某组织启用的功能标识（enabled=1），按 feature_code 排序
func (d *DB) ListUserOrgFeatures(ctx context.Context, userID, orgID int64) ([]string, error) {
	rows, err := d.conn.QueryContext(ctx,
		`SELECT feature_code FROM user_org_feature WHERE user_id = ? AND org_id = ? AND enabled = 1 ORDER BY feature_code`, userID, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	features := make([]string, 0)
	for rows.Next() {
		var code string
		if err := rows.Scan(&code); err != nil {
			return nil, err
		}
		features = append(features, code)
	}
	return features, rows.Err()
}

// ListUserOrgFeaturesWithState 列出用户在某组织的全部功能及启用状态（含未启用的）。
// 返回 AllFeatures 中的每个功能，未在 user_org_feature 表出现的功能按 enabled=false 补齐。
func (d *DB) ListUserOrgFeaturesWithState(ctx context.Context, userID, orgID int64) ([]UserOrgFeature, error) {
	rows, err := d.conn.QueryContext(ctx,
		`SELECT feature_code, enabled FROM user_org_feature WHERE user_id = ? AND org_id = ? ORDER BY feature_code`, userID, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	state := make(map[string]bool, len(AllFeatures))
	for rows.Next() {
		var code string
		var enabled int
		if err := rows.Scan(&code, &enabled); err != nil {
			return nil, err
		}
		state[code] = enabled == 1
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// 按 AllFeatures 定义顺序补齐，保证输出稳定且覆盖全部功能
	result := make([]UserOrgFeature, 0, len(AllFeatures))
	for _, code := range AllFeatures {
		result = append(result, UserOrgFeature{
			UserID:      userID,
			OrgID:       orgID,
			FeatureCode: code,
			Enabled:     state[code],
		})
	}
	return result, nil
}

// UpsertUserOrgFeature 设置用户在某组织某功能的启用状态（幂等：已存在则更新 enabled）
func (d *DB) UpsertUserOrgFeature(ctx context.Context, userID, orgID int64, featureCode string, enabled bool) error {
	enabledInt := 0
	if enabled {
		enabledInt = 1
	}
	const q = `INSERT INTO user_org_feature (user_id, org_id, feature_code, enabled) VALUES (?, ?, ?, ?)
		ON CONFLICT(user_id, org_id, feature_code) DO UPDATE SET enabled = excluded.enabled, updated_at = datetime('now')`
	if _, err := d.conn.ExecContext(ctx, q, userID, orgID, featureCode, enabledInt); err != nil {
		return err
	}
	return nil
}

// featuresForUserOrgs 批量查询某用户在多个组织启用的功能（enabled=1），
// 返回 org_id → feature_code 列表映射。供 userinfo 聚合接口一次查出所有组织功能，避免 N+1 查询。
func (d *DB) featuresForUserOrgs(ctx context.Context, userID int64, orgIDs []int64) (map[int64][]string, error) {
	result := make(map[int64][]string, len(orgIDs))
	if len(orgIDs) == 0 {
		return result, nil
	}

	placeholders := strings.Repeat("?,", len(orgIDs))
	placeholders = placeholders[:len(placeholders)-1]
	args := make([]any, 0, len(orgIDs)+1)
	args = append(args, userID)
	for _, id := range orgIDs {
		args = append(args, id)
	}

	q := fmt.Sprintf(
		`SELECT org_id, feature_code FROM user_org_feature WHERE user_id = ? AND enabled = 1 AND org_id IN (%s) ORDER BY org_id, feature_code`,
		placeholders)
	rows, err := d.conn.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var orgID int64
		var code string
		if err := rows.Scan(&orgID, &code); err != nil {
			return nil, err
		}
		result[orgID] = append(result[orgID], code)
	}
	return result, rows.Err()
}
