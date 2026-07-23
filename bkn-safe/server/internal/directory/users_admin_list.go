// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package directory

import (
	"context"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
)

// UserListFilter is the admin user-list query (pagination + optional filters).
type UserListFilter struct {
	Search         string
	Enabled        *bool
	DepartmentID   string
	IncludeSubtree bool
	RoleID         string
	Offset         int
	Limit          int
}

// UserSummary is the admin list view of a user.
type UserSummary struct {
	ID              string    `json:"id"`
	Account         string    `json:"account"`
	Name            string    `json:"name"`
	Email           string    `json:"email"`
	Telephone       string    `json:"telephone"`
	Enabled         bool      `json:"enabled"`
	AccountType     string    `json:"account_type"`
	UpdatedAt       time.Time `json:"updated_at"`
	DepartmentIDs   []string  `json:"department_ids"`
	DepartmentNames []string  `json:"department_names"`
	RoleIDs         []string  `json:"role_ids"`
	RoleNames       []string  `json:"role_names"`
}

// userSummaryCols selects the scalar user fields for list queries.
var userSummaryCols = []string{
	"id", "account", "name", "email", "telephone", "enabled", "account_type", "updated_at",
}

// ListUsers returns a filtered, paginated user list plus the total match count.
func (s *Service) ListUsers(ctx context.Context, filter UserListFilter) ([]UserSummary, int64, error) {
	limit := filter.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}
	offset := filter.Offset
	if offset < 0 {
		offset = 0
	}

	q := s.db.WithContext(ctx).Model(&model.User{})
	if filter.Search != "" {
		like := "%" + filter.Search + "%"
		q = q.Where(
			"account LIKE ? OR name LIKE ? OR email LIKE ? OR telephone LIKE ?",
			like, like, like, like,
		)
	}
	if filter.Enabled != nil {
		q = q.Where("enabled = ?", *filter.Enabled)
	}
	if filter.DepartmentID != "" {
		deptIDs, err := s.departmentScopeIDs(ctx, filter.DepartmentID, filter.IncludeSubtree)
		if err != nil {
			return nil, 0, err
		}
		if len(deptIDs) == 0 {
			return []UserSummary{}, 0, nil
		}
		q = q.Where(
			"id IN (?)",
			s.db.Model(&model.UserDepartment{}).
				Select("user_id").
				Where("department_id IN ?", deptIDs),
		)
	}
	if filter.RoleID != "" {
		q = q.Where(
			"id IN (?)",
			s.db.Table("casbin_rule").
				Select("v0").
				Where("ptype = ? AND v1 = ?", "g", filter.RoleID),
		)
	}

	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	out := make([]UserSummary, 0, limit)
	rows := make([]struct {
		ID          string
		Account     string
		Name        string
		Email       string
		Enabled     bool
		AccountType string
		UpdatedAt   time.Time
	}, 0, limit)
	if err := q.Select(userSummaryCols).Order("account").Offset(offset).Limit(limit).Scan(&rows).Error; err != nil {
		return nil, 0, err
	}
	for _, row := range rows {
		out = append(out, UserSummary{
			ID:          row.ID,
			Account:     row.Account,
			Name:        row.Name,
			Email:       row.Email,
			Enabled:     row.Enabled,
			AccountType: row.AccountType,
			UpdatedAt:   row.UpdatedAt,
		})
	}
	if err := s.enrichUserSummaries(ctx, out); err != nil {
		return nil, 0, err
	}
	return out, total, nil
}

// departmentScopeIDs returns the department id(s) used for membership filtering.
func (s *Service) departmentScopeIDs(ctx context.Context, deptID string, includeSubtree bool) ([]string, error) {
	if !includeSubtree {
		return []string{deptID}, nil
	}
	ids := []string{deptID}
	queue := []string{deptID}
	for len(queue) > 0 {
		parentID := queue[0]
		queue = queue[1:]
		children, err := s.ListDepartments(ctx, parentID)
		if err != nil {
			return nil, err
		}
		for _, child := range children {
			ids = append(ids, child.ID)
			queue = append(queue, child.ID)
		}
	}
	return ids, nil
}

func (s *Service) enrichUserSummaries(ctx context.Context, users []UserSummary) error {
	if len(users) == 0 {
		return nil
	}
	ids := make([]string, len(users))
	byID := make(map[string]*UserSummary, len(users))
	for i := range users {
		ids[i] = users[i].ID
		users[i].DepartmentIDs = []string{}
		users[i].DepartmentNames = []string{}
		users[i].RoleIDs = []string{}
		users[i].RoleNames = []string{}
		byID[users[i].ID] = &users[i]
	}

	var uds []model.UserDepartment
	if err := s.db.WithContext(ctx).Where("user_id IN ?", ids).Find(&uds).Error; err != nil {
		return err
	}
	deptIDSet := map[string]bool{}
	userDeptIDs := map[string][]string{}
	for _, ud := range uds {
		userDeptIDs[ud.UserID] = append(userDeptIDs[ud.UserID], ud.DepartmentID)
		deptIDSet[ud.DepartmentID] = true
	}
	deptIDs := make([]string, 0, len(deptIDSet))
	for id := range deptIDSet {
		deptIDs = append(deptIDs, id)
	}
	deptNames, err := s.ResolveDepartmentNames(ctx, deptIDs)
	if err != nil {
		return err
	}
	deptNameByID := map[string]string{}
	for _, ref := range deptNames {
		deptNameByID[ref.ID] = ref.Name
	}
	for userID, dids := range userDeptIDs {
		u := byID[userID]
		if u == nil {
			continue
		}
		u.DepartmentIDs = append(u.DepartmentIDs, dids...)
		for _, did := range dids {
			if name := deptNameByID[did]; name != "" {
				u.DepartmentNames = append(u.DepartmentNames, name)
			}
		}
	}

	var rules []struct {
		UserID string
		RoleID string
	}
	if err := s.db.WithContext(ctx).Table("casbin_rule").
		Select("v0 AS user_id, v1 AS role_id").
		Where("ptype = ? AND v0 IN ?", "g", ids).
		Scan(&rules).Error; err != nil {
		return err
	}
	roleIDSet := map[string]bool{}
	userRoleIDs := map[string][]string{}
	for _, rule := range rules {
		userRoleIDs[rule.UserID] = append(userRoleIDs[rule.UserID], rule.RoleID)
		roleIDSet[rule.RoleID] = true
	}
	roleIDs := make([]string, 0, len(roleIDSet))
	for id := range roleIDSet {
		roleIDs = append(roleIDs, id)
	}
	roleNameByID := map[string]string{}
	if len(roleIDs) > 0 {
		var roles []model.Role
		if err := s.db.WithContext(ctx).Where("id IN ?", roleIDs).Find(&roles).Error; err != nil {
			return err
		}
		for _, role := range roles {
			roleNameByID[role.ID] = role.Name
		}
	}
	for userID, rids := range userRoleIDs {
		u := byID[userID]
		if u == nil {
			continue
		}
		u.RoleIDs = append(u.RoleIDs, rids...)
		for _, rid := range rids {
			if name := roleNameByID[rid]; name != "" {
				u.RoleNames = append(u.RoleNames, name)
			}
		}
	}
	return nil
}
