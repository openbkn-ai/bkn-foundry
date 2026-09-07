// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/openbkn-ai/licverify"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/extension/permdata"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
	"github.com/openbkn-ai/bkn-foundry/comm-go/entitlement"
	"github.com/openbkn-ai/bkn-foundry/comm-go/propertyaccess"
)

type recordingPropertyResolver struct {
	calls int
	err   error
}

func (resolver *recordingPropertyResolver) Resolve(_ context.Context, request permdata.Request) (permdata.Resolution, error) {
	resolver.calls++
	if resolver.err != nil {
		return permdata.Resolution{}, resolver.err
	}
	result := permdata.Resolution{Entries: make([]permdata.ResolutionEntry, 0, len(request.Items))}
	for _, item := range request.Items {
		entry := permdata.ResolutionEntry{
			ObjectTypeRef: item.ObjectTypeRef,
			Properties:    make([]permdata.ResolvedProperty, 0, len(item.Properties)),
		}
		for _, name := range item.Properties {
			entry.Properties = append(entry.Properties, permdata.ResolvedProperty{
				Name: name, Level: propertyaccess.Masked, Explicit: true,
			})
		}
		result.Entries = append(result.Entries, entry)
	}
	return result, nil
}

func TestPropertyLevelsResolverFailureIsUnavailable(t *testing.T) {
	permdata.ResetForTest()
	entitlement.SetGateForTest(entitlement.FixedGate(licverify.EditionEnterprise))
	t.Cleanup(func() {
		permdata.ResetForTest()
		entitlement.ResetForTest()
	})
	resolver := &recordingPropertyResolver{err: errors.New("property grant store unavailable")}
	permdata.Register(licverify.EditionEnterprise, resolver)

	router, enforcer, db := newTestServer(t)
	seedEnabledUser(t, db, "user-1")
	if err := enforcer.GrantObjectPermission("user-1", "object_type", "kn-1/customer", "query_data"); err != nil {
		t.Fatal(err)
	}
	response := do(t, router, http.MethodPost, "/api/safe/v1/authz/property-levels", propertyLevelsBody("kn-1/customer", []string{"name"}))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("property-levels = %d, want 503: %s", response.Code, response.Body.String())
	}
	if resolver.calls != 1 {
		t.Fatalf("enterprise resolver calls = %d, want one", resolver.calls)
	}
}

func TestPropertyLevelsCommunityFallback(t *testing.T) {
	router, enforcer, db := newTestServer(t)
	seedEnabledUser(t, db, "user-1")
	if err := db.Create(&model.Operation{
		ResourceTypeID: "object_type", ID: "query_data", ParentOperationID: "query_data",
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.ResourceParent{
		ResourceTypeID: "object_type", ResourceID: "kn-1/full",
		ParentTypeID: "knowledge_network", ParentID: "kn-1",
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := enforcer.GrantObjectPermission("user-1", "knowledge_network", "kn-1", "query_data"); err != nil {
		t.Fatal(err)
	}
	if err := enforcer.GrantObjectPermission("user-1", "object_type", "kn-1/schema", "view_detail"); err != nil {
		t.Fatal(err)
	}

	response := do(t, router, http.MethodPost, "/api/safe/v1/authz/property-levels", map[string]any{
		"accessor_id": "user-1",
		"items": []map[string]any{
			{"object_type_ref": "kn-1/full", "properties": []string{"name", "mobile"}},
			{"object_type_ref": "kn-1/schema", "properties": []string{"name"}},
			{"object_type_ref": "kn-1/none", "properties": []string{"name"}},
		},
	})
	if response.Code != http.StatusOK {
		t.Fatalf("property-levels = %d: %s", response.Code, response.Body.String())
	}
	var body permdata.Response
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	want := []propertyaccess.Level{propertyaccess.Full, propertyaccess.Schema, propertyaccess.None}
	if len(body.Entries) != len(want) {
		t.Fatalf("entries = %d, want %d", len(body.Entries), len(want))
	}
	for index, level := range want {
		for _, property := range body.Entries[index].Properties {
			if property.Level != level || property.Source != permdata.SourceObjectType {
				t.Errorf("entry %d property = %+v, want %s from object_type", index, property, level)
			}
		}
	}
}

func TestPropertyLevelsDisabledOrMissingAccessorGetsNone(t *testing.T) {
	router, enforcer, db := newTestServer(t)
	if err := enforcer.GrantObjectPermission("disabled", "object_type", "kn-1/customer", "query_data"); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO users (id, account, enabled) VALUES (?, ?, ?)", "disabled", "disabled", false).Error; err != nil {
		t.Fatal(err)
	}
	for _, accessor := range []string{"disabled", "missing"} {
		t.Run(accessor, func(t *testing.T) {
			response := do(t, router, http.MethodPost, "/api/safe/v1/authz/property-levels", map[string]any{
				"accessor_id": accessor,
				"items": []map[string]any{{
					"object_type_ref": "kn-1/customer",
					"properties":      []string{"name"},
				}},
			})
			var body permdata.Response
			if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &body) != nil {
				t.Fatalf("property-levels = %d: %s", response.Code, response.Body.String())
			}
			property := body.Entries[0].Properties[0]
			if property.Level != propertyaccess.None {
				t.Fatalf("property = %+v, want none", property)
			}
		})
	}
}

func TestPropertyLevelsAcceptsTwoHundredPropertiesInOneBatch(t *testing.T) {
	permdata.ResetForTest()
	entitlement.SetGateForTest(entitlement.FixedGate(licverify.EditionEnterprise))
	t.Cleanup(func() {
		permdata.ResetForTest()
		entitlement.ResetForTest()
	})
	resolver := &recordingPropertyResolver{}
	permdata.Register(licverify.EditionEnterprise, resolver)

	router, enforcer, db := newTestServer(t)
	seedEnabledUser(t, db, "user-1")
	if err := enforcer.GrantObjectPermission("user-1", "object_type", "kn-1/customer", "query_data"); err != nil {
		t.Fatal(err)
	}
	properties := make([]string, maxPropertiesPerObjectType)
	for index := range properties {
		properties[index] = fmt.Sprintf("property_%03d", index)
	}
	response := do(t, router, http.MethodPost, "/api/safe/v1/authz/property-levels", map[string]any{
		"accessor_id": "user-1",
		"items": []map[string]any{{
			"object_type_ref": "kn-1/customer",
			"properties":      properties,
		}},
	})
	var body permdata.Response
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &body) != nil {
		t.Fatalf("property-levels = %d: %s", response.Code, response.Body.String())
	}
	if got := len(body.Entries[0].Properties); got != maxPropertiesPerObjectType {
		t.Fatalf("properties = %d, want %d", got, maxPropertiesPerObjectType)
	}
	if resolver.calls != 1 {
		t.Fatalf("enterprise resolver calls = %d, want one batch call", resolver.calls)
	}
	for _, property := range body.Entries[0].Properties {
		if property.Level != propertyaccess.Masked || property.Source != permdata.SourceProperty {
			t.Fatalf("property = %+v, want masked from property", property)
		}
	}
}

func TestPropertyLevelsRejectsInvalidAndOversizedRequests(t *testing.T) {
	router, _, _ := newTestServer(t)
	tooManyProperties := make([]string, maxPropertiesPerObjectType+1)
	for index := range tooManyProperties {
		tooManyProperties[index] = fmt.Sprintf("p%d", index)
	}
	tooManyObjects := make([]map[string]any, maxPropertyLevelObjectTypes+1)
	for index := range tooManyObjects {
		tooManyObjects[index] = map[string]any{
			"object_type_ref": fmt.Sprintf("kn-%d/object", index),
			"properties":      []string{"name"},
		}
	}
	tooManyTotalProperties := make([]map[string]any, 6)
	for objectIndex := range tooManyTotalProperties {
		properties := make([]string, maxPropertiesPerObjectType)
		for propertyIndex := range properties {
			properties[propertyIndex] = fmt.Sprintf("p%d", propertyIndex)
		}
		tooManyTotalProperties[objectIndex] = map[string]any{
			"object_type_ref": fmt.Sprintf("kn-%d/object", objectIndex),
			"properties":      properties,
		}
	}

	for _, test := range []struct {
		name string
		body map[string]any
	}{
		{name: "invalid object ref", body: propertyLevelsBody("kn-only", []string{"name"})},
		{name: "wildcard object ref", body: propertyLevelsBody("kn-1/*", []string{"name"})},
		{name: "non-canonical object ref", body: propertyLevelsBody("KN-1/customer", []string{"name"})},
		{name: "empty items", body: map[string]any{"accessor_id": "user-1", "items": []any{}}},
		{name: "empty properties", body: propertyLevelsBody("kn-1/customer", []string{})},
		{name: "invalid property", body: propertyLevelsBody("kn-1/customer", []string{"_secret"})},
		{name: "duplicate property", body: propertyLevelsBody("kn-1/customer", []string{"name", "name"})},
		{name: "too many properties", body: propertyLevelsBody("kn-1/customer", tooManyProperties)},
		{name: "too many objects", body: map[string]any{"accessor_id": "user-1", "items": tooManyObjects}},
		{name: "too many total properties", body: map[string]any{"accessor_id": "user-1", "items": tooManyTotalProperties}},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := do(t, router, http.MethodPost, "/api/safe/v1/authz/property-levels", test.body)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("property-levels = %d, want 400: %s", response.Code, response.Body.String())
			}
		})
	}
}

func propertyLevelsBody(objectTypeRef string, properties []string) map[string]any {
	return map[string]any{
		"accessor_id": "user-1",
		"items": []map[string]any{{
			"object_type_ref": objectTypeRef,
			"properties":      properties,
		}},
	}
}
