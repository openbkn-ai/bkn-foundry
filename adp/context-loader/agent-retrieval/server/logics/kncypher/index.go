// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package kncypher runs read-only Cypher against a knowledge network by asking
// bkn-backend to compile it.
//
// The compiler lives there because that is where the model lives: labels are
// object types, relationship types are join keys, properties are columns. This
// service adds no query language of its own -- it carries the caller's request
// across, and carries the compiler's refusal back with the construct it names,
// because that message is what tells the caller how to fix the query.
package kncypher

import (
	"context"
	"errors"
	"strings"
	"sync"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/drivenadapters"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/bkntrace"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

var (
	// ErrKnIDRequired the knowledge network was not named.
	ErrKnIDRequired = errors.New("kn_id is required")
	// ErrQueryRequired the query was empty.
	ErrQueryRequired = errors.New("query is required")

	emitRunCypherEvents  = bkntrace.EmitRunCypherEvents
	emitRunCypherFailure = bkntrace.EmitRunCypherFailure
)

// RunCypherReq is the run_cypher input, shared by the MCP tool and the
// internal REST endpoint.
type RunCypherReq struct {
	KnID       string         `json:"kn_id"`
	Branch     string         `json:"branch"`
	Query      string         `json:"query"`
	Parameters map[string]any `json:"parameters"`
}

// KnCypherService runs one read-only Cypher query against a knowledge network.
type KnCypherService interface {
	RunCypher(ctx context.Context, req *RunCypherReq) (*interfaces.CypherQueryResp, error)
}

type knCypherService struct {
	bkn interfaces.BknBackendAccess
}

var (
	once     sync.Once
	instance KnCypherService
)

// NewKnCypherService creates the KnCypherService singleton.
func NewKnCypherService() KnCypherService {
	once.Do(func() {
		instance = &knCypherService{bkn: drivenadapters.NewBknBackendAccess()}
	})
	return instance
}

// NewKnCypherServiceWith builds the service with an injected dependency.
func NewKnCypherServiceWith(bkn interfaces.BknBackendAccess) KnCypherService {
	return &knCypherService{bkn: bkn}
}

// RunCypher validates what can be answered here and hands the rest to the
// compiler. Everything about the query itself -- which constructs are
// supported, which names exist, what the caller may read -- is decided there,
// so repeating any of it here would be a second opinion that can drift.
func (s *knCypherService) RunCypher(ctx context.Context, req *RunCypherReq) (*interfaces.CypherQueryResp, error) {
	knID, query := "", ""
	if req != nil {
		knID, query = strings.TrimSpace(req.KnID), req.Query
	}
	if knID == "" {
		emitRunCypherFailure(ctx, nil, knID, query, bkntrace.RunCypherFailure{
			Stage: "input_validation", Code: "RUN_CYPHER_KN_ID_REQUIRED", Summary: ErrKnIDRequired.Error(),
		})
		return nil, ErrKnIDRequired
	}
	if strings.TrimSpace(query) == "" {
		emitRunCypherFailure(ctx, nil, knID, query, bkntrace.RunCypherFailure{
			Stage: "input_validation", Code: "RUN_CYPHER_QUERY_REQUIRED", Summary: ErrQueryRequired.Error(),
		})
		return nil, ErrQueryRequired
	}

	resp, err := s.bkn.RunCypherQuery(ctx, &interfaces.CypherQueryReq{
		KnID:       knID,
		Branch:     strings.TrimSpace(req.Branch),
		Query:      query,
		Parameters: req.Parameters,
	})
	if err != nil {
		emitRunCypherFailure(ctx, nil, knID, query, bkntrace.RunCypherFailure{
			Stage: "cypher_query", Code: "RUN_CYPHER_QUERY_FAILED", Summary: err.Error(),
		})
		return nil, err
	}

	rowCount := 0
	if resp != nil {
		rowCount = len(resp.Entries)
	}
	emitRunCypherEvents(ctx, nil, knID, query, rowCount)
	return resp, nil
}
