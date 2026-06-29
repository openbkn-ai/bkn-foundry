// Copyright 2026 openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package knresources

import (
	"context"
	"errors"
	"testing"

	"github.com/openbkn-ai/adp/context-loader/agent-retrieval/server/interfaces"
)

// fakeVega 实现 interfaces.DrivenVega，仅供本包测试。
type fakeVega struct {
	listReq       *interfaces.VegaListResourcesReq
	listResp      *interfaces.VegaListResourcesResp
	listErr       error
	getResource   *interfaces.VegaResource
	getErr        error
	connectorType string
	connectorErr  error
	gotResourceID string
}

func (f *fakeVega) RawQuery(_ context.Context, _ *interfaces.VegaRawQueryReq) (*interfaces.VegaRawQueryResp, error) {
	return nil, errors.New("not used")
}

func (f *fakeVega) GetResourceConnectorType(_ context.Context, resourceID string) (string, error) {
	f.gotResourceID = resourceID
	return f.connectorType, f.connectorErr
}

func (f *fakeVega) ListResources(_ context.Context, req *interfaces.VegaListResourcesReq) (*interfaces.VegaListResourcesResp, error) {
	f.listReq = req
	return f.listResp, f.listErr
}

func (f *fakeVega) GetResource(_ context.Context, resourceID string) (*interfaces.VegaResource, error) {
	f.gotResourceID = resourceID
	return f.getResource, f.getErr
}

func TestListResources_MapsAndForwardsFilters(t *testing.T) {
	fake := &fakeVega{
		listResp: &interfaces.VegaListResourcesResp{
			TotalCount: 2,
			Entries: []interfaces.VegaResource{
				{ID: "r1", Name: "orders", Category: "table", Status: "active", CatalogID: "c1"},
				{ID: "r2", Name: "events", Category: "topic", Status: "stale", CatalogID: "c1"},
			},
		},
	}
	svc := NewKnResourcesServiceWith(fake)

	resp, err := svc.ListResources(context.Background(), &ListResourcesReq{
		CatalogID: "c1",
		Type:      "table",
		Offset:    5,
		Limit:     10,
	})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}

	// type(入参) 映射到 vega category；分页透传。
	if fake.listReq.Category != "table" || fake.listReq.CatalogID != "c1" {
		t.Fatalf("filters not forwarded: %+v", fake.listReq)
	}
	if fake.listReq.Offset != 5 || fake.listReq.Limit != 10 {
		t.Fatalf("paging not forwarded: %+v", fake.listReq)
	}
	if resp.TotalCount != 2 || len(resp.Entries) != 2 {
		t.Fatalf("unexpected resp: %+v", resp)
	}
	// vega category → 输出 type。
	if resp.Entries[0].Type != "table" || resp.Entries[0].ResourceID != "r1" {
		t.Fatalf("entry0 mapping wrong: %+v", resp.Entries[0])
	}
	if resp.Entries[1].Type != "topic" || resp.Entries[1].Status != "stale" {
		t.Fatalf("entry1 mapping wrong: %+v", resp.Entries[1])
	}
}

func TestListResources_NilReqEmptyEntries(t *testing.T) {
	fake := &fakeVega{listResp: &interfaces.VegaListResourcesResp{TotalCount: 0, Entries: nil}}
	svc := NewKnResourcesServiceWith(fake)

	resp, err := svc.ListResources(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if resp.Entries == nil {
		t.Fatal("entries should be non-nil empty slice")
	}
	if len(resp.Entries) != 0 || resp.TotalCount != 0 {
		t.Fatalf("expected empty, got %+v", resp)
	}
}

func TestListResources_VegaErrorPropagates(t *testing.T) {
	fake := &fakeVega{listErr: errors.New("boom")}
	svc := NewKnResourcesServiceWith(fake)
	if _, err := svc.ListResources(context.Background(), &ListResourcesReq{}); err == nil {
		t.Fatal("expected error to propagate")
	}
}

func TestDescribeResource_EmptyID(t *testing.T) {
	svc := NewKnResourcesServiceWith(&fakeVega{})
	if _, err := svc.DescribeResource(context.Background(), "  "); !errors.Is(err, ErrResourceIDRequired) {
		t.Fatalf("expected ErrResourceIDRequired, got %v", err)
	}
}

func TestDescribeResource_MapsColumnsAndConnector(t *testing.T) {
	fake := &fakeVega{
		getResource: &interfaces.VegaResource{
			ID: "r1",
			SchemaDefinition: []interfaces.VegaResourceColumn{
				{Name: "id", Type: "bigint", Description: "主键"},
				{Name: "amount", Type: "decimal"},
			},
		},
		connectorType: "mysql",
	}
	svc := NewKnResourcesServiceWith(fake)

	resp, err := svc.DescribeResource(context.Background(), "r1")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if resp.ResourceID != "r1" || resp.ConnectorType != "mysql" {
		t.Fatalf("header mapping wrong: %+v", resp)
	}
	if len(resp.Columns) != 2 {
		t.Fatalf("expected 2 columns, got %d", len(resp.Columns))
	}
	if resp.Columns[0].Name != "id" || resp.Columns[0].Type != "bigint" || resp.Columns[0].Description != "主键" {
		t.Fatalf("col0 wrong: %+v", resp.Columns[0])
	}
	if resp.Columns[1].Name != "amount" || resp.Columns[1].Description != "" {
		t.Fatalf("col1 wrong: %+v", resp.Columns[1])
	}
}

func TestDescribeResource_EmptySchemaIsEmptyColumns(t *testing.T) {
	fake := &fakeVega{
		getResource:   &interfaces.VegaResource{ID: "rf", SchemaDefinition: nil},
		connectorType: "postgresql",
	}
	svc := NewKnResourcesServiceWith(fake)

	resp, err := svc.DescribeResource(context.Background(), "rf")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if resp.Columns == nil {
		t.Fatal("columns should be non-nil empty slice")
	}
	if len(resp.Columns) != 0 {
		t.Fatalf("expected 0 columns, got %d", len(resp.Columns))
	}
}

func TestDescribeResource_GetResourceErrorPropagates(t *testing.T) {
	fake := &fakeVega{getErr: errors.New("403 forbidden")}
	svc := NewKnResourcesServiceWith(fake)
	if _, err := svc.DescribeResource(context.Background(), "r1"); err == nil {
		t.Fatal("expected error to propagate")
	}
}
