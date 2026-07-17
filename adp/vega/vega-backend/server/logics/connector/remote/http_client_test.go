package remote

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClientRequest(t *testing.T) {
	t.Run("sends json body and returns response bytes", func(t *testing.T) {
		client := &Client{httpClient: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			assert.Equal(t, http.MethodPost, r.Method)
			assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
			assert.Equal(t, "application/json", r.Header.Get("Accept"))
			body, err := io.ReadAll(r.Body)
			require.NoError(t, err)
			assert.JSONEq(t, `{"name":"orders"}`, string(body))
			return jsonResponse(http.StatusOK, `{"ok":true}`), nil
		})}}

		got, err := client.Request(context.Background(), http.MethodPost, "http://remote.local/resources", map[string]any{"name": "orders"})

		require.NoError(t, err)
		assert.JSONEq(t, `{"ok":true}`, string(got))
	})

	t.Run("omits content type when body is nil", func(t *testing.T) {
		client := &Client{httpClient: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			assert.Empty(t, r.Header.Get("Content-Type"))
			assert.Equal(t, "application/json", r.Header.Get("Accept"))
			return jsonResponse(http.StatusOK, `[]`), nil
		})}}

		got, err := client.Request(context.Background(), http.MethodGet, "http://remote.local/resources", nil)

		require.NoError(t, err)
		assert.JSONEq(t, `[]`, string(got))
	})

	t.Run("returns marshal error", func(t *testing.T) {
		client := &Client{httpClient: http.DefaultClient}

		got, err := client.Request(context.Background(), http.MethodPost, "http://example.com", map[string]any{"fn": func() {}})

		require.Error(t, err)
		assert.Nil(t, got)
		assert.Contains(t, err.Error(), "failed to marshal request body")
	})

	t.Run("returns request creation error", func(t *testing.T) {
		client := &Client{httpClient: http.DefaultClient}

		got, err := client.Request(context.Background(), http.MethodGet, "://bad-url", nil)

		require.Error(t, err)
		assert.Nil(t, got)
		assert.Contains(t, err.Error(), "failed to create request")
	})

	t.Run("returns status error with body", func(t *testing.T) {
		client := &Client{httpClient: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			return jsonResponse(http.StatusBadGateway, "upstream down"), nil
		})}}

		got, err := client.Request(context.Background(), http.MethodGet, "http://remote.local/resources", nil)

		require.Error(t, err)
		assert.Nil(t, got)
		assert.Contains(t, err.Error(), "status 502")
		assert.Contains(t, err.Error(), "upstream down")
	})
}

func TestClientGet(t *testing.T) {
	t.Run("delegates to request with get method", func(t *testing.T) {
		client := &Client{httpClient: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			assert.Equal(t, http.MethodGet, r.Method)
			return jsonResponse(http.StatusOK, `{"method":"GET"}`), nil
		})}}

		got, err := client.Get(context.Background(), "http://remote.local/resources")

		require.NoError(t, err)
		assert.JSONEq(t, `{"method":"GET"}`, string(got))
	})
}

func TestClientPost(t *testing.T) {
	t.Run("delegates to request with post method", func(t *testing.T) {
		client := &Client{httpClient: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			assert.Equal(t, http.MethodPost, r.Method)
			return jsonResponse(http.StatusOK, `{"method":"POST"}`), nil
		})}}

		got, err := client.Post(context.Background(), "http://remote.local/resources", map[string]any{"ok": true})

		require.NoError(t, err)
		assert.JSONEq(t, `{"method":"POST"}`, string(got))
	})
}

func TestClientDelete(t *testing.T) {
	t.Run("delegates to request with delete method", func(t *testing.T) {
		client := &Client{httpClient: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			assert.Equal(t, http.MethodDelete, r.Method)
			return jsonResponse(http.StatusOK, `{"method":"DELETE"}`), nil
		})}}

		got, err := client.Delete(context.Background(), "http://remote.local/resources")

		require.NoError(t, err)
		assert.JSONEq(t, `{"method":"DELETE"}`, string(got))
	})
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func jsonResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}
}
