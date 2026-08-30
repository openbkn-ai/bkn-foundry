package parsers

import (
	"context"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
)

func described(name string) *interfaces.ParameterDef {
	return &interfaces.ParameterDef{Name: name, Description: "what " + name + " has to be"}
}

// TestUndescribedParametersReachesIntoSubParameters is the case that motivates
// walking the tree at all. A composite argument names itself and says nothing
// about the fields a caller actually fills in - the published function whose
// demands parameter is an array of {product, qty} objects is exactly this shape,
// and those two fields are the ones that get sent wrong.
func TestUndescribedParametersReachesIntoSubParameters(t *testing.T) {
	demands := described("demands")
	demands.Type = interfaces.ParameterTypeArray
	demands.SubParameters = []*interfaces.ParameterDef{
		// The element node carries no description of its own and does not need
		// one; its fields do.
		{Name: "items", Type: interfaces.ParameterTypeObject, SubParameters: []*interfaces.ParameterDef{
			described("product"),
			{Name: "qty", Description: "  "},
		}},
	}

	missing := undescribedParameters([]*interfaces.ParameterDef{demands}, "", "")
	if len(missing) != 1 || missing[0] != "demands.items.qty" {
		t.Fatalf("missing = %v, want [demands.items.qty]", missing)
	}
}

func TestValidateParameterDescriptions(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name    string
		input   *interfaces.FunctionInput
		wantErr string
	}{
		{
			name:  "fully described passes",
			input: &interfaces.FunctionInput{Description: "computes coverage", Inputs: []*interfaces.ParameterDef{described("product")}},
		},
		{
			name:  "no parameters is fine",
			input: &interfaces.FunctionInput{Description: "returns a constant"},
		},
		{
			name: "empty parameter description is refused",
			input: &interfaces.FunctionInput{Description: "computes coverage", Inputs: []*interfaces.ParameterDef{
				described("product"),
				{Name: "demand_end", Description: ""},
			}},
			wantErr: "demand_end",
		},
		{
			// Whitespace is not a description. Accepting it would leave the gate
			// open to exactly the caller it exists to stop.
			name: "whitespace parameter description is refused",
			input: &interfaces.FunctionInput{Description: "computes coverage", Inputs: []*interfaces.ParameterDef{
				{Name: "depth", Description: "\t\n "},
			}},
			wantErr: "depth",
		},
		{
			// List[str]. The SDK invents items to carry the element type and no
			// docstring can reach it, so demanding a description there would
			// reject the function with an error its author cannot act on.
			name: "array element placeholder is not required to describe itself",
			input: &interfaces.FunctionInput{Inputs: []*interfaces.ParameterDef{func() *interfaces.ParameterDef {
				tags := described("tags")
				tags.Type = interfaces.ParameterTypeArray
				tags.SubParameters = []*interfaces.ParameterDef{{Name: "items", Type: interfaces.ParameterTypeString}}
				return tags
			}()}},
		},
		{
			// Dict[str, int], where values plays the same role as items above.
			name: "dict value placeholder is not required to describe itself",
			input: &interfaces.FunctionInput{Inputs: []*interfaces.ParameterDef{func() *interfaces.ParameterDef {
				quotas := described("quotas")
				quotas.Type = interfaces.ParameterTypeObject
				quotas.SubParameters = []*interfaces.ParameterDef{{Name: "values", Type: interfaces.ParameterTypeNumber}}
				return quotas
			}()}},
		},
		{
			// A named field that happens to be called values is the author's to
			// describe: the placeholder rule only applies to a lone child.
			name: "a named sibling called values is still required",
			input: &interfaces.FunctionInput{Inputs: []*interfaces.ParameterDef{func() *interfaces.ParameterDef {
				cfg := described("cfg")
				cfg.Type = interfaces.ParameterTypeObject
				cfg.SubParameters = []*interfaces.ParameterDef{described("mode"), {Name: "values"}}
				return cfg
			}()}},
			wantErr: "cfg.values",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateParameterDescriptions(ctx, tc.input)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("expected an error, got none")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error %q does not name %q", err.Error(), tc.wantErr)
			}
		})
	}
}
