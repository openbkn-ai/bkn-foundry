package gokb

import (
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"database/sql/driver"
	"encoding/binary"
	"encoding/pem"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/db/driver/kingbase/gokb/oid/mysqlOid"
)

func TestTextDecodeRejectsIntegerOverflow(t *testing.T) {
	cn := conn{allOid: mysqlOid.MysqlOid, databaseMode: "mysql"}
	tests := []struct {
		name  string
		value string
	}{
		{name: "uint4", value: "4294967296"},
		{name: "signed tinyint", value: "128"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("expected integer overflow to be rejected")
				}
			}()
			typ := cn.allOid.T_uint4
			if test.name == "signed tinyint" {
				typ = cn.allOid.T_tinyint
			}
			textDecode(&parameterStatus{}, []byte(test.value), typ, cn)
		})
	}
}

func TestTextDecodePreservesIntegerBoundaries(t *testing.T) {
	cn := conn{allOid: mysqlOid.MysqlOid, databaseMode: "mysql"}
	if got := textDecode(&parameterStatus{}, []byte("4294967295"), cn.allOid.T_uint4, cn); got != uint32(4294967295) {
		t.Fatalf("uint4 = %v", got)
	}
	if got := textDecode(&parameterStatus{}, []byte("-128"), cn.allOid.T_tinyint, cn); got != int8(-128) {
		t.Fatalf("tinyint = %v", got)
	}
}

func TestParseOutValuesRejectsNarrowingOverflow(t *testing.T) {
	cn := conn{allOid: mysqlOid.MysqlOid, databaseMode: "mysql"}
	var destination uint8
	message := outParameterMessage("256")
	err := cn.ParseOutValues(
		&message,
		[]driver.Value{sql.Out{Dest: &destination}},
		[]fieldDesc{{OID: cn.allOid.T_uint8}},
	)
	if err == nil || !strings.Contains(err.Error(), "value out of range") {
		t.Fatalf("ParseOutValues() error = %v, want range error", err)
	}
}

func TestParseOutValuesAssignsSignedTinyNumeric(t *testing.T) {
	cn := conn{allOid: mysqlOid.MysqlOid, databaseMode: "mysql"}
	var destination int8
	message := outParameterMessage("-128")
	err := cn.ParseOutValues(
		&message,
		[]driver.Value{sql.Out{Dest: &destination}},
		[]fieldDesc{{OID: cn.allOid.T_numeric}},
	)
	if err != nil {
		t.Fatalf("ParseOutValues() error = %v", err)
	}
	if destination != -128 {
		t.Fatalf("destination = %d, want -128", destination)
	}
}

func TestLegacyMD5AuthenticationIsRejected(t *testing.T) {
	message := make([]byte, 4)
	binary.BigEndian.PutUint32(message, 5)
	read := readBuf(message)

	defer func() {
		got := recover()
		if got == nil || !strings.Contains(got.(error).Error(), "legacy MD5 authentication is disabled") {
			t.Fatalf("auth() panic = %v", got)
		}
	}()
	new(conn).auth(&read, values{})
}

func TestSSLVerifiesPrivateCertificateAuthority(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer server.Close()

	handler, err := ssl(values{
		"sslmode":     "verify-full",
		"host":        "127.0.0.1",
		"sslrootcert": writeServerCA(t, server),
	})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := net.Dial("tcp", server.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	tlsConnection, err := handler(raw)
	if err != nil {
		t.Fatal(err)
	}
	if err := tlsConnection.(*tls.Conn).Handshake(); err != nil {
		t.Fatalf("TLS handshake failed with trusted private CA: %v", err)
	}
}

func TestSSLRejectsUntrustedCertificate(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer server.Close()

	handler, err := ssl(values{"sslmode": "require", "host": "127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	if err := handshakeWithServer(t, handler, server); err == nil {
		t.Fatal("TLS handshake succeeded with an untrusted certificate")
	}
}

func TestSSLRejectsMismatchedHostname(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer server.Close()

	handler, err := ssl(values{
		"sslmode":     "verify-ca",
		"host":        "wrong.example",
		"sslrootcert": writeServerCA(t, server),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := handshakeWithServer(t, handler, server); err == nil {
		t.Fatal("TLS handshake succeeded with a mismatched hostname")
	}
}

func writeServerCA(t *testing.T, server *httptest.Server) string {
	t.Helper()
	certificate := server.Certificate()
	if _, err := certificate.Verify(x509.VerifyOptions{Roots: server.Client().Transport.(*http.Transport).TLSClientConfig.RootCAs}); err != nil {
		t.Fatalf("test certificate setup failed: %v", err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate.Raw})
	caFile := t.TempDir() + "/ca.pem"
	if err := os.WriteFile(caFile, pemBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	return caFile
}

func handshakeWithServer(t *testing.T, handler func(net.Conn) (net.Conn, error), server *httptest.Server) error {
	t.Helper()
	raw, err := net.Dial("tcp", server.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	tlsConnection, err := handler(raw)
	if err != nil {
		return err
	}
	return tlsConnection.(*tls.Conn).Handshake()
}

func outParameterMessage(value string) readBuf {
	message := make([]byte, 2+4+len(value))
	binary.BigEndian.PutUint16(message[0:2], 1)
	binary.BigEndian.PutUint32(message[2:6], uint32(len(value)))
	copy(message[6:], value)
	return readBuf(message)
}
