// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package knlogicpropertyresolver

import (
	"context"
	stderrors "errors"
	"math"
	"net/http"
	"testing"

	infraerrors "github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/logger"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

func TestEffectiveMaxConcurrency(t *testing.T) {
	for _, c := range []struct{ in, want int }{
		{-1, defaultMaxConcurrency},
		{0, defaultMaxConcurrency},
		{1, 1},
		{10, 10},
		{maxAllowedConcurrency, maxAllowedConcurrency},
	} {
		got, err := effectiveMaxConcurrency(context.Background(), c.in)
		if err != nil || got != c.want {
			t.Errorf("effectiveMaxConcurrency(%d) = %d, %v; want %d, nil", c.in, got, err, c.want)
		}
	}
	for _, in := range []int{maxAllowedConcurrency + 1, math.MaxInt} {
		_, err := effectiveMaxConcurrency(context.Background(), in)
		var httpErr *infraerrors.HTTPError
		if !stderrors.As(err, &httpErr) || httpErr.HTTPCode != http.StatusBadRequest {
			t.Errorf("effectiveMaxConcurrency(%d) error = %v, want 400", in, err)
		}
	}
}

// max_concurrency used to size a channel buffer unchecked, so a huge value could exhaust memory.
// It must now be rejected before any upstream call (the service has no backends wired here, so
// reaching one would panic).
func TestResolveLogicPropertiesRejectsAbusiveMaxConcurrency(t *testing.T) {
	service := &knLogicPropertyResolverService{logger: logger.DefaultLogger()}
	req := &interfaces.ResolveLogicPropertiesRequest{
		KnID:               "kn-001",
		OtID:               "ot-001",
		Query:              "q",
		InstanceIdentities: []map[string]interface{}{{"id": "obj-001"}},
		Properties:         []string{"p1"},
		Options:            &interfaces.ResolveOptions{MaxConcurrency: math.MaxInt},
	}
	_, err := service.ResolveLogicProperties(context.Background(), req)
	var httpErr *infraerrors.HTTPError
	if !stderrors.As(err, &httpErr) || httpErr.HTTPCode != http.StatusBadRequest {
		t.Fatalf("ResolveLogicProperties error = %v, want 400", err)
	}
}
