// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package logics

import (
	"context"
	"errors"
	"net/http"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"

	bknsdk "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/bkn-specification/bkn"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

// A save refused for its declarations, or a delete refused for what still refers to the definition,
// says why twice. error_details keeps the English text it always had, for the command line and the
// logs, and a caller that read it as a string reads it still. metadata carries the same refusal as
// records, so that a caller words it in its own language and finds the definition and the property
// the rule concerns: metadata.issues for a refused save, metadata.references for a refused delete.

// DeclarationIssueDetail is one refused declaration as the response carries it: the stable rule code,
// the definition (object_type_id, relation_type_id or action_type_id) and the data property it
// concerns, the params the message is built from, and the English message.
type DeclarationIssueDetail map[string]any

// definitionIDKey names the field that holds the ID of a definition of kind.
func definitionIDKey(kind string) string { return kind + "_id" }

// NewDeclarationIssue builds the detail of one rule broken by one definition of kind.
func NewDeclarationIssue(rule, kind, id, property string, params map[string]any, message string) DeclarationIssueDetail {
	issue := DeclarationIssueDetail{"rule": rule, "message": message}
	if id != "" {
		issue[definitionIDKey(kind)] = id
	}
	if property != "" {
		issue["property"] = property
	}
	if len(params) > 0 {
		issue["params"] = params
	}
	return issue
}

// ruleOther is the rule of an error that names none.
const ruleOther = "declaration_invalid"

// SpecIssueDetail turns an error of the specification library, found in the definition of kind and
// id, into the detail of one issue.
func SpecIssueDetail(kind, id string, err error) DeclarationIssueDetail {
	var specErr *bknsdk.SpecError
	if errors.As(err, &specErr) && specErr.Rule() != "" {
		return NewDeclarationIssue(specErr.Rule(), kind, id, specErr.Property(), specErr.Params(), err.Error())
	}
	return NewDeclarationIssue(ruleOther, kind, id, "", nil, err.Error())
}

// NetworkIssueDetail turns an issue spanning definitions into the detail of one issue: it is the
// issue of the definition that refers first, and involved lists all the definitions it concerns.
func NetworkIssueDetail(issue bknsdk.DeclarationIssue) DeclarationIssueDetail {
	if len(issue.Involved) == 0 {
		return SpecIssueDetail(interfaces.RESOURCE_TYPE_OBJECT_TYPE, "", issue.Err)
	}
	subject := issue.Involved[0]
	detail := SpecIssueDetail(subject.Kind, subject.ID, issue.Err)
	involved := make([]map[string]string, 0, len(issue.Involved))
	for _, ref := range issue.Involved {
		involved = append(involved, map[string]string{"kind": ref.Kind, "id": ref.ID})
	}
	detail["involved"] = involved
	return detail
}

// AliasConflictDetail turns an alias conflict of definitions of kind into the detail of one issue,
// on the definition whose alias it is.
func AliasConflictDetail(kind string, conflict *AliasConflictFound) DeclarationIssueDetail {
	return NewDeclarationIssue(bknsdk.RuleAliasConflict, kind, conflict.DefinitionID, "",
		map[string]any{"alias": conflict.Alias, "conflicts_with": conflict.ConflictsWith}, conflict.Message)
}

// DeclarationRefusal answers a save refused for its declarations with a 400 of code: message is the
// English text, issues the records.
func DeclarationRefusal(ctx context.Context, code, message string, issues ...DeclarationIssueDetail) error {
	refusal := httperrors.NewHTTPError(ctx, http.StatusBadRequest, code).WithErrorDetails(message)
	if len(issues) > 0 {
		refusal = refusal.WithMetadata(map[string]any{"issues": issues})
	}
	return refusal
}

// Reference kinds of a delete refusal: the declaration through which a definition still refers to
// the one being deleted.
const (
	ReferenceViaReplacedBy  = "replaced_by"
	ReferenceViaFallbackFor = "fallback_for"
	// ReferenceViaHandlingAction: an event class names the action type as its handling action.
	ReferenceViaHandlingAction = "handling_action"
)

// Reference is a definition that still refers to one a delete removes. Kind and ID name what is
// referred to, ReferrerKind and ReferrerID who refers to it, Via the declaration.
type Reference struct {
	Kind         string `json:"kind"`
	ID           string `json:"id"`
	ReferrerKind string `json:"referrer_kind"`
	ReferrerID   string `json:"referrer_id"`
	Via          string `json:"via"`
	Message      string `json:"message"`
}

// ReferencedRefusal answers a delete refused because definitions still refer to it with a 409 of
// code: error_details lists the English sentences, metadata.references the records.
func ReferencedRefusal(ctx context.Context, code string, references []Reference) error {
	sentences := make([]string, 0, len(references))
	for _, reference := range references {
		sentences = append(sentences, reference.Message)
	}
	return httperrors.NewHTTPError(ctx, http.StatusConflict, code).
		WithErrorDetails(map[string]any{"references": sentences}).
		WithMetadata(map[string]any{"references": references})
}
