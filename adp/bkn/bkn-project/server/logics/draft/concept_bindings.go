// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"context"
	"net/http"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"

	perrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// A concept is bound to the data it is read from: a table of the project's catalogs (structured),
// or the dataset a knowledge-base extraction writes (unstructured). The two are never merged: one
// business object read from both needs a custom view that maps the properties of each, which the
// concepts page does not make. They may be related.

// conceptBindings tells the data the items of a draft are bound to.
type conceptBindings struct {
	// datasets are the datasets the draft's extractions prepared, by resource id, with the object
	// type each is for.
	datasets map[string]*interfaces.DraftDatasetSpec
	// tables are the tables of the project's catalogs by id, when they were listed: a resource the
	// network reads that is none of them is a dataset.
	tables map[string]bool
	// objectTypes are the bindings of the object types, by id, as they are found.
	objectTypes map[string]*interfaces.DraftConceptBinding
}

func newConceptBindings(items *interfaces.DraftItems, tables []*interfaces.VegaTable) *conceptBindings {
	b := &conceptBindings{datasets: map[string]*interfaces.DraftDatasetSpec{},
		objectTypes: map[string]*interfaces.DraftConceptBinding{}}
	for _, plan := range items.DatasetPlans {
		if plan == nil {
			continue
		}
		for i := range plan.Datasets {
			if spec := &plan.Datasets[i]; spec.ResourceID != "" {
				b.datasets[spec.ResourceID] = spec
			}
		}
	}
	if len(tables) > 0 {
		b.tables = map[string]bool{}
		for _, table := range tables {
			if table != nil {
				b.tables[table.ID] = true
			}
		}
	}
	return b
}

// datasetOf is the dataset prepared for the object type, nil when none was.
func (b *conceptBindings) datasetOf(objectTypeID string) *interfaces.DraftDatasetSpec {
	for _, spec := range b.datasets {
		if spec.ObjectTypeID == objectTypeID && spec.RelationTypeID == "" {
			return spec
		}
	}
	return nil
}

// objectType is the binding of an object type: a document's is a dataset; the network's is the
// resource it reads, a dataset when an extraction prepared it or it is no table of the catalogs;
// any other's is its table.
func (b *conceptBindings) objectType(ot *interfaces.DraftObjectType) *interfaces.DraftConceptBinding {
	binding := &interfaces.DraftConceptBinding{Kind: interfaces.CONCEPT_BINDING_TABLE, ID: ot.Table.ResourceID,
		Name: ot.Table.Name}
	switch {
	case ot.Document != nil:
		binding = &interfaces.DraftConceptBinding{Kind: interfaces.CONCEPT_BINDING_DATASET}
		if spec := b.datasetOf(ot.ID); spec != nil {
			binding.ID, binding.Name = spec.ResourceID, spec.Name
		}
	case ot.Existing && ot.Table.ResourceID != "":
		if b.datasets[ot.Table.ResourceID] != nil || (b.tables != nil && !b.tables[ot.Table.ResourceID]) {
			binding.Kind = interfaces.CONCEPT_BINDING_DATASET
		}
	}
	b.objectTypes[ot.ID] = binding
	return binding
}

// relationType is the binding of a relation type: its link table's, else its source object type's.
func (b *conceptBindings) relationType(rt *interfaces.DraftRelationType) *interfaces.DraftConceptBinding {
	if rt.Document == nil && rt.LinkTable != nil {
		return &interfaces.DraftConceptBinding{Kind: interfaces.CONCEPT_BINDING_TABLE, ID: rt.LinkTable.ResourceID,
			Name: rt.LinkTable.Name}
	}
	if binding := b.objectTypes[rt.Source]; binding != nil {
		copied := *binding
		return &copied
	}
	if rt.Document != nil {
		return &interfaces.DraftConceptBinding{Kind: interfaces.CONCEPT_BINDING_DATASET}
	}
	return nil
}

// crossBinding tells two concepts bound to different kinds of data, which are never merged. A
// concept with no binding, such as a requirement concept, merges into either.
func crossBinding(a, b *interfaces.DraftConcept) bool {
	return a != nil && b != nil && a.Binding != nil && b.Binding != nil && a.Binding.Kind != b.Binding.Kind
}

// crossBindingMerge refuses merging concepts bound to different kinds of data.
func crossBindingMerge(ctx context.Context) error {
	return httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Draft_CrossBindingMerge)
}
