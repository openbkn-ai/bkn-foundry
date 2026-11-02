// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbdataset

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"slices"

	kb "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/knowledgebase"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

var ErrStalePreparation = errors.New("document model changed during Dataset preparation")

// ModelFingerprint excludes physical bindings and preparation history. Changing
// an instruction, identity, source version or relationship invalidates the plan.
func ModelFingerprint(draft *interfaces.Draft) (string, error) {
	if draft == nil || draft.ProjectID == "" {
		return "", ErrInvalidLayout
	}
	raw, err := json.Marshal(draft.Items)
	if err != nil {
		return "", err
	}
	var items interfaces.DraftItems
	if err = json.Unmarshal(raw, &items); err != nil {
		return "", err
	}
	objects := []*interfaces.DraftObjectType{}
	for _, object := range items.ObjectTypes {
		if object == nil {
			return "", ErrInvalidLayout
		}
		if !object.Adopted {
			continue
		}
		if object.Document != nil {
			object.Table = interfaces.DraftTable{}
			for _, property := range object.Properties {
				if property == nil {
					return "", ErrInvalidLayout
				}
				property.MappedField = ""
			}
		}
		objects = append(objects, object)
	}
	relations := []*interfaces.DraftRelationType{}
	for _, relation := range items.RelationTypes {
		if relation == nil {
			return "", ErrInvalidLayout
		}
		if relation.Document != nil {
			relation.LinkTable = nil
			relation.SourceMapping = nil
			relation.TargetMapping = nil
			relation.Properties = nil
		}
		if relation.Adopted {
			relations = append(relations, relation)
		}
	}
	slices.SortFunc(objects, func(a, b *interfaces.DraftObjectType) int { return compare(a.ID, b.ID) })
	slices.SortFunc(relations, func(a, b *interfaces.DraftRelationType) int { return compare(a.ID, b.ID) })
	encoded, err := json.Marshal(struct {
		Version   int                             `json:"version"`
		ProjectID string                          `json:"project_id"`
		Objects   []*interfaces.DraftObjectType   `json:"objects"`
		Relations []*interfaces.DraftRelationType `json:"relations"`
	}{1, draft.ProjectID, objects, relations})
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(encoded)
	return hex.EncodeToString(hash[:]), nil
}

func compare(a, b string) int {
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}

// NewPlan assigns every resource identity before any remote write. The target
// generation is persisted by core and later associated with its release; retries
// never derive a new generation from the current time.
func NewPlan(draft *interfaces.Draft, generation, catalog string, owner interfaces.AccountInfo) (*interfaces.DraftDatasetPlan, error) {
	if generation == "" || catalog == "" || owner.ID == "" || (owner.Type != "user" && owner.Type != "app") {
		return nil, ErrInvalidLayout
	}
	fingerprint, err := ModelFingerprint(draft)
	if err != nil {
		return nil, err
	}
	plan := &interfaces.DraftDatasetPlan{Owner: owner, ID: generation, ModelFingerprint: fingerprint, State: "pending", Datasets: []interfaces.DraftDatasetSpec{}}
	seen := map[string]bool{}
	for _, object := range draft.Items.ObjectTypes {
		if !object.Adopted || object.Document == nil {
			continue
		}
		if object.ID == "" || seen[object.ID] || object.Document.SelectionID == "" || object.Document.SelectionRevision < 1 {
			return nil, ErrInvalidLayout
		}
		if plan.SelectionID == "" {
			plan.SelectionID = object.Document.SelectionID
			plan.SelectionRevision = object.Document.SelectionRevision
		}
		if plan.SelectionID != object.Document.SelectionID || plan.SelectionRevision != object.Document.SelectionRevision {
			return nil, ErrInvalidLayout
		}
		seen[object.ID] = true
		layout, err := BuildLayout(object)
		if err != nil {
			return nil, err
		}
		seed, _ := json.Marshal([]string{"bkn-document-dataset-v1", draft.ProjectID, generation, object.ID})
		identity := sha256.Sum256(seed)
		plan.Datasets = append(plan.Datasets, interfaces.DraftDatasetSpec{Owner: owner, ProjectID: draft.ProjectID, GenerationID: generation, ObjectTypeID: object.ID, ResourceID: hex.EncodeToString(identity[:16]), CatalogID: catalog, Name: object.Name, Layout: *layout})
	}
	seenRelations := map[string]bool{}
	for _, relation := range draft.Items.RelationTypes {
		if !relationStorage(relation) {
			continue
		}
		if relation.ID == "" || seenRelations[relation.ID] {
			return nil, ErrInvalidLayout
		}
		seenRelations[relation.ID] = true
		layout, err := relationLayout(draft.Items, relation)
		if err != nil {
			return nil, err
		}
		seed, _ := json.Marshal([]string{"bkn-document-relation-dataset-v1", draft.ProjectID, generation, relation.ID})
		identity := sha256.Sum256(seed)
		plan.Datasets = append(plan.Datasets, interfaces.DraftDatasetSpec{Owner: owner, ProjectID: draft.ProjectID, GenerationID: generation, RelationTypeID: relation.ID, ResourceID: hex.EncodeToString(identity[:16]), CatalogID: catalog, Name: relation.Name, Layout: *layout})
	}
	slices.SortFunc(plan.Datasets, func(a, b interfaces.DraftDatasetSpec) int {
		return compare(a.ObjectTypeID+"/"+a.RelationTypeID, b.ObjectTypeID+"/"+b.RelationTypeID)
	})
	if len(plan.Datasets) == 0 {
		return nil, ErrInvalidLayout
	}
	return plan, nil
}

// BindPreview returns an independent model for the existing BKN compiler. It
// exposes no half-bound definitions when a remote operation or final CAS fails.
func BindPreview(draft *interfaces.Draft, plan *interfaces.DraftDatasetPlan) (*interfaces.Draft, error) {
	fingerprint, err := ModelFingerprint(draft)
	if err != nil {
		return nil, err
	}
	if plan == nil || fingerprint != plan.ModelFingerprint {
		return nil, ErrStalePreparation
	}
	if len(plan.Datasets) == 0 {
		return nil, ErrInvalidLayout
	}
	expected, err := NewPlan(draft, plan.ID, plan.Datasets[0].CatalogID, plan.Owner)
	if err != nil {
		return nil, err
	}
	if expected.SelectionID != plan.SelectionID || expected.SelectionRevision != plan.SelectionRevision || len(expected.Datasets) != len(plan.Datasets) {
		return nil, ErrInvalidLayout
	}
	for i, spec := range expected.Datasets {
		actual := plan.Datasets[i]
		actual.Ready = false
		if !reflect.DeepEqual(spec, actual) {
			return nil, ErrInvalidLayout
		}
	}
	raw, err := json.Marshal(draft)
	if err != nil {
		return nil, err
	}
	bound := &interfaces.Draft{}
	if err = json.Unmarshal(raw, bound); err != nil {
		return nil, err
	}
	specs := map[string]interfaces.DraftDatasetSpec{}
	relations := map[string]interfaces.DraftDatasetSpec{}
	for _, spec := range plan.Datasets {
		if spec.RelationTypeID != "" {
			relations[spec.RelationTypeID] = spec
		} else {
			specs[spec.ObjectTypeID] = spec
		}
	}

	for _, object := range bound.Items.ObjectTypes {
		if !object.Adopted || object.Document == nil {
			continue
		}
		spec, ok := specs[object.ID]
		if !ok || spec.ProjectID != draft.ProjectID || spec.GenerationID != plan.ID || spec.CatalogID == "" || spec.ResourceID == "" {
			return nil, ErrInvalidLayout
		}
		object.Table = interfaces.DraftTable{ResourceID: spec.ResourceID, CatalogID: spec.CatalogID, Name: spec.Name}
		for _, property := range object.Properties {
			mapped, ok := spec.Layout.Mapping[property.Name]
			if !ok {
				return nil, ErrInvalidLayout
			}
			property.MappedField = mapped
		}
	}
	for _, relation := range bound.Items.RelationTypes {
		if !relationStorage(relation) {
			continue
		}
		spec, ok := relations[relation.ID]
		if !ok {
			return nil, ErrInvalidLayout
		}
		if err := bindRelation(&bound.Items, relation, spec); err != nil {
			return nil, err
		}
	}
	return bound, nil
}

// Prepare persists intent and each verified resource through the caller's atomic
// source/draft guard. Validate checks current operator permissions and process
// state before each remote operation and again before the final binding.
func Prepare(ctx context.Context, draft *interfaces.Draft, plan *interfaces.DraftDatasetPlan, storage kb.Datasets,
	save func(context.Context, *interfaces.Draft) error, validate func(context.Context, *interfaces.Draft) error) error {
	if storage == nil || save == nil || validate == nil || draft == nil || plan == nil {
		return ErrInvalidLayout
	}
	check := func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := validate(ctx, draft); err != nil {
			return err
		}
		fingerprint, err := ModelFingerprint(draft)
		if err != nil {
			return err
		}
		if fingerprint != plan.ModelFingerprint {
			return ErrStalePreparation
		}
		return nil
	}
	if err := check(); err != nil {
		return err
	}
	if _, err := BindPreview(draft, plan); err != nil {
		return err
	}
	attached := false
	for _, existing := range draft.Items.DatasetPlans {
		if existing == nil {
			return ErrInvalidLayout
		}
		if existing.ID == plan.ID {
			if existing != plan {
				return ErrInvalidLayout
			}
			attached = true
		}
	}
	if !attached {
		draft.Items.DatasetPlans = append(draft.Items.DatasetPlans, plan)
	}
	plan.State = "preparing"
	if err := save(ctx, draft); err != nil {
		return err
	}
	fail := func(err error) error {
		plan.State = "failed"
		// A concurrent edit/archive causes the caller's CAS to reject this record.
		_ = save(context.WithoutCancel(ctx), draft)
		return err
	}
	for index := range plan.Datasets {
		if err := check(); err != nil {
			return fail(err)
		}
		if err := storage.Ensure(ctx, plan.Datasets[index]); err != nil {
			return fail(err)
		}
		plan.Datasets[index].Ready = true
		if err := save(ctx, draft); err != nil {
			return err
		}
	}
	if err := check(); err != nil {
		return fail(err)
	}
	bound, err := BindPreview(draft, plan)
	if err != nil {
		return fail(err)
	}
	for _, candidate := range bound.Items.DatasetPlans {
		if candidate == nil {
			return ErrInvalidLayout
		}
		if candidate.ID == plan.ID {
			candidate.State = "ready"
		}
	}
	if err := save(ctx, bound); err != nil {
		return err
	}
	plan.State = "ready"
	*draft = *bound
	return nil
}
