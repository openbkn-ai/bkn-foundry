// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package opensearch

import (
	"context"
	"errors"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

type boundedTransport func(*http.Request) (*http.Response, error)

func (f boundedTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type trackedBody struct {
	io.Reader
	closed bool
	read   int64
}

func (b *trackedBody) Read(p []byte) (int, error) {
	n, e := b.Reader.Read(p)
	b.read += int64(n)
	return n, e
}
func (b *trackedBody) Close() error { b.closed = true; return nil }
func TestGetDocumentBoundedLimitsAllResponses(t *testing.T) {
	for _, status := range []int{200, 404, 500} {
		for _, size := range []int{16, 17, 1000000} {
			t.Run(http.StatusText(status)+"/"+strconv.Itoa(size), func(t *testing.T) {
				body := &trackedBody{Reader: strings.NewReader(strings.Repeat("x", size))}
				c := NewWithHTTPClient("http://example.test", AuthConfig{}, &http.Client{Transport: boundedTransport(func(r *http.Request) (*http.Response, error) {
					return &http.Response{StatusCode: status, Body: body, ContentLength: 1, Header: make(http.Header)}, nil
				})})
				doc, n, err := c.GetDocumentBounded(context.Background(), "artifacts", "a", 16)
				want := int64(size)
				if want > 17 {
					want = 17
				}
				if n != want || body.read != want || !body.closed {
					t.Fatalf("read=%d body=%d closed=%v", n, body.read, body.closed)
				}
				if size > 16 && !errors.Is(err, ErrResponseReadBudget) {
					t.Fatalf("expected budget error: %v", err)
				}
				if size == 16 && errors.Is(err, ErrResponseReadBudget) {
					t.Fatalf("exact size is within budget")
				}
				if len(doc.Source) != 0 {
					t.Fatal("failed response returned source")
				}
			})
		}
	}
}
func TestGetDocumentBoundedRejectsInvalidBudgetBeforeRequest(t *testing.T) {
	c := NewWithHTTPClient("http://example.test", AuthConfig{}, &http.Client{Transport: boundedTransport(func(*http.Request) (*http.Response, error) { t.Fatal("unexpected request"); return nil, nil })})
	for _, limit := range []int64{-1, 0, math.MaxInt64} {
		_, n, e := c.GetDocumentBounded(context.Background(), "i", "a", limit)
		if n != 0 || !errors.Is(e, ErrInvalidReadBudget) {
			t.Fatalf("limit %d: %d %v", limit, n, e)
		}
	}
}
func TestGetDocumentBoundedSuccessfulExactResponse(t *testing.T) {
	raw := `{"_source":{"n":9007199254740993},"_seq_no":2,"_primary_term":3}`
	c := NewWithHTTPClient("http://example.test", AuthConfig{Enabled: true, Username: "reader", Password: "test"}, &http.Client{Transport: boundedTransport(func(r *http.Request) (*http.Response, error) {
		u, p, ok := r.BasicAuth()
		if r.Method != "GET" || r.URL.Path != "/i/_doc/a" || !ok || u != "reader" || p != "test" {
			t.Fatal("incorrect read request")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(raw)), Header: make(http.Header)}, nil
	})})
	d, n, e := c.GetDocumentBounded(context.Background(), "i", "a", int64(len(raw)))
	if e != nil || n != int64(len(raw)) || string(d.Source) != `{"n":9007199254740993}` || d.SeqNo != 2 || d.PrimaryTerm != 3 {
		t.Fatalf("result=%+v bytes=%d err=%v", d, n, e)
	}
}
func TestGetDocumentBoundedPreservesReadAndCancellationErrors(t *testing.T) {
	marker := errors.New("interrupted read")
	body := &trackedBody{Reader: io.MultiReader(strings.NewReader("abc"), boundedErrorReader{marker})}
	c := NewWithHTTPClient("http://example.test", AuthConfig{}, &http.Client{Transport: boundedTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: body, Header: make(http.Header)}, nil
	})})
	_, n, e := c.GetDocumentBounded(context.Background(), "i", "a", 10)
	if n != 3 || !errors.Is(e, marker) || !body.closed {
		t.Fatalf("n=%d e=%v closed=%v", n, e, body.closed)
	}
	c = NewWithHTTPClient("http://example.test", AuthConfig{}, &http.Client{Transport: boundedTransport(func(r *http.Request) (*http.Response, error) { return nil, r.Context().Err() })})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, n, e = c.GetDocumentBounded(ctx, "i", "a", 10)
	if n != 0 || !errors.Is(e, context.Canceled) {
		t.Fatalf("n=%d e=%v", n, e)
	}
}

type boundedErrorReader struct{ err error }

func (r boundedErrorReader) Read([]byte) (int, error) { return 0, r.err }

func TestGetDocumentBoundedCancellationWhileReadingHTTPBody(t *testing.T) {
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "abc")
		w.(http.Flusher).Flush()
		close(started)
		<-r.Context().Done()
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := New(server.URL, AuthConfig{}, time.Second)
	done := make(chan error, 1)
	go func() {
		document, n, err := client.GetDocumentBounded(ctx, "i", "a", 100)
		if len(document.Source) != 0 || n > 3 {
			done <- errors.New("cancel returned document or excessive byte count")
			return
		}
		done <- err
	}()
	<-started
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected cancellation, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("read did not observe cancellation")
	}
}
func TestGetDocumentBoundedStatusErrorDoesNotExposeBody(t *testing.T) {
	raw := "private source detail"
	client := NewWithHTTPClient("http://example.test", AuthConfig{}, &http.Client{Transport: boundedTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 500, Body: io.NopCloser(strings.NewReader(raw)), Header: make(http.Header)}, nil
	})})
	_, n, err := client.GetDocumentBounded(context.Background(), "i", "a", 100)
	var status *StatusError
	if n != int64(len(raw)) || !errors.As(err, &status) || status.StatusCode != 500 || strings.Contains(err.Error(), raw) || status.Body != "" {
		t.Fatalf("invalid status or exposed response: bytes=%d", n)
	}
}

func TestGetDocumentBoundedDoesNotFollowOrDrainRedirects(t *testing.T) {
	for _, status := range []int{301, 302, 303, 307, 308} {
		for _, size := range []int{3, 1000000} {
			t.Run(strconv.Itoa(status)+"/"+strconv.Itoa(size), func(t *testing.T) {
				body := &trackedBody{Reader: strings.NewReader(strings.Repeat("x", size))}
				calls, redirectChecks := 0, 0
				client := NewWithHTTPClient("http://example.test", AuthConfig{}, &http.Client{
					CheckRedirect: func(*http.Request, []*http.Request) error { redirectChecks++; return nil },
					Transport: boundedTransport(func(r *http.Request) (*http.Response, error) {
						calls++
						if r.URL.Path != "/i/_doc/a" {
							return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"_source":{}}`)), Header: make(http.Header)}, nil
						}
						return &http.Response{StatusCode: status, Body: body, ContentLength: -1, Header: http.Header{"Location": []string{"http://other.test/redirect-target"}}}, nil
					}),
				})
				doc, n, err := client.GetDocumentBounded(context.Background(), "i", "a", 10)
				wantRead := int64(size)
				if wantRead > 11 {
					wantRead = 11
				}
				if calls != 1 || redirectChecks != 0 || body.read != wantRead || n != wantRead || !body.closed || len(doc.Source) != 0 {
					t.Fatalf("calls=%d redirect checks=%d read=%d counted=%d closed=%v source bytes=%d", calls, redirectChecks, body.read, n, body.closed, len(doc.Source))
				}
				if size > 10 {
					if !errors.Is(err, ErrResponseReadBudget) {
						t.Fatalf("expected redirect body budget error: %v", err)
					}
				} else {
					var statusErr *StatusError
					if !errors.As(err, &statusErr) || statusErr.StatusCode != status {
						t.Fatalf("expected redirect status error: %v", err)
					}
				}
			})
		}
	}
}

func TestGetDocumentBoundedLeavesExistingRedirectPolicyUnchanged(t *testing.T) {
	redirectChecks := 0
	client := NewWithHTTPClient("http://example.test", AuthConfig{}, &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error { redirectChecks++; return nil },
		Transport: boundedTransport(func(r *http.Request) (*http.Response, error) {
			if r.URL.Path == "/i/_doc/a" {
				return &http.Response{StatusCode: 302, Body: io.NopCloser(strings.NewReader("redirect")), Header: http.Header{"Location": []string{"/target"}}}, nil
			}
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"_source":{"legacy":true}}`)), Header: make(http.Header)}, nil
		}),
	})
	_, _, _ = client.GetDocumentBounded(context.Background(), "i", "a", 100)
	if redirectChecks != 0 {
		t.Fatal("bounded read called shared redirect policy")
	}
	doc, err := client.GetDocument(context.Background(), "i", "a")
	if err != nil || string(doc.Source) != `{"legacy":true}` || redirectChecks != 1 {
		t.Fatalf("old API redirect behavior changed: checks=%d err=%v", redirectChecks, err)
	}
}
