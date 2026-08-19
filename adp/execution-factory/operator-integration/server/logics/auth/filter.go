package auth

import (
	"context"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/common"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
)

// FilterViewableIDs filters resource IDs according to viewing permissions on the public side, and returns them unchanged on the internal side.
//
// Used for batch naming interfaces such as /operator/names, /tool-box/names, /skills/names: they are inherently suitable for non-existent.
// IDs are silently skipped, so unauthorized IDs are also skipped rather than a total 403 - otherwise the difference between 403 and 200 becomes itself.
// Presence detection channel.
//
// Determine reuse of ResourceListIDs: Type-level authorization (including super management) will directly return ResourceIDAll, which can be overwritten with one call.
// Wildcard scenarios eliminate the need to return to the source one by one by ID.
//
// Deliberately only judge by view, excluding execute/public_access: names serving the management state (names of object-level authorization pages.
// echo), the semantics is the same as the management list - the skill list, toolbox list, and operator list are only filtered by view.
// (See logics/skill/registry.go:945, logics/toolbox/toolbox.go:199, logics/operator/query.go:294).
// The market status list adopts another set of standards (only public_access, see logics/operator/market.go:266),
// Therefore, if the market page wants to reuse batch naming in the future, it should open another entrance filtered by public_access instead of relaxing here.
func FilterViewableIDs(ctx context.Context, authService interfaces.IAuthorizationService, userID string,
	ids []string, resourceType interfaces.AuthResourceType) ([]string, error) {
	if !common.IsPublicAPIFromCtx(ctx) || len(ids) == 0 {
		return ids, nil
	}
	accessor, err := authService.GetAccessor(ctx, userID)
	if err != nil {
		return nil, err
	}
	authorizedIDs, err := authService.ResourceListIDs(ctx, accessor, resourceType, interfaces.AuthOperationTypeView)
	if err != nil {
		return nil, err
	}
	allowed := make(map[string]struct{}, len(authorizedIDs))
	for _, id := range authorizedIDs {
		if id == interfaces.ResourceIDAll {
			return ids, nil
		}
		allowed[id] = struct{}{}
	}
	filtered := make([]string, 0, len(ids))
	for _, id := range ids {
		if _, ok := allowed[id]; ok {
			filtered = append(filtered, id)
		}
	}
	return filtered, nil
}
