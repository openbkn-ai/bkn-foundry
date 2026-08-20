package opensearchprojection

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/infra/opensearch"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/iprojectionoutbox"
)

const receiptProjectionIndexMapping = `{
  "mappings": {
    "dynamic": true,
    "properties": {
      "receipt_id": {"type": "text", "fields": {"keyword": {"type": "keyword"}}},
      "conversation_id": {"type": "text", "fields": {"keyword": {"type": "keyword"}}},
      "interaction_id": {"type": "text", "fields": {"keyword": {"type": "keyword"}}},
      "request_id": {"type": "text", "fields": {"keyword": {"type": "keyword"}}},
      "trace_id": {"type": "text", "fields": {"keyword": {"type": "keyword"}}},
      "operation_id": {"type": "keyword"},
      "receipt_status": {"type": "text", "fields": {"keyword": {"type": "keyword"}}},
      "knowledge_network_ids": {"type": "text", "fields": {"keyword": {"type": "keyword"}}},
      "issued_at": {"type": "date"},
      "terminal_at": {"type": "date"},
      "owner": {
        "properties": {
          "tenant_id": {"type": "text", "fields": {"keyword": {"type": "keyword"}}},
          "business_domain_id": {"type": "text", "fields": {"keyword": {"type": "keyword"}}},
          "effective_subject_id": {"type": "text", "fields": {"keyword": {"type": "keyword"}}},
          "application_principal_id": {"type": "text", "fields": {"keyword": {"type": "keyword"}}}
        }
      }
    }
  }
}`

type Sink struct {
	client *opensearch.Client
	index  string
}

func New(client *opensearch.Client, index string) *Sink {
	return &Sink{client: client, index: index}
}

func (s *Sink) Project(ctx context.Context, item iprojectionoutbox.Item) error {
	if item.AggregateVersion == 0 {
		return iprojectionoutbox.Permanent(errors.New("projection aggregate version is required"))
	}
	_, err := s.client.IndexDocumentVersion(
		ctx, s.index, iprojectionoutbox.DocumentID(item),
		item.AggregateVersion, item.Payload,
	)
	if errors.Is(err, opensearch.ErrVersionConflict) {
		return nil
	}
	if opensearch.IsPermanentStatusError(err) {
		return iprojectionoutbox.Permanent(err)
	}
	return err
}

func (s *Sink) PrepareVersion(ctx context.Context, indexVersion string) error {
	return s.client.EnsureIndex(ctx, indexVersion, []byte(receiptProjectionIndexMapping))
}

// EnsureBootstrap creates the initial versioned index only when the configured
// projection alias does not exist. Writing directly to an alias name would let
// OpenSearch auto-create a concrete index and make later alias swaps fail.
func (s *Sink) EnsureBootstrap(ctx context.Context, indexVersion string) error {
	aliasExists, err := s.client.AliasExists(ctx, s.index)
	if err != nil {
		return fmt.Errorf("check projection alias: %w", err)
	}
	if aliasExists {
		return nil
	}
	indexExists, err := s.client.IndexExists(ctx, s.index)
	if err != nil {
		return fmt.Errorf("check projection index: %w", err)
	}
	if indexExists {
		return fmt.Errorf("projection index %q exists as a concrete index; expected an alias", s.index)
	}
	if err := s.PrepareVersion(ctx, indexVersion); err != nil {
		return fmt.Errorf("prepare initial projection index: %w", err)
	}
	if err := s.SwitchAlias(ctx, s.index, indexVersion); err != nil {
		return fmt.Errorf("switch initial projection alias: %w", err)
	}
	return nil
}

func (s *Sink) ProjectVersion(ctx context.Context, indexVersion string, item iprojectionoutbox.Item) error {
	if item.AggregateVersion == 0 {
		return iprojectionoutbox.Permanent(errors.New("projection aggregate version is required"))
	}
	_, err := s.client.IndexDocumentVersion(
		ctx, indexVersion, iprojectionoutbox.DocumentID(item),
		item.AggregateVersion, item.Payload,
	)
	if errors.Is(err, opensearch.ErrVersionConflict) {
		return nil
	}
	if opensearch.IsPermanentStatusError(err) {
		return iprojectionoutbox.Permanent(err)
	}
	return err
}

func (s *Sink) ValidateVersion(
	ctx context.Context,
	indexVersion string,
	items []iprojectionoutbox.Item,
) error {
	type expectedDocument struct {
		version uint64
		body    []byte
	}
	expected := make(map[string]expectedDocument, len(items))
	versionPayloads := make(map[string]map[uint64][]byte, len(items))
	for _, item := range items {
		id := iprojectionoutbox.DocumentID(item)
		canonical, err := canonicalJSON(item.Payload)
		if err != nil {
			return fmt.Errorf("canonicalize expected projection %q: %w", id, err)
		}
		payloads, exists := versionPayloads[id]
		if !exists {
			payloads = make(map[uint64][]byte)
			versionPayloads[id] = payloads
		}
		if known, versionSeen := payloads[item.AggregateVersion]; versionSeen {
			if !bytes.Equal(canonical, known) {
				return fmt.Errorf(
					"projection contract conflict for document %q at aggregate version %d",
					id,
					item.AggregateVersion,
				)
			}
		} else {
			payloads[item.AggregateVersion] = canonical
		}
		current, exists := expected[id]
		if !exists || item.AggregateVersion > current.version {
			expected[id] = expectedDocument{
				version: item.AggregateVersion,
				body:    canonical,
			}
		}
	}
	ids := make([]string, 0, len(expected))
	for id := range expected {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	documents, err := s.client.MultiGetDocuments(ctx, indexVersion, ids)
	if err != nil {
		return err
	}
	if len(documents) != len(expected) {
		return fmt.Errorf(
			"projection validation returned %d documents for %d aggregates",
			len(documents), len(expected),
		)
	}
	seen := make(map[string]struct{}, len(documents))
	for _, document := range documents {
		if _, duplicate := seen[document.ID]; duplicate {
			return fmt.Errorf("projection returned duplicate document %q", document.ID)
		}
		seen[document.ID] = struct{}{}
		if !document.Found {
			return fmt.Errorf("projection document %q was not found", document.ID)
		}
		canonical, err := canonicalJSON(document.Source)
		if err != nil {
			return fmt.Errorf("canonicalize projected document %q: %w", document.ID, err)
		}
		expectedDocument, ok := expected[document.ID]
		if !ok {
			return fmt.Errorf("projection returned unexpected document %q", document.ID)
		}
		if !bytes.Equal(canonical, expectedDocument.body) {
			return fmt.Errorf("projection document %q content does not match", document.ID)
		}
		delete(expected, document.ID)
	}
	if len(expected) != 0 {
		return fmt.Errorf("projection validation omitted %d documents", len(expected))
	}
	return nil
}

func canonicalJSON(body []byte) ([]byte, error) {
	var value any
	if err := json.Unmarshal(body, &value); err != nil {
		return nil, err
	}
	return json.Marshal(value)
}

func (s *Sink) CountVersion(ctx context.Context, indexVersion string) (uint64, error) {
	if err := s.client.RefreshIndex(ctx, indexVersion); err != nil {
		return 0, err
	}
	return s.client.CountDocuments(ctx, indexVersion)
}

func (s *Sink) SwitchAlias(ctx context.Context, alias, indexVersion string) error {
	return s.client.SwitchAlias(ctx, alias, indexVersion)
}
