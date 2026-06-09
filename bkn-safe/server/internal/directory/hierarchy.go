package directory

import (
	"context"
	"errors"

	"gorm.io/gorm"

	"bkn-safe/internal/model"
)

// This file adds the org-hierarchy read surface the DA umcmp client needs:
// department ancestor chains, transitive department ids, batch user detail
// (with parent_deps + groups + roles), group-member split, and subtree-aware
// search-org. Semantics (ratified): "under a department" is TRANSITIVE (the
// department and all its descendants); a user's groups include both direct
// memberships and groups inherited via the user's departments (incl ancestors).

// DeptRef is a department reference in a parent chain. Type is always
// "department" (mirrors ISF ParentDepInfo).
type DeptRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"`
}

// DeptInfo is a department with its root-first ancestor chain (inclusive of the
// department itself: [root, ..., dept]).
type DeptInfo struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	ParentDeps []DeptRef `json:"parent_deps"`
}

// GroupRef is a resolved group.
type GroupRef struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Notes string `json:"notes"`
}

// UserFull is a user's full directory record (the union of fields the umcmp
// client may request: name/account/enabled/roles/parent_deps/groups).
type UserFull struct {
	ID         string      `json:"id"`
	Account    string      `json:"account"`
	Name       string      `json:"name"`
	Enabled    bool        `json:"enabled"`
	Roles      []string    `json:"roles"`
	ParentDeps [][]DeptRef `json:"parent_deps"` // one chain per membership department
	Groups     []GroupRef  `json:"groups"`
}

// deptChain returns the path [root, ..., deptID] (root first, inclusive of the
// department). Cycle-guarded; a missing department yields what was collected.
func (s *Service) deptChain(ctx context.Context, deptID string) ([]DeptRef, error) {
	var rev []DeptRef
	seen := map[string]bool{}
	cur := deptID
	for cur != "" && !seen[cur] {
		seen[cur] = true
		var d model.Department
		err := s.db.WithContext(ctx).First(&d, "id = ?", cur).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			break
		}
		if err != nil {
			return nil, err
		}
		rev = append(rev, DeptRef{ID: d.ID, Name: d.Name, Type: "department"})
		cur = d.ParentID
	}
	// rev is dept->root; reverse to root->dept.
	for i, j := 0, len(rev)-1; i < j; i, j = i+1, j-1 {
		rev[i], rev[j] = rev[j], rev[i]
	}
	return rev, nil
}

// userDirectDeptIDs returns the departments a user is directly assigned to.
func (s *Service) userDirectDeptIDs(ctx context.Context, userID string) ([]string, error) {
	var uds []model.UserDepartment
	if err := s.db.WithContext(ctx).Where("user_id = ?", userID).Find(&uds).Error; err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(uds))
	for _, ud := range uds {
		ids = append(ids, ud.DepartmentID)
	}
	return ids, nil
}

// UserDeptIDs returns the transitive set of department ids a user belongs to:
// every direct department plus all of its ancestors, de-duplicated.
func (s *Service) UserDeptIDs(ctx context.Context, userID string) ([]string, error) {
	direct, err := s.userDirectDeptIDs(ctx, userID)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(direct))
	for _, d := range direct {
		chain, err := s.deptChain(ctx, d)
		if err != nil {
			return nil, err
		}
		for _, c := range chain {
			if !seen[c.ID] {
				seen[c.ID] = true
				out = append(out, c.ID)
			}
		}
	}
	return out, nil
}

// UserParentDeps returns one root-first chain per department the user is
// directly assigned to.
func (s *Service) UserParentDeps(ctx context.Context, userID string) ([][]DeptRef, error) {
	direct, err := s.userDirectDeptIDs(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := make([][]DeptRef, 0, len(direct))
	for _, d := range direct {
		chain, err := s.deptChain(ctx, d)
		if err != nil {
			return nil, err
		}
		out = append(out, chain)
	}
	return out, nil
}

// DepartmentInfos returns each existing department with its root-first ancestor
// chain. Unknown ids are omitted (clean contract).
func (s *Service) DepartmentInfos(ctx context.Context, ids []string) ([]DeptInfo, error) {
	out := make([]DeptInfo, 0, len(ids))
	for _, id := range ids {
		var d model.Department
		err := s.db.WithContext(ctx).First(&d, "id = ?", id).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		chain, err := s.deptChain(ctx, id)
		if err != nil {
			return nil, err
		}
		out = append(out, DeptInfo{ID: d.ID, Name: d.Name, ParentDeps: chain})
	}
	return out, nil
}

// GroupMembersSplit returns a group's member ids split into users and
// departments (by member_type).
func (s *Service) GroupMembersSplit(ctx context.Context, groupID string) (userIDs, deptIDs []string, err error) {
	var ms []model.GroupMember
	if err = s.db.WithContext(ctx).Where("group_id = ?", groupID).Find(&ms).Error; err != nil {
		return nil, nil, err
	}
	userIDs, deptIDs = []string{}, []string{}
	for _, m := range ms {
		if m.MemberType == "department" {
			deptIDs = append(deptIDs, m.MemberID)
		} else {
			userIDs = append(userIDs, m.MemberID)
		}
	}
	return userIDs, deptIDs, nil
}

// UserGroups returns the groups a user belongs to: direct memberships plus
// groups whose member is any of the user's departments (transitive, so
// department-inherited groups are included).
func (s *Service) UserGroups(ctx context.Context, userID string) ([]GroupRef, error) {
	deptIDs, err := s.UserDeptIDs(ctx, userID)
	if err != nil {
		return nil, err
	}
	var ms []model.GroupMember
	q := s.db.WithContext(ctx).Where("member_id = ? AND member_type = ?", userID, "user")
	if len(deptIDs) > 0 {
		q = s.db.WithContext(ctx).Where(
			"(member_id = ? AND member_type = ?) OR (member_id IN ? AND member_type = ?)",
			userID, "user", deptIDs, "department")
	}
	if err := q.Find(&ms).Error; err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	gids := make([]string, 0, len(ms))
	for _, m := range ms {
		if !seen[m.GroupID] {
			seen[m.GroupID] = true
			gids = append(gids, m.GroupID)
		}
	}
	if len(gids) == 0 {
		return []GroupRef{}, nil
	}
	var groups []model.Group
	if err := s.db.WithContext(ctx).Where("id IN ?", gids).Find(&groups).Error; err != nil {
		return nil, err
	}
	out := make([]GroupRef, 0, len(groups))
	for _, g := range groups {
		out = append(out, GroupRef{ID: g.ID, Name: g.Name, Notes: g.Notes})
	}
	return out, nil
}

// userRoles returns the role ids bound to a user (Casbin g-rules in the shared
// casbin_rule table: ptype=g, v0=user, v1=role).
func (s *Service) userRoles(ctx context.Context, userID string) ([]string, error) {
	var roles []string
	if err := s.db.WithContext(ctx).Table("casbin_rule").
		Where("ptype = ? AND v0 = ?", "g", userID).Pluck("v1", &roles).Error; err != nil {
		return nil, err
	}
	return roles, nil
}

// UsersDetail returns the full record for each existing user id (missing ids
// omitted). All requestable fields are populated; the caller selects what it
// needs (cheap on the small Kowell directory).
func (s *Service) UsersDetail(ctx context.Context, ids []string) ([]UserFull, error) {
	out := make([]UserFull, 0, len(ids))
	for _, id := range ids {
		var u model.User
		err := s.db.WithContext(ctx).First(&u, "id = ?", id).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		roles, err := s.userRoles(ctx, id)
		if err != nil {
			return nil, err
		}
		parentDeps, err := s.UserParentDeps(ctx, id)
		if err != nil {
			return nil, err
		}
		groups, err := s.UserGroups(ctx, id)
		if err != nil {
			return nil, err
		}
		out = append(out, UserFull{
			ID: u.ID, Account: u.Account, Name: u.Name, Enabled: u.Enabled,
			Roles: roles, ParentDeps: parentDeps, Groups: groups,
		})
	}
	return out, nil
}

// SearchOrgFull reports which of the given users and departments fall under any
// scope department (transitive: the scope department or any descendant). A user
// matches if any of their direct departments is in scope.
func (s *Service) SearchOrgFull(ctx context.Context, userIDs, deptIDs, scope []string) (users, depts []string, err error) {
	users, depts = []string{}, []string{}
	if len(scope) == 0 {
		return users, depts, nil
	}
	scopeSet := map[string]bool{}
	for _, id := range scope {
		scopeSet[id] = true
	}
	// inScope: dept is the scope or a descendant of it (scope appears in its
	// root-first ancestor chain).
	inScope := func(deptID string) (bool, error) {
		chain, err := s.deptChain(ctx, deptID)
		if err != nil {
			return false, err
		}
		for _, c := range chain {
			if scopeSet[c.ID] {
				return true, nil
			}
		}
		return false, nil
	}
	for _, uid := range userIDs {
		dd, err := s.userDirectDeptIDs(ctx, uid)
		if err != nil {
			return nil, nil, err
		}
		for _, d := range dd {
			ok, err := inScope(d)
			if err != nil {
				return nil, nil, err
			}
			if ok {
				users = append(users, uid)
				break
			}
		}
	}
	for _, d := range deptIDs {
		ok, err := inScope(d)
		if err != nil {
			return nil, nil, err
		}
		if ok {
			depts = append(depts, d)
		}
	}
	return users, depts, nil
}
