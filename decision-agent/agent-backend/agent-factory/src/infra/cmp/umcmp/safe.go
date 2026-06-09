package umcmp

import (
	"context"
	"fmt"
	"net/url"

	"github.com/openbkn-ai/bkn-foundry/decision-agent/agent-backend/agent-factory/src/infra/cmp/umcmp/dto/umarg"
	"github.com/openbkn-ai/bkn-foundry/decision-agent/agent-backend/agent-factory/src/infra/cmp/umcmp/dto/umret"
	"github.com/openbkn-ai/bkn-foundry/decision-agent/agent-backend/agent-factory/src/infra/cmp/umcmp/umtypes"
	"github.com/openbkn-ai/bkn-foundry/decision-agent/agent-backend/agent-factory/src/infra/common/chelper/httphelper"
	"github.com/openbkn-ai/bkn-foundry/decision-agent/agent-backend/agent-factory/src/infra/common/cutil"
)

// bkn-safe directory cutover for DA's umcmp client (revertible, env-gated):
// DIRECTORY_PROVIDER=bkn-safe + BKN_SAFE_URL routes every umcmp method to
// bkn-safe's clean /api/safe/v1/directory/* instead of ISF user-management.
// Unset DIRECTORY_PROVIDER to revert (default = ISF). bkn-safe omits unknown ids
// (clean contract), so the ISF "remove-not-found and retry" loops are unneeded
// on this path.

// useBknSafe reports whether umcmp should talk to bkn-safe.
func (u *Um) useBknSafe() bool {
	return u.directoryProvider == "bkn-safe" && u.bknSafeURL != ""
}

func (u *Um) safeURL(path string) string {
	return u.bknSafeURL + "/api/safe/v1/directory" + path
}

// ---- bkn-safe wire types ----

type safeIDName struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type safeDeptRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"`
}

type safeUserFull struct {
	ID         string          `json:"id"`
	Account    string          `json:"account"`
	Name       string          `json:"name"`
	Enabled    bool            `json:"enabled"`
	Roles      []string        `json:"roles"`
	ParentDeps [][]safeDeptRef `json:"parent_deps"`
	Groups     []struct {
		ID    string `json:"id"`
		Name  string `json:"name"`
		Notes string `json:"notes"`
	} `json:"groups"`
}

// ---- bkn-safe HTTP helpers ----

func (u *Um) safeGet(ctx context.Context, path string, out any) error {
	c := httphelper.NewHTTPClient()
	resp, err := c.GetExpect2xx(ctx, u.safeURL(path))
	if err != nil {
		return err
	}
	return cutil.JSON().Unmarshal([]byte(resp), out)
}

func (u *Um) safePost(ctx context.Context, path string, body, out any) error {
	c := httphelper.NewHTTPClient()
	resp, err := c.PostJSONExpect2xx(ctx, u.safeURL(path), body)
	if err != nil {
		return err
	}
	if out == nil {
		return nil
	}
	return cutil.JSON().Unmarshal([]byte(resp), out)
}

func toObjectBaseInfos(chains [][]safeDeptRef) [][]ObjectBaseInfo {
	out := make([][]ObjectBaseInfo, 0, len(chains))
	for _, chain := range chains {
		row := make([]ObjectBaseInfo, 0, len(chain))
		for _, d := range chain {
			row = append(row, ObjectBaseInfo{ID: d.ID, Name: d.Name, Type: d.Type})
		}
		out = append(out, row)
	}
	return out
}

func (sf *safeUserFull) toUserInfo() *UserInfo {
	ui := &UserInfo{
		Id:         sf.ID,
		Name:       sf.Name,
		Enabled:    sf.Enabled,
		Roles:      sf.Roles,
		Account:    sf.Account,
		ParentDeps: toObjectBaseInfos(sf.ParentDeps),
	}
	for _, g := range sf.Groups {
		ui.Groups = append(ui.Groups, &GroupInfo{ID: g.ID, Name: g.Name, Notes: g.Notes})
	}
	return ui
}

// usersDetail fetches batch user records from bkn-safe.
func (u *Um) usersDetail(ctx context.Context, userIDs []string) ([]*safeUserFull, error) {
	var resp struct {
		Users []*safeUserFull `json:"users"`
	}
	if err := u.safePost(ctx, "/users-detail", map[string]any{"user_ids": userIDs}, &resp); err != nil {
		return nil, err
	}
	return resp.Users, nil
}

// ---- bkn-safe method variants (1:1 with the ISF methods) ----

func (u *Um) getOsnNamesSafe(ctx context.Context, args *umarg.GetOsnArgDto) (*umtypes.OsnInfoMapS, error) {
	body := map[string]any{
		"user_ids":       args.UserIDs,
		"department_ids": args.DepartmentIDs,
		"group_ids":      args.GroupIDs,
		"app_ids":        args.AppIDs,
	}
	var resp struct {
		UserNames       []safeIDName `json:"user_names"`
		AppNames        []safeIDName `json:"app_names"`
		DepartmentNames []safeIDName `json:"department_names"`
		GroupNames      []safeIDName `json:"group_names"`
	}
	if err := u.safePost(ctx, "/names", body, &resp); err != nil {
		return nil, err
	}
	ret := umtypes.NewOsnInfoMapS()
	for _, v := range resp.UserNames {
		ret.UserNameMap[v.ID] = v.Name
	}
	for _, v := range resp.DepartmentNames {
		ret.DepartmentNameMap[v.ID] = v.Name
	}
	for _, v := range resp.GroupNames {
		ret.GroupNameMap[v.ID] = v.Name
	}
	for _, v := range resp.AppNames {
		ret.AppNameMap[v.ID] = v.Name
	}
	return ret, nil
}

func (u *Um) getUserInfoSafe(ctx context.Context, args *umarg.GetUserInfoArgDto) (UserInfoMap, error) {
	users, err := u.usersDetail(ctx, args.UserIds)
	if err != nil {
		return nil, err
	}
	uim := make(UserInfoMap, len(users))
	for _, sf := range users {
		uim[sf.ID] = sf.toUserInfo()
	}
	return uim, nil
}

func (u *Um) getUserNameSafe(ctx context.Context, userID string) (name string, isNotFound bool, err error) {
	users, err := u.usersDetail(ctx, []string{userID})
	if err != nil {
		return "", false, err
	}
	if len(users) == 0 {
		return "", true, nil
	}
	return users[0].Name, false, nil
}

func (u *Um) getUserEnableStatusSafe(ctx context.Context, args *umarg.GetUserEnableStatusArgDto) (umret.UserEnabledMap, error) {
	users, err := u.usersDetail(ctx, args.UserIds)
	if err != nil {
		return nil, err
	}
	uem := make(umret.UserEnabledMap, len(users))
	for _, sf := range users {
		uem[sf.ID] = sf.Enabled
	}
	return uem, nil
}

func (u *Um) getUserInfoSingleSafe(ctx context.Context, args *umarg.GetUserInfoSingleArgDto) (info UserInfo, isNotFound bool, err error) {
	users, err := u.usersDetail(ctx, []string{args.UserID})
	if err != nil {
		return UserInfo{}, false, err
	}
	if len(users) == 0 {
		return UserInfo{}, true, nil
	}
	return *users[0].toUserInfo(), false, nil
}

func (u *Um) getUserDeptIDsSafe(ctx context.Context, userID string) ([]string, error) {
	var resp struct {
		DepartmentIDs []string `json:"department_ids"`
	}
	if err := u.safeGet(ctx, fmt.Sprintf("/users/%s/department-ids", url.PathEscape(userID)), &resp); err != nil {
		return nil, err
	}
	if resp.DepartmentIDs == nil {
		resp.DepartmentIDs = []string{}
	}
	return resp.DepartmentIDs, nil
}

func (u *Um) getDeptInfoMapSafe(ctx context.Context, args *umarg.GetDeptInfoArgDto) (map[string]*umtypes.DepartmentInfo, error) {
	var resp struct {
		Departments []struct {
			ID         string        `json:"id"`
			Name       string        `json:"name"`
			ParentDeps []safeDeptRef `json:"parent_deps"`
		} `json:"departments"`
	}
	if err := u.safePost(ctx, "/departments-detail", map[string]any{"department_ids": args.DeptIds}, &resp); err != nil {
		return nil, err
	}
	dim := make(map[string]*umtypes.DepartmentInfo, len(resp.Departments))
	for _, d := range resp.Departments {
		parents := make([]*umtypes.ParentDepInfo, 0, len(d.ParentDeps))
		for _, p := range d.ParentDeps {
			parents = append(parents, &umtypes.ParentDepInfo{ID: p.ID, Name: p.Name, Type: p.Type})
		}
		dim[d.ID] = &umtypes.DepartmentInfo{DepartmentId: d.ID, Name: d.Name, ParentDeps: parents}
	}
	return dim, nil
}

func (u *Um) getUserDeptSafe(ctx context.Context, userID string) ([][]ObjectBaseInfo, error) {
	users, err := u.usersDetail(ctx, []string{userID})
	if err != nil {
		return nil, err
	}
	if len(users) == 0 {
		return make([][]ObjectBaseInfo, 0), nil
	}
	return toObjectBaseInfos(users[0].ParentDeps), nil
}

func (u *Um) searchOrgSafe(ctx context.Context, args *umarg.SearchOrgArgDto) (*umret.SearchOrgRetDto, error) {
	body := map[string]any{
		"user_ids":       orEmpty(args.UserIDs),
		"department_ids": orEmpty(args.DepartmentIDs),
		"scope":          orEmpty(args.Scope),
	}
	ret := &umret.SearchOrgRetDto{}
	if err := u.safePost(ctx, "/search-org", body, ret); err != nil {
		return nil, err
	}
	return ret, nil
}

func (u *Um) getGroupMembersSafe(ctx context.Context, args *umarg.GetGroupMembersArgDto) (*umret.GetGroupMembersRetDto, error) {
	ret := umret.NewGetGroupMembersRetDto()
	seenU, seenD := map[string]bool{}, map[string]bool{}
	for _, gid := range args.GroupIDs {
		var resp struct {
			UserIDs       []string `json:"user_ids"`
			DepartmentIDs []string `json:"department_ids"`
		}
		if err := u.safeGet(ctx, fmt.Sprintf("/groups/%s/members", url.PathEscape(gid)), &resp); err != nil {
			return nil, err
		}
		for _, id := range resp.UserIDs {
			if !seenU[id] {
				seenU[id] = true
				ret.UserIDs = append(ret.UserIDs, id)
			}
		}
		for _, id := range resp.DepartmentIDs {
			if !seenD[id] {
				seenD[id] = true
				ret.DepartmentIDs = append(ret.DepartmentIDs, id)
			}
		}
	}
	return ret, nil
}

func orEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
