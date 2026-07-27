// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package directory

import (
	"context"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
)

// DepartmentListItem is the admin list view of a department (snake_case JSON).
type DepartmentListItem struct {
	ID                 string `json:"id"`
	Name               string `json:"name"`
	ParentID           string `json:"parent_id"`
	Type               string `json:"type"`
	ManagerID          string `json:"manager_id,omitempty"`
	ManagerName        string `json:"manager_name,omitempty"`
	Code               string `json:"code,omitempty"`
	Email              string `json:"email,omitempty"`
	Remark             string `json:"remark,omitempty"`
	MemberCount        int64  `json:"member_count"`
	SubtreeMemberCount int64  `json:"subtree_member_count"`
}

func departmentListItem(d model.Department, memberCount, subtreeMemberCount int64, managerName string) DepartmentListItem {
	return DepartmentListItem{
		ID:                 d.ID,
		Name:               d.Name,
		ParentID:           d.ParentID,
		Type:               d.Type,
		ManagerID:          d.ManagerID,
		ManagerName:        managerName,
		Code:               d.Code,
		Email:              d.Email,
		Remark:             d.Remark,
		MemberCount:        memberCount,
		SubtreeMemberCount: subtreeMemberCount,
	}
}

func (s *Service) memberCounts(ctx context.Context, deptIDs []string) (map[string]int64, error) {
	out := map[string]int64{}
	if len(deptIDs) == 0 {
		return out, nil
	}
	type row struct {
		DepartmentID string
		Count        int64
	}
	var rows []row
	if err := s.db.WithContext(ctx).Model(&model.UserDepartment{}).
		Select("department_id, COUNT(*) AS count").
		Where("department_id IN ?", deptIDs).
		Group("department_id").
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	for _, r := range rows {
		out[r.DepartmentID] = r.Count
	}
	return out, nil
}

// subtreeMemberCounts returns distinct user counts per department including all descendants.
func (s *Service) subtreeMemberCounts(ctx context.Context, deptIDs []string) (map[string]int64, error) {
	out := map[string]int64{}
	if len(deptIDs) == 0 {
		return out, nil
	}
	var allDeps []model.Department
	if err := s.db.WithContext(ctx).Select("id", "parent_id").Find(&allDeps).Error; err != nil {
		return nil, err
	}
	childrenOf := map[string][]string{}
	allDeptIDs := make([]string, len(allDeps))
	for i, d := range allDeps {
		allDeptIDs[i] = d.ID
		if d.ParentID != "" {
			childrenOf[d.ParentID] = append(childrenOf[d.ParentID], d.ID)
		}
	}
	subtreeCache := map[string][]string{}
	var subtree func(id string) []string
	subtree = func(id string) []string {
		if cached, ok := subtreeCache[id]; ok {
			return cached
		}
		ids := []string{id}
		for _, child := range childrenOf[id] {
			ids = append(ids, subtree(child)...)
		}
		subtreeCache[id] = ids
		return ids
	}
	type row struct {
		UserID       string
		DepartmentID string
	}
	var rows []row
	if err := s.db.WithContext(ctx).Model(&model.UserDepartment{}).
		Select("user_id, department_id").
		Where("department_id IN ?", allDeptIDs).
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	usersByDept := map[string]map[string]struct{}{}
	for _, r := range rows {
		if usersByDept[r.DepartmentID] == nil {
			usersByDept[r.DepartmentID] = map[string]struct{}{}
		}
		usersByDept[r.DepartmentID][r.UserID] = struct{}{}
	}
	for _, id := range deptIDs {
		users := map[string]struct{}{}
		for _, did := range subtree(id) {
			for uid := range usersByDept[did] {
				users[uid] = struct{}{}
			}
		}
		out[id] = int64(len(users))
	}
	return out, nil
}

// ListAllDepartments returns a flat, paginated department list with member_count.
func (s *Service) ListAllDepartments(ctx context.Context, search string, offset, limit int) ([]DepartmentListItem, int64, error) {
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}
	if offset < 0 {
		offset = 0
	}
	q := s.db.WithContext(ctx).Model(&model.Department{})
	if search != "" {
		q = q.Where("name LIKE ?", "%"+search+"%")
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	deps := make([]model.Department, 0, limit)
	if err := q.Order("parent_id, name").Offset(offset).Limit(limit).Find(&deps).Error; err != nil {
		return nil, 0, err
	}
	ids := make([]string, len(deps))
	for i, d := range deps {
		ids[i] = d.ID
	}
	counts, err := s.memberCounts(ctx, ids)
	if err != nil {
		return nil, 0, err
	}
	subtreeCounts, err := s.subtreeMemberCounts(ctx, ids)
	if err != nil {
		return nil, 0, err
	}
	managerIDs := make([]string, len(deps))
	for i, d := range deps {
		managerIDs[i] = d.ManagerID
	}
	managerNames, err := s.managerNamesByID(ctx, managerIDs)
	if err != nil {
		return nil, 0, err
	}
	out := make([]DepartmentListItem, len(deps))
	for i, d := range deps {
		out[i] = departmentListItem(d, counts[d.ID], subtreeCounts[d.ID], managerNames[d.ManagerID])
	}
	return out, total, nil
}

// ListDepartmentsWithCounts returns direct children of parentID with member_count.
func (s *Service) ListDepartmentsWithCounts(ctx context.Context, parentID string) ([]DepartmentListItem, error) {
	deps, err := s.ListDepartments(ctx, parentID)
	if err != nil {
		return nil, err
	}
	ids := make([]string, len(deps))
	for i, d := range deps {
		ids[i] = d.ID
	}
	counts, err := s.memberCounts(ctx, ids)
	if err != nil {
		return nil, err
	}
	subtreeCounts, err := s.subtreeMemberCounts(ctx, ids)
	if err != nil {
		return nil, err
	}
	managerIDs := make([]string, len(deps))
	for i, d := range deps {
		managerIDs[i] = d.ManagerID
	}
	managerNames, err := s.managerNamesByID(ctx, managerIDs)
	if err != nil {
		return nil, err
	}
	out := make([]DepartmentListItem, len(deps))
	for i, d := range deps {
		out[i] = departmentListItem(d, counts[d.ID], subtreeCounts[d.ID], managerNames[d.ManagerID])
	}
	return out, nil
}
