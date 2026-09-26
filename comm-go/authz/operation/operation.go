// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

// Package operation defines the stable authorization-operation wire contract.
//
// An ID is serialized as its string value. Once published, that wire value is
// immutable: rename a Go identifier if necessary, but introduce a new ID for a
// new authorization meaning. Resource-specific catalogs, Community bundles,
// prerequisites, and parent-operation mappings intentionally do not live here;
// those are authorization decisions owned by bkn-safe and its resource owners.
package operation

// ID is an authorization operation wire value.
type ID string

// Wildcard is an authorization policy matcher, not an operation. It is valid
// in policy references such as a super-admin grant, but it is deliberately not
// returned by All and Known(Wildcard) remains false.
const Wildcard ID = "*"

const (
	Authorize                  ID = "authorize"
	Create                     ID = "create"
	CreateSystemAgent          ID = "create_system_agent"
	DataWrite                  ID = "data_write"
	Delete                     ID = "delete"
	Display                    ID = "display"
	Edit                       ID = "edit"
	Execute                    ID = "execute"
	FullBusinessAccess         ID = "full_business_access"
	Grant                      ID = "grant"
	Heartbeat                  ID = "heartbeat"
	Manage                     ID = "manage"
	Members                    ID = "members"
	ManageBuiltInAgent         ID = "mgnt_built_in_agent"
	Modify                     ID = "modify"
	Permissions                ID = "permissions"
	PublicAccess               ID = "public_access"
	Publish                    ID = "publish"
	PublishToAPI               ID = "publish_to_be_api_agent"
	PublishToDataFlow          ID = "publish_to_be_data_flow_agent"
	PublishToSkill             ID = "publish_to_be_skill_agent"
	PublishToWebSDK            ID = "publish_to_be_web_sdk_agent"
	QueryData                  ID = "query_data"
	Read                       ID = "read"
	Reconcile                  ID = "reconcile"
	ResetPassword              ID = "reset-password"
	ResourceManage             ID = "resource_manage"
	Revoke                     ID = "revoke"
	SeeTrajectoryAnalysis      ID = "see_trajectory_analysis"
	TaskManage                 ID = "task_manage"
	Toggle                     ID = "toggle"
	Unpublish                  ID = "unpublish"
	UnpublishOtherUserAgent    ID = "unpublish_other_user_agent"
	UnpublishOtherUserAgentTpl ID = "unpublish_other_user_agent_tpl"
	Use                        ID = "use"
	View                       ID = "view"
	ViewDetail                 ID = "view_detail"
	ViewSummary                ID = "view_summary"
	Write                      ID = "write"
)

var all = []ID{
	Authorize, Create, CreateSystemAgent, DataWrite, Delete, Display, Edit,
	Execute, FullBusinessAccess, Grant, Heartbeat, Manage, Members, ManageBuiltInAgent,
	Modify, Permissions, PublicAccess, Publish, PublishToAPI, PublishToDataFlow,
	PublishToSkill, PublishToWebSDK, QueryData, Read, Reconcile, ResetPassword, ResourceManage,
	Revoke, SeeTrajectoryAnalysis, TaskManage, Toggle, Unpublish,
	UnpublishOtherUserAgent, UnpublishOtherUserAgentTpl, Use, View, ViewDetail,
	ViewSummary, Write,
}

var known = func() map[ID]struct{} {
	values := make(map[ID]struct{}, len(all))
	for _, value := range all {
		values[value] = struct{}{}
	}
	return values
}()

// All returns all published authorization operation IDs. The returned slice is
// a copy and must not be used to derive a resource's grantable operation set.
func All() []ID {
	return append([]ID(nil), all...)
}

// Known reports whether value is a published authorization operation ID.
func Known(value string) bool {
	_, ok := known[ID(value)]
	return ok
}

// KnownReference reports whether value is either a published operation or the
// wildcard policy matcher. Use this for policy/seed references; use Known when
// validating an actual operation requested by an authorization decision.
func KnownReference(value string) bool {
	return value == string(Wildcard) || Known(value)
}
