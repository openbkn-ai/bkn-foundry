// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"go.opentelemetry.io/otel/codes"

	perrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// Decisions that read the source tables: choosing an object type's identity, counted over the
// full table, and restoring a field the privacy rules excluded.

// identityCountTimeout bounds counting a chosen key within the request: gateways commonly cut
// requests at 60 seconds, and a cut request could no longer write the choice. A slower count
// gives up, and the choice is kept without counts.
const identityCountTimeout = 30 * time.Second

func findObjectType(items *interfaces.DraftItems, id string) *interfaces.DraftObjectType {
	for _, o := range items.ObjectTypes {
		if o.ID == id {
			return o
		}
	}
	return nil
}

func findRelationType(items *interfaces.DraftItems, id string) *interfaces.DraftRelationType {
	for _, r := range items.RelationTypes {
		if r.ID == id {
			return r
		}
	}
	return nil
}

// identityProperties returns the properties named, refusing an empty, repeated, unknown or
// non-key-typed one.
func identityProperties(ctx context.Context, objectType *interfaces.DraftObjectType,
	names []string) ([]*interfaces.DraftProperty, error) {
	invalid := httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Draft_InvalidIdentity)
	if len(names) == 0 {
		return nil, invalid
	}
	properties := []*interfaces.DraftProperty{}
	for i, name := range names {
		property := findProperty(objectType, name)
		if property == nil || !keyTypes[property.Type] || slices.Contains(names[:i], name) {
			return nil, invalid
		}
		properties = append(properties, property)
	}
	return properties, nil
}

func (s *draftService) ChooseIdentity(ctx context.Context, token string, projectID string, itemID string,
	identity interfaces.DraftIdentity, caller interfaces.AccountInfo) (*interfaces.Draft, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "ChooseIdentity")
	defer span.End()

	draft, err := s.readyDraft(ctx, token, projectID, identity.UpdateTime)
	if err != nil {
		span.SetStatus(codes.Error, "Draft not editable")
		return nil, err
	}
	objectType := findObjectType(&draft.Items, itemID)
	if objectType == nil {
		span.SetStatus(codes.Error, "Object type not found")
		return nil, httperrors.NewHTTPError(ctx, http.StatusNotFound, perrors.BknProject_Draft_ItemNotFound)
	}
	if objectType.Document != nil {
		if objectType.Document.Role != "entity" && !slices.Equal(identity.Properties, objectType.PrimaryKeys) {
			return nil, httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Draft_InvalidIdentity)
		}
		if objectType.Document.Role == "entity" {
			for _, name := range identity.Properties {
				if p := findProperty(objectType, name); p != nil && p.ReadOnly {
					return nil, httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Draft_InvalidIdentity)
				}
			}
		}
	}
	properties, err := identityProperties(ctx, objectType, identity.Properties)
	if err != nil {
		span.SetStatus(codes.Error, "Invalid identity")
		return nil, err
	}
	if !objectType.NeedsIdentity && slices.Equal(objectType.PrimaryKeys, identity.Properties) {
		span.SetStatus(codes.Ok, "")
		return draft, nil
	}

	before := s.setIdentity(ctx, draft, objectType, properties, caller)
	decisions := []*interfaces.DraftDecision{{
		Kind: interfaces.DRAFT_KIND_OBJECT_TYPES, ItemID: itemID, Action: interfaces.DRAFT_DECISION_IDENTITY,
		Before: before, After: objectType.PrimaryKeys,
	}}
	write := s.commit
	if objectType.Document != nil {
		write = func(ctx context.Context, d *interfaces.Draft, decisions []*interfaces.DraftDecision, c interfaces.AccountInfo) error {
			return s.commitDocumentEdit(ctx, d, decisions, c, objectType.Document)
		}
	}
	if err := write(ctx, draft, decisions, caller); err != nil {
		span.SetStatus(codes.Error, "Write draft failed")
		return nil, err
	}
	span.SetStatus(codes.Ok, "")
	return draft, nil
}

func (s *draftService) RestoreExcluded(ctx context.Context, token string, projectID string,
	restore interfaces.DraftRestore, caller interfaces.AccountInfo) (*interfaces.Draft, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "RestoreExcluded")
	defer span.End()

	draft, err := s.readyDraft(ctx, token, projectID, restore.UpdateTime)
	if err != nil {
		span.SetStatus(codes.Error, "Draft not editable")
		return nil, err
	}
	at := slices.IndexFunc(draft.Items.Excluded, func(e *interfaces.DraftExcludedField) bool {
		return e.ObjectType == restore.ObjectType && e.Field == restore.Field
	})
	objectType := findObjectType(&draft.Items, restore.ObjectType)
	if at < 0 || objectType == nil {
		span.SetStatus(codes.Error, "Excluded field not found")
		return nil, httperrors.NewHTTPError(ctx, http.StatusNotFound, perrors.BknProject_Draft_ItemNotFound)
	}
	excluded := draft.Items.Excluded[at]
	if excluded.Reason != excludedPrivacy {
		span.SetStatus(codes.Error, "Field not restorable")
		return nil, httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Draft_NotRestorable)
	}

	// The draft keeps no field types, so the field is read again; it may have changed since.
	tables, err := s.va.GetTables(ctx, caller, []string{objectType.Table.ResourceID})
	if err != nil {
		span.SetStatus(codes.Error, "Read table failed")
		return nil, internalError(ctx, "Read table failed", err)
	}
	var field *interfaces.VegaColumn
	for _, table := range tables {
		if c, ok := column(table, restore.Field); ok && bknTypes[c.Type] != "" {
			field = &c
		}
	}
	if field == nil {
		span.SetStatus(codes.Error, "Field not found")
		return nil, httperrors.NewHTTPError(ctx, http.StatusConflict, perrors.BknProject_Draft_FieldNotFound)
	}

	taken := map[string]bool{}
	for _, p := range objectType.Properties {
		taken[p.Name] = true
	}
	property := newProperty(*field, uniqueName(idFrom(field.Name, idPrefixProperty), taken), draftLanguage(draft))
	objectType.Properties = append(objectType.Properties, property)
	dropAliasesOfOtherProperties(objectType.Properties)
	draft.Items.Excluded = slices.Delete(draft.Items.Excluded, at, at+1)
	decisions := []*interfaces.DraftDecision{{
		Kind: interfaces.DRAFT_KIND_EXCLUDED, ItemID: objectType.ID, Action: interfaces.DRAFT_DECISION_RESTORE,
		Before: *excluded, After: property.Name,
	}}
	if err := s.commit(ctx, draft, decisions, caller); err != nil {
		span.SetStatus(codes.Error, "Write draft failed")
		return nil, err
	}
	span.SetStatus(codes.Ok, "")
	return draft, nil
}

// setIdentity makes properties the object type's identity, as a person chose them: a single field's
// key is counted over the full table as the caller, and the relations the identity allows are
// proposed. It returns the identity before.
func (s *draftService) setIdentity(ctx context.Context, draft *interfaces.Draft, objectType *interfaces.DraftObjectType,
	properties []*interfaces.DraftProperty, caller interfaces.AccountInfo) []string {
	names := make([]string, 0, len(properties))
	for _, p := range properties {
		names = append(names, p.Name)
	}
	if objectType.Document != nil {
		before := append([]string(nil), objectType.PrimaryKeys...)
		objectType.PrimaryKeys = names
		objectType.Key = interfaces.DraftKeyEvidence{Source: interfaces.DRAFT_KEY_CHOSEN}
		objectType.NeedsIdentity = false
		if objectType.DisplayKey == "" {
			objectType.DisplayKey = names[0]
		}
		return before
	}
	// One bound for every count of the choice: the key's, then those of the relations it allows.
	countCtx, cancel := context.WithTimeout(ctx, identityCountTimeout)
	defer cancel()
	timeout := min(s.setting.QueryTimeout, int(identityCountTimeout.Seconds()))
	c := &vegaCounter{va: s.va, account: caller, timeoutSeconds: timeout}
	key := interfaces.DraftKeyEvidence{Source: interfaces.DRAFT_KEY_CHOSEN, Reason: reasonCompositeKey}
	if len(properties) == 1 {
		// A person may keep a key with duplicates or nulls, e.g. to clean the data later; the
		// counts stay as found and the check on confirming the draft reports them.
		key = interfaces.DraftKeyEvidence{Source: interfaces.DRAFT_KEY_CHOSEN, Field: properties[0].MappedField}
		table := &interfaces.VegaTable{ID: objectType.Table.ResourceID}
		rows, distinct, nulls, err := c.keyStats(countCtx, table, properties[0].MappedField)
		if err != nil {
			key.Reason = fmt.Sprintf("%s: %v", reasonQueryFailed, err)
		} else {
			key.Computed, key.Rows, key.Distinct, key.Nulls = true, rows, distinct, nulls
		}
	}

	before := append([]string{}, objectType.PrimaryKeys...)
	objectType.PrimaryKeys = append([]string{}, names...)
	objectType.Key = key
	objectType.NeedsIdentity = false
	if objectType.DisplayKey == "" {
		objectType.DisplayKey = objectType.PrimaryKeys[0]
	}
	d := newDrafter(s.setting, c)
	d.namingLanguage = draftLanguage(draft)
	proposeRelations(countCtx, d, s.va, caller, &draft.Items, objectType)
	return before
}
