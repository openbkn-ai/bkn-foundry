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
	Cursor         *Position
	Limit          int
}

type Position struct {
	OccurredAt time.Time
	EventID    string
}

type Record struct {
	EventID    string
	OccurredAt time.Time
	SourceID   string
	Category   string
	EventName  string
	ActorID    string
	TargetType string
	TargetID   string
	Action     string
	Outcome    string
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
