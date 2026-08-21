package driveradapters

import (
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/bkntrace"
)

// Regression for the second instance of #1098's root cause.
//
// normalizedHTTPInputHash exists to give identical input an identical digest — it even sorts
// causation ids by hand to get there. It then marshals a map[string]any with the default sonic
// config, which does not sort map keys, and Go randomises map iteration order. So the "normalized"
// hash came out different on every call, and the operation key built from it with it.
//
// The input needs several keys for the bug to show: with one key there is no order to get wrong.
func lifecycleHashFixture() (map[string]any, bkntrace.BusinessContext) {
	input := map[string]any{
		"query":         "供应商 对象类 字段",
		"kn_id":         "kn_probe",
		"max_concepts":  10,
		"schema_brief":  true,
		"enable_rerank": true,
		"zebra_key":     "sorts last",
		"alpha_key":     "sorts first",
	}
	businessContext := bkntrace.BusinessContext{
		ConversationID:    "conv_1",
		InteractionID:     "int_1",
		ParentOperationID: "op_parent",
		CausationEventIDs: []string{"evt_b", "evt_a"},
	}
	return input, businessContext
}

func TestNormalizedHTTPInputHashIsStable(t *testing.T) {
	input, businessContext := lifecycleHashFixture()

	want := normalizedHTTPInputHash(input, businessContext)
	for i := 0; i < 64; i++ {
		if got := normalizedHTTPInputHash(input, businessContext); got != want {
			t.Fatalf("normalized input hash changed between calls (call %d):\n  %s\n  %s", i, got, want)
		}
	}
}

// The hash is not an end in itself: it is the identity half of the HTTP operation key, so an
// unstable hash makes the same request look like a different operation on every retry.
func TestHTTPOperationKeyIsStable(t *testing.T) {
	input, businessContext := lifecycleHashFixture()

	key := func() string {
		return hashHTTPOperationKey("http-request", businessContext, "search_schema",
			"req_1\x00"+normalizedHTTPInputHash(input, businessContext))
	}

	want := key()
	for i := 0; i < 64; i++ {
		if got := key(); got != want {
			t.Fatalf("operation key changed between calls (call %d): %s != %s", i, got, want)
		}
	}
}
