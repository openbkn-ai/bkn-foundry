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
	demands.SubParameters = []*interfaces.ParameterDef{
		{Name: "items", Description: "one demand", SubParameters: []*interfaces.ParameterDef{
			described("product"),
			{Name: "qty", Description: "  "},
		}},
	}

	missing := undescribedParameters([]*interfaces.ParameterDef{demands}, "")
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
			// A function that says nothing about itself is worse than a parameter
			// that does: the caller cannot even tell whether to reach for it.
			name:    "empty function description is refused",
			input:   &interfaces.FunctionInput{Description: "  ", Inputs: []*interfaces.ParameterDef{described("product")}},
			wantErr: "function description is empty",
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
