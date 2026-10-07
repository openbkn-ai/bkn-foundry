// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN

package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/projectionrebuildsvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/dbaccess/mariadb/sessionstore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/httpaccess/opensearchprojection"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/infra/opensearch"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/iprojectionrebuild"
)

// One-time upgrade command. Reuses the native authoritative projection builder;
// no historical reader, query switch, or alternative projection shape is added.
func rebuildCoreProjection(input io.Reader, output io.Writer) error {
	dsn := os.Getenv("BKN_TRACE_CORE_MARIADB_DSN")
	endpoint := os.Getenv("OPENSEARCH_ENDPOINT")
	alias := os.Getenv("BKN_TRACE_PROJECTION_INDEX")
	if dsn == "" || endpoint == "" || alias == "" {
		return fmt.Errorf("rebuild configuration missing")
	}
	if strings.ContainsAny(alias, " /#?\\") {
		return fmt.Errorf("invalid projection alias")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return fmt.Errorf("open native Core database failed")
	}
	defer db.Close()
	source := sessionstore.New(db)
	if err := source.EnsureSchema(ctx, false); err != nil {
		return fmt.Errorf("native Core schema check failed")
	}
	var settings struct {
		TLSVerify *bool  `json:"tls_verify"`
		CAPEM     string `json:"ca_pem"`
	}
	decoder := json.NewDecoder(io.LimitReader(input, 4<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&settings); err != nil && err != io.EOF {
		return fmt.Errorf("invalid rebuild transport settings")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	if value := os.Getenv("BKN_HISTORY_OPENSEARCH_TLS_VERIFY"); value != "" {
		verify, err := strconv.ParseBool(value)
		if err != nil {
			return fmt.Errorf("invalid TLS verification setting")
		}
		// Explicit operator option for customer deployments, never inferred from host.
		transport.TLSClientConfig.InsecureSkipVerify = !verify
	}
	if path := os.Getenv("BKN_HISTORY_OPENSEARCH_CA_FILE"); path != "" {
		body, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read configured CA bundle failed")
		}
		roots, err := x509.SystemCertPool()
		if err != nil || roots == nil {
			roots = x509.NewCertPool()
		}
		if !roots.AppendCertsFromPEM(body) {
			return fmt.Errorf("configured CA bundle contains no certificates")
		}
		transport.TLSClientConfig.RootCAs = roots
	}
	if settings.TLSVerify != nil {
		transport.TLSClientConfig.InsecureSkipVerify = !*settings.TLSVerify
	}
	if settings.CAPEM != "" {
		roots, err := x509.SystemCertPool()
		if err != nil || roots == nil {
			roots = x509.NewCertPool()
		}
		if !roots.AppendCertsFromPEM([]byte(settings.CAPEM)) {
			return fmt.Errorf("configured CA bundle contains no certificates")
		}
		transport.TLSClientConfig.RootCAs = roots
	}
	defer transport.CloseIdleConnections()
	auth := opensearch.AuthConfig{Enabled: os.Getenv("OPENSEARCH_AUTH_ENABLED") == "true",
		Username: os.Getenv("OPENSEARCH_AUTH_USERNAME"), Password: os.Getenv("OPENSEARCH_AUTH_PASSWORD")}
	client := opensearch.NewWithHTTPClient(endpoint, auth, &http.Client{Transport: transport, Timeout: 60 * time.Second})
	sink := opensearchprojection.New(client, alias)
	if count, err := verifyNativeCoreProjection(ctx, source, sink, alias); err == nil {
		return json.NewEncoder(output).Encode(map[string]any{"verified": true, "index_version": alias,
			"projected_count": count, "already_verified": true})
	}
	version := alias + "-history-" + strconv.FormatInt(time.Now().UTC().UnixNano(), 16)
	result, err := projectionrebuildsvc.New(source, sink, projectionrebuildsvc.Options{}).Rebuild(ctx, "core", alias, version)
	if err != nil {
		return fmt.Errorf("native Core projection rebuild failed")
	}
	return json.NewEncoder(output).Encode(map[string]any{"verified": true, "index_version": result.IndexVersion,
		"projected_count": result.ProjectedCount, "last_outbox_id": result.LastOutboxID})
}

func verifyNativeCoreProjection(ctx context.Context, source iprojectionrebuild.Source, target iprojectionrebuild.Target, index string) (uint64, error) {
	afterType, afterID := "", ""
	for {
		items, err := source.ScanAuthoritativeProjection(ctx, afterType, afterID, 500)
		if err != nil {
			return 0, err
		}
		if len(items) == 0 {
			break
		}
		if err := target.ValidateVersion(ctx, index, items); err != nil {
			return 0, err
		}
		last := items[len(items)-1]
		afterType, afterID = last.AggregateType, last.AggregateID
	}
	expected, err := source.CountAuthoritativeProjection(ctx)
	if err != nil {
		return 0, err
	}
	actual, err := target.CountVersion(ctx, index)
	if err != nil {
		return 0, err
	}
	if actual != expected {
		return 0, projectionrebuildsvc.ErrProjectionValidation
	}
	return actual, nil
}
