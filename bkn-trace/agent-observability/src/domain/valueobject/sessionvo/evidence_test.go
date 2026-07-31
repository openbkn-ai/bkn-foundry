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
