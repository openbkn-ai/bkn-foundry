// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package mcp

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMetricAppResourceIsAdvertisedOnlyOnFullProfile(t *testing.T) {
	html := "<!DOCTYPE html><html><body>metric app</body></html>"
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/studio/metric-analysis-mcp.html" {
			http.NotFound(w, r)
			return
		}
		_, _ = fmt.Fprint(w, html)
	}))
	defer host.Close()
	t.Setenv(metricAppEnv, host.URL+"/studio/metric-analysis-mcp.html")

	full, _ := newMCPServerForProfile(nil, "zh-CN", defaultPTCServicePort, fullProfile)
	var listed struct {
		Tools []struct {
			Name string         `json:"name"`
			Meta map[string]any `json:"_meta"`
		} `json:"tools"`
	}
	resultOf(t, rpc(t, full, "tools/list", map[string]any{}), &listed)
	var found bool
	for _, tool := range listed.Tools {
		if tool.Name != toolKeyQueryMetric {
			continue
		}
		ui, _ := tool.Meta["ui"].(map[string]any)
		if ui["resourceUri"] != metricAppURI {
			t.Fatalf("query_metric UI metadata = %#v", tool.Meta)
		}
		found = true
	}
	if !found {
		t.Fatal("query_metric is missing")
	}

	var resources struct {
		Resources []struct {
			URI      string `json:"uri"`
			MIMEType string `json:"mimeType"`
		} `json:"resources"`
	}
	resultOf(t, rpc(t, full, "resources/list", map[string]any{}), &resources)
	if len(resources.Resources) != 1 || resources.Resources[0].URI != metricAppURI ||
		resources.Resources[0].MIMEType != metricAppMIMEType {
		t.Fatalf("unexpected MCP resources: %#v", resources.Resources)
	}
	var read struct {
		Contents []struct {
			URI      string         `json:"uri"`
			MIMEType string         `json:"mimeType"`
			Text     string         `json:"text"`
			Meta     map[string]any `json:"_meta"`
		} `json:"contents"`
	}
	resultOf(t, rpc(t, full, "resources/read", map[string]any{"uri": metricAppURI}), &read)
	if len(read.Contents) != 1 || read.Contents[0].Text != html ||
		read.Contents[0].MIMEType != metricAppMIMEType || read.Contents[0].Meta["ui"] == nil {
		t.Fatalf("unexpected MCP App content: %#v", read.Contents)
	}

	compact, _ := newMCPServerForProfile(nil, "zh-CN", defaultPTCServicePort, compactProfile)
	listed = struct {
		Tools []struct {
			Name string         `json:"name"`
			Meta map[string]any `json:"_meta"`
		} `json:"tools"`
	}{}
	resultOf(t, rpc(t, compact, "tools/list", map[string]any{}), &listed)
	for _, tool := range listed.Tools {
		if tool.Name == toolKeyQueryMetric && tool.Meta["ui"] != nil {
			t.Fatalf("compact query_metric must not advertise UI: %#v", tool.Meta)
		}
	}
	if len(compact.ListResources()) != 0 {
		t.Fatal("compact profile must not register the metric app resource")
	}
}

func TestMetricAppResourceIsOptionalAndRejectsInvalidSource(t *testing.T) {
	for _, value := range []string{"", "file:///tmp/metric-analysis-mcp.html", "http://user:pass@example.com/metric-analysis-mcp.html", "http://example.com/other.html"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv(metricAppEnv, value)
			full, _ := newMCPServerForProfile(nil, "zh-CN", defaultPTCServicePort, fullProfile)
			if len(full.ListResources()) != 0 {
				t.Fatal("invalid or absent UI source must not advertise a resource")
			}
			var listed struct {
				Tools []struct {
					Name string         `json:"name"`
					Meta map[string]any `json:"_meta"`
				} `json:"tools"`
			}
			resultOf(t, rpc(t, full, "tools/list", map[string]any{}), &listed)
			for _, tool := range listed.Tools {
				if tool.Name == toolKeyQueryMetric && tool.Meta["ui"] != nil {
					t.Fatalf("unexpected metric UI metadata: %#v", tool.Meta)
				}
			}
		})
	}
}

func TestMetricAppResourceRejectsHTMLRedirectAndOversize(t *testing.T) {
	for _, response := range []struct {
		name   string
		status int
		body   string
	}{
		{name: "redirect", status: http.StatusFound, body: "redirect"},
		{name: "oversize", status: http.StatusOK, body: "<!doctype html>" + strings.Repeat("x", metricAppMaxBytes)},
	} {
		t.Run(response.name, func(t *testing.T) {
			host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/redirect-target" {
					_, _ = fmt.Fprint(w, "<!doctype html><html></html>")
					return
				}
				if response.status == http.StatusFound {
					w.Header().Set("Location", "/redirect-target")
				}
				w.WriteHeader(response.status)
				_, _ = fmt.Fprint(w, response.body)
			}))
			defer host.Close()
			t.Setenv(metricAppEnv, host.URL+"/metric-analysis-mcp.html")
			srv, _ := newMCPServerForProfile(nil, "zh-CN", defaultPTCServicePort, fullProfile)
			got := rpc(t, srv, "resources/read", map[string]any{"uri": metricAppURI})
			if !strings.Contains(fmt.Sprint(got), "metric app resource") {
				t.Fatalf("expected resource failure, got %#v", got)
			}
		})
	}
}

func TestMetricAppResourceDoesNotExposeSourceAddressOnFetchError(t *testing.T) {
	host := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	address := host.URL + "/metric-analysis-mcp.html"
	host.Close()
	t.Setenv(metricAppEnv, address)
	srv, _ := newMCPServerForProfile(nil, "zh-CN", defaultPTCServicePort, fullProfile)
	got := fmt.Sprint(rpc(t, srv, "resources/read", map[string]any{"uri": metricAppURI}))
	if !strings.Contains(got, "metric app resource unavailable") || strings.Contains(got, host.URL) {
		t.Fatalf("resource error leaked source address or lost public message: %s", got)
	}
}
