// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package anyshare

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/bytedance/sonic"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
)

func TestAnyShareConnectorMetadata(t *testing.T) {
	t.Run("any share connector metadata", func(t *testing.T) {
		connector := &AnyShareConnector{}

		assert.Equal(t, interfaces.ConnectorTypeAnyShare, connector.GetType())
		assert.Equal(t, interfaces.ConnectorTypeAnyShare, connector.GetName())
		assert.Equal(t, interfaces.ConnectorModeLocal, connector.GetMode())
		assert.Equal(t, interfaces.ConnectorCategoryFileset, connector.GetCategory())
		assert.Equal(t, []string{"token", "app_secret"}, connector.GetSensitiveFields())

		assert.False(t, connector.GetEnabled())
		connector.SetEnabled(true)
		assert.True(t, connector.GetEnabled())

		fields := connector.GetFieldConfig()
		assert.Equal(t, map[string]interfaces.ConnectorFieldConfig{
			"protocol":     {Name: "Protocol", Type: "string", Description: "http or https", Required: true, Encrypted: false},
			"host":         {Name: "Host", Type: "string", Description: "AnyShare server host", Required: true, Encrypted: false},
			"port":         {Name: "Port", Type: "integer", Description: "Server port", Required: true, Encrypted: false},
			"auth_type":    {Name: "Authentication type", Type: "integer", Description: "1=access token, 2=AppID/AppSecret", Required: true, Encrypted: false},
			"token":        {Name: "Access token", Type: "string", Description: "Required when auth_type=1", Required: false, Encrypted: true},
			"app_id":       {Name: "Application account ID", Type: "string", Description: "Required when auth_type=2", Required: false, Encrypted: false},
			"app_secret":   {Name: "Application secret", Type: "string", Description: "Required when auth_type=2", Required: false, Encrypted: true},
			"doc_lib_type": {Name: "Library type", Type: "integer", Description: "1=knowledge library, 2=document library", Required: true, Encrypted: false},
			"paths":        {Name: "Paths", Type: "array", Description: "Optional starting paths resolved by library name; leave empty to enumerate libraries of the selected type", Required: false, Encrypted: false},
		}, fields)
		require.Contains(t, fields, "token")
		assert.True(t, fields["token"].Encrypted)
		require.Contains(t, fields, "app_secret")
		assert.True(t, fields["app_secret"].Encrypted)
		require.Contains(t, fields, "paths")
		assert.False(t, fields["paths"].Required)
	})
}

func TestAnyShareConnectorNew(t *testing.T) {
	builder := &AnyShareConnector{}

	t.Run("token config success", func(t *testing.T) {
		connector, err := builder.New(interfaces.ConnectorConfig{
			"protocol":     "https",
			"host":         "anyshare.local",
			"port":         443,
			"auth_type":    authTypeToken,
			"token":        "token",
			"doc_lib_type": docLibTypeKnowledge,
			"paths":        []string{"/docs"},
		})

		require.NoError(t, err)
		require.IsType(t, &AnyShareConnector{}, connector)

		anyshareConnector := connector.(*AnyShareConnector)
		require.NotNil(t, anyshareConnector.config)
		assert.Equal(t, "https://anyshare.local:443", anyshareConnector.baseURL)
		assert.Equal(t, []string{"/docs"}, anyshareConnector.config.Paths)
		assert.Equal(t, httpTimeout, anyshareConnector.httpClient.Timeout)
	})

	tests := []struct {
		name    string
		cfg     interfaces.ConnectorConfig
		wantErr string
	}{
		{
			name: "invalid protocol",
			cfg: interfaces.ConnectorConfig{
				"protocol":     "ftp",
				"host":         "anyshare.local",
				"port":         443,
				"auth_type":    authTypeToken,
				"token":        "token",
				"doc_lib_type": docLibTypeKnowledge,
			},
			wantErr: "protocol must be http or https",
		},
		{
			name: "missing host",
			cfg: interfaces.ConnectorConfig{
				"protocol":     "https",
				"port":         443,
				"auth_type":    authTypeToken,
				"token":        "token",
				"doc_lib_type": docLibTypeKnowledge,
			},
			wantErr: "host and port are required",
		},
		{
			name: "invalid port",
			cfg: interfaces.ConnectorConfig{
				"protocol":     "https",
				"host":         "anyshare.local",
				"port":         PORT_MAX + 1,
				"auth_type":    authTypeToken,
				"token":        "token",
				"doc_lib_type": docLibTypeKnowledge,
			},
			wantErr: "out of valid range",
		},
		{
			name: "missing token",
			cfg: interfaces.ConnectorConfig{
				"protocol":     "https",
				"host":         "anyshare.local",
				"port":         443,
				"auth_type":    authTypeToken,
				"doc_lib_type": docLibTypeKnowledge,
			},
			wantErr: "token is required",
		},
		{
			name: "missing app secret",
			cfg: interfaces.ConnectorConfig{
				"protocol":     "https",
				"host":         "anyshare.local",
				"port":         443,
				"auth_type":    authTypeAppSecret,
				"app_id":       "app",
				"doc_lib_type": docLibTypeKnowledge,
			},
			wantErr: "app_id and app_secret are required",
		},
		{
			name: "invalid doc lib type",
			cfg: interfaces.ConnectorConfig{
				"protocol":     "https",
				"host":         "anyshare.local",
				"port":         443,
				"auth_type":    authTypeToken,
				"token":        "token",
				"doc_lib_type": 99,
			},
			wantErr: "doc_lib_type must be 1",
		},
		{
			name: "duplicate paths",
			cfg: interfaces.ConnectorConfig{
				"protocol":     "https",
				"host":         "anyshare.local",
				"port":         443,
				"auth_type":    authTypeToken,
				"token":        "token",
				"doc_lib_type": docLibTypeKnowledge,
				"paths":        []string{"/docs", "/docs"},
			},
			wantErr: "duplicate element",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			connector, err := builder.New(tt.cfg)

			require.Error(t, err)
			assert.Nil(t, connector)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestAnyShareConnectorConnectAndMetadata(t *testing.T) {
	t.Run("token auth does not call remote server", func(t *testing.T) {
		connector := &AnyShareConnector{
			config: &anyshareConfig{
				Protocol:   "https",
				Host:       "anyshare.local",
				Port:       443,
				AuthType:   authTypeToken,
				Token:      "Bearer existing",
				DocLibType: docLibTypeKnowledge,
				Paths:      []string{"/docs"},
			},
		}

		metadata, err := connector.GetMetadata(context.Background())

		require.NoError(t, err)
		assert.True(t, connector.connected)
		assert.Equal(t, "Bearer existing", connector.authHeader)
		assert.Equal(t, interfaces.ConnectorTypeAnyShare, metadata["connector"])
		assert.Equal(t, true, metadata["paths_configured"])
	})

	t.Run("app secret fetches oauth token", func(t *testing.T) {
		connector := newConnectedTestConnector()
		connector.connected = false
		connector.config.AuthType = authTypeAppSecret
		connector.config.AppID = "app"
		connector.config.AppSecret = "secret"
		connector.httpClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			assert.Equal(t, "/oauth2/token", r.URL.Path)
			user, password, ok := r.BasicAuth()
			assert.True(t, ok)
			assert.Equal(t, "app", user)
			assert.Equal(t, "secret", password)
			return jsonResponse(http.StatusOK, `{"access_token":"remote-token","expires_in":3600}`), nil
		})}

		require.NoError(t, connector.Connect(context.Background()))
		assert.Equal(t, "Bearer remote-token", connector.authHeader)
	})
}

func TestAnyShareConnectorListFilesetsFromEntryDocLib(t *testing.T) {
	t.Run("any share connector list filesets from entry doc lib", func(t *testing.T) {
		connector := newConnectedTestConnector()
		connector.httpClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			assert.Equal(t, "/api/document/v1/entry-doc-lib", r.URL.Path)
			assert.Equal(t, "knowledge_doc_lib", r.URL.Query().Get("type"))
			assert.Equal(t, "Bearer token", r.Header.Get("Authorization"))

			return jsonResponse(http.StatusOK, `[{
				"id":"lib-1",
				"name":"Knowledge",
				"type":"knowledge_doc_lib",
				"rev":"1",
				"created_at":"2026-01-01",
				"modified_at":"2026-01-02",
				"created_by":{"id":"u1","name":"Alice","type":"user"},
				"modified_by":{"id":"u2","name":"Bob","type":"user"}
			}]`), nil
		})}

		filesets, err := connector.ListFilesets(context.Background())

		require.NoError(t, err)
		require.Len(t, filesets, 1)
		assert.Equal(t, "lib-1", filesets[0].ID)
		assert.Equal(t, "Knowledge", filesets[0].Name)
		assert.Equal(t, "Knowledge", filesets[0].DisplayPath)
		assert.Len(t, filesets[0].Columns, 20)
		for _, column := range filesets[0].Columns {
			require.NotNil(t, column.Filterable)
			assert.Equal(t, DefaultFilterable(column.Type), *column.Filterable)
		}
		assert.Equal(t, "1", filesets[0].SourceMetadata["rev"])
	})
}

func TestAnyShareConnectorSearchFiles(t *testing.T) {
	t.Run("any share connector search files", func(t *testing.T) {
		connector := newConnectedTestConnector()
		connector.httpClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			assert.Equal(t, "/api/ecosearch/v1/file-search", r.URL.Path)
			assert.Equal(t, "Bearer token", r.Header.Get("Authorization"))

			var payload map[string]any
			decodeAnyShareRequestJSON(t, r, &payload)
			assert.Equal(t, "needle", payload["keyword"])
			assert.Equal(t, []any{"doc-1/*"}, payload["range"])
			assert.Equal(t, float64(10), payload["rows"])
			assert.Equal(t, float64(2), payload["start"])
			assert.Equal(t, []any{"basename"}, payload["dimension"])
			assert.Equal(t, "semantic", payload["model"])

			return jsonResponse(http.StatusOK, `{"files":[{"doc_id":"1","basename":"a.txt","content":"hidden"}]}`), nil
		})}

		files, err := connector.SearchFiles(
			context.Background(),
			"doc-1",
			"needle",
			[]string{"basename"},
			"semantic",
			[]map[string]interface{}{{"created_at": map[string]any{"gte": 1}}},
			map[string]interface{}{"extension": []string{"txt"}},
			10,
			2,
			map[string]interface{}{"field": "created_at", "sort_type": "desc"},
			[]string{"doc_id", "basename"},
		)

		require.NoError(t, err)
		require.Len(t, files, 1)
		assert.Equal(t, map[string]any{"doc_id": "1", "basename": "a.txt"}, files[0])
	})
}

func TestAnyShareQueryHelpers(t *testing.T) {
	t.Run("process sort params", func(t *testing.T) {
		sort, err := processSortParams([]*interfaces.SortField{{Field: "created_at", Direction: "desc"}})
		require.NoError(t, err)
		assert.Equal(t, map[string]interface{}{"field": "created_at", "sort_type": "desc"}, sort)

		sort, err = processSortParams([]*interfaces.SortField{{Field: "created_at", Direction: "DESC"}})
		require.NoError(t, err)
		assert.Equal(t, map[string]interface{}{"field": "created_at", "sort_type": "desc"}, sort)

		sort, err = processSortParams([]*interfaces.SortField{{Field: "modified_at"}})
		require.NoError(t, err)
		assert.Equal(t, "asc", sort["sort_type"])

		sort, err = processSortParams([]*interfaces.SortField{nil})
		require.NoError(t, err)
		assert.Nil(t, sort)
	})

	t.Run("rejects invalid sort params", func(t *testing.T) {
		_, err := processSortParams([]*interfaces.SortField{
			{Field: "created_at"},
			{Field: "modified_at"},
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "only one sort field")

		_, err = processSortParams([]*interfaces.SortField{{Field: "name"}})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid sort field")

		_, err = processSortParams([]*interfaces.SortField{{Field: "created_at", Direction: "sideways"}})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid sort type")
	})

	t.Run("validates and filters output fields", func(t *testing.T) {
		require.NoError(t, validateOutputFields([]string{"doc_id", "basename"}))
		require.Error(t, validateOutputFields([]string{"bad"}))

		files := []map[string]any{{"doc_id": "1", "basename": "a.txt", "content": "hidden"}}
		assert.Equal(t, []map[string]any{{"doc_id": "1"}}, filterOutputFields(files, []string{"doc_id"}))
		assert.Equal(t, files, filterOutputFields(files, []string{"*"}))
	})

	t.Run("time and integer helpers", func(t *testing.T) {
		assert.Equal(t, int64(2000), convertTimeValue(2))
		assert.Equal(t, int64(-1), convertTimeValue(-1))

		got, err := toInt64(float64(10))
		require.NoError(t, err)
		assert.Equal(t, int64(10), got)

		got, err = toInt64(json.Number("1710000000000"))
		require.NoError(t, err)
		assert.Equal(t, int64(1710000000000), got)

		_, err = toInt64("10")
		require.Error(t, err)
	})
}

func TestAnyShareValidateDocLibType(t *testing.T) {
	tests := []struct {
		name       string
		configType int
		docLib     docLibDTO
		wantErr    string
	}{
		{
			name:       "knowledge matches",
			configType: docLibTypeKnowledge,
			docLib:     docLibDTO{Type: "knowledge_doc_lib"},
		},
		{
			name:       "document matches",
			configType: docLibTypeDocument,
			docLib: docLibDTO{
				Type: "custom_doc_lib",
				SubType: &docLibSubType{
					ID: customDocLibSubTypeDocumentId,
				},
			},
		},
		{
			name:       "wrong knowledge config",
			configType: docLibTypeDocument,
			docLib:     docLibDTO{Type: "knowledge_doc_lib"},
			wantErr:    "expects document lib",
		},
		{
			name:       "document missing subtype",
			configType: docLibTypeDocument,
			docLib:     docLibDTO{Type: "custom_doc_lib"},
			wantErr:    "missing subtype",
		},
		{
			name:       "unknown type",
			configType: docLibTypeDocument,
			docLib:     docLibDTO{Type: "other"},
			wantErr:    "unknown doc lib type",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			connector := &AnyShareConnector{config: &anyshareConfig{DocLibType: tt.configType}}

			err := connector.validateDocLibType(tt.docLib)
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func newConnectedTestConnector() *AnyShareConnector {
	return &AnyShareConnector{
		config: &anyshareConfig{
			Protocol:   "http",
			Host:       "anyshare.local",
			Port:       80,
			AuthType:   authTypeToken,
			Token:      "token",
			DocLibType: docLibTypeKnowledge,
		},
		connected:  true,
		httpClient: http.DefaultClient,
		baseURL:    "http://anyshare.local",
		authHeader: "Bearer token",
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func jsonResponse(statusCode int, body string) *http.Response {
	return &http.Response{
		StatusCode: statusCode,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}
}

func decodeAnyShareRequestJSON(t *testing.T, r *http.Request, out any) {
	t.Helper()

	body, err := io.ReadAll(r.Body)
	require.NoError(t, err)
	require.NoError(t, sonic.Unmarshal(body, out))
}

func TestAnyShareConnectorValidateFilterability(t *testing.T) {
	c := &AnyShareConnector{}
	flag := func(value bool) *bool { return &value }
	t.Run("closed or omitted flags do not inspect source metadata", func(t *testing.T) {
		resource := &interfaces.Resource{SourceMetadata: map[string]any{"columns": "corrupt"}, SchemaDefinition: []*interfaces.Property{{Name: "closed", Filterable: flag(false)}, {Name: "legacy"}}}
		require.NoError(t, c.ValidateFilterability(resource))
	})
	t.Run("trusted fileset columns override stale properties and saved flags", func(t *testing.T) {
		for _, persisted := range []bool{false, true} {
			t.Run(map[bool]string{false: "discovery", true: "persisted JSON"}[persisted], func(t *testing.T) {
				resource := &interfaces.Resource{SourceMetadata: map[string]any{"columns": []interfaces.FilesetColumnMeta{{Name: "native_path", Type: interfaces.DataType_String, Filterable: flag(false)}}}, SchemaDefinition: []*interfaces.Property{{Name: "public_path", OriginalName: "native_path", OriginalType: "unsupported", Filterable: flag(true)}, {Name: "closed", Filterable: flag(false)}, {Name: "legacy"}}}
				if persisted {
					encoded, err := json.Marshal(resource)
					require.NoError(t, err)
					var stored interfaces.Resource
					require.NoError(t, json.Unmarshal(encoded, &stored))
					resource = &stored
				}
				before, err := json.Marshal(resource)
				require.NoError(t, err)
				require.NoError(t, c.ValidateFilterability(resource))
				after, err := json.Marshal(resource)
				require.NoError(t, err)
				assert.JSONEq(t, string(before), string(after))
			})
		}
	})
	t.Run("property name binds source columns when original name is absent", func(t *testing.T) {
		require.NoError(t, c.ValidateFilterability(&interfaces.Resource{SourceMetadata: map[string]any{"columns": []interfaces.FilesetColumnMeta{{Name: "path", Type: interfaces.DataType_String}}}, SchemaDefinition: []*interfaces.Property{{Name: "path", Filterable: flag(true)}}}))
	})
	t.Run("native source column types use fileset capabilities", func(t *testing.T) {
		for _, tc := range []struct {
			native string
			want   bool
		}{{"string", true}, {"integer", true}, {"boolean", true}, {"json", false}, {"binary", false}, {"vector", false}, {"datetime", false}, {"unknown", false}, {"", false}} {
			t.Run(tc.native, func(t *testing.T) {
				resource := &interfaces.Resource{SourceMetadata: map[string]any{"columns": []interfaces.FilesetColumnMeta{{Name: "field", Type: tc.native}}, "mapping": make(chan int)}, SchemaDefinition: []*interfaces.Property{{Name: "field", Type: interfaces.DataType_String, OriginalType: "stale", Filterable: flag(true)}}}
				err := c.ValidateFilterability(resource)
				if tc.want {
					require.NoError(t, err)
				} else {
					var invalid *interfaces.ConditionBuildError
					require.ErrorAs(t, err, &invalid)
				}
			})
		}
	})
	t.Run("missing source columns cannot be fabricated from schema", func(t *testing.T) {
		for _, tc := range []struct {
			name     string
			metadata map[string]any
		}{
			{"missing source metadata", nil}, {"missing columns", map[string]any{"mapping": "unrelated"}},
			{"empty columns", map[string]any{"columns": []interfaces.FilesetColumnMeta{}}},
			{"other typed column", map[string]any{"columns": []interfaces.FilesetColumnMeta{{Name: "real", Type: "string"}}}},
			{"other persisted column", map[string]any{"columns": []any{map[string]any{"name": "real", "type": "string"}}}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				resource := &interfaces.Resource{SourceMetadata: tc.metadata, SchemaDefinition: []*interfaces.Property{{Name: "public_field", OriginalName: "missing", OriginalType: "string", Type: interfaces.DataType_String, Attributes: map[string]any{"type": "string"}, Filterable: flag(true)}}}
				before, err := json.Marshal(resource)
				require.NoError(t, err)
				err = c.ValidateFilterability(resource)
				var invalid *interfaces.ConditionBuildError
				require.ErrorAs(t, err, &invalid)
				assert.Contains(t, invalid.Reason, "public_field")
				after, err := json.Marshal(resource)
				require.NoError(t, err)
				assert.JSONEq(t, string(before), string(after))
			})
		}
	})
	t.Run("stored corrupt columns do not fall back to supported property types", func(t *testing.T) {
		for _, tc := range []struct {
			name string
			raw  any
		}{{"explicit nil", nil}, {"JSON null", json.RawMessage("null")}, {"typed nil", []interfaces.FilesetColumnMeta(nil)}, {"invalid shape", "corrupt"}, {"marshal failure", make(chan int)}} {
			t.Run(tc.name, func(t *testing.T) {
				err := c.ValidateFilterability(&interfaces.Resource{SourceMetadata: map[string]any{"columns": tc.raw}, SchemaDefinition: []*interfaces.Property{{Name: "path", OriginalType: interfaces.DataType_String, Filterable: flag(true)}}})
				require.Error(t, err)
				var invalid *interfaces.ConditionBuildError
				assert.NotErrorAs(t, err, &invalid)
			})
		}
	})
}
