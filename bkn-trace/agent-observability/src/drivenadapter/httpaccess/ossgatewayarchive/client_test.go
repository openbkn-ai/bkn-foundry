package ossgatewayarchive

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
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
