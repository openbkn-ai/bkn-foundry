// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package logics

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"

	oerrors "ontology-query/errors"
	"ontology-query/interfaces"
)

func TestResolveTypeEdgeDirection(t *testing.T) {
	orderUser := interfaces.RelationType{RTID: "rel_order_user", SourceObjectTypeID: "order", TargetObjectTypeID: "user"}
	replyOf := interfaces.RelationType{RTID: "comment_replyof_comment", SourceObjectTypeID: "comment", TargetObjectTypeID: "comment"}
	edge := func(relationType interfaces.RelationType, from, to, direction string) interfaces.TypeEdge {
		return interfaces.TypeEdge{RelationTypeId: relationType.RTID, SourceObjectTypeId: from,
			TargetObjectTypeId: to, Direction: direction}
	}

	tests := []struct {
		name          string
		edge          interfaces.TypeEdge
		relationType  interfaces.RelationType
		wantDirection string
		wantParts     []string
	}{
		{name: "along the definition is forward", edge: edge(orderUser, "order", "user", ""),
			relationType: orderUser, wantDirection: interfaces.DIRECTION_FORWARD},
		{name: "against the definition is backward", edge: edge(orderUser, "user", "order", ""),
			relationType: orderUser, wantDirection: interfaces.DIRECTION_BACKWARD},
		{name: "a declared direction that agrees is kept", edge: edge(orderUser, "user", "order", interfaces.DIRECTION_BACKWARD),
			relationType: orderUser, wantDirection: interfaces.DIRECTION_BACKWARD},
		{name: "self relation defaults to forward", edge: edge(replyOf, "comment", "comment", ""),
			relationType: replyOf, wantDirection: interfaces.DIRECTION_FORWARD},
		{name: "self relation walks backward only when declared", edge: edge(replyOf, "comment", "comment", interfaces.DIRECTION_BACKWARD),
			relationType: replyOf, wantDirection: interfaces.DIRECTION_BACKWARD},
		{
			name: "a declared direction that contradicts the endpoints", edge: edge(orderUser, "order", "user", interfaces.DIRECTION_BACKWARD),
			relationType: orderUser,
			wantParts: []string{"第 1 条边 rel_order_user 指定 direction=backward",
				"要求路径为 user → order，当前为 order → user", "关系类定义为 order → user"},
		},
		{
			name: "endpoints that are not the relation's ends", edge: edge(orderUser, "user_address", "user", ""),
			relationType: orderUser,
			wantParts: []string{"第 1 条边 rel_order_user 连不上路径节点",
				"路径要求 user_address → user，而该关系类定义为 order → user",
				"正向遍历时相邻节点应依次为 order、user，反向遍历时为 user、order"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := rest.WithLanguage(context.Background(), rest.SimplifiedChinese)
			direction, err := ResolveTypeEdgeDirection(ctx, 0, tt.edge, tt.relationType)
			if len(tt.wantParts) == 0 {
				if err != nil || direction != tt.wantDirection {
					t.Fatalf("ResolveTypeEdgeDirection() = %q, %v; want %q", direction, err, tt.wantDirection)
				}
				return
			}
			httpErr, ok := err.(*rest.HTTPError)
			if !ok {
				t.Fatalf("error = %T %v, want *rest.HTTPError", err, err)
			}
			if httpErr.HTTPCode != http.StatusBadRequest ||
				httpErr.BaseError.ErrorCode != oerrors.OntologyQuery_KnowledgeNetwork_InvalidParameter_TypePath {
				t.Fatalf("error = %d %q, want 400 TypePath", httpErr.HTTPCode, httpErr.BaseError.ErrorCode)
			}
			details, _ := httpErr.BaseError.ErrorDetails.(string)
			for _, part := range tt.wantParts {
				if !strings.Contains(details, part) {
					t.Errorf("error details = %q, want it to contain %q", details, part)
				}
			}
		})
	}
}
