package interfaces

import (
	"encoding/json"
	"testing"
)

func TestTLSReadFieldsDoNotAccidentallyChangeTrustOnUpdate(t *testing.T) {
	var mcp MCPServerUpdateRequest
	var box UpdateToolBoxReq
	readFields := []byte(`{"tls":{"fingerprint":"","skip_verify":true},"tls_policies":{}}`)
	if err := json.Unmarshal(readFields, &mcp); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(readFields, &box); err != nil {
		t.Fatal(err)
	}
	if mcp.TLS != nil || box.TLS != nil {
		t.Fatal("forwarding read-only response fields changed trust")
	}
	change := []byte(`{"tls_change":{"expected":{"fingerprint":"","skip_verify":false},"policy":{"fingerprint":"","skip_verify":true}}}`)
	if err := json.Unmarshal(change, &mcp); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(change, &box); err != nil {
		t.Fatal(err)
	}
	if mcp.TLS == nil || box.TLS == nil || mcp.TLS.Expected == nil || !mcp.TLS.Policy.SkipVerify || !box.TLS.Policy.SkipVerify {
		t.Fatal("explicit trust change was not decoded")
	}
}
