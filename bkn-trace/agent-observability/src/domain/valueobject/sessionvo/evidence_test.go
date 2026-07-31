package sessionvo

import "testing"

func TestBusinessRefTypeCanonicalRefPrefix(t *testing.T) {
	t.Parallel()

	tests := map[BusinessRefType]string{
		BusinessRefKnowledgeNetwork: "kn",
		BusinessRefObjectType:       "object",
		BusinessRefObjectInstance:   "object_instance",
		BusinessRefProperty:         "property",
		BusinessRefRelationType:     "relation",
		BusinessRefDataResource:     "resource",
		BusinessRefMetric:           "metric",
		BusinessRefLogic:            "logic",
		BusinessRefFunction:         "function",
		BusinessRefActionType:       "action_type",
		BusinessRefActionInstance:   "action_instance",
	}
	for refType, want := range tests {
		if got := refType.CanonicalRefPrefix(); got != want {
			t.Fatalf("%s canonical prefix=%q, want %q", refType, got, want)
		}
	}
	if got := BusinessRefType("unknown").CanonicalRefPrefix(); got != "" {
		t.Fatalf("unknown canonical prefix=%q, want empty", got)
	}
}

func TestBusinessRefTypeRequiresCanonicalRefIDShape(t *testing.T) {
	t.Parallel()

	tests := []struct {
		refType BusinessRefType
		valid   string
	}{
		{BusinessRefKnowledgeNetwork, "kn:supplychain"},
		{BusinessRefObjectType, "object:supplychain:forecast"},
		{BusinessRefObjectInstance, "object_instance:supplychain:forecast:row-1"},
		{BusinessRefProperty, "property:supplychain:forecast:qty"},
		{BusinessRefRelationType, "relation:supplychain:contains"},
		{BusinessRefDataResource, "resource:resource-1"},
		{BusinessRefMetric, "metric:supplychain:total"},
		{BusinessRefLogic, "logic:supplychain:forecast:total"},
		{BusinessRefFunction, "function:supplychain:calculate"},
		{BusinessRefActionType, "action_type:supplychain:approve"},
		{BusinessRefActionInstance, "action_instance:supplychain:approve:run-1"},
	}
	for _, test := range tests {
		if !test.refType.MatchesCanonicalRefID(test.valid) {
			t.Fatalf("%s rejected canonical ref ID %q", test.refType, test.valid)
		}
		if test.refType != BusinessRefKnowledgeNetwork && test.refType != BusinessRefDataResource &&
			test.refType.MatchesCanonicalRefID(test.refType.CanonicalRefPrefix()+":short") {
			t.Fatalf("%s accepted underspecified ref ID", test.refType)
		}
		if test.refType != BusinessRefObjectInstance && test.refType != BusinessRefActionInstance &&
			test.refType.MatchesCanonicalRefID(test.valid+":extra") {
			t.Fatalf("%s accepted over-specified ref ID", test.refType)
		}
	}
	if !BusinessRefObjectInstance.MatchesCanonicalRefID("object_instance:supplychain:forecast:bkn://object/row-1") ||
		!BusinessRefActionInstance.MatchesCanonicalRefID("action_instance:supplychain:approve:workflow:run:1") {
		t.Fatal("opaque instance ID tails must allow URI and composite identifiers")
	}
	if BusinessRefType("unknown").MatchesCanonicalRefID("unknown:value") {
		t.Fatal("unknown business reference type accepted a ref ID")
	}
}
