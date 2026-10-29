package adapter

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestECEPHRejectsUnknownCertificateBeforeSendingCredentials(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); w.WriteHeader(200) }))
	defer server.Close()
	client, err := NewMinIOAdapter(StorageConfig{VendorType: VendorECEPH, Endpoint: server.URL, UseSSL: true, AccessKeyID: "test-key", AccessKeySecret: "test-secret"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.client.ListBuckets(context.Background())
	if err == nil || requests.Load() != 0 {
		t.Fatalf("untrusted ECEPH received credentials: err=%v requests=%d", err, requests.Load())
	}
}
