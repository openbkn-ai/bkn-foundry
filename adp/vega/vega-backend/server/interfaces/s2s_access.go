// Copyright 2026 openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

import "context"

// s2sInternalAccessKey 标记当前请求来自集群内部 S2S 调用（/in/ 内网端点）。
// 用于系统内部基础设施资源（internal_resource，如 BKN 概念 dataset）在被
// 内部服务代用户访问时默认放行 per-account 鉴权——这类资源从不授权给业务用户，
// 对其做 per-account view_detail 校验只会误拒。仅 /in/ 内网处理器设置该标记，
// 外网 /v1 端点不受影响。
type s2sInternalAccessKey struct{}

// WithS2SInternalAccess 在 ctx 上标记为内部 S2S 访问。仅由 /in/ 内网处理器调用。
func WithS2SInternalAccess(ctx context.Context) context.Context {
	return context.WithValue(ctx, s2sInternalAccessKey{}, true)
}

// IsS2SInternalAccess 判断 ctx 是否被标记为内部 S2S 访问。
func IsS2SInternalAccess(ctx context.Context) bool {
	v, ok := ctx.Value(s2sInternalAccessKey{}).(bool)
	return ok && v
}
