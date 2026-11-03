// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.
package bknbackend

import (
	"context"
	"encoding/json"
	kb "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/knowledgebase"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"golang.org/x/sync/errgroup"
	"net/http"
	"net/url"
	"slices"
	"strings"
)

type boundResource struct{ Type, ID string }
type boundProperty struct {
	Name, Type  string
	MappedField *struct{ Name string } `json:"mapped_field"`
}
type boundMapping struct {
	Source struct{ Name string } `json:"source_property"`
	Target struct{ Name string } `json:"target_property"`
}

func sameProperties(actual []boundProperty, expected []*interfaces.DraftProperty) bool {
	if len(actual) != len(expected) {
		return false
	}
	fields := map[string]boundProperty{}
	for _, field := range actual {
		if field.Name == "" || fields[field.Name].Name != "" {
			return false
		}
		fields[field.Name] = field
	}
	for _, field := range expected {
		if field == nil {
			return false
		}
		value, ok := fields[field.Name]
		if !ok || value.Type != field.Type || value.MappedField == nil || value.MappedField.Name != field.MappedField {
			return false
		}
	}
	return true
}
func sameMappings(actual []boundMapping, expected []interfaces.DraftFieldMapping) bool {
	if len(actual) != len(expected) {
		return false
	}
	pairs := map[string]bool{}
	for _, mapping := range actual {
		key := mapping.Source.Name + "\x00" + mapping.Target.Name
		if pairs[key] {
			return false
		}
		pairs[key] = true
	}
	for _, mapping := range expected {
		if !pairs[mapping.SourceProperty+"\x00"+mapping.TargetProperty] {
			return false
		}
	}
	return true
}
func (bba *bknBackendAccess) readBoundSchema(ctx context.Context, record *interfaces.KBPublication, path string, out any) error {
	if !strings.HasSuffix(bba.baseUrl, "/v1") {
		return kb.ErrUnavailable
	}
	endpoint := strings.TrimSuffix(bba.baseUrl, "/v1") + "/in/v1/knowledge-networks/" + url.PathEscape(record.NetworkID) + path
	status, body, err := bba.httpClient.GetNoUnmarshal(ctx, endpoint, url.Values{"branch": {"main"}}, map[string]string{"x-account-id": record.Publisher.ID, "x-account-type": record.Publisher.Type})
	if err != nil || status >= 500 {
		return kb.ErrUnavailable
	}
	if status != http.StatusOK || json.Unmarshal(body, out) != nil {
		return kb.ErrPublishedBindingsChanged
	}
	return nil
}

// CheckPublishedBindings reads actual internal definitions, including physical
// fields and edge mappings. A ready proxy grant alone does not prove the model
// is unchanged: changing mapped fields can retain exactly the same grants.
func (bba *bknBackendAccess) CheckPublishedBindings(ctx context.Context, record *interfaces.KBPublication, draft *interfaces.Draft) error {
	if record == nil || draft == nil || record.NetworkID == "" || record.Publisher.ID == "" || (record.Publisher.Type != "user" && record.Publisher.Type != "app") {
		return kb.ErrPublishedBindingsChanged
	}
	group, ctx := errgroup.WithContext(ctx)
	group.SetLimit(4)
	for _, object := range draft.Items.ObjectTypes {
		if object == nil || !object.Adopted {
			continue
		}
		expected := object
		group.Go(func() error {
			var reply struct {
				Entries []struct {
					ID         string
					Source     *boundResource  `json:"data_source"`
					Keys       []string        `json:"primary_keys"`
					Properties []boundProperty `json:"data_properties"`
				}
			}
			if err := bba.readBoundSchema(ctx, record, "/object-types/"+url.PathEscape(expected.ID), &reply); err != nil {
				return err
			}
			if len(reply.Entries) != 1 {
				return kb.ErrPublishedBindingsChanged
			}
			actual := reply.Entries[0]
			keys := slices.Clone(actual.Keys)
			wanted := slices.Clone(expected.PrimaryKeys)
			slices.Sort(keys)
			slices.Sort(wanted)
			if actual.ID != expected.ID || actual.Source == nil || actual.Source.Type != "resource" || actual.Source.ID != expected.Table.ResourceID || !slices.Equal(keys, wanted) || !sameProperties(actual.Properties, expected.Properties) {
				return kb.ErrPublishedBindingsChanged
			}
			return nil
		})
	}
	for _, relation := range draft.Items.RelationTypes {
		if relation == nil || !relation.Adopted {
			continue
		}
		expected := relation
		group.Go(func() error {
			var reply struct {
				Entries []struct {
					ID, Type string
					Source   string          `json:"source_object_type_id"`
					Target   string          `json:"target_object_type_id"`
					Rules    json.RawMessage `json:"mapping_rules"`
				}
			}
			if err := bba.readBoundSchema(ctx, record, "/relation-types/"+url.PathEscape(expected.ID), &reply); err != nil {
				return err
			}
			if len(reply.Entries) != 1 {
				return kb.ErrPublishedBindingsChanged
			}
			actual := reply.Entries[0]
			if actual.ID != expected.ID || actual.Source != expected.Source || actual.Target != expected.Target {
				return kb.ErrPublishedBindingsChanged
			}
			if expected.LinkTable == nil {
				var mappings []boundMapping
				if actual.Type != "direct" || json.Unmarshal(actual.Rules, &mappings) != nil || !sameMappings(mappings, expected.Mapping) {
					return kb.ErrPublishedBindingsChanged
				}
				return nil
			}
			var mappings struct {
				Source     *boundResource  `json:"backing_data_source"`
				From       []boundMapping  `json:"source_mapping_rules"`
				To         []boundMapping  `json:"target_mapping_rules"`
				Properties []boundProperty `json:"properties"`
			}
			if actual.Type != "indirect" || json.Unmarshal(actual.Rules, &mappings) != nil || mappings.Source == nil || mappings.Source.Type != "resource" || mappings.Source.ID != expected.LinkTable.ResourceID || !sameMappings(mappings.From, expected.SourceMapping) || !sameMappings(mappings.To, expected.TargetMapping) || !sameProperties(mappings.Properties, expected.Properties) {
				return kb.ErrPublishedBindingsChanged
			}
			return nil
		})
	}
	return group.Wait()
}

var _ kb.PublishedModelCheck = (*bknBackendAccess)(nil)
