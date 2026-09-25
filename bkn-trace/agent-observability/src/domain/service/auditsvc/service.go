// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root.

// Package auditsvc defines the constrained Query Gateway contract over the
// center bkn_audit ledger. It has no fallback to module operation-audit APIs.
package auditsvc

import (
	"context"
	"errors"
	"fmt"
	"time"
)

var (
	ErrUnauthorized = errors.New("audit query unauthorized")
	ErrInvalidQuery = errors.New("invalid audit query")
)

type Principal struct {
	SubjectID         string
	AllowedCategories map[string]bool
	CanReadSecurity   bool
}

type Query struct {
	Categories     []string
	From, To       time.Time
	SourceID       string
	BusinessModule string
	ActorID        string
	TargetType     string
	TargetID       string
	Action         string
	Outcome        string
	Outcomes       []string
	ObservedBefore time.Time
	Cursor         *Position
	Limit          int
}

type Position struct {
	OccurredAt time.Time
	EventID    string
}

type Record struct {
	EventID             string    `json:"event_id"`
	OccurredAt          time.Time `json:"occurred_at"`
	BrokerReceivedAt    time.Time `json:"broker_received_at"`
	RecordedAt          time.Time `json:"recorded_at"`
	SourceID            string    `json:"source_id"`
	Category            string    `json:"category"`
	EventName           string    `json:"event_name"`
	BusinessModule      string    `json:"business_module"`
	ActorID             string    `json:"actor_id"`
	TargetType          string    `json:"target_type"`
	TargetID            string    `json:"target_id"`
	TargetNameSnapshot  string    `json:"target_name_snapshot"`
	Action              string    `json:"action"`
	Outcome             string    `json:"outcome"`
	Environment         string    `json:"environment"`
	ApplicationID       string    `json:"application_id"`
	KnowledgeNetworkIDs []string  `json:"knowledge_network_ids"`
	EffectiveSubjectID  string    `json:"effective_subject_id"`
	ActorNameSnapshot   string    `json:"actor_name_snapshot"`
	ActorType           string    `json:"actor_type"`
	AuthMethod          string    `json:"auth_method"`
	SourceChannel       string    `json:"source_channel"`
	Summary             string    `json:"summary"`
	FailureCode         string    `json:"failure_code"`
	HTTPStatus          int       `json:"http_status"`
	Transport           string    `json:"transport"`
	Method              string    `json:"method"`
	ClientIP            string    `json:"client_ip"`
	RequestID           string    `json:"request_id"`
	TraceID             string    `json:"trace_id"`
	OperationID         string    `json:"operation_id"`
}

type Page struct {
	Records    []Record
	Next       *Position
	Incomplete bool
}

type Reader interface {
	Query(context.Context, Query) (Page, error)
}

type Service struct{ reader Reader }

func New(reader Reader) (*Service, error) {
	if reader == nil {
		return nil, errors.New("audit query reader is required")
	}
	return &Service{reader: reader}, nil
}

func (s *Service) Query(ctx context.Context, principal Principal, query Query) (Page, error) {
	if principal.SubjectID == "" {
		return Page{}, ErrUnauthorized
	}
	if err := validateQuery(query); err != nil {
		return Page{}, err
	}
	for _, category := range query.Categories {
		if category != "audit.admin" && category != "audit.security" {
			return Page{}, ErrUnauthorized
		}
		if !principal.AllowedCategories[category] {
			return Page{}, ErrUnauthorized
		}
		if category == "audit.security" && !principal.CanReadSecurity {
			return Page{}, ErrUnauthorized
		}
	}
	page, err := s.reader.Query(ctx, query)
	if err != nil {
		return Page{}, fmt.Errorf("query center audit ledger: %w", err)
	}
	return page, nil
}

func validateQuery(query Query) error {
	if len(query.Categories) == 0 || query.From.IsZero() || query.To.IsZero() || !query.From.Before(query.To) {
		return ErrInvalidQuery
	}
	from, to := query.From.UTC(), query.To.UTC()
	if to.Sub(from) > 30*24*time.Hour {
		return fmt.Errorf("audit query window exceeds 30 days: %w", ErrInvalidQuery)
	}
	months := monthSpan(from, to)
	if months > 3 {
		return fmt.Errorf("audit query spans %d monthly tables: %w", months, ErrInvalidQuery)
	}
	if query.Limit <= 0 || query.Limit > 200 {
		return ErrInvalidQuery
	}
	if query.Cursor != nil && (query.Cursor.EventID == "" || query.Cursor.OccurredAt.IsZero()) {
		return ErrInvalidQuery
	}
	return nil
}

func monthSpan(from, to time.Time) int {
	from, to = from.UTC(), to.UTC()
	return (to.Year()-from.Year())*12 + int(to.Month()-from.Month()) + 1
}
