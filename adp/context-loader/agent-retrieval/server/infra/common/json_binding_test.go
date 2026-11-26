// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package common

import (
	"encoding/json"
	"encoding/json/jsontext"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

func TestBindJSON(t *testing.T) {
	type request struct {
		Name  string `json:"name" binding:"required"`
		Value any    `json:"value"`
	}
	var req request
	if err := BindJSON(strings.NewReader(`{"name":"example","value":9007199254740993,"extra":true}`), &req); err != nil {
		t.Fatal(err)
	}
	if req.Name != "example" || req.Value != json.Number("9007199254740993") {
		t.Fatalf("bound request = %#v", req)
	}
	if err := BindJSON(strings.NewReader(`{"extra":true}`), new(request)); err == nil {
		t.Fatal("BindJSON accepted a request without the required name")
	}
}

func TestBindJSONMetricQuery(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  interfaces.ObjectMetricQuery
	}{
		{input: `{"kn_id":"kn1","metric_id":"m1","query":{"instance_identities":[{"id":1}]}}`},
		{
			input: `{"kn_id":"kn1","metric_id":"m1","query":{"bucket_selection":{"bucket_key":"b1","parent_query":{"instance_identities":[{"id":1}]}}}}`,
			want:  interfaces.ObjectMetricQuery{BucketSelection: &interfaces.ObjectMetricBucketSelection{BucketKey: "b1"}},
		},
	} {
		var req interfaces.QueryMetricReq
		if err := BindJSON(strings.NewReader(tc.input), &req); err != nil {
			t.Fatalf("BindJSON(%s): %v", tc.input, err)
		}
		if req.MetricID != "m1" || !reflect.DeepEqual(req.Query, tc.want) {
			t.Fatalf("BindJSON(%s) = %#v, want metric m1 with query %#v", tc.input, req, tc.want)
		}
	}

	var req interfaces.QueryMetricReq
	if err := BindJSON(strings.NewReader(`{"kn_id":"kn1","metric_id":"m1","query":{"having":{"operator":"eq","value":9007199254740993}}}`), &req); err != nil {
		t.Fatal(err)
	}
	if got, ok := req.Query.Having.Value.(json.Number); !ok || got.String() != "9007199254740993" {
		t.Fatalf("having value = %#v, want exact json.Number", req.Query.Having.Value)
	}

	if err := BindJSON(strings.NewReader(`{"kn_id":"kn1","metric_id":"m1","metric_id":"m2","query":{}}`), &req); !errors.Is(err, jsontext.ErrDuplicateName) {
		t.Fatalf("duplicate metric_id error = %v, want duplicate name error", err)
	}
	if err := BindJSON(strings.NewReader(`{"kn_id":"kn1","metric_id":"m1","query":{}} {}`), &req); err == nil || err.Error() != "unexpected trailing JSON value" {
		t.Fatalf("trailing value error = %v, want unexpected trailing JSON value", err)
	}
}
