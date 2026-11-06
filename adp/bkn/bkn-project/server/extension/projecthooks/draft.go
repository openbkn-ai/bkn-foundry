// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package projecthooks

import (
	"context"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// Why a revision of Ask AI is dropped, and why a relation is refused or not adopted: shared words.
const (
	AskDroppedUnchanged   = "unchanged"
	AskDroppedNotEditable = "not_editable"
	AskDroppedInvalid     = "invalid"
	AskDroppedTooLong     = "too_long"

	RelationNoMapping     = "no_mapping"
	ProposedEndNotAdopted = "end_not_adopted"
)

// AskRevisionOutput is a revision as the Agent gives it: Value for a name, comment or display key,
// Properties for an identity, Property for a property, Source, Target and Mapping for ends.
type AskRevisionOutput struct {
	DocumentMapping *interfaces.DraftDocumentMapping     `json:"document_mapping"`
	Cardinality     string                               `json:"cardinality"`
	Field           string                               `json:"field"`
	Value           string                               `json:"value"`
	Properties      []string                             `json:"properties"`
	Property        *interfaces.DraftAskPropertyRevision `json:"property"`
	Source          string                               `json:"source_object_type_id"`
	Target          string                               `json:"target_object_type_id"`
	Mapping         []interfaces.DraftFieldMapping       `json:"mapping"`
	Aliases         []string                             `json:"aliases"`
	Reason          string                               `json:"reason"`
}

// RelationReviser checks one revision of a relation the hooks own; the string is why it is dropped.
type RelationReviser func(out AskRevisionOutput) (*interfaces.DraftAskRevision, string)

// CommitWrite writes the draft in the guard of the caller; readAt is the update time read.
type CommitWrite func(ctx context.Context, draft *interfaces.Draft, readAt int64) (bool, error)

// BusinessKeyCount is how a table's field counts as a key; Refusal, when set, says why not counted.
type BusinessKeyCount struct {
	Rows, Distinct, Nulls, Blanks int64
	Refusal                       string
}

// Why a business key is not counted.
const (
	KeyInvalid     = "invalid"
	KeyUnavailable = "unavailable"
)

// Host is what the draft service gives the hooks: its stateful steps.
type Host interface {
	Snapshot(ot *interfaces.DraftObjectType) interfaces.DraftObjectTypeSnapshot
	SnapshotsEqual(a, b interfaces.DraftObjectTypeSnapshot) bool
	RelationSnapshot(rt *interfaces.DraftRelationType) interfaces.DraftRelationTypeSnapshot
	SameJoin(a, b interfaces.DraftRelationTypeSnapshot) bool
	ApplyEdit(ctx context.Context, items *interfaces.DraftItems, kind, id string, edit interfaces.DraftItemEdit) ([]*interfaces.DraftDecision, error)
	CommitUsing(ctx context.Context, draft *interfaces.Draft, decisions []*interfaces.DraftDecision, caller interfaces.AccountInfo, write CommitWrite) error
	InternalError(ctx context.Context, operation string, err error) error
	InvalidRelation(ctx context.Context, details map[string]any) error
	NameOrComment(ctx context.Context, field, value, name, description string) (*interfaces.DraftAskRevision, string)
	CountBusinessKey(ctx context.Context, caller interfaces.AccountInfo, resourceID, mappedField, fieldType string) BusinessKeyCount
	UnarchivedProject(ctx context.Context, token, projectID string) (*interfaces.ProjectEntry, error)
	CheckCompiles(items *interfaces.DraftItems, evidence *interfaces.DraftEvidenceExport) error
	DraftAccess() interfaces.DraftAccess
}

// AskObject is how Ask AI reads a document object type: the form checked, the examples verified by
// property name, and the revisions it may offer (nil keeps those of any object type).
type AskObject struct {
	Working  *interfaces.DraftObjectType
	Examples map[string][]string
	Editable []string
}

// AskRelation is how Ask AI reads a document relation type: the form checked, the revisions it may
// offer (nil keeps those of any relation type) and the check of each revision (nil checks it as any).
type AskRelation struct {
	Working  *interfaces.DraftRelationType
	Editable []string
	Revise   RelationReviser
}

// Drafts is what document items add to a draft. The community default owns no item.
//
// The bool of a Save or Commit says the hook handled it; otherwise the draft goes on as for any item.
type Drafts interface {
	// CheckRead refuses reading a draft whose document sources the caller may not read.
	CheckRead(ctx context.Context, token string, draft *interfaces.Draft) error
	// ProjectRead refreshes what a read shows of document items: fact and dual source states.
	ProjectRead(items *interfaces.DraftItems)

	SnapshotObject(ot *interfaces.DraftObjectType, into *interfaces.DraftObjectTypeSnapshot)
	SnapshotRelation(rt *interfaces.DraftRelationType, into *interfaces.DraftRelationTypeSnapshot)

	SaveObject(ctx context.Context, h Host, token string, draft *interfaces.Draft, ot *interfaces.DraftObjectType, save interfaces.DraftObjectTypeSave, caller interfaces.AccountInfo) (*interfaces.Draft, bool, error)
	CreateRelation(ctx context.Context, h Host, token string, draft *interfaces.Draft, rt *interfaces.DraftRelationType, save interfaces.DraftRelationTypeSave, caller interfaces.AccountInfo) (*interfaces.Draft, bool, error)
	SaveRelation(ctx context.Context, h Host, token string, draft *interfaces.Draft, rt *interfaces.DraftRelationType, save interfaces.DraftRelationTypeSave, caller interfaces.AccountInfo) (*interfaces.Draft, bool, error)
	// CheckRelationDelete refuses deleting a relation a document item depends on.
	CheckRelationDelete(ctx context.Context, items *interfaces.DraftItems, relationID string) error
	CommitEdit(ctx context.Context, h Host, draft *interfaces.Draft, decisions []*interfaces.DraftDecision, caller interfaces.AccountInfo, model *interfaces.DraftDocumentModel) (bool, error)

	CheckIdentity(ctx context.Context, items *interfaces.DraftItems, ot *interfaces.DraftObjectType, properties []string) error
	SetIdentity(ot *interfaces.DraftObjectType, names []string) ([]string, bool)

	// AskAgentID is the Agent that answers questions about items of the draft; empty keeps the one
	// of the open core.
	AskAgentID(draft *interfaces.Draft) string
	// AskObject checks the form before asking.
	AskObject(ctx context.Context, h Host, token string, draft *interfaces.Draft, original, working *interfaces.DraftObjectType) (AskObject, error)
	// AskObjectContext is what the Agent reads of a document object type beside its fields, and the
	// extraction instruction of each property by name.
	AskObjectContext(ot *interfaces.DraftObjectType) (any, map[string]string)
	AskRelationContext(rt *interfaces.DraftRelationType) (any, *interfaces.DraftDocumentMapping)
	AskRelation(ctx context.Context, h Host, token string, draft *interfaces.Draft, relation, working *interfaces.DraftRelationType) (AskRelation, error)
	// AskNewRelation shapes a relation not saved yet; a zero Working means it is not a document relation.
	AskNewRelation(ctx context.Context, h Host, token string, draft *interfaces.Draft, req interfaces.DraftRelationAskRequest) (AskRelation, error)
	AskOfferable(ot *interfaces.DraftObjectType) bool
	// RevisionSchema adds to the JSON Schema of a revision.
	RevisionSchema(limit int) map[string]any

	WorkingObject(base *interfaces.DraftObjectType, working *interfaces.DraftObjectTypeSave)
	WorkingProperty(base *interfaces.DraftObjectType, p *interfaces.DraftProperty, saved interfaces.DraftDataPropertySave)
	WorkingRelation(base *interfaces.DraftRelationType, working *interfaces.DraftRelationTypeSave) bool
	IdentityRevisionAllowed(base *interfaces.DraftObjectType, keys []string, relations []*interfaces.DraftRelationType) bool
	InstructionRevision(base *interfaces.DraftObjectType, p *interfaces.DraftProperty, proposed *string) (*string, string)

	// CheckConfirm refuses confirming a draft whose document items are not ready, before the checks
	// of any draft. It skips nil relations, which the draft refuses after it.
	CheckConfirm(ctx context.Context, draft *interfaces.Draft) error
	// PrepareConfirm runs after the checks of any draft: it checks source fields and business
	// identities and prepares the Datasets of document items.
	PrepareConfirm(ctx context.Context, h Host, token string, entry *interfaces.ProjectEntry, draft *interfaces.Draft, caller interfaces.AccountInfo) (*interfaces.Draft, error)
	DatasetProgress(ctx context.Context, h Host, draft *interfaces.Draft)

	// ObjectNeedsRepair holds back adopting an object type.
	ObjectNeedsRepair(items *interfaces.DraftItems, ot *interfaces.DraftObjectType) bool
	CheckAdoptRelation(ctx context.Context, items *interfaces.DraftItems, rt *interfaces.DraftRelationType) error
	// ExportItems is the items as they compile; evidence says how document evidence is
	// published, nil for the projection of earlier confirmations.
	ExportItems(items *interfaces.DraftItems, evidence *interfaces.DraftEvidenceExport) (*interfaces.DraftItems, error)
	// ConfirmEvidence is how confirming the draft now publishes its document evidence, from the
	// project's source selection; nil without document items.
	ConfirmEvidence(ctx context.Context, h Host, draft *interfaces.Draft) (*interfaces.DraftEvidenceExport, error)
	// DefinitionRules is what a commit of the model carries beside its files.
	DefinitionRules(ctx context.Context, draft *interfaces.Draft, lineageAvailable bool) ([]byte, error)
}

// FindObjectType, FindRelationType, FindProperty, ObjectTypeAdopted and DraftLanguage are the pure
// look-ups both sides use.
func FindObjectType(items *interfaces.DraftItems, id string) *interfaces.DraftObjectType {
	for _, o := range items.ObjectTypes {
		if o.ID == id {
			return o
		}
	}
	return nil
}

func FindRelationType(items *interfaces.DraftItems, id string) *interfaces.DraftRelationType {
	for _, r := range items.RelationTypes {
		if r.ID == id {
			return r
		}
	}
	return nil
}

func FindProperty(objectType *interfaces.DraftObjectType, name string) *interfaces.DraftProperty {
	for _, p := range objectType.Properties {
		if p.Name == name {
			return p
		}
	}
	return nil
}

func ObjectTypeAdopted(items *interfaces.DraftItems, id string) bool {
	for _, o := range items.ObjectTypes {
		if o.ID == id {
			return o.Adopted
		}
	}
	return false
}

func DraftLanguage(draft *interfaces.Draft) string {
	if draft.Items.Inputs == nil || draft.Items.Inputs.NamingLanguage == "" {
		return interfaces.NAMING_LANGUAGE_ZH_CN
	}
	return draft.Items.Inputs.NamingLanguage
}
