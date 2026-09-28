// Copyright openbkn.ai

package objectpermission

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

type schemaAccessStub struct {
	response *interfaces.ObjectTypeSchemaResp
	err      error
}

func (s schemaAccessStub) GetObjectTypeSchema(context.Context, string, string) (*interfaces.ObjectTypeSchemaResp, error) {
	return s.response, s.err
}

func TestFilterObjectTypesAppliesEffectivePermissionPlan(t *testing.T) {
	objects := []*interfaces.ObjectType{{
		ID:          "customer",
		DataSource:  &interfaces.ResourceInfo{Type: "resource", ID: "customers"},
		PrimaryKeys: []string{"id", "secret"},
		DataProperties: []*interfaces.DataProperty{
			{Name: "id", ConditionOperations: []interfaces.KnOperationType{interfaces.KnOperationTypeEqual}},
			{Name: "phone", ConditionOperations: []interfaces.KnOperationType{interfaces.KnOperationTypeLike}},
			{Name: "notes", ConditionOperations: []interfaces.KnOperationType{interfaces.KnOperationTypeMatch}},
			{Name: "secret", ConditionOperations: []interfaces.KnOperationType{interfaces.KnOperationTypeEqual}},
		},
		LogicProperties: []*interfaces.LogicPropertyDef{
			{Name: "visible_logic", Parameters: []interfaces.PropertyParameter{{ValueFrom: "property", Value: "id"}}},
			{Name: "hidden_logic", Parameters: []interfaces.PropertyParameter{{ValueFrom: "property", Value: "phone"}}},
		},
	}}
	permissions := map[string]interfaces.PropertyAccessLevel{
		"id": interfaces.PropertyAccessFull, "phone": interfaces.PropertyAccessMasked,
		"notes": interfaces.PropertyAccessSchema, "secret": interfaces.PropertyAccessNone,
	}

	filtered, err := FilterObjectTypes(context.Background(), schemaAccessStub{response: &interfaces.ObjectTypeSchemaResp{
		EffectivePermissions: permissions,
	}}, "kn-1", objects)
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered) != 1 || len(filtered[0].DataProperties) != 3 {
		t.Fatalf("filtered object types = %#v", filtered)
	}
	if got := filtered[0].DataProperties[0].ConditionOperations; len(got) != 1 {
		t.Fatalf("full property operations = %#v", got)
	}
	if got := filtered[0].DataProperties[1].ConditionOperations; len(got) != 0 {
		t.Fatalf("masked property advertised operations = %#v", got)
	}
	if got := filtered[0].DataProperties[2].ConditionOperations; len(got) != 0 {
		t.Fatalf("schema-only property advertised operations = %#v", got)
	}
	if !reflect.DeepEqual(filtered[0].PrimaryKeys, []string{"id"}) {
		t.Fatalf("primary keys = %#v", filtered[0].PrimaryKeys)
	}
	if len(filtered[0].LogicProperties) != 1 || filtered[0].LogicProperties[0].Name != "visible_logic" {
		t.Fatalf("logic properties = %#v", filtered[0].LogicProperties)
	}
	if _, exists := filtered[0].EffectivePermissions["secret"]; exists {
		t.Fatal("none permission must not be exposed")
	}
	if len(objects[0].DataProperties) != 4 || len(objects[0].LogicProperties) != 2 {
		t.Fatal("source object type was mutated")
	}
}

func TestFilterObjectTypesDeniesUnknownPermission(t *testing.T) {
	objects := []*interfaces.ObjectType{{ID: "customer", DataSource: &interfaces.ResourceInfo{Type: "resource", ID: "customers"}, DataProperties: []*interfaces.DataProperty{{Name: "email"}}}}
	filtered, err := FilterObjectTypes(context.Background(), schemaAccessStub{response: &interfaces.ObjectTypeSchemaResp{
		EffectivePermissions: map[string]interfaces.PropertyAccessLevel{"email": "future-level"},
	}}, "kn-1", objects)
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered[0].DataProperties) != 0 || len(filtered[0].EffectivePermissions) != 0 {
		t.Fatalf("unknown permission was exposed: %#v", filtered[0])
	}
}

func TestFilterObjectTypesKeepsUnboundObjectWithoutProperties(t *testing.T) {
	objects := []*interfaces.ObjectType{{ID: "unbound", DataProperties: []*interfaces.DataProperty{{Name: "secret"}}}}
	filtered, err := FilterObjectTypes(context.Background(), schemaAccessStub{err: fmt.Errorf("must not be called")}, "kn-1", objects)
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered) != 1 || len(filtered[0].DataProperties) != 0 {
		t.Fatalf("unbound object result = %#v", filtered)
	}
}

func TestFilterObjectTypesSupportsLegacyBindingWithoutType(t *testing.T) {
	objects := []*interfaces.ObjectType{{
		ID:         "legacy",
		DataSource: &interfaces.ResourceInfo{ID: "legacy-view"},
		DataProperties: []*interfaces.DataProperty{
			{Name: "visible"}, {Name: "hidden"},
		},
	}}
	filtered, err := FilterObjectTypes(context.Background(), schemaAccessStub{response: &interfaces.ObjectTypeSchemaResp{
		EffectivePermissions: map[string]interfaces.PropertyAccessLevel{"visible": interfaces.PropertyAccessFull},
	}}, "kn-1", objects)
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered) != 1 || len(filtered[0].DataProperties) != 1 || filtered[0].DataProperties[0].Name != "visible" {
		t.Fatalf("legacy resource binding was not authorized: %#v", filtered)
	}
}

// recordingSchemaAccess answers every object type and reports how many reads
// were ever in flight at once, which is what "no longer serial" means here.
type recordingSchemaAccess struct {
	mu        sync.Mutex
	inFlight  int
	peak      int
	calls     int
	seen      []string
	block     chan struct{}
	release   sync.Once
	failOn    map[string]error
	afterFail func()
}

func (r *recordingSchemaAccess) GetObjectTypeSchema(ctx context.Context, _, otID string) (*interfaces.ObjectTypeSchemaResp, error) {
	r.mu.Lock()
	r.calls++
	r.seen = append(r.seen, otID)
	r.inFlight++
	if r.inFlight > r.peak {
		r.peak = r.inFlight
	}
	reached := r.inFlight
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		r.inFlight--
		r.mu.Unlock()
	}()

	// Hold every reader until the pool is full, so the peak below is the bound
	// the code enforces and not a scheduling accident.
	if r.block != nil {
		if reached >= schemaFanOut {
			r.release.Do(func() { close(r.block) })
		}
		select {
		case <-r.block:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if err := r.failOn[otID]; err != nil {
		if r.afterFail != nil {
			r.afterFail()
		}
		return nil, err
	}
	return &interfaces.ObjectTypeSchemaResp{
		EffectivePermissions: map[string]interfaces.PropertyAccessLevel{"id": interfaces.PropertyAccessFull},
	}, nil
}

func boundObjectTypes(n int) []*interfaces.ObjectType {
	objects := make([]*interfaces.ObjectType, 0, n)
	for i := range n {
		objects = append(objects, &interfaces.ObjectType{
			ID:             fmt.Sprintf("ot-%03d", i),
			DataSource:     &interfaces.ResourceInfo{Type: "resource", ID: "res"},
			DataProperties: []*interfaces.DataProperty{{Name: "id"}},
		})
	}
	return objects
}

// The regression #1877 reported: one read at a time made get_kn_detail's cost
// the network's size times a round trip, which a 1000-object network could not
// pay inside a caller's timeout.
func TestFilterObjectTypesReadsSchemasConcurrently(t *testing.T) {
	access := &recordingSchemaAccess{block: make(chan struct{})}
	objects := boundObjectTypes(64)

	result, err := FilterObjectTypes(context.Background(), access, "kn", objects)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != len(objects) {
		t.Fatalf("got %d object types, want %d", len(result), len(objects))
	}
	if access.calls != len(objects) {
		t.Fatalf("got %d schema reads, want %d", access.calls, len(objects))
	}
	if access.peak < 2 {
		t.Fatalf("peak concurrent reads = %d: the schema fan-out is serial again (#1877)", access.peak)
	}
	if access.peak != schemaFanOut {
		t.Fatalf("peak concurrent reads = %d, want the whole pool of %d", access.peak, schemaFanOut)
	}
}

// An unbound object type has no property plan to read, so it must not cost a
// round trip -- and must still hold its place in the answer.
func TestFilterObjectTypesKeepsCallerOrderAndSkipsUnboundReads(t *testing.T) {
	access := &recordingSchemaAccess{}
	objects := boundObjectTypes(24)
	objects[5] = &interfaces.ObjectType{ID: "unbound", DataProperties: []*interfaces.DataProperty{{Name: "id"}}}
	objects[11] = nil

	result, err := FilterObjectTypes(context.Background(), access, "kn", objects)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if access.calls != 22 {
		t.Fatalf("got %d schema reads, want 22 (24 minus one unbound and one nil)", access.calls)
	}
	want := make([]string, 0, 23)
	for _, objectType := range objects {
		if objectType != nil {
			want = append(want, objectType.ID)
		}
	}
	got := make([]string, 0, len(result))
	for _, objectType := range result {
		got = append(got, objectType.ID)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("order not preserved:\n got %v\nwant %v", got, want)
	}
}

// Concurrency must not make the reported failure depend on which read lost the
// race, so the lowest-index failure wins and our own cancellation stays quiet.
func TestFilterObjectTypesReportsTheFirstFailureInCallerOrder(t *testing.T) {
	boom := errors.New("ontology-query refused ot-002")
	for range 20 {
		access := &recordingSchemaAccess{failOn: map[string]error{
			"ot-002": boom,
			"ot-005": errors.New("ontology-query refused ot-005"),
		}}
		_, err := FilterObjectTypes(context.Background(), access, "kn", boundObjectTypes(64))
		if !errors.Is(err, boom) {
			t.Fatalf("got error %v, want %v", err, boom)
		}
	}
}

// A failure stops the fan-out instead of paying for the rest of the network.
func TestFilterObjectTypesStopsReadingAfterAFailure(t *testing.T) {
	access := &recordingSchemaAccess{failOn: map[string]error{"ot-000": errors.New("refused")}}
	if _, err := FilterObjectTypes(context.Background(), access, "kn", boundObjectTypes(512)); err == nil {
		t.Fatal("expected the refusal to fail the call")
	}
	access.mu.Lock()
	defer access.mu.Unlock()
	if access.calls >= 512 {
		t.Fatalf("read %d schemas after a refusal, want the fan-out to stop early", access.calls)
	}
}

// A caller that gives up mid-fan-out gets an error, not a network quietly short
// the object types whose reads never ran.
func TestFilterObjectTypesReportsCallerCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	access := &recordingSchemaAccess{afterFail: cancel, failOn: map[string]error{}}
	access.failOn["ot-000"] = context.Canceled

	_, err := FilterObjectTypes(ctx, access, "kn", boundObjectTypes(128))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got error %v, want context.Canceled", err)
	}
}
