// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package ossgatewayarchive

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type roundTripper func(*http.Request) (*http.Response, error)

func (fn roundTripper) RoundTrip(request *http.Request) (*http.Response, error) { return fn(request) }

func TestAvailabilityResolvesEnabledDefaultStorage(t *testing.T) {
	client := New(Config{BaseURL: "http://gateway/api/v1"})
	client.http = &http.Client{Transport: roundTripper(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path != "/api/v1/storages" {
			t.Fatalf("path = %s", request.URL.Path)
		}
		if request.URL.Query().Get("enabled") != "true" || request.URL.Query().Get("is_default") != "true" {
			t.Fatalf("missing default storage query: %s", request.URL.RawQuery)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"data":[{"storage_id":"default-archive"}]}`)), Header: make(http.Header)}, nil
	})}
	ready, err := client.Availability(context.Background())
	if err != nil || !ready {
		t.Fatalf("availability = %v, %v", ready, err)
	}
	if client.resolvedStorageID != "default-archive" {
		t.Fatalf("storage id = %q", client.resolvedStorageID)
	}
}

func TestReadDoesNotApplyControlPlaneTimeoutToArchiveBody(t *testing.T) {
	client := New(Config{BaseURL: "http://gateway/api/v1", StorageID: "archive", Timeout: time.Millisecond})
	client.http.Transport = roundTripper(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"data":{"method":"GET","url":"http://object-store/archive.jsonl"}}`)),
			Header:     make(http.Header),
		}, nil
	})
	client.stream.Transport = roundTripper(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       &delayedReadCloser{delay: 10 * time.Millisecond, content: strings.NewReader("archive\n")},
			Header:     make(http.Header),
		}, nil
	})

	body, err := client.Read(context.Background(), "archive.jsonl")
	if err != nil {
		t.Fatalf("read archive: %v", err)
	}
	defer func() { _ = body.Close() }()
	content, err := io.ReadAll(body)
	if err != nil {
		t.Fatalf("read archive body: %v", err)
	}
	if string(content) != "archive\n" {
		t.Fatalf("archive body = %q", content)
	}
}

type delayedReadCloser struct {
	delay   time.Duration
	content io.Reader
}

func (body *delayedReadCloser) Read(target []byte) (int, error) {
	time.Sleep(body.delay)
	return body.content.Read(target)
}

func (*delayedReadCloser) Close() error { return nil }
