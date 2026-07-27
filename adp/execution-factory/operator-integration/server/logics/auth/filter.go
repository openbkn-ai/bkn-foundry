package auth

import (
	"context"

	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/infra/common"
	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/interfaces"
)

// FilterViewableIDs 在公开面按查看权限过滤资源ID，内部面原样返回。
//
// 用于 /operator/names、/tool-box/names、/skills/names 这类批量取名接口：它们本就对不存在的
// ID 静默略过，因此无权限的 ID 同样略过而不是整体 403——否则 403 与 200 的差异本身就成了
// 存在性探测信道。
//
// 判定复用 ResourceListIDs：类型级授权（含超管）会直接返回 ResourceIDAll，一次调用即可覆盖
// 通配场景，无需按 ID 逐个回源。
//
// 刻意只按 view 判定，不含 execute/public_access：names 服务于管理态（对象级授权页的名称
// 回显），口径与管理态列表一致——skill 列表、工具箱列表、算子列表都只按 view 过滤
// （见 logics/skill/registry.go:945、logics/toolbox/toolbox.go:199、logics/operator/query.go:294）。
// 市场态列表走的是另一套口径（只按 public_access，见 logics/operator/market.go:266），
// 因此若将来市场页要复用批量取名，应另开按 public_access 过滤的入口，而不是放宽这里。
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
