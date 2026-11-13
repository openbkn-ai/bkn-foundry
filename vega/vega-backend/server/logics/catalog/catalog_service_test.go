// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/agiledragon/gomonkey/v2"
	kwcrypto "github.com/openbkn-ai/bkn-foundry/comm-go/crypto"
	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/otellog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/common"
	verrors "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/errors"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
	vmock "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces/mock"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/connector/factory"
)

// mockCipher isolates credential encryption failures in service tests.
type mockCipher struct {
	encryptFunc func(plaintext string) (string, error)
	decryptFunc func(ciphertext string) (string, error)
}

func (m *mockCipher) Encrypt(plaintext string) (string, error) {
	if m.encryptFunc != nil {
		return m.encryptFunc(plaintext)
	}
	return "encrypted_" + plaintext, nil
}

func (m *mockCipher) Decrypt(ciphertext string) (string, error) {
	return m.decryptFunc(ciphertext)
}

func (m *mockCipher) Signature(string) (string, error) {
	return "", errors.New("signature is not supported by the credential test cipher")
}

func requireRedactedConnectorInitializationError(t *testing.T, err error, sensitiveError string) {
	t.Helper()

	var httpErr *httperrors.HTTPError
	require.ErrorAs(t, err, &httpErr)
	assert.Equal(t, http.StatusBadRequest, httpErr.HTTPCode)
	assert.Equal(t, verrors.VegaBackend_Catalog_InternalError_CreateFailed, httpErr.ErrorCode)
	assert.NotContains(t, fmt.Sprint(httpErr.ErrorDetails), sensitiveError)
	assert.Contains(t, fmt.Sprint(httpErr.ErrorDetails), connectorInitializationFailedResult)
}

func TestEncryptSensitiveFields(t *testing.T) {
	t.Run("reserved plaintext prefix is rejected before encryption regardless of the switch", func(t *testing.T) {
		for _, enabled := range []bool{false, true} {
			for _, value := range []string{"ENC:", "enc:fixture", "EnC:fixture"} {
				for _, nested := range []bool{false, true} {
					t.Run(fmt.Sprintf("enabled=%t nested=%t value=%s", enabled, nested, value), func(t *testing.T) {
						cfg := interfaces.ConnectorConfig{"token": value, "variables": map[string]any{"token": "plain-fixture"}}
						if nested {
							cfg["token"] = nil
							cfg["variables"].(map[string]any)["token"] = value
						}
						before, err := json.Marshal(cfg)
						require.NoError(t, err)
						cs := &catalogService{appSetting: &common.AppSetting{CryptoSetting: common.CryptoSetting{Enabled: enabled}}, cipher: &mockCipher{encryptFunc: func(string) (string, error) {
							t.Fatal("reserved prefix must be rejected before encryption")
							return "", nil
						}}}
						got, err := cs.encryptSensitiveFields([]string{"token"}, cfg)
						require.ErrorContains(t, err, "plaintext must not start with ENC:")
						assert.NotContains(t, err.Error(), "fixture")
						assert.Nil(t, got)
						after, err := json.Marshal(cfg)
						require.NoError(t, err)
						assert.Equal(t, before, after)
					})
				}
			}
		}
	})
	t.Run("AES credentials survive write encryption switches without changing stored inputs", func(t *testing.T) {
		key := []byte(strings.Repeat("k", 32))
		cipher, err := kwcrypto.NewAESGCMCipher(key)
		require.NoError(t, err)
		setting := &common.AppSetting{CryptoSetting: common.CryptoSetting{AESKey: key}}
		cs := &catalogService{appSetting: setting, cipher: cipher}
		input := interfaces.ConnectorConfig{
			"password":  strings.Repeat("long-fixture-", 512),
			"variables": map[string]any{"token": strings.Repeat("示例凭据", 256), "empty": "", "tenant": "public"},
		}
		fields := []string{"password", "token", "empty"}
		var stored []interfaces.ConnectorConfig
		for _, enabled := range []bool{false, true, false, true} {
			setting.CryptoSetting.Enabled = enabled
			sealed, err := cs.encryptSensitiveFields(fields, input)
			require.NoError(t, err)
			if enabled {
				assert.True(t, strings.HasPrefix(sealed["password"].(string), EncryptedPrefix))
				assert.NotEqual(t, input["password"], sealed["password"])
				assert.NotEqual(t, input["variables"].(map[string]any)["token"], sealed["variables"].(map[string]any)["token"])
			} else {
				assert.Equal(t, input, sealed)
			}
			assert.Equal(t, "", sealed["variables"].(map[string]any)["empty"])
			stored = append(stored, sealed)
			for _, previous := range stored {
				plain, err := cs.decryptSensitiveFields(fields, previous)
				require.NoError(t, err)
				assert.Equal(t, input, plain)
			}
		}
		assert.Equal(t, strings.Repeat("long-fixture-", 512), input["password"])
		assert.Equal(t, strings.Repeat("示例凭据", 256), input["variables"].(map[string]any)["token"])
		assert.Equal(t, "public", input["variables"].(map[string]any)["tenant"])
	})
	t.Run("driver options are not encrypted when the selected field is absent", func(t *testing.T) {
		cs := &catalogService{appSetting: &common.AppSetting{CryptoSetting: common.CryptoSetting{Enabled: true}}, cipher: &mockCipher{encryptFunc: func(string) (string, error) {
			t.Error("driver options must not be selected for encryption")
			return "unexpected", nil
		}}}
		cfg := interfaces.ConnectorConfig{"variables": map[string]any{"tenant": "public"}, "options": map[string]any{"fixture_option": "driver-fixture"}}
		got, err := cs.encryptSensitiveFields([]string{"fixture_option"}, cfg)
		require.NoError(t, err)
		assert.Equal(t, cfg, got)
	})
	t.Run("encrypts declared variables without changing caller configuration", func(t *testing.T) {
		var encrypted []string
		cs := &catalogService{appSetting: &common.AppSetting{CryptoSetting: common.CryptoSetting{Enabled: true}}, cipher: &mockCipher{encryptFunc: func(value string) (string, error) {
			encrypted = append(encrypted, value)
			return "sealed_" + value, nil
		}}}
		cfg := interfaces.ConnectorConfig{"token": "fixed-fixture", "variables": map[string]any{"api_key": "key-fixture", "tenant": "public"}, "sensitive_variables": []string{"api_key"}, "options": map[string]any{"driver_option": "unchanged"}}
		got, err := cs.encryptSensitiveFields([]string{"token", "api_key"}, cfg)
		require.NoError(t, err)
		assert.Equal(t, []string{"fixed-fixture", "key-fixture"}, encrypted)
		assert.Equal(t, "ENC:sealed_fixed-fixture", got["token"])
		assert.Equal(t, "ENC:sealed_key-fixture", got["variables"].(map[string]any)["api_key"])
		assert.Equal(t, "public", got["variables"].(map[string]any)["tenant"])
		assert.Equal(t, cfg["options"], got["options"])
		assert.Equal(t, "key-fixture", cfg["variables"].(map[string]any)["api_key"])
	})
	t.Run("retained keys support off on off writes without migrating existing values", func(t *testing.T) {
		setting := &common.AppSetting{}
		cs := &catalogService{appSetting: setting, cipher: &mockCipher{
			encryptFunc: func(value string) (string, error) { return "sealed_" + value, nil },
			decryptFunc: func(value string) (string, error) { return strings.TrimPrefix(value, "sealed_"), nil },
		}}
		fields := []string{"fixture_option"}
		input := interfaces.ConnectorConfig{"variables": map[string]any{"fixture_option": "client-fixture"}}
		legacy, err := cs.encryptSensitiveFields(fields, input)
		require.NoError(t, err)
		assert.Equal(t, input, legacy)
		setting.CryptoSetting.Enabled = true
		plain, err := cs.decryptSensitiveFields(fields, legacy)
		require.NoError(t, err)
		assert.Equal(t, input, plain)
		sealed, err := cs.encryptSensitiveFields(fields, input)
		require.NoError(t, err)
		assert.Equal(t, "ENC:sealed_client-fixture", sealed["variables"].(map[string]any)["fixture_option"])
		setting.CryptoSetting.Enabled = false
		plain, err = cs.decryptSensitiveFields(fields, sealed)
		require.NoError(t, err)
		assert.Equal(t, input, plain)
		unsealed, err := cs.encryptSensitiveFields(fields, input)
		require.NoError(t, err)
		assert.Equal(t, input, unsealed)
		setting.CryptoSetting.Enabled = true
		for _, stored := range []interfaces.ConnectorConfig{legacy, sealed, unsealed} {
			plain, err = cs.decryptSensitiveFields(fields, stored)
			require.NoError(t, err)
			assert.Equal(t, input, plain)
		}
		assert.Equal(t, "ENC:sealed_client-fixture", sealed["variables"].(map[string]any)["fixture_option"])
		assert.Equal(t, "client-fixture", input["variables"].(map[string]any)["fixture_option"])
	})
	t.Run("fixed and variables values are encrypted in an independent storage copy", func(t *testing.T) {
		var encrypted []string
		cs := &catalogService{appSetting: &common.AppSetting{CryptoSetting: common.CryptoSetting{Enabled: true}}, cipher: &mockCipher{encryptFunc: func(value string) (string, error) {
			encrypted = append(encrypted, value)
			return "cipher_" + value, nil
		}, decryptFunc: func(string) (string, error) { t.Fatal("storage must encrypt plaintext"); return "", nil }}}
		cfg := interfaces.ConnectorConfig{"token": "client-fixture", "variables": map[string]any{"fixture_option": "literal-client", "nested": []any{map[string]any{"flag": false}}}}
		before, _ := json.Marshal(cfg)
		got, err := cs.encryptSensitiveFields([]string{"token", "fixture_option"}, cfg)
		require.NoError(t, err)
		assert.Equal(t, []string{"client-fixture", "literal-client"}, encrypted)
		assert.Equal(t, "ENC:cipher_client-fixture", got["token"])
		assert.Equal(t, "ENC:cipher_literal-client", got["variables"].(map[string]any)["fixture_option"])
		got["variables"].(map[string]any)["nested"].([]any)[0].(map[string]any)["flag"] = true
		after, _ := json.Marshal(cfg)
		assert.Equal(t, string(before), string(after))
	})
	t.Run("top-level fields do not shadow sensitive variables", func(t *testing.T) {
		for _, value := range []any{"top-level", "", nil, false, 0} {
			cs := &catalogService{appSetting: &common.AppSetting{CryptoSetting: common.CryptoSetting{Enabled: true}}, cipher: &mockCipher{encryptFunc: func(text string) (string, error) {
				assert.Contains(t, []string{"top-level", "nested-value"}, text)
				return "cipher_" + text, nil
			}}}
			cfg := interfaces.ConnectorConfig{"fixture_option": value, "variables": map[string]any{"fixture_option": "nested-value"}}
			got, err := cs.encryptSensitiveFields([]string{"fixture_option"}, cfg)
			require.NoError(t, err)
			if value == "top-level" {
				assert.Equal(t, "ENC:cipher_top-level", got["fixture_option"])
			} else {
				assert.Equal(t, value, got["fixture_option"])
			}
			assert.Equal(t, value, cfg["fixture_option"])
			assert.Equal(t, "ENC:cipher_nested-value", got["variables"].(map[string]any)["fixture_option"])
			assert.Equal(t, "nested-value", cfg["variables"].(map[string]any)["fixture_option"])
		}
	})
	t.Run("failed encryption does not mutate any submitted value", func(t *testing.T) {
		calls := 0
		cs := &catalogService{appSetting: &common.AppSetting{CryptoSetting: common.CryptoSetting{Enabled: true}}, cipher: &mockCipher{encryptFunc: func(value string) (string, error) {
			calls++
			if value == "fail-fixture" {
				return "", errors.New("provider private-fixture")
			}
			return "cipher", nil
		}}}
		cfg := interfaces.ConnectorConfig{"token": "first-fixture", "variables": map[string]any{"fixture_option": "fail-fixture"}}
		before, _ := json.Marshal(cfg)
		got, err := cs.encryptSensitiveFields([]string{"token", "fixture_option"}, cfg)
		require.Error(t, err)
		assert.Nil(t, got)
		assert.Equal(t, 2, calls)
		assert.NotContains(t, err.Error(), "provider private-fixture")
		after, _ := json.Marshal(cfg)
		assert.Equal(t, string(before), string(after))
	})
	t.Run("disabled crypto keeps plaintext in a deep independent copy", func(t *testing.T) {
		cfg := interfaces.ConnectorConfig{"token": "fixture", "variables": map[string]any{"rows": []any{map[string]any{"flag": false}}}}
		got, err := (&catalogService{appSetting: &common.AppSetting{}}).encryptSensitiveFields([]string{"token"}, cfg)
		require.NoError(t, err)
		assert.Equal(t, cfg, got)
		got["variables"].(map[string]any)["rows"].([]any)[0].(map[string]any)["flag"] = true
		assert.Equal(t, false, cfg["variables"].(map[string]any)["rows"].([]any)[0].(map[string]any)["flag"])
	})
	t.Run("missing fields and legacy variables shapes are preserved", func(t *testing.T) {
		cfg := interfaces.ConnectorConfig{"variables": "legacy-shape"}
		got, err := (&catalogService{appSetting: &common.AppSetting{CryptoSetting: common.CryptoSetting{Enabled: true}}, cipher: &mockCipher{encryptFunc: func(string) (string, error) { t.Fatal("no selected value exists"); return "", nil }}}).encryptSensitiveFields([]string{"missing"}, cfg)
		require.NoError(t, err)
		assert.Equal(t, cfg, got)
	})
}

func TestDecryptSensitiveFields(t *testing.T) {
	t.Run("top-level fields do not shadow encrypted variables", func(t *testing.T) {
		for _, value := range []any{"ENC:fixed", "plain", "", nil, false, 0} {
			cs := &catalogService{cipher: &mockCipher{decryptFunc: func(ciphertext string) (string, error) { return "plain_" + ciphertext, nil }}}
			cfg := interfaces.ConnectorConfig{"fixture_option": value, "variables": map[string]any{"fixture_option": "ENC:nested"}, "options": map[string]any{"fixture_option": "ENC:driver"}}
			got, err := cs.decryptSensitiveFields([]string{"fixture_option"}, cfg)
			require.NoError(t, err)
			assert.Equal(t, "plain_nested", got["variables"].(map[string]any)["fixture_option"])
			if value == "ENC:fixed" {
				assert.Equal(t, "plain_fixed", got["fixture_option"])
			} else {
				assert.Equal(t, value, got["fixture_option"])
			}
			assert.Equal(t, cfg["options"], got["options"])
			assert.Equal(t, value, cfg["fixture_option"])
			assert.Equal(t, "ENC:nested", cfg["variables"].(map[string]any)["fixture_option"])
		}
	})
	t.Run("driver options are not decrypted when the selected field is absent", func(t *testing.T) {
		cs := &catalogService{cipher: &mockCipher{decryptFunc: func(string) (string, error) {
			t.Error("driver options must not be selected for decryption")
			return "unexpected", nil
		}}}
		cfg := interfaces.ConnectorConfig{"variables": map[string]any{"tenant": "public"}, "options": map[string]any{"fixture_option": "ENC:driver-fixture"}}
		got, err := cs.decryptSensitiveFields([]string{"fixture_option"}, cfg)
		require.NoError(t, err)
		assert.Equal(t, cfg, got)
	})
	t.Run("decrypts declared variables independently of the write encryption switch", func(t *testing.T) {
		for _, enabled := range []bool{false, true} {
			cs := &catalogService{appSetting: &common.AppSetting{CryptoSetting: common.CryptoSetting{Enabled: enabled}}, cipher: &mockCipher{decryptFunc: func(value string) (string, error) { return strings.TrimPrefix(value, "sealed_"), nil }}}
			cfg := interfaces.ConnectorConfig{"variables": map[string]any{"api_key": "ENC:sealed_key-fixture", "tenant": "public"}, "sensitive_variables": []string{"api_key"}}
			got, err := cs.decryptSensitiveFields([]string{"api_key"}, cfg)
			require.NoError(t, err)
			assert.Equal(t, "key-fixture", got["variables"].(map[string]any)["api_key"])
			assert.Equal(t, "public", got["variables"].(map[string]any)["tenant"])
			assert.Equal(t, "ENC:sealed_key-fixture", cfg["variables"].(map[string]any)["api_key"])
		}
	})
	t.Run("variable presence takes precedence over driver options", func(t *testing.T) {
		for _, value := range []any{nil, "", false, 0, "plain-fixture"} {
			cs := &catalogService{cipher: &mockCipher{decryptFunc: func(string) (string, error) { t.Fatal("must not decrypt shadowed options"); return "", nil }}}
			cfg := interfaces.ConnectorConfig{"variables": map[string]any{"api_key": value}, "options": map[string]any{"api_key": "ENC:driver-fixture"}}
			got, err := cs.decryptSensitiveFields([]string{"api_key"}, cfg)
			require.NoError(t, err)
			assert.Equal(t, value, got["variables"].(map[string]any)["api_key"])
			assert.Equal(t, cfg["options"], got["options"])
		}
	})
	t.Run("plaintext and ciphertext remain readable after switching write encryption", func(t *testing.T) {
		for _, enabled := range []bool{false, true} {
			t.Run(fmt.Sprintf("write encryption=%t", enabled), func(t *testing.T) {
				cs := &catalogService{appSetting: &common.AppSetting{CryptoSetting: common.CryptoSetting{Enabled: enabled}}, cipher: &mockCipher{
					encryptFunc: func(string) (string, error) {
						t.Fatal("reading must not encrypt or migrate stored values")
						return "", nil
					},
					decryptFunc: func(value string) (string, error) {
						assert.Equal(t, "cipher-fixture", value)
						return "decoded-fixture", nil
					},
				}}
				cfg := interfaces.ConnectorConfig{"token": "legacy-fixture", "variables": map[string]any{"fixture_option": "ENC:cipher-fixture"}}
				got, err := cs.decryptSensitiveFields([]string{"token", "fixture_option"}, cfg)
				require.NoError(t, err)
				assert.Equal(t, "legacy-fixture", got["token"])
				assert.Equal(t, "decoded-fixture", got["variables"].(map[string]any)["fixture_option"])
				assert.Equal(t, "ENC:cipher-fixture", cfg["variables"].(map[string]any)["fixture_option"])
			})
		}
	})
	t.Run("reads fixed and nested credentials without modifying storage", func(t *testing.T) {
		cs := &catalogService{cipher: &mockCipher{decryptFunc: func(value string) (string, error) { return "plain_" + value, nil }}}
		cfg := interfaces.ConnectorConfig{"token": "ENC:fixed", "variables": map[string]any{"fixture_option": "ENC:nested", "count": 0, "rows": []any{map[string]any{"flag": false}}}, "sensitive_variables": []any{"fixture_option"}}
		got, err := cs.decryptSensitiveFields([]string{"token", "fixture_option"}, cfg)
		require.NoError(t, err)
		assert.Equal(t, "plain_fixed", got["token"])
		assert.Equal(t, "plain_nested", got["variables"].(map[string]any)["fixture_option"])
		got["variables"].(map[string]any)["count"] = 99
		got["variables"].(map[string]any)["rows"].([]any)[0].(map[string]any)["flag"] = true
		assert.Equal(t, false, cfg["variables"].(map[string]any)["rows"].([]any)[0].(map[string]any)["flag"])
		assert.Equal(t, "ENC:fixed", cfg["token"])
		assert.Equal(t, "ENC:nested", cfg["variables"].(map[string]any)["fixture_option"])
		assert.Equal(t, 0, cfg["variables"].(map[string]any)["count"])
	})
	for _, value := range []string{"ENC:bad"} {
		t.Run("rejects malformed stored variable "+value, func(t *testing.T) {
			cs := &catalogService{cipher: &mockCipher{decryptFunc: func(string) (string, error) { return "", errors.New("provider fixture disclosure") }}}
			cfg := interfaces.ConnectorConfig{"variables": map[string]any{"fixture_option": value}, "sensitive_variables": []string{"fixture_option"}}
			got, err := cs.decryptSensitiveFields([]string{"fixture_option"}, cfg)
			require.Error(t, err)
			assert.Nil(t, got)
			assert.NotContains(t, err.Error(), "provider fixture")
			assert.Equal(t, value, cfg["variables"].(map[string]any)["fixture_option"])
		})
	}
	t.Run("crypto disabled isolates declared variable fields", func(t *testing.T) {
		cs := &catalogService{}
		cfg := interfaces.ConnectorConfig{"variables": map[string]any{"fixture_option": "fixture"}, "sensitive_variables": []string{"fixture_option"}}
		got, err := cs.decryptSensitiveFields([]string{"fixture_option"}, cfg)
		require.NoError(t, err)
		got["variables"].(map[string]any)["fixture_option"] = "changed"
		assert.Equal(t, "fixture", cfg["variables"].(map[string]any)["fixture_option"])
	})

	t.Run("encrypted storage never falls back to plaintext without its key", func(t *testing.T) {
		config := interfaces.ConnectorConfig{"token": "ENC:fixture"}
		got, err := (&catalogService{}).decryptSensitiveFields([]string{"token"}, config)
		require.ErrorContains(t, err, "decryption key is unavailable")
		assert.Nil(t, got)
		assert.Equal(t, "ENC:fixture", config["token"])
	})
	t.Run("decrypt with cipher success", func(t *testing.T) {
		cs := &catalogService{
			cipher: &mockCipher{
				decryptFunc: func(ciphertext string) (string, error) {
					return "plaintext_" + ciphertext, nil
				},
			},
		}
		config := interfaces.ConnectorConfig{"password": "ENC:cipher_data"} //nolint:gosec // Test-only placeholder ciphertext.

		decrypted, err := cs.decryptSensitiveFields([]string{"password"}, config)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if decrypted["password"] != "plaintext_cipher_data" {
			t.Errorf("expected 'plaintext_cipher_data', got '%v'", decrypted["password"])
		}
	})
	t.Run("legacy plaintext does not require decryption", func(t *testing.T) {
		cs := &catalogService{cipher: &mockCipher{decryptFunc: func(string) (string, error) { t.Fatal("legacy plaintext must not be decrypted"); return "", nil }}}
		config := interfaces.ConnectorConfig{"token": "legacy-fixture"}
		got, err := cs.decryptSensitiveFields([]string{"token"}, config)
		require.NoError(t, err)
		assert.Equal(t, config, got)
	})
	t.Run("decrypt decrypt fails", func(t *testing.T) {
		cs := &catalogService{
			cipher: &mockCipher{
				decryptFunc: func(ciphertext string) (string, error) {
					return "", fmt.Errorf("corrupted data")
				},
			},
		}
		config := interfaces.ConnectorConfig{"password": "ENC:bad_data"} //nolint:gosec // Test-only malformed ciphertext.

		_, err := cs.decryptSensitiveFields([]string{"password"}, config)
		if err == nil {
			t.Fatal("expected error for corrupted data")
		}
		if !strings.Contains(err.Error(), "password") {
			t.Errorf("error should mention field name, got: %v", err)
		}
	})
}

func TestCatalogServiceCheckExistByID(t *testing.T) {
	t.Run("check exist by idfound", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCA := vmock.NewMockCatalogAccess(ctrl)
		mockCA.EXPECT().GetByID(gomock.Any(), "test-id").
			Return(&interfaces.Catalog{ID: "test-id"}, nil)

		cs := &catalogService{ca: mockCA}
		exists, err := cs.CheckExistByID(context.Background(), "test-id")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !exists {
			t.Error("expected catalog to exist")
		}
	})
	t.Run("check exist by idnot found", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCA := vmock.NewMockCatalogAccess(ctrl)
		mockCA.EXPECT().GetByID(gomock.Any(), "missing-id").
			Return(nil, nil)

		cs := &catalogService{ca: mockCA}
		exists, err := cs.CheckExistByID(context.Background(), "missing-id")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if exists {
			t.Error("expected catalog to not exist")
		}
	})
	t.Run("check exist by iderror", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCA := vmock.NewMockCatalogAccess(ctrl)
		mockCA.EXPECT().GetByID(gomock.Any(), "test-id").
			Return(nil, fmt.Errorf("db error"))

		cs := &catalogService{ca: mockCA}
		_, err := cs.CheckExistByID(context.Background(), "test-id")
		if err == nil {
			t.Fatal("expected error")
		}
	})
}

func TestCatalogServiceCheckExistByName(t *testing.T) {
	t.Run("check exist by name found", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCA := vmock.NewMockCatalogAccess(ctrl)
		mockCA.EXPECT().GetByName(gomock.Any(), "test").
			Return(&interfaces.Catalog{Name: "test"}, nil)

		cs := &catalogService{ca: mockCA}
		exists, err := cs.checkExistByName(context.Background(), "test")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !exists {
			t.Error("expected catalog to exist")
		}
	})
}

func TestCatalogServiceCreate(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(fmt.Sprintf("reserved sensitive plaintext returns 400 without persistence crypto=%t", enabled), func(t *testing.T) {
			ctrl := gomock.NewController(t)
			ps := vmock.NewMockPermissionService(ctrl)
			ca := vmock.NewMockCatalogAccess(ctrl)
			cf := vmock.NewMockConnectorFactory(ctrl)
			connector := vmock.NewMockConnector(ctrl)
			cfg := interfaces.ConnectorConfig{"fixture_option": nil, "variables": map[string]any{"fixture_option": "eNc:private-fixture"}, "sensitive_variables": []string{"fixture_option"}}
			ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			cf.EXPECT().GetSensitiveFields("http", cfg).Return([]string{"fixture_option"}, nil)
			cf.EXPECT().CreateConnectorInstance(gomock.Any(), "http", gomock.Any()).Return(connector, nil)
			connector.EXPECT().GetCategory().Return(interfaces.ConnectorCategoryAPI).AnyTimes()
			connector.EXPECT().TestConnection(gomock.Any()).Return(nil)
			connector.EXPECT().Close(gomock.Any()).Return(nil)
			cs := &catalogService{ca: ca, ps: ps, cf: cf, appSetting: &common.AppSetting{CryptoSetting: common.CryptoSetting{Enabled: enabled}}, cipher: &mockCipher{encryptFunc: func(string) (string, error) {
				t.Fatal("reserved plaintext must not reach the cipher")
				return "", nil
			}}}
			req := &interfaces.CatalogRequest{ID: "one", Name: "API", ConnectorType: "http", ConnectorCfg: cfg}
			_, err := cs.Create(context.Background(), req, true)
			var httpErr *httperrors.HTTPError
			require.ErrorAs(t, err, &httpErr)
			assert.Equal(t, http.StatusBadRequest, httpErr.HTTPCode)
			assert.Equal(t, verrors.VegaBackend_Catalog_InvalidParameter, httpErr.ErrorCode)
			assert.Contains(t, fmt.Sprint(httpErr.ErrorDetails), "plaintext must not start with ENC:")
			assert.NotContains(t, fmt.Sprint(httpErr.ErrorDetails), "private-fixture")
			assert.Equal(t, "eNc:private-fixture", cfg["variables"].(map[string]any)["fixture_option"])
		})
	}
	for _, allowUnhealthy := range []bool{false, true} {
		t.Run(fmt.Sprintf("storage encryption failure never persists allow_unhealthy=%t", allowUnhealthy), func(t *testing.T) {
			ctrl := gomock.NewController(t)
			ca := vmock.NewMockCatalogAccess(ctrl)
			ps := vmock.NewMockPermissionService(ctrl)
			cf := vmock.NewMockConnectorFactory(ctrl)
			connector := vmock.NewMockConnector(ctrl)
			cfg := interfaces.ConnectorConfig{"token": "first-fixture", "variables": map[string]any{"fixture_option": "failed-fixture", "rows": []any{map[string]any{"flag": false}}}}
			before, _ := json.Marshal(cfg)
			req := &interfaces.CatalogRequest{Name: "API", ConnectorType: "http", ConnectorCfg: cfg}
			ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			cf.EXPECT().GetSensitiveFields("http", cfg).Return([]string{"token", "fixture_option"}, nil)
			cf.EXPECT().CreateConnectorInstance(gomock.Any(), "http", gomock.Any()).DoAndReturn(func(_ context.Context, _ string, got interfaces.ConnectorConfig) (interfaces.Connector, error) {
				assert.Equal(t, cfg, got)
				got["variables"].(map[string]any)["rows"].([]any)[0].(map[string]any)["flag"] = true
				return connector, nil
			})
			checked := false
			connector.EXPECT().TestConnection(gomock.Any()).DoAndReturn(func(context.Context) error {
				checked = true
				if allowUnhealthy {
					return errors.New("source temporarily unavailable")
				}
				return nil
			})
			connector.EXPECT().Close(gomock.Any()).Return(nil)
			cs := &catalogService{ca: ca, ps: ps, cf: cf, appSetting: &common.AppSetting{CryptoSetting: common.CryptoSetting{Enabled: true}}, cipher: &mockCipher{encryptFunc: func(value string) (string, error) {
				assert.True(t, checked)
				if value == "failed-fixture" {
					return "", errors.New("provider disclosed failed-fixture")
				}
				return "cipher", nil
			}, decryptFunc: func(string) (string, error) { t.Fatal("plaintext update must not decrypt"); return "", nil }}}
			connector.EXPECT().GetCategory().Return(interfaces.ConnectorCategoryAPI)
			ca.EXPECT().GetByName(gomock.Any(), "API").Return(nil, nil).AnyTimes()
			_, err := cs.Create(context.Background(), req, allowUnhealthy)
			var httpErr *httperrors.HTTPError
			require.ErrorAs(t, err, &httpErr)
			assert.Equal(t, http.StatusInternalServerError, httpErr.HTTPCode)
			assert.Equal(t, verrors.VegaBackend_Catalog_InternalError_CreateFailed, httpErr.ErrorCode)
			assert.NotContains(t, fmt.Sprint(httpErr.ErrorDetails), "provider disclosed")
			assert.NotContains(t, fmt.Sprint(httpErr.ErrorDetails), "failed-fixture")
			after, _ := json.Marshal(cfg)
			assert.Equal(t, string(before), string(after))
		})
	}

	for _, cryptoEnabled := range []bool{false, true} {
		t.Run(fmt.Sprintf("plaintext client input reaches source and storage crypto=%t", cryptoEnabled), func(t *testing.T) {
			ctrl := gomock.NewController(t)
			ca := vmock.NewMockCatalogAccess(ctrl)
			ps := vmock.NewMockPermissionService(ctrl)
			cf := vmock.NewMockConnectorFactory(ctrl)
			connector := vmock.NewMockConnector(ctrl)
			hcss := vmock.NewMockCatalogHealthCheckScheduleService(ctrl)
			db, sqlMock, err := sqlmock.New()
			require.NoError(t, err)
			t.Cleanup(func() { _ = db.Close() })
			cfg := interfaces.ConnectorConfig{"token": "client-fixture", "variables": map[string]any{"fixture_option": "literal-client", "nested": map[string]any{"value": false}}, "sensitive_variables": []string{"fixture_option"}}
			ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			cf.EXPECT().GetSensitiveFields("http", cfg).Return([]string{"token", "fixture_option"}, nil)
			sourceChecked := false
			cf.EXPECT().CreateConnectorInstance(gomock.Any(), "http", gomock.Any()).DoAndReturn(func(_ context.Context, _ string, got interfaces.ConnectorConfig) (interfaces.Connector, error) {
				assert.Equal(t, cfg, got)
				got["variables"].(map[string]any)["nested"].(map[string]any)["value"] = true
				return connector, nil
			})
			connector.EXPECT().GetCategory().Return(interfaces.ConnectorCategoryAPI)
			connector.EXPECT().TestConnection(gomock.Any()).DoAndReturn(func(context.Context) error { sourceChecked = true; return nil })
			connector.EXPECT().Close(gomock.Any()).Return(nil)
			ca.EXPECT().GetByName(gomock.Any(), "API").Return(nil, nil)
			sqlMock.ExpectBegin()
			sqlMock.ExpectCommit()
			ca.EXPECT().Create(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, _ *sql.Tx, saved *interfaces.Catalog) error {
				if cryptoEnabled {
					assert.Equal(t, "ENC:encrypted_client-fixture", saved.ConnectorCfg["token"])
					assert.Equal(t, "ENC:encrypted_literal-client", saved.ConnectorCfg["variables"].(map[string]any)["fixture_option"])
				} else {
					assert.Equal(t, "client-fixture", saved.ConnectorCfg["token"])
					assert.Equal(t, "literal-client", saved.ConnectorCfg["variables"].(map[string]any)["fixture_option"])
				}
				assert.Equal(t, false, saved.ConnectorCfg["variables"].(map[string]any)["nested"].(map[string]any)["value"])
				return nil
			})
			hcss.EXPECT().Create(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(&interfaces.CatalogHealthCheckSchedule{}, nil)
			ps.EXPECT().CreateResources(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			cs := &catalogService{ca: ca, ps: ps, cf: cf, hcss: hcss, db: db, appSetting: &common.AppSetting{CryptoSetting: common.CryptoSetting{Enabled: true}}, cipher: &mockCipher{encryptFunc: func(value string) (string, error) { assert.True(t, sourceChecked); return "encrypted_" + value, nil }, decryptFunc: func(string) (string, error) { return "", errors.New("plaintext input must not be decrypted") }}}
			cs.appSetting.CryptoSetting.Enabled = cryptoEnabled
			_, err = cs.Create(context.Background(), &interfaces.CatalogRequest{Name: "API", ConnectorType: "http", ConnectorCfg: cfg}, false)
			require.NoError(t, err)
			assert.Equal(t, "client-fixture", cfg["token"])
			assert.Equal(t, "literal-client", cfg["variables"].(map[string]any)["fixture_option"])
			assert.Equal(t, false, cfg["variables"].(map[string]any)["nested"].(map[string]any)["value"])
			require.NoError(t, sqlMock.ExpectationsWereMet())
		})

	}

	t.Run("metadata lookup error is not a decryption or connectivity failure", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		ps, cf, ca := vmock.NewMockPermissionService(ctrl), vmock.NewMockConnectorFactory(ctrl), vmock.NewMockCatalogAccess(ctrl)
		ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
		cf.EXPECT().GetSensitiveFields("missing", gomock.Any()).Return(nil, factory.ErrConnectorUnavailable)
		cs := &catalogService{ps: ps, cf: cf, ca: ca}
		req := &interfaces.CatalogRequest{ID: "one", Name: "name", ConnectorType: "missing", ConnectorCfg: map[string]any{}}
		_, err := cs.Create(context.Background(), req, true)
		var httpErr *httperrors.HTTPError
		require.ErrorAs(t, err, &httpErr)
		assert.Equal(t, verrors.VegaBackend_Catalog_InternalError_CreateFailed, httpErr.ErrorCode)
	})
	t.Run("API cancelled connection test closes without persistence", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		ps, cf := vmock.NewMockPermissionService(ctrl), vmock.NewMockConnectorFactory(ctrl)
		connector := vmock.NewMockConnector(ctrl)
		ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
		cf.EXPECT().GetSensitiveFields("http", gomock.Any()).Return(nil, nil)
		cf.EXPECT().CreateConnectorInstance(gomock.Any(), "http", gomock.Any()).Return(connector, nil)
		connector.EXPECT().GetCategory().Return(interfaces.ConnectorCategoryAPI)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		connector.EXPECT().TestConnection(gomock.Any()).DoAndReturn(func(probeCtx context.Context) error {
			require.ErrorIs(t, probeCtx.Err(), context.Canceled)
			return probeCtx.Err()
		})
		connector.EXPECT().Close(gomock.Any()).Return(nil)
		cs := &catalogService{ps: ps, cf: cf, appSetting: &common.AppSetting{}}
		_, err := cs.Create(ctx, &interfaces.CatalogRequest{ConnectorType: "http", Name: "API"}, false)
		var httpErr *httperrors.HTTPError
		require.ErrorAs(t, err, &httpErr)
		assert.Equal(t, http.StatusBadRequest, httpErr.HTTPCode)
	})
	for _, allow := range []bool{false, true} {
		t.Run(fmt.Sprintf("API transport failure allow_unhealthy=%t", allow), func(t *testing.T) {
			ctrl := gomock.NewController(t)
			ps, cf, ca := vmock.NewMockPermissionService(ctrl), vmock.NewMockConnectorFactory(ctrl), vmock.NewMockCatalogAccess(ctrl)
			connector := vmock.NewMockConnector(ctrl)
			ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			cf.EXPECT().GetSensitiveFields("http", gomock.Any()).Return(nil, nil)
			cf.EXPECT().CreateConnectorInstance(gomock.Any(), "http", gomock.Any()).Return(connector, nil)
			connector.EXPECT().GetCategory().Return(interfaces.ConnectorCategoryAPI)
			connector.EXPECT().TestConnection(gomock.Any()).Return(context.DeadlineExceeded)
			connector.EXPECT().Close(gomock.Any()).Return(nil)
			cs := &catalogService{ps: ps, cf: cf, ca: ca, appSetting: &common.AppSetting{}}
			req := &interfaces.CatalogRequest{ID: "api-1", Name: "API", ConnectorType: "http", ConnectorCfg: map[string]any{"auth_type": "none"}, ExpectedUpdateTime: 9}
			if allow {
				db, sqlMock, err := sqlmock.New()
				require.NoError(t, err)
				t.Cleanup(func() { _ = db.Close(); require.NoError(t, sqlMock.ExpectationsWereMet()) })
				cs.db = db
				sqlMock.ExpectBegin()
				sqlMock.ExpectCommit()
				ca.EXPECT().GetByName(gomock.Any(), "API").Return(nil, nil)
				ca.EXPECT().GetByID(gomock.Any(), "api-1").Return(nil, nil)
				ca.EXPECT().Create(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, _ *sql.Tx, saved *interfaces.Catalog) error {
					assert.Equal(t, interfaces.ConnectorCategoryAPI, saved.Category)
					assert.Equal(t, interfaces.CatalogHealthStatusUnhealthy, saved.HealthCheckStatus)
					assert.Equal(t, connectionTestFailedResult, saved.HealthCheckResult)
					return nil
				})
				hcss := vmock.NewMockCatalogHealthCheckScheduleService(ctrl)
				cs.hcss = hcss
				hcss.EXPECT().Create(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(&interfaces.CatalogHealthCheckSchedule{}, nil)
				ps.EXPECT().CreateResources(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			}
			_, err := cs.Create(context.Background(), req, allow)
			if allow {
				require.NoError(t, err)
			} else {
				var httpErr *httperrors.HTTPError
				require.ErrorAs(t, err, &httpErr)
				assert.Equal(t, http.StatusBadRequest, httpErr.HTTPCode)
			}
		})
	}
	for _, tc := range []struct {
		name   string
		cause  error
		status int
	}{
		{"invalid config", errors.New("fixture-invalid-config"), http.StatusBadRequest},
		{"unavailable", factory.ErrConnectorUnavailable, http.StatusBadRequest},
		{"disabled", factory.ErrConnectorDisabled, http.StatusConflict},
		{"edition denied", factory.ErrConnectorEntitlementDenied, http.StatusForbidden},
	} {
		t.Run("API rejects "+tc.name+" before writes", func(t *testing.T) {
			ctrl := gomock.NewController(t)
			ps, cf, ca := vmock.NewMockPermissionService(ctrl), vmock.NewMockConnectorFactory(ctrl), vmock.NewMockCatalogAccess(ctrl)
			ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			cf.EXPECT().GetSensitiveFields("http", gomock.Any()).Return(nil, nil)
			cf.EXPECT().CreateConnectorInstance(gomock.Any(), "http", gomock.Any()).Return(nil, tc.cause)
			cs := &catalogService{ps: ps, cf: cf, ca: ca}
			_, err := cs.Create(context.Background(), &interfaces.CatalogRequest{Name: "API", ConnectorType: "http", ConnectorCfg: map[string]any{"auth_type": "none"}}, true)
			var httpErr *httperrors.HTTPError
			require.ErrorAs(t, err, &httpErr)
			assert.Equal(t, tc.status, httpErr.HTTPCode)
		})
	}
	for _, schedule := range []*interfaces.CatalogHealthCheckScheduleRequest{nil, {Mode: interfaces.CatalogHealthCheckScheduleModeEnabled, CronExpr: "0 * * * *"}} {
		t.Run(fmt.Sprintf("API config tests transport with schedule supplied=%t", schedule != nil), func(t *testing.T) {
			ctrl := gomock.NewController(t)
			ca, ps, cf := vmock.NewMockCatalogAccess(ctrl), vmock.NewMockPermissionService(ctrl), vmock.NewMockConnectorFactory(ctrl)
			hcss := vmock.NewMockCatalogHealthCheckScheduleService(ctrl)
			hcss.EXPECT().Create(gomock.Any(), gomock.Any(), gomock.Any(), schedule).Return(&interfaces.CatalogHealthCheckSchedule{}, nil)
			connector := vmock.NewMockConnector(ctrl)
			db, sqlMock, err := sqlmock.New()
			require.NoError(t, err)
			t.Cleanup(func() { _ = db.Close() })
			ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			cf.EXPECT().GetSensitiveFields("kingdee", gomock.Any()).Return([]string{"token"}, nil)
			cf.EXPECT().CreateConnectorInstance(gomock.Any(), "kingdee", interfaces.ConnectorConfig{"base_url": "https://api.example.com/", "auth_type": "bearer", "token": "test-only-value"}).Return(connector, nil)
			connector.EXPECT().GetCategory().Return(interfaces.ConnectorCategoryAPI).AnyTimes()
			connector.EXPECT().TestConnection(gomock.Any()).Return(nil)
			connector.EXPECT().Close(gomock.Any()).Return(nil)
			ca.EXPECT().GetByName(gomock.Any(), "API").Return(nil, nil)
			sqlMock.ExpectBegin()
			ca.EXPECT().Create(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, _ *sql.Tx, saved *interfaces.Catalog) error {
				assert.Equal(t, interfaces.CatalogTypePhysical, saved.Type)
				assert.Equal(t, interfaces.ConnectorCategoryAPI, saved.Category)
				assert.Equal(t, interfaces.CatalogHealthStatusHealthy, saved.HealthCheckStatus)
				assert.Equal(t, "Connection test succeeded.", saved.HealthCheckResult)
				assert.Equal(t, "ENC:fixture", saved.ConnectorCfg["token"])
				return nil
			})
			sqlMock.ExpectCommit()
			ps.EXPECT().CreateResources(gomock.Any(), gomock.Any(), interfaces.CATALOG_CREATOR_OPERATIONS).Return(nil)
			cs := &catalogService{appSetting: &common.AppSetting{CryptoSetting: common.CryptoSetting{Enabled: true}}, db: db, ca: ca, ps: ps, cf: cf, hcss: hcss, cipher: &mockCipher{encryptFunc: func(value string) (string, error) { assert.Equal(t, "test-only-value", value); return "fixture", nil }}}
			req := &interfaces.CatalogRequest{Name: "API", ConnectorType: "kingdee", HealthCheckSchedule: schedule, ConnectorCfg: map[string]any{"base_url": "https://api.example.com/", "auth_type": "bearer", "token": "test-only-value"}}
			require.NoError(t, json.Unmarshal([]byte(`{"category":"table"}`), req))
			id, err := cs.Create(context.Background(), req, false)
			require.NoError(t, err)
			assert.NotEmpty(t, id)
			require.NoError(t, sqlMock.ExpectationsWereMet())
		})
	}

	t.Run("checks permission before catalog uniqueness", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockPS := vmock.NewMockPermissionService(ctrl)
		permissionErr := httperrors.NewHTTPError(context.Background(), http.StatusForbidden, httperrors.PublicError_Forbidden)
		mockPS.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(permissionErr)

		cs := &catalogService{ps: mockPS}
		_, err := cs.Create(context.Background(), &interfaces.CatalogRequest{
			ID: "probe-id", Name: "probe-name",
		}, false)

		require.ErrorIs(t, err, permissionErr)
	})

	t.Run("rejects duplicate name after permission check", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockPS := vmock.NewMockPermissionService(ctrl)
		mockCA := vmock.NewMockCatalogAccess(ctrl)
		mockPS.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
		mockCA.EXPECT().GetByName(gomock.Any(), "duplicate").Return(&interfaces.Catalog{Name: "duplicate"}, nil)

		cs := &catalogService{ps: mockPS, ca: mockCA}
		_, err := cs.Create(context.Background(), &interfaces.CatalogRequest{Name: "duplicate"}, false)

		var httpErr *httperrors.HTTPError
		require.ErrorAs(t, err, &httpErr)
		assert.Equal(t, verrors.VegaBackend_Catalog_NameExists, httpErr.ErrorCode)
		assert.Equal(t, http.StatusConflict, httpErr.HTTPCode)
	})

	t.Run("does not expose connector initialization error", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockPS := vmock.NewMockPermissionService(ctrl)
		sensitiveError := "invalid endpoint db.internal"

		mockPS.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
		connectorFactory := vmock.NewMockConnectorFactory(ctrl)
		connectorFactory.EXPECT().GetSensitiveFields("mariadb", gomock.Any()).Return(nil, nil)
		connectorFactory.EXPECT().CreateConnectorInstance(gomock.Any(), "mariadb", gomock.Any()).
			Return(nil, errors.New(sensitiveError))
		cs := &catalogService{
			ps: mockPS,
			cf: connectorFactory,
		}
		_, err := cs.Create(context.Background(), &interfaces.CatalogRequest{
			Name:          "physical-catalog",
			ConnectorType: "mariadb",
		}, false)

		requireRedactedConnectorInitializationError(t, err, sensitiveError)
	})

	t.Run("rejects physical internal catalog before permission check", func(t *testing.T) {
		cs := &catalogService{}

		_, err := cs.Create(context.Background(), &interfaces.CatalogRequest{
			Name:          "internal-physical-catalog",
			Builtin:       true,
			ConnectorType: "postgresql",
		}, false)

		var httpErr *httperrors.HTTPError
		require.ErrorAs(t, err, &httpErr)
		assert.Equal(t, http.StatusBadRequest, httpErr.HTTPCode)
		assert.Equal(t, verrors.VegaBackend_Catalog_InvalidParameter, httpErr.ErrorCode)
		assert.Contains(t, fmt.Sprint(httpErr.ErrorDetails), "built-in catalogs must be logical")
	})

	t.Run("does not expose connector error when connection test fails", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockPS := vmock.NewMockPermissionService(ctrl)
		connector := vmock.NewMockConnector(ctrl)
		connector.EXPECT().GetCategory().Return(interfaces.ConnectorCategoryTable).AnyTimes()
		sensitiveError := "dial tcp db.internal:3306 failed"

		mockPS.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
		connector.EXPECT().TestConnection(gomock.Any()).Return(errors.New(sensitiveError))
		connector.EXPECT().Close(gomock.Any()).Return(nil)
		connectorFactory := vmock.NewMockConnectorFactory(ctrl)
		connectorFactory.EXPECT().GetSensitiveFields("mariadb", gomock.Any()).Return(nil, nil)
		connectorFactory.EXPECT().CreateConnectorInstance(gomock.Any(), "mariadb", gomock.Any()).Return(connector, nil)

		cs := &catalogService{
			appSetting: &common.AppSetting{},
			ps:         mockPS,
			cf:         connectorFactory,
		}
		_, err := cs.Create(context.Background(), &interfaces.CatalogRequest{
			Name:          "physical-catalog",
			ConnectorType: "mariadb",
		}, false)

		var httpErr *httperrors.HTTPError
		require.ErrorAs(t, err, &httpErr)
		assert.Equal(t, http.StatusBadRequest, httpErr.HTTPCode)
		assert.Equal(t, verrors.VegaBackend_Catalog_InternalError_TestConnectionFailed, httpErr.ErrorCode)
		assert.NotContains(t, fmt.Sprint(httpErr.ErrorDetails), sensitiveError)
		assert.Contains(t, fmt.Sprint(httpErr.ErrorDetails), "Connection test failed")
	})

	t.Run("create logical catalog defaults to disabled and unchecked", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCA := vmock.NewMockCatalogAccess(ctrl)
		mockPS := vmock.NewMockPermissionService(ctrl)
		db, sqlMock, err := sqlmock.New()
		require.NoError(t, err)
		t.Cleanup(func() { _ = db.Close() })

		sqlMock.ExpectBegin()
		sqlMock.ExpectCommit()
		mockPS.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
		mockCA.EXPECT().GetByName(gomock.Any(), "catalog").Return(nil, nil)
		mockCA.EXPECT().Create(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, tx *sql.Tx, catalog *interfaces.Catalog) error {
				require.NotNil(t, tx)
				assert.Empty(t, catalog.Category)
				if catalog.Enabled {
					t.Fatal("expected catalog to be disabled by default")
				}
				if catalog.HealthCheckStatus != interfaces.CatalogHealthStatusUnchecked {
					t.Fatalf("expected unchecked status, got %s", catalog.HealthCheckStatus)
				}
				return nil
			},
		)
		mockPS.EXPECT().CreateResources(gomock.Any(), gomock.Any(), interfaces.CATALOG_CREATOR_OPERATIONS).DoAndReturn(
			func(_ context.Context, resources []interfaces.PermissionResource, _ []string) error {
				if resources[0].Type != interfaces.AUTH_RESOURCE_TYPE_CATALOG {
					t.Fatalf("expected catalog auth type, got %s", resources[0].Type)
				}
				return nil
			},
		)

		cs := &catalogService{db: db, ca: mockCA, ps: mockPS}
		_, err = cs.Create(context.Background(), &interfaces.CatalogRequest{
			Name: "catalog",
		}, false)
		require.NoError(t, err)
		require.NoError(t, sqlMock.ExpectationsWereMet())
	})

	t.Run("rolls back transaction when catalog creation fails", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCA := vmock.NewMockCatalogAccess(ctrl)
		mockPS := vmock.NewMockPermissionService(ctrl)
		db, sqlMock, err := sqlmock.New()
		require.NoError(t, err)
		t.Cleanup(func() { _ = db.Close() })

		sensitiveError := "insert into t_catalog failed at db.internal"
		createErr := errors.New(sensitiveError)
		sqlMock.ExpectBegin()
		sqlMock.ExpectRollback()
		mockPS.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
		mockCA.EXPECT().GetByName(gomock.Any(), "catalog").Return(nil, nil)
		mockCA.EXPECT().Create(gomock.Any(), gomock.Any(), gomock.Any()).Return(createErr)

		cs := &catalogService{db: db, ca: mockCA, ps: mockPS}
		_, err = cs.Create(context.Background(), &interfaces.CatalogRequest{Name: "catalog"}, false)

		var httpErr *httperrors.HTTPError
		require.ErrorAs(t, err, &httpErr)
		assert.Equal(t, http.StatusInternalServerError, httpErr.HTTPCode)
		assert.Equal(t, verrors.VegaBackend_Catalog_InternalError_CreateFailed, httpErr.ErrorCode)
		assert.Contains(t, fmt.Sprint(httpErr.ErrorDetails), "failed to create catalog")
		assert.NotContains(t, fmt.Sprint(httpErr.ErrorDetails), sensitiveError)
		require.NoError(t, sqlMock.ExpectationsWereMet())
	})

	t.Run("rejects health check schedule for logical catalog before persistence", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockPS := vmock.NewMockPermissionService(ctrl)
		mockPS.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)

		cs := &catalogService{ps: mockPS}
		_, err := cs.Create(context.Background(), &interfaces.CatalogRequest{
			Name: "logical-catalog",
			HealthCheckSchedule: &interfaces.CatalogHealthCheckScheduleRequest{
				Mode:     interfaces.CatalogHealthCheckScheduleModeEnabled,
				CronExpr: "0 * * * *",
			},
		}, false)

		var httpErr *httperrors.HTTPError
		require.ErrorAs(t, err, &httpErr)
		assert.Equal(t, http.StatusBadRequest, httpErr.HTTPCode)
		assert.Equal(t, verrors.VegaBackend_Catalog_InvalidParameter, httpErr.ErrorCode)
		assert.Contains(t, fmt.Sprint(httpErr.ErrorDetails), "only supported for physical catalogs")
	})

	t.Run("create enabled true", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCA := vmock.NewMockCatalogAccess(ctrl)
		mockPS := vmock.NewMockPermissionService(ctrl)
		db, sqlMock, err := sqlmock.New()
		require.NoError(t, err)
		t.Cleanup(func() { _ = db.Close() })

		sqlMock.ExpectBegin()
		sqlMock.ExpectCommit()
		mockPS.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
		mockCA.EXPECT().GetByName(gomock.Any(), "catalog").Return(nil, nil)
		mockCA.EXPECT().Create(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, _ *sql.Tx, catalog *interfaces.Catalog) error {
				if !catalog.Enabled {
					t.Fatal("expected catalog to be enabled")
				}
				if catalog.HealthCheckStatus != interfaces.CatalogHealthStatusUnchecked {
					t.Fatalf("expected unchecked status, got %s", catalog.HealthCheckStatus)
				}
				return nil
			},
		)
		mockPS.EXPECT().CreateResources(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)

		cs := &catalogService{db: db, ca: mockCA, ps: mockPS}
		_, err = cs.Create(context.Background(), &interfaces.CatalogRequest{
			Name:    "catalog",
			Enabled: true,
		}, false)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		require.NoError(t, sqlMock.ExpectationsWereMet())
	})
	t.Run("create internal requires built-in admin and uses catalog hierarchy", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCA := vmock.NewMockCatalogAccess(ctrl)
		mockPS := vmock.NewMockPermissionService(ctrl)
		db, sqlMock, err := sqlmock.New()
		require.NoError(t, err)
		t.Cleanup(func() { _ = db.Close() })

		sqlMock.ExpectBegin()
		sqlMock.ExpectCommit()
		mockPS.EXPECT().CheckPermission(gomock.Any(), interfaces.PermissionResource{
			Type: interfaces.AUTH_RESOURCE_TYPE_CATALOG,
			ID:   interfaces.RESOURCE_ID_ALL,
		}, []string{interfaces.OPERATION_TYPE_CREATE}).Return(nil)
		mockCA.EXPECT().GetByName(gomock.Any(), "internal-catalog").Return(nil, nil)
		mockCA.EXPECT().Create(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, _ *sql.Tx, catalog *interfaces.Catalog) error {
				if !catalog.Builtin {
					t.Fatal("expected catalog.Builtin=true")
				}
				return nil
			},
		)
		mockPS.EXPECT().CreateResources(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, resources []interfaces.PermissionResource, ops []string) error {
				if resources[0].Type != interfaces.AUTH_RESOURCE_TYPE_CATALOG {
					t.Fatalf("expected catalog auth type, got %s", resources[0].Type)
				}
				assert.Equal(t, interfaces.CATALOG_CREATOR_OPERATIONS, ops)
				return nil
			},
		)

		cs := &catalogService{db: db, ca: mockCA, ps: mockPS}
		ctx := context.WithValue(context.Background(), interfaces.ACCOUNT_INFO_KEY,
			interfaces.AccountInfo{ID: interfaces.BuiltinAdminID})
		_, err = cs.Create(ctx, &interfaces.CatalogRequest{
			Name:    "internal-catalog",
			Builtin: true,
		}, false)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		require.NoError(t, sqlMock.ExpectationsWereMet())
	})
	t.Run("physical catalog creates default inherit schedule", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockHCSS := vmock.NewMockCatalogHealthCheckScheduleService(ctrl)
		mockHCSS.EXPECT().Create(gomock.Any(), gomock.Any(), gomock.AssignableToTypeOf(&interfaces.Catalog{}), nil).Return(&interfaces.CatalogHealthCheckSchedule{}, nil)

		cs := &catalogService{hcss: mockHCSS}
		err := cs.createHealthCheckSchedule(context.Background(), nil, &interfaces.Catalog{
			ID:   "catalog-1",
			Type: interfaces.CatalogTypePhysical,
		}, nil)

		require.NoError(t, err)
	})

	t.Run("logical catalog without schedule does not create one", func(t *testing.T) {
		cs := &catalogService{}

		err := cs.createHealthCheckSchedule(context.Background(), nil, &interfaces.Catalog{
			ID:   "catalog-1",
			Type: interfaces.CatalogTypeLogical,
		}, nil)

		require.NoError(t, err)
	})

	t.Run("returns schedule creation error without compensating delete", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockHCSS := vmock.NewMockCatalogHealthCheckScheduleService(ctrl)
		createErr := errors.New("schedule insert failed")
		catalog := &interfaces.Catalog{ID: "catalog-1", Type: interfaces.CatalogTypePhysical}

		mockHCSS.EXPECT().Create(gomock.Any(), gomock.Any(), catalog, gomock.Any()).Return(nil, createErr)

		cs := &catalogService{hcss: mockHCSS}
		err := cs.createHealthCheckSchedule(context.Background(), nil, catalog, &interfaces.CatalogHealthCheckScheduleRequest{
			Mode: interfaces.CatalogHealthCheckScheduleModeEnabled, CronExpr: "0 * * * *",
		})

		require.ErrorIs(t, err, createErr)
	})
}

func TestCatalogServiceTestConnection(t *testing.T) {
	t.Run("temporary plaintext config never calls storage cipher", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		ps := vmock.NewMockPermissionService(ctrl)
		cf := vmock.NewMockConnectorFactory(ctrl)
		connector := vmock.NewMockConnector(ctrl)
		cfg := interfaces.ConnectorConfig{"token": "fixture", "variables": map[string]any{"nested": []any{map[string]any{"value": false}}}}
		ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
		cf.EXPECT().CreateConnectorInstance(gomock.Any(), "http", gomock.Any()).DoAndReturn(func(_ context.Context, _ string, got interfaces.ConnectorConfig) (interfaces.Connector, error) {
			assert.Equal(t, cfg, got)
			got["variables"].(map[string]any)["nested"].([]any)[0].(map[string]any)["value"] = true
			return connector, nil
		})
		connector.EXPECT().TestConnection(gomock.Any()).Return(nil)
		connector.EXPECT().Close(gomock.Any()).Return(nil)
		cs := &catalogService{ps: ps, cf: cf, appSetting: &common.AppSetting{CryptoSetting: common.CryptoSetting{Enabled: true}}, cipher: &mockCipher{encryptFunc: func(string) (string, error) { t.Fatal("temporary config must not encrypt"); return "", nil }, decryptFunc: func(string) (string, error) { t.Fatal("temporary config must not decrypt"); return "", nil }}}
		got, err := cs.TestConnectionConfig(context.Background(), &interfaces.CatalogConnectionTestRequest{ConnectorType: "http", ConnectorCfg: cfg})
		require.NoError(t, err)
		assert.Equal(t, interfaces.CatalogHealthStatusHealthy, got.HealthCheckStatus)
		assert.Equal(t, false, cfg["variables"].(map[string]any)["nested"].([]any)[0].(map[string]any)["value"])
	})

	for _, saved := range []bool{false, true} {
		for _, failInitialization := range []bool{false, true} {
			t.Run(fmt.Sprintf("dynamic credentials source and logs saved=%t initialization=%t", saved, failInitialization), func(t *testing.T) {
				ctrl := gomock.NewController(t)
				cf := vmock.NewMockConnectorFactory(ctrl)
				ca := vmock.NewMockCatalogAccess(ctrl)
				ps := vmock.NewMockPermissionService(ctrl)
				connector := vmock.NewMockConnector(ctrl)
				var logged []string
				patches := gomonkey.ApplyFunc(otellog.LogError, func(_ context.Context, _ string, err error, _ ...attribute.KeyValue) {
					logged = append(logged, err.Error())
				})
				defer patches.Reset()
				cs := &catalogService{cf: cf, ca: ca, ps: ps, appSetting: &common.AppSetting{CryptoSetting: common.CryptoSetting{Enabled: true}}, cipher: &mockCipher{decryptFunc: func(value string) (string, error) { return "plain@" + value + " value", nil }}}
				plaintextFixture := "plain@nested value"
				sourceErr := errors.New("source connection unavailable")
				cfg := interfaces.ConnectorConfig{"variables": map[string]any{"fixture_option": plaintextFixture, "normal": false}, "sensitive_variables": []any{"fixture_option"}}
				if saved {
					cfg["variables"].(map[string]any)["fixture_option"] = "ENC:nested"
				}
				if saved {
					cf.EXPECT().GetSensitiveFields("custom", gomock.Any()).Return([]string{"fixture_option"}, nil)
				}
				cf.EXPECT().CreateConnectorInstance(gomock.Any(), "custom", gomock.Any()).DoAndReturn(func(_ context.Context, _ string, got interfaces.ConnectorConfig) (interfaces.Connector, error) {
					assert.Equal(t, plaintextFixture, got["variables"].(map[string]any)["fixture_option"])
					got["variables"].(map[string]any)["normal"] = true
					if failInitialization {
						return nil, sourceErr
					}
					return connector, nil
				})
				if !failInitialization {
					connector.EXPECT().TestConnection(gomock.Any()).Return(sourceErr)
					connector.EXPECT().Close(gomock.Any()).Return(nil)
				}
				var result *interfaces.CatalogHealthCheckStatus
				var err error
				if saved {
					ca.EXPECT().GetByID(gomock.Any(), "one").Return(&interfaces.Catalog{ID: "one", ConnectorType: "custom", ConnectorCfg: cfg}, nil)
					if !failInitialization {
						ca.EXPECT().UpdateHealthCheckStatus(gomock.Any(), "one", gomock.Any()).Return(nil)
					}
					result, err = cs.InternalTestConnection(context.Background(), "one")
				} else {
					ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
					result, err = cs.TestConnectionConfig(context.Background(), &interfaces.CatalogConnectionTestRequest{ConnectorType: "custom", ConnectorCfg: cfg})
				}
				if failInitialization {
					require.Error(t, err)
					assert.Nil(t, result)
					assert.NotContains(t, err.Error(), plaintextFixture)
				} else {
					require.NoError(t, err)
					assert.NotContains(t, result.HealthCheckResult, plaintextFixture)
					assert.NotContains(t, result.HealthCheckResult, "plain%40nested%20value")
				}
				require.NotEmpty(t, logged)
				for _, entry := range logged {
					assert.NotContains(t, entry, plaintextFixture)
					assert.NotContains(t, entry, "plain%40nested%20value")
				}
				if saved {
					assert.Equal(t, "ENC:nested", cfg["variables"].(map[string]any)["fixture_option"])
				} else {
					assert.Equal(t, plaintextFixture, cfg["variables"].(map[string]any)["fixture_option"])
				}
				assert.Equal(t, false, cfg["variables"].(map[string]any)["normal"])
			})
		}
	}

	for _, tc := range []struct {
		name   string
		cause  error
		status string
	}{
		{"TCP TLS success", nil, interfaces.CatalogHealthStatusHealthy},
		{"TCP TLS failure", errors.New("TCP dial failed"), interfaces.CatalogHealthStatusUnhealthy},
		{"caller cancelled", context.Canceled, interfaces.CatalogHealthStatusUnhealthy},
	} {
		t.Run("API worker health "+tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			ca, cf := vmock.NewMockCatalogAccess(ctrl), vmock.NewMockConnectorFactory(ctrl)
			connector := vmock.NewMockConnector(ctrl)
			config := interfaces.ConnectorConfig{"base_url": "https://api.example.com/", "token": "fixture-secret"}
			ca.EXPECT().GetByID(gomock.Any(), "api-1").Return(&interfaces.Catalog{ID: "api-1", ConnectorType: "http", ConnectorCfg: config}, nil)
			cf.EXPECT().GetSensitiveFields("http", gomock.Any()).Return([]string{"token"}, nil)
			cf.EXPECT().CreateConnectorInstance(gomock.Any(), "http", config).Return(connector, nil)
			connector.EXPECT().TestConnection(gomock.Any()).DoAndReturn(func(ctx context.Context) error {
				deadline, ok := ctx.Deadline()
				require.True(t, ok)
				assert.LessOrEqual(t, time.Until(deadline), 2*time.Second)
				return tc.cause
			})
			connector.EXPECT().Close(gomock.Any()).Return(nil)
			ca.EXPECT().UpdateHealthCheckStatus(gomock.Any(), "api-1", gomock.Any()).DoAndReturn(func(_ context.Context, _ string, result interfaces.CatalogHealthCheckStatus) error {
				assert.Equal(t, tc.status, result.HealthCheckStatus)
				assert.NotContains(t, result.HealthCheckResult, "fixture-secret")
				if tc.cause == nil {
					assert.Equal(t, "Connection test succeeded.", result.HealthCheckResult)
				}
				return nil
			})
			cs := &catalogService{ca: ca, cf: cf, appSetting: &common.AppSetting{CatalogHealthCheck: common.CatalogHealthCheckConfig{Timeout: 2 * time.Second}}}
			result, err := cs.InternalTestConnection(context.Background(), "api-1")
			require.NoError(t, err)
			assert.Equal(t, tc.status, result.HealthCheckStatus)
		})
	}
	for _, saved := range []bool{false, true} {
		for _, cause := range []error{nil, errors.New("fixture dial failed")} {
			t.Run(fmt.Sprintf("API transport test saved=%t failure=%t", saved, cause != nil), func(t *testing.T) {
				ctrl := gomock.NewController(t)
				ps, cf, ca := vmock.NewMockPermissionService(ctrl), vmock.NewMockConnectorFactory(ctrl), vmock.NewMockCatalogAccess(ctrl)
				connector := vmock.NewMockConnector(ctrl)
				ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
				if saved {
					cf.EXPECT().GetSensitiveFields("http", gomock.Any()).Return(nil, nil)
				}
				cf.EXPECT().CreateConnectorInstance(gomock.Any(), "http", gomock.Any()).Return(connector, nil)
				connector.EXPECT().TestConnection(gomock.Any()).Return(cause)
				connector.EXPECT().Close(gomock.Any()).Return(nil)
				cs := &catalogService{ps: ps, cf: cf, ca: ca, appSetting: &common.AppSetting{}}
				var result *interfaces.CatalogHealthCheckStatus
				var err error
				if saved {
					ca.EXPECT().GetByID(gomock.Any(), "api-1").Return(&interfaces.Catalog{ID: "api-1", ConnectorType: "http"}, nil)
					ca.EXPECT().UpdateHealthCheckStatus(gomock.Any(), "api-1", gomock.Any()).Return(nil)
					result, err = cs.TestConnection(context.Background(), "api-1")
				} else {
					result, err = cs.TestConnectionConfig(context.Background(), &interfaces.CatalogConnectionTestRequest{ConnectorType: "http"})
				}
				require.NoError(t, err)
				require.NotNil(t, result)
				if cause == nil {
					assert.Equal(t, interfaces.CatalogHealthStatusHealthy, result.HealthCheckStatus)
					assert.Equal(t, "Connection test succeeded.", result.HealthCheckResult)
				} else {
					assert.Equal(t, interfaces.CatalogHealthStatusUnhealthy, result.HealthCheckStatus)
					assert.Contains(t, result.HealthCheckResult, "fixture dial failed")
				}
			})
		}
	}

	t.Run("records safe connector details during preflight", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		ps := vmock.NewMockPermissionService(ctrl)
		connector := vmock.NewMockConnector(ctrl)

		connectorFactory := vmock.NewMockConnectorFactory(ctrl)
		config := interfaces.ConnectorConfig{
			"host":      "db.example.com",
			"port":      3306,
			"username":  "reporter",
			"password":  "secret-password",
			"databases": []string{"sales"},
		}
		connectorError := "dial tcp db.example.com:3306: access denied for reporter on sales"

		ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
		connectorFactory.EXPECT().CreateConnectorInstance(gomock.Any(), "mariadb", config).Return(connector, nil)
		connector.EXPECT().TestConnection(gomock.Any()).Return(errors.New(connectorError))
		connector.EXPECT().Close(gomock.Any()).Return(nil)

		cs := &catalogService{
			appSetting: &common.AppSetting{},
			ps:         ps,
			cf:         connectorFactory,
		}
		result, err := cs.TestConnectionConfig(context.Background(), &interfaces.CatalogConnectionTestRequest{
			ConnectorType: "mariadb",
			ConnectorCfg:  config,
		})

		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Equal(t, interfaces.CatalogHealthStatusUnhealthy, result.HealthCheckStatus)
		assert.Equal(t,
			"dial tcp db.example.com:3306: access denied for reporter on sales",
			result.HealthCheckResult,
		)
		assert.NotContains(t, result.HealthCheckResult, "secret-password")
	})
	t.Run("returns connector details for a saved catalog", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		ca := vmock.NewMockCatalogAccess(ctrl)
		ps := vmock.NewMockPermissionService(ctrl)
		connector := vmock.NewMockConnector(ctrl)

		connectorFactory := vmock.NewMockConnectorFactory(ctrl)
		config := interfaces.ConnectorConfig{
			"host":     "db.example.com",
			"port":     3306,
			"username": "reporter",
			"password": "secret-password",
		}
		connectorError := "dial tcp db.example.com:3306: i/o timeout"
		expectedError := "dial tcp db.example.com:3306: i/o timeout"

		ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
		ca.EXPECT().GetByID(gomock.Any(), "catalog-1").Return(&interfaces.Catalog{
			ID:            "catalog-1",
			Type:          interfaces.CatalogTypePhysical,
			ConnectorType: "mariadb",
			ConnectorCfg:  config,
		}, nil)
		connectorFactory.EXPECT().GetSensitiveFields("mariadb", gomock.Any()).Return([]string{"password"}, nil)
		connectorFactory.EXPECT().CreateConnectorInstance(gomock.Any(), "mariadb", config).Return(connector, nil)
		connector.EXPECT().TestConnection(gomock.Any()).Return(errors.New(connectorError))
		connector.EXPECT().Close(gomock.Any()).Return(nil)
		ca.EXPECT().UpdateHealthCheckStatus(gomock.Any(), "catalog-1", gomock.Any()).DoAndReturn(
			func(_ context.Context, _ string, status interfaces.CatalogHealthCheckStatus) error {
				assert.Equal(t, expectedError, status.HealthCheckResult)
				return nil
			},
		)

		cs := &catalogService{
			appSetting: &common.AppSetting{},
			ca:         ca,
			ps:         ps,
			cf:         connectorFactory,
		}
		result, err := cs.TestConnection(context.Background(), "catalog-1")

		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Equal(t, interfaces.CatalogHealthStatusUnhealthy, result.HealthCheckStatus)
		assert.Equal(t, expectedError, result.HealthCheckResult)
		assert.NotContains(t, result.HealthCheckResult, "secret-password")
	})

	t.Run("does not expose connector initialization error during preflight", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockPS := vmock.NewMockPermissionService(ctrl)
		sensitiveError := "invalid endpoint db.internal"

		mockPS.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
		connectorFactory := vmock.NewMockConnectorFactory(ctrl)
		connectorFactory.EXPECT().CreateConnectorInstance(gomock.Any(), "mariadb", gomock.Any()).
			Return(nil, errors.New(sensitiveError))
		cs := &catalogService{
			ps: mockPS,
			cf: connectorFactory,
		}
		result, err := cs.TestConnectionConfig(context.Background(), &interfaces.CatalogConnectionTestRequest{
			ConnectorType: "mariadb",
		})

		assert.Nil(t, result)
		requireRedactedConnectorInitializationError(t, err, sensitiveError)
	})

	t.Run("returns not found for missing catalog", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		ca := vmock.NewMockCatalogAccess(ctrl)
		ps := vmock.NewMockPermissionService(ctrl)
		ps.EXPECT().CheckPermission(gomock.Any(), interfaces.PermissionResource{
			Type: interfaces.AUTH_RESOURCE_TYPE_CATALOG,
			ID:   "missing",
		}, []string{interfaces.OPERATION_TYPE_MODIFY}).Return(nil)
		ca.EXPECT().GetByID(gomock.Any(), "missing").Return(nil, nil)

		cs := &catalogService{ca: ca, ps: ps}
		result, err := cs.TestConnection(context.Background(), "missing")

		var httpErr *httperrors.HTTPError
		ok := errors.As(err, &httpErr)
		require.True(t, ok)
		assert.Equal(t, http.StatusNotFound, httpErr.HTTPCode)
		assert.Nil(t, result)
	})
	t.Run("rejects internal catalog for non-admin", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		ca := vmock.NewMockCatalogAccess(ctrl)
		ps := vmock.NewMockPermissionService(ctrl)
		ps.EXPECT().CheckPermission(gomock.Any(), interfaces.PermissionResource{
			Type: interfaces.AUTH_RESOURCE_TYPE_CATALOG,
			ID:   "internal-catalog",
		}, []string{interfaces.OPERATION_TYPE_MODIFY}).Return(nil)
		ca.EXPECT().GetByID(gomock.Any(), "internal-catalog").Return(&interfaces.Catalog{
			ID:      "internal-catalog",
			Builtin: true,
		}, nil)

		cs := &catalogService{ca: ca, ps: ps}
		result, err := cs.TestConnection(context.WithValue(context.Background(), interfaces.ACCOUNT_INFO_KEY,
			interfaces.AccountInfo{ID: "regular-user", Type: interfaces.ACCESSOR_TYPE_USER}), "internal-catalog")

		var httpErr *httperrors.HTTPError
		require.ErrorAs(t, err, &httpErr)
		assert.Equal(t, http.StatusForbidden, httpErr.HTTPCode)
		assert.Nil(t, result)
	})
	t.Run("redacts catalog query error", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		ca := vmock.NewMockCatalogAccess(ctrl)
		ps := vmock.NewMockPermissionService(ctrl)
		sensitiveError := "select t_catalog failed at db.internal"
		ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
		ca.EXPECT().GetByID(gomock.Any(), "catalog-1").Return(nil, errors.New(sensitiveError))

		cs := &catalogService{ca: ca, ps: ps}
		result, err := cs.TestConnection(context.Background(), "catalog-1")

		var httpErr *httperrors.HTTPError
		require.ErrorAs(t, err, &httpErr)
		assert.Equal(t, http.StatusInternalServerError, httpErr.HTTPCode)
		assert.Equal(t, verrors.VegaBackend_Catalog_InternalError_GetFailed, httpErr.ErrorCode)
		assert.Contains(t, fmt.Sprint(httpErr.ErrorDetails), "failed to get catalog for connection test")
		assert.NotContains(t, fmt.Sprint(httpErr.ErrorDetails), sensitiveError)
		assert.Nil(t, result)
	})
	t.Run("redacts health status update error", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		ca := vmock.NewMockCatalogAccess(ctrl)
		ps := vmock.NewMockPermissionService(ctrl)
		connector := vmock.NewMockConnector(ctrl)

		sensitiveError := "update t_catalog health status failed at db.internal"

		ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
		ca.EXPECT().GetByID(gomock.Any(), "catalog-1").Return(&interfaces.Catalog{
			ID:            "catalog-1",
			Type:          interfaces.CatalogTypePhysical,
			ConnectorType: "mariadb",
		}, nil)
		connector.EXPECT().TestConnection(gomock.Any()).Return(nil)
		connector.EXPECT().Close(gomock.Any()).Return(nil)
		ca.EXPECT().UpdateHealthCheckStatus(gomock.Any(), "catalog-1", gomock.Any()).
			Return(errors.New(sensitiveError))
		connectorFactory := vmock.NewMockConnectorFactory(ctrl)
		connectorFactory.EXPECT().GetSensitiveFields("mariadb", gomock.Any()).Return(nil, nil)
		connectorFactory.EXPECT().CreateConnectorInstance(gomock.Any(), "mariadb", gomock.Any()).Return(connector, nil)

		cs := &catalogService{
			appSetting: &common.AppSetting{},
			ca:         ca,
			ps:         ps,
			cf:         connectorFactory,
		}
		result, err := cs.TestConnection(context.Background(), "catalog-1")

		var httpErr *httperrors.HTTPError
		require.ErrorAs(t, err, &httpErr)
		assert.Equal(t, http.StatusInternalServerError, httpErr.HTTPCode)
		assert.Equal(t, verrors.VegaBackend_Catalog_InternalError_UpdateFailed, httpErr.ErrorCode)
		assert.Contains(t, fmt.Sprint(httpErr.ErrorDetails), "failed to update catalog health check status")
		assert.NotContains(t, fmt.Sprint(httpErr.ErrorDetails), sensitiveError)
		assert.Nil(t, result)
	})
	t.Run("test connection logical catalog returns an explicit failure", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCA := vmock.NewMockCatalogAccess(ctrl)
		mockPS := vmock.NewMockPermissionService(ctrl)
		catalog := &interfaces.Catalog{
			ID: "catalog-1",
			CatalogHealthCheckStatus: interfaces.CatalogHealthCheckStatus{
				HealthCheckStatus: interfaces.CatalogHealthStatusHealthy,
				LastCheckTime:     1234567890,
			},
		}
		mockCA.EXPECT().GetByID(gomock.Any(), "catalog-1").Return(catalog, nil)
		mockPS.EXPECT().CheckPermission(gomock.Any(), interfaces.PermissionResource{
			Type: interfaces.AUTH_RESOURCE_TYPE_CATALOG,
			ID:   "catalog-1",
		}, []string{interfaces.OPERATION_TYPE_MODIFY}).Return(nil)

		cs := &catalogService{ca: mockCA, ps: mockPS}
		result, err := cs.TestConnection(context.Background(), "catalog-1")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.HealthCheckStatus != interfaces.CatalogHealthStatusUnhealthy {
			t.Errorf("expected unhealthy status, got %s", result.HealthCheckStatus)
		}
		if result.HealthCheckResult == "" {
			t.Fatal("expected an explicit logical catalog failure message")
		}
	})

	t.Run("rejects manual connection test without modify permission", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockPS := vmock.NewMockPermissionService(ctrl)
		permissionErr := errors.New("modify permission denied")
		mockPS.EXPECT().CheckPermission(gomock.Any(), interfaces.PermissionResource{
			Type: interfaces.AUTH_RESOURCE_TYPE_CATALOG,
			ID:   "catalog-1",
		}, []string{interfaces.OPERATION_TYPE_MODIFY}).Return(permissionErr)

		cs := &catalogService{ps: mockPS}
		result, err := cs.TestConnection(context.Background(), "catalog-1")

		require.ErrorIs(t, err, permissionErr)
		assert.Nil(t, result)
	})

	t.Run("internal connection test bypasses user permission checks", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCA := vmock.NewMockCatalogAccess(ctrl)
		mockCA.EXPECT().GetByID(gomock.Any(), "catalog-1").Return(&interfaces.Catalog{ID: "catalog-1"}, nil)

		cs := &catalogService{ca: mockCA}
		result, err := cs.InternalTestConnection(context.Background(), "catalog-1")

		require.NoError(t, err)
		assert.Equal(t, interfaces.CatalogHealthStatusUnhealthy, result.HealthCheckStatus)
	})
}

func TestConnectionErrorResult(t *testing.T) {
	t.Run("preserves the connector-provided safe message", func(t *testing.T) {
		assert.Equal(t, "source unavailable", connectionErrorResult(errors.New("source unavailable")))
		assert.Empty(t, connectionErrorResult(nil))
		assert.Equal(t, connectionTestFailedResult, connectionErrorResult(errors.New(" \r\n ")))
	})
	t.Run("flattens and limits a connector response", func(t *testing.T) {
		err := errors.New("first line\r\n" + strings.Repeat("x", maximumConnectionTestResultLength+10))
		result := connectionErrorResult(err)
		assert.NotContains(t, result, "\n")
		assert.LessOrEqual(t, len([]rune(result)), maximumConnectionTestResultLength)
		assert.True(t, strings.HasSuffix(result, "..."))
	})
}

func TestCatalogServiceTestConnectorConnection(t *testing.T) {
	t.Run("passes configured timeout to connector", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		connector := vmock.NewMockConnector(ctrl)

		cs := &catalogService{appSetting: &common.AppSetting{CatalogHealthCheck: common.CatalogHealthCheckConfig{Timeout: 2 * time.Second}}}

		connector.EXPECT().TestConnection(gomock.Any()).DoAndReturn(func(ctx context.Context) error {
			deadline, ok := ctx.Deadline()
			require.True(t, ok)
			assert.WithinDuration(t, time.Now().Add(2*time.Second), deadline, 100*time.Millisecond)
			return nil
		})

		require.NoError(t, cs.testConnectorConnection(context.Background(), connector))
	})

	t.Run("uses default timeout when configuration is absent", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		connector := vmock.NewMockConnector(ctrl)

		cs := &catalogService{appSetting: &common.AppSetting{}}

		connector.EXPECT().TestConnection(gomock.Any()).DoAndReturn(func(ctx context.Context) error {
			deadline, ok := ctx.Deadline()
			require.True(t, ok)
			assert.WithinDuration(t, time.Now().Add(defaultConnectionTestTimeout), deadline, 100*time.Millisecond)
			return nil
		})

		require.NoError(t, cs.testConnectorConnection(context.Background(), connector))
	})
}

func TestCatalogServiceUpdate(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(fmt.Sprintf("reserved sensitive plaintext returns 400 without persistence crypto=%t", enabled), func(t *testing.T) {
			ctrl := gomock.NewController(t)
			ps := vmock.NewMockPermissionService(ctrl)
			ca := vmock.NewMockCatalogAccess(ctrl)
			cf := vmock.NewMockConnectorFactory(ctrl)
			connector := vmock.NewMockConnector(ctrl)
			cfg := interfaces.ConnectorConfig{"fixture_option": nil, "variables": map[string]any{"fixture_option": "eNc:private-fixture"}, "sensitive_variables": []string{"fixture_option"}}
			ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			cf.EXPECT().GetSensitiveFields("http", cfg).Return([]string{"fixture_option"}, nil)
			cf.EXPECT().CreateConnectorInstance(gomock.Any(), "http", gomock.Any()).Return(connector, nil)
			connector.EXPECT().GetCategory().Return(interfaces.ConnectorCategoryAPI).AnyTimes()
			connector.EXPECT().TestConnection(gomock.Any()).Return(nil)
			connector.EXPECT().Close(gomock.Any()).Return(nil)
			cs := &catalogService{ca: ca, ps: ps, cf: cf, appSetting: &common.AppSetting{CryptoSetting: common.CryptoSetting{Enabled: enabled}}, cipher: &mockCipher{encryptFunc: func(string) (string, error) {
				t.Fatal("reserved plaintext must not reach the cipher")
				return "", nil
			}}}
			req := &interfaces.CatalogRequest{ID: "one", Name: "API", ConnectorType: "http", ConnectorCfg: cfg}
			ca.EXPECT().GetByID(gomock.Any(), "one").Return(&interfaces.Catalog{ID: "one", Name: "API", ConnectorType: "http", Category: interfaces.ConnectorCategoryAPI}, nil)
			err := cs.Update(context.Background(), req, true)
			var httpErr *httperrors.HTTPError
			require.ErrorAs(t, err, &httpErr)
			assert.Equal(t, http.StatusBadRequest, httpErr.HTTPCode)
			assert.Equal(t, verrors.VegaBackend_Catalog_InvalidParameter, httpErr.ErrorCode)
			assert.Contains(t, fmt.Sprint(httpErr.ErrorDetails), "plaintext must not start with ENC:")
			assert.NotContains(t, fmt.Sprint(httpErr.ErrorDetails), "private-fixture")
			assert.Equal(t, "eNc:private-fixture", cfg["variables"].(map[string]any)["fixture_option"])
		})
	}
	t.Run("explicit plaintext replacement encrypts fixed and variables without mutating the request", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		ca := vmock.NewMockCatalogAccess(ctrl)
		ps := vmock.NewMockPermissionService(ctrl)
		cf := vmock.NewMockConnectorFactory(ctrl)
		connector := vmock.NewMockConnector(ctrl)
		db, sqlMock, err := sqlmock.New()
		require.NoError(t, err)
		t.Cleanup(func() { _ = db.Close() })
		cfg := interfaces.ConnectorConfig{"token": "updated-fixture", "variables": map[string]any{"fixture_option": "literal-client", "rows": []any{map[string]any{"flag": false}}}}
		before, _ := json.Marshal(cfg)
		ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
		ca.EXPECT().GetByID(gomock.Any(), "one").Return(&interfaces.Catalog{ID: "one", Name: "API", ConnectorType: "http", Category: interfaces.ConnectorCategoryAPI, ConnectorCfg: interfaces.ConnectorConfig{"token": "ENC:previous"}}, nil)
		cf.EXPECT().GetSensitiveFields("http", cfg).Return([]string{"token", "fixture_option"}, nil)
		cf.EXPECT().CreateConnectorInstance(gomock.Any(), "http", gomock.Any()).DoAndReturn(func(_ context.Context, _ string, got interfaces.ConnectorConfig) (interfaces.Connector, error) {
			assert.Equal(t, cfg, got)
			got["variables"].(map[string]any)["rows"].([]any)[0].(map[string]any)["flag"] = true
			return connector, nil
		})
		checked := false
		connector.EXPECT().TestConnection(gomock.Any()).DoAndReturn(func(context.Context) error { checked = true; return nil })
		connector.EXPECT().Close(gomock.Any()).Return(nil)
		sqlMock.ExpectBegin()
		sqlMock.ExpectCommit()
		ca.EXPECT().Update(gomock.Any(), gomock.Any(), gomock.Any(), int64(9), true).DoAndReturn(func(_ context.Context, _ *sql.Tx, saved *interfaces.Catalog, _ int64, _ bool) (int64, error) {
			assert.Equal(t, "ENC:encrypted_updated-fixture", saved.ConnectorCfg["token"])
			assert.Equal(t, "ENC:encrypted_literal-client", saved.ConnectorCfg["variables"].(map[string]any)["fixture_option"])
			assert.Equal(t, false, saved.ConnectorCfg["variables"].(map[string]any)["rows"].([]any)[0].(map[string]any)["flag"])
			assert.Equal(t, interfaces.CatalogHealthStatusHealthy, saved.HealthCheckStatus)
			return 1, nil
		})
		cs := &catalogService{ca: ca, ps: ps, cf: cf, db: db, appSetting: &common.AppSetting{CryptoSetting: common.CryptoSetting{Enabled: true}}, cipher: &mockCipher{encryptFunc: func(value string) (string, error) { assert.True(t, checked); return "encrypted_" + value, nil }, decryptFunc: func(string) (string, error) {
			t.Fatal("plaintext replacement must not decrypt old or new values")
			return "", nil
		}}}
		err = cs.Update(context.Background(), &interfaces.CatalogRequest{ID: "one", Name: "API", ConnectorType: "http", ConnectorCfg: cfg, ExpectedUpdateTime: 9}, false)
		require.NoError(t, err)
		after, _ := json.Marshal(cfg)
		assert.Equal(t, string(before), string(after))
		require.NoError(t, sqlMock.ExpectationsWereMet())
	})

	for _, allowUnhealthy := range []bool{false, true} {
		t.Run(fmt.Sprintf("storage encryption failure never persists allow_unhealthy=%t", allowUnhealthy), func(t *testing.T) {
			ctrl := gomock.NewController(t)
			ca := vmock.NewMockCatalogAccess(ctrl)
			ps := vmock.NewMockPermissionService(ctrl)
			cf := vmock.NewMockConnectorFactory(ctrl)
			connector := vmock.NewMockConnector(ctrl)
			cfg := interfaces.ConnectorConfig{"token": "first-fixture", "variables": map[string]any{"fixture_option": "failed-fixture", "rows": []any{map[string]any{"flag": false}}}}
			before, _ := json.Marshal(cfg)
			req := &interfaces.CatalogRequest{Name: "API", ConnectorType: "http", ConnectorCfg: cfg}
			ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			cf.EXPECT().GetSensitiveFields("http", cfg).Return([]string{"token", "fixture_option"}, nil)
			cf.EXPECT().CreateConnectorInstance(gomock.Any(), "http", gomock.Any()).DoAndReturn(func(_ context.Context, _ string, got interfaces.ConnectorConfig) (interfaces.Connector, error) {
				assert.Equal(t, cfg, got)
				got["variables"].(map[string]any)["rows"].([]any)[0].(map[string]any)["flag"] = true
				return connector, nil
			})
			checked := false
			connector.EXPECT().TestConnection(gomock.Any()).DoAndReturn(func(context.Context) error {
				checked = true
				if allowUnhealthy {
					return errors.New("source temporarily unavailable")
				}
				return nil
			})
			connector.EXPECT().Close(gomock.Any()).Return(nil)
			cs := &catalogService{ca: ca, ps: ps, cf: cf, appSetting: &common.AppSetting{CryptoSetting: common.CryptoSetting{Enabled: true}}, cipher: &mockCipher{encryptFunc: func(value string) (string, error) {
				assert.True(t, checked)
				if value == "failed-fixture" {
					return "", errors.New("provider disclosed failed-fixture")
				}
				return "cipher", nil
			}, decryptFunc: func(string) (string, error) { t.Fatal("plaintext update must not decrypt"); return "", nil }}}
			req.ID = "one"
			ca.EXPECT().GetByID(gomock.Any(), "one").Return(&interfaces.Catalog{ID: "one", Name: "API", ConnectorType: "http", Category: interfaces.ConnectorCategoryAPI}, nil)
			err := cs.Update(context.Background(), req, allowUnhealthy)
			var httpErr *httperrors.HTTPError
			require.ErrorAs(t, err, &httpErr)
			assert.Equal(t, http.StatusInternalServerError, httpErr.HTTPCode)
			assert.Equal(t, verrors.VegaBackend_Catalog_InternalError_UpdateFailed, httpErr.ErrorCode)
			assert.NotContains(t, fmt.Sprint(httpErr.ErrorDetails), "provider disclosed")
			assert.NotContains(t, fmt.Sprint(httpErr.ErrorDetails), "failed-fixture")
			after, _ := json.Marshal(cfg)
			assert.Equal(t, string(before), string(after))
		})
	}

	t.Run("metadata lookup error is not a decryption or connectivity failure", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		ps, cf, ca := vmock.NewMockPermissionService(ctrl), vmock.NewMockConnectorFactory(ctrl), vmock.NewMockCatalogAccess(ctrl)
		ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
		cf.EXPECT().GetSensitiveFields("missing", gomock.Any()).Return(nil, factory.ErrConnectorUnavailable)
		cs := &catalogService{ps: ps, cf: cf, ca: ca}
		req := &interfaces.CatalogRequest{ID: "one", Name: "name", ConnectorType: "missing", ConnectorCfg: map[string]any{}}
		ca.EXPECT().GetByID(gomock.Any(), "one").Return(&interfaces.Catalog{ID: "one", Name: "name", ConnectorType: "missing"}, nil)
		err := cs.Update(context.Background(), req, true)
		var httpErr *httperrors.HTTPError
		require.ErrorAs(t, err, &httpErr)
		assert.Equal(t, verrors.VegaBackend_Catalog_InternalError_CreateFailed, httpErr.ErrorCode)
	})
	t.Run("stored API category preserves local validation for unavailable connectors", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		ps, cf, ca := vmock.NewMockPermissionService(ctrl), vmock.NewMockConnectorFactory(ctrl), vmock.NewMockCatalogAccess(ctrl)
		ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
		ca.EXPECT().GetByID(gomock.Any(), "one").Return(&interfaces.Catalog{ID: "one", Name: "name", ConnectorType: "missing", Category: interfaces.ConnectorCategoryAPI}, nil)
		cf.EXPECT().GetSensitiveFields("missing", gomock.Any()).Return(nil, factory.ErrConnectorUnavailable)
		cs := &catalogService{ps: ps, cf: cf, ca: ca}
		err := cs.Update(context.Background(), &interfaces.CatalogRequest{ID: "one", Name: "name", ConnectorType: "missing"}, true)
		var httpErr *httperrors.HTTPError
		require.ErrorAs(t, err, &httpErr)
		assert.Equal(t, http.StatusBadRequest, httpErr.HTTPCode)
		assert.Equal(t, verrors.VegaBackend_Catalog_InternalError_CreateFailed, httpErr.ErrorCode)
	})
	for _, allow := range []bool{false, true} {
		t.Run(fmt.Sprintf("API transport failure allow_unhealthy=%t", allow), func(t *testing.T) {
			ctrl := gomock.NewController(t)
			ps, cf, ca := vmock.NewMockPermissionService(ctrl), vmock.NewMockConnectorFactory(ctrl), vmock.NewMockCatalogAccess(ctrl)
			connector := vmock.NewMockConnector(ctrl)
			ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			cf.EXPECT().GetSensitiveFields("http", gomock.Any()).Return(nil, nil)
			cf.EXPECT().CreateConnectorInstance(gomock.Any(), "http", gomock.Any()).Return(connector, nil)
			connector.EXPECT().TestConnection(gomock.Any()).Return(context.DeadlineExceeded)
			connector.EXPECT().Close(gomock.Any()).Return(nil)
			cs := &catalogService{ps: ps, cf: cf, ca: ca, appSetting: &common.AppSetting{}}
			req := &interfaces.CatalogRequest{ID: "api-1", Name: "API", ConnectorType: "http", ConnectorCfg: map[string]any{"auth_type": "none"}, ExpectedUpdateTime: 9}
			ca.EXPECT().GetByID(gomock.Any(), "api-1").Return(&interfaces.Catalog{ID: "api-1", Name: "API", ConnectorType: "http"}, nil)
			if allow {
				db, sqlMock, err := sqlmock.New()
				require.NoError(t, err)
				t.Cleanup(func() { _ = db.Close(); require.NoError(t, sqlMock.ExpectationsWereMet()) })
				cs.db = db
				sqlMock.ExpectBegin()
				sqlMock.ExpectCommit()
				ca.EXPECT().Update(gomock.Any(), gomock.Any(), gomock.Any(), int64(9), true).DoAndReturn(func(_ context.Context, _ *sql.Tx, saved *interfaces.Catalog, _ int64, _ bool) (int64, error) {
					assert.Equal(t, interfaces.CatalogHealthStatusUnhealthy, saved.HealthCheckStatus)
					assert.Equal(t, connectionTestFailedResult, saved.HealthCheckResult)
					return 1, nil
				})
			}
			err := cs.Update(context.Background(), req, allow)
			if allow {
				require.NoError(t, err)
			} else {
				var httpErr *httperrors.HTTPError
				require.ErrorAs(t, err, &httpErr)
				assert.Equal(t, http.StatusBadRequest, httpErr.HTTPCode)
			}
		})
	}
	for _, tc := range []struct {
		name   string
		cause  error
		status int
	}{
		{"invalid config", errors.New("fixture-invalid-config"), http.StatusBadRequest},
		{"unavailable", factory.ErrConnectorUnavailable, http.StatusBadRequest},
		{"disabled", factory.ErrConnectorDisabled, http.StatusConflict},
		{"edition denied", factory.ErrConnectorEntitlementDenied, http.StatusForbidden},
	} {
		t.Run("API rejects "+tc.name+" before writes", func(t *testing.T) {
			ctrl := gomock.NewController(t)
			ps, cf, ca := vmock.NewMockPermissionService(ctrl), vmock.NewMockConnectorFactory(ctrl), vmock.NewMockCatalogAccess(ctrl)
			ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			cf.EXPECT().GetSensitiveFields("http", gomock.Any()).Return(nil, nil)
			cf.EXPECT().CreateConnectorInstance(gomock.Any(), "http", gomock.Any()).Return(nil, tc.cause)
			cs := &catalogService{ps: ps, cf: cf, ca: ca}
			ca.EXPECT().GetByID(gomock.Any(), "api-1").Return(&interfaces.Catalog{ID: "api-1", Name: "API", ConnectorType: "http", Category: interfaces.ConnectorCategoryAPI, ConnectorCfg: map[string]any{"auth_type": "none"}}, nil)
			err := cs.Update(context.Background(), &interfaces.CatalogRequest{ID: "api-1", Name: "API", ConnectorType: "http"}, true)
			var httpErr *httperrors.HTTPError
			require.ErrorAs(t, err, &httpErr)
			assert.Equal(t, tc.status, httpErr.HTTPCode)
		})
	}
	for _, replacement := range []bool{false, true} {
		t.Run(fmt.Sprintf("API configuration local validation replacement=%t", replacement), func(t *testing.T) {
			ctrl := gomock.NewController(t)
			ca, ps, cf := vmock.NewMockCatalogAccess(ctrl), vmock.NewMockPermissionService(ctrl), vmock.NewMockConnectorFactory(ctrl)
			connector := vmock.NewMockConnector(ctrl)
			db, sqlMock, err := sqlmock.New()
			require.NoError(t, err)
			t.Cleanup(func() { _ = db.Close() })
			stored := interfaces.ConnectorConfig{"base_url": "https://api.example.com/", "auth_type": "bearer", "token": "ENC:fixture"}
			ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			ca.EXPECT().GetByID(gomock.Any(), "api-1").Return(&interfaces.Catalog{ID: "api-1", Name: "API", ConnectorType: "kingdee", Category: interfaces.ConnectorCategoryAPI, ConnectorCfg: stored, Type: interfaces.CatalogTypePhysical, CatalogHealthCheckStatus: interfaces.CatalogHealthCheckStatus{HealthCheckStatus: interfaces.CatalogHealthStatusUnhealthy, HealthCheckResult: "previous-result"}}, nil)
			cf.EXPECT().GetSensitiveFields("kingdee", gomock.Any()).DoAndReturn(func(_ string, cfg interfaces.ConnectorConfig) ([]string, error) {
				if replacement {
					assert.Equal(t, "test-only-value", cfg["token"])
				} else {
					assert.Equal(t, stored, cfg)
				}
				return []string{"token"}, nil
			})
			cf.EXPECT().CreateConnectorInstance(gomock.Any(), "kingdee", interfaces.ConnectorConfig{"base_url": "https://api.example.com/", "auth_type": "bearer", "token": "test-only-value"}).Return(connector, nil)

			if replacement {
				connector.EXPECT().TestConnection(gomock.Any()).Return(nil)
			}
			connector.EXPECT().Close(gomock.Any()).Return(nil)
			sqlMock.ExpectBegin()
			ca.EXPECT().Update(gomock.Any(), gomock.Any(), gomock.Any(), int64(9), replacement).DoAndReturn(func(_ context.Context, _ *sql.Tx, saved *interfaces.Catalog, _ int64, _ bool) (int64, error) {
				assert.Equal(t, stored, saved.ConnectorCfg)
				assert.Equal(t, interfaces.ConnectorCategoryAPI, saved.Category)
				if replacement {
					assert.Equal(t, interfaces.CatalogHealthStatusHealthy, saved.HealthCheckStatus)
					assert.Equal(t, "Connection test succeeded.", saved.HealthCheckResult)
				} else {
					assert.Equal(t, interfaces.CatalogHealthStatusUnhealthy, saved.HealthCheckStatus)
					assert.Equal(t, "previous-result", saved.HealthCheckResult)
				}
				return 1, nil
			})
			sqlMock.ExpectCommit()
			cs := &catalogService{appSetting: &common.AppSetting{CryptoSetting: common.CryptoSetting{Enabled: true}}, db: db, ca: ca, ps: ps, cf: cf, cipher: &mockCipher{encryptFunc: func(value string) (string, error) {
				assert.True(t, replacement)
				assert.Equal(t, "test-only-value", value)
				return "fixture", nil
			}, decryptFunc: func(string) (string, error) { assert.False(t, replacement); return "test-only-value", nil }}}
			req := &interfaces.CatalogRequest{ID: "api-1", Name: "API", ConnectorType: "kingdee", ExpectedUpdateTime: 9}
			require.NoError(t, json.Unmarshal([]byte(`{"category":"table"}`), req))
			if replacement {
				req.ConnectorCfg = map[string]any{"base_url": "https://api.example.com/", "auth_type": "bearer", "token": "test-only-value"}
			}
			require.NoError(t, cs.Update(context.Background(), req, false))
			require.NoError(t, sqlMock.ExpectationsWereMet())
		})
	}
	for _, tc := range []struct {
		name           string
		configJSON     string
		allowUnhealthy bool
		rowsAffected   int64
	}{
		{name: "omitted config preserves unhealthy connection", allowUnhealthy: true, rowsAffected: 1},
		{name: "metadata update does not require a healthy connection", rowsAffected: 1},
		{name: "null config preserves connection", configJSON: `,"connector_config":null`, rowsAffected: 1},
		{name: "omitted config still respects optimistic locking", rowsAffected: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			ca := vmock.NewMockCatalogAccess(ctrl)
			ps := vmock.NewMockPermissionService(ctrl)
			cf := vmock.NewMockConnectorFactory(ctrl)
			db, sqlMock, err := sqlmock.New()
			require.NoError(t, err)
			t.Cleanup(func() { _ = db.Close() })

			// 与 SDK 部分更新组装的 PUT 请求一致，名称和版本号均保留。
			var req interfaces.CatalogRequest
			require.NoError(t, json.Unmarshal([]byte(`{"id":"catalog-1","name":"physical-catalog","connector_type":"mariadb","enabled":false,"tags":["saved"],"description":"updated description","expected_update_time":42`+tc.configJSON+`}`), &req))
			config := interfaces.ConnectorConfig{"host": "db.example.com", "password": "ENC:test-only-ciphertext"} //nolint:gosec // Test-only placeholder ciphertext.
			status := interfaces.CatalogHealthCheckStatus{
				HealthCheckStatus: interfaces.CatalogHealthStatusUnhealthy,
				LastCheckTime:     123, HealthCheckResult: connectionTestFailedResult,
			}
			ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			ca.EXPECT().GetByID(gomock.Any(), req.ID).Return(&interfaces.Catalog{
				ID: req.ID, Name: req.Name, ConnectorType: req.ConnectorType,
				ConnectorCfg: config, CatalogHealthCheckStatus: status, UpdateTime: 42,
			}, nil)
			sqlMock.ExpectBegin()
			ca.EXPECT().Update(gomock.Any(), gomock.Not(nil), gomock.Any(), int64(42), false).DoAndReturn(
				func(_ context.Context, _ *sql.Tx, saved *interfaces.Catalog, _ int64, _ bool) (int64, error) {
					assert.Equal(t, config, saved.ConnectorCfg)
					assert.Equal(t, status, saved.CatalogHealthCheckStatus)
					assert.Equal(t, req.Name, saved.Name)
					assert.Equal(t, req.ConnectorType, saved.ConnectorType)
					assert.False(t, saved.Enabled)
					assert.Equal(t, req.Tags, saved.Tags)
					assert.Equal(t, req.Description, saved.Description)
					return tc.rowsAffected, nil
				})
			if tc.rowsAffected == 0 {
				sqlMock.ExpectRollback()
			} else {
				sqlMock.ExpectCommit()
			}
			cs := &catalogService{db: db, ca: ca, ps: ps, cf: cf, cipher: &mockCipher{
				decryptFunc: func(string) (string, error) {
					t.Fatal("metadata update must not decrypt saved credentials")
					return "", nil
				},
			}}
			err = cs.Update(context.Background(), &req, tc.allowUnhealthy)
			if tc.rowsAffected == 0 {
				var httpErr *httperrors.HTTPError
				require.ErrorAs(t, err, &httpErr)
				assert.Equal(t, http.StatusConflict, httpErr.HTTPCode)
				assert.Equal(t, verrors.VegaBackend_Catalog_UpdateConflict, httpErr.ErrorCode)
			} else {
				require.NoError(t, err)
			}
			require.NoError(t, sqlMock.ExpectationsWereMet())
		})
	}

	t.Run("does not load catalog when modify permission is denied", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCA := vmock.NewMockCatalogAccess(ctrl)
		mockPS := vmock.NewMockPermissionService(ctrl)
		permissionErr := errors.New("modify permission denied")

		mockPS.EXPECT().CheckPermission(gomock.Any(), interfaces.PermissionResource{
			Type: interfaces.AUTH_RESOURCE_TYPE_CATALOG,
			ID:   "catalog-1",
		}, []string{interfaces.OPERATION_TYPE_MODIFY}).Return(permissionErr)

		cs := &catalogService{ca: mockCA, ps: mockPS}
		err := cs.Update(context.Background(), &interfaces.CatalogRequest{ID: "catalog-1"}, false)

		require.ErrorIs(t, err, permissionErr)
	})

	t.Run("rejects connector type change", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCA := vmock.NewMockCatalogAccess(ctrl)
		mockPS := vmock.NewMockPermissionService(ctrl)

		mockPS.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
		mockCA.EXPECT().GetByID(gomock.Any(), "catalog-1").Return(&interfaces.Catalog{
			ID: "catalog-1", Name: "catalog", Enabled: true, ConnectorType: "mariadb",
		}, nil)

		cs := &catalogService{ca: mockCA, ps: mockPS}
		err := cs.Update(context.Background(), &interfaces.CatalogRequest{
			ID: "catalog-1", Name: "catalog", Enabled: true, ConnectorType: "postgresql",
		}, false)

		var httpErr *httperrors.HTTPError
		require.ErrorAs(t, err, &httpErr)
		assert.Equal(t, http.StatusBadRequest, httpErr.HTTPCode)
		assert.Equal(t, verrors.VegaBackend_Catalog_InvalidParameter_ConnectorType, httpErr.ErrorCode)
	})

	t.Run("rejects enabled state change", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCA := vmock.NewMockCatalogAccess(ctrl)
		mockPS := vmock.NewMockPermissionService(ctrl)

		mockPS.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
		mockCA.EXPECT().GetByID(gomock.Any(), "catalog-1").Return(&interfaces.Catalog{
			ID: "catalog-1", Name: "catalog", Enabled: true, ConnectorType: "mariadb",
		}, nil)

		cs := &catalogService{ca: mockCA, ps: mockPS}
		err := cs.Update(context.Background(), &interfaces.CatalogRequest{
			ID: "catalog-1", Name: "catalog", Enabled: false, ConnectorType: "mariadb",
		}, false)

		var httpErr *httperrors.HTTPError
		require.ErrorAs(t, err, &httpErr)
		assert.Equal(t, http.StatusConflict, httpErr.HTTPCode)
		assert.Equal(t, verrors.VegaBackend_Catalog_EnabledFieldNotAllowed, httpErr.ErrorCode)
	})

	t.Run("rejects duplicate changed name", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCA := vmock.NewMockCatalogAccess(ctrl)
		mockPS := vmock.NewMockPermissionService(ctrl)

		mockPS.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
		mockCA.EXPECT().GetByID(gomock.Any(), "catalog-1").Return(&interfaces.Catalog{
			ID: "catalog-1", Name: "catalog", Enabled: true, ConnectorType: "mariadb",
		}, nil)
		mockCA.EXPECT().GetByName(gomock.Any(), "duplicate").Return(&interfaces.Catalog{ID: "catalog-2"}, nil)

		cs := &catalogService{ca: mockCA, ps: mockPS}
		err := cs.Update(context.Background(), &interfaces.CatalogRequest{
			ID: "catalog-1", Name: "duplicate", Enabled: true, ConnectorType: "mariadb",
		}, false)

		var httpErr *httperrors.HTTPError
		require.ErrorAs(t, err, &httpErr)
		assert.Equal(t, http.StatusConflict, httpErr.HTTPCode)
		assert.Equal(t, verrors.VegaBackend_Catalog_NameExists, httpErr.ErrorCode)
	})

	t.Run("explicit empty config fails construction even when unhealthy is allowed", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCA := vmock.NewMockCatalogAccess(ctrl)
		mockPS := vmock.NewMockPermissionService(ctrl)
		sensitiveError := "invalid endpoint db.internal"

		mockPS.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
		mockCA.EXPECT().GetByID(gomock.Any(), "catalog-1").Return(&interfaces.Catalog{
			ID: "catalog-1", Name: "physical-catalog", ConnectorType: "mariadb",
		}, nil)
		connectorFactory := vmock.NewMockConnectorFactory(ctrl)
		connectorFactory.EXPECT().GetSensitiveFields("mariadb", gomock.Any()).Return(nil, nil)
		connectorFactory.EXPECT().CreateConnectorInstance(gomock.Any(), "mariadb", interfaces.ConnectorConfig{}).
			Return(nil, errors.New(sensitiveError))
		cs := &catalogService{
			ca: mockCA,
			ps: mockPS,
			cf: connectorFactory,
		}
		err := cs.Update(context.Background(), &interfaces.CatalogRequest{
			ID:            "catalog-1",
			Name:          "physical-catalog",
			ConnectorType: "mariadb",
			ConnectorCfg:  interfaces.ConnectorConfig{},
		}, true)

		requireRedactedConnectorInitializationError(t, err, sensitiveError)
	})

	t.Run("does not expose connector error when connection test fails", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCA := vmock.NewMockCatalogAccess(ctrl)
		mockPS := vmock.NewMockPermissionService(ctrl)
		connector := vmock.NewMockConnector(ctrl)

		sensitiveError := "dial tcp db.internal:3306 failed"

		mockPS.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
		mockCA.EXPECT().GetByID(gomock.Any(), "catalog-1").Return(&interfaces.Catalog{
			ID: "catalog-1", Name: "physical-catalog", ConnectorType: "mariadb",
		}, nil)
		connector.EXPECT().TestConnection(gomock.Any()).Return(errors.New(sensitiveError))
		connector.EXPECT().Close(gomock.Any()).Return(nil)
		connectorFactory := vmock.NewMockConnectorFactory(ctrl)
		connectorFactory.EXPECT().GetSensitiveFields("mariadb", gomock.Any()).Return(nil, nil)
		connectorFactory.EXPECT().CreateConnectorInstance(gomock.Any(), "mariadb", gomock.Any()).Return(connector, nil)

		cs := &catalogService{
			appSetting: &common.AppSetting{},
			ca:         mockCA,
			ps:         mockPS,
			cf:         connectorFactory,
		}
		err := cs.Update(context.Background(), &interfaces.CatalogRequest{
			ID:            "catalog-1",
			Name:          "physical-catalog",
			ConnectorType: "mariadb",
			ConnectorCfg:  interfaces.ConnectorConfig{},
		}, false)

		var httpErr *httperrors.HTTPError
		require.ErrorAs(t, err, &httpErr)
		assert.Equal(t, http.StatusBadRequest, httpErr.HTTPCode)
		assert.Equal(t, verrors.VegaBackend_Catalog_InternalError_TestConnectionFailed, httpErr.ErrorCode)
		assert.NotContains(t, fmt.Sprint(httpErr.ErrorDetails), sensitiveError)
		assert.Contains(t, fmt.Sprint(httpErr.ErrorDetails), "Connection test failed")
	})

	t.Run("successful connection test updates health check status", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCA := vmock.NewMockCatalogAccess(ctrl)
		mockPS := vmock.NewMockPermissionService(ctrl)
		connector := vmock.NewMockConnector(ctrl)

		connectorFactory := vmock.NewMockConnectorFactory(ctrl)
		db, sqlMock, err := sqlmock.New()
		require.NoError(t, err)
		t.Cleanup(func() { _ = db.Close() })

		sqlMock.ExpectBegin()
		sqlMock.ExpectCommit()
		mockPS.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
		mockCA.EXPECT().GetByID(gomock.Any(), "catalog-1").Return(&interfaces.Catalog{
			ID: "catalog-1", Name: "physical-catalog", ConnectorType: "mariadb",
			CatalogHealthCheckStatus: interfaces.CatalogHealthCheckStatus{
				HealthCheckStatus: interfaces.CatalogHealthStatusUnchecked,
				LastCheckTime:     111,
				HealthCheckResult: "awaiting scheduled health check",
			},
		}, nil)
		connectorFactory.EXPECT().GetSensitiveFields("mariadb", gomock.Any()).Return([]string{"password"}, nil)
		connectorFactory.EXPECT().CreateConnectorInstance(gomock.Any(), "mariadb", interfaces.ConnectorConfig{
			"host": "new.example.com", "password": "test-only-plaintext",
		}).Return(connector, nil)
		connector.EXPECT().TestConnection(gomock.Any()).Return(nil)
		connector.EXPECT().Close(gomock.Any()).Return(nil)
		mockCA.EXPECT().Update(gomock.Any(), gomock.Not(nil), gomock.Any(), int64(0), true).DoAndReturn(
			func(_ context.Context, _ *sql.Tx, catalog *interfaces.Catalog, _ int64, _ bool) (int64, error) {
				assert.Equal(t, interfaces.ConnectorConfig{ //nolint:gosec // Test-only placeholder ciphertext.
					"host": "new.example.com", "password": "ENC:test-only-ciphertext",
				}, catalog.ConnectorCfg)
				assert.Equal(t, interfaces.CatalogHealthStatusHealthy, catalog.HealthCheckStatus)
				assert.Greater(t, catalog.LastCheckTime, int64(111))
				assert.Equal(t, "Connection test succeeded.", catalog.HealthCheckResult)
				return 1, nil
			},
		)

		cs := &catalogService{appSetting: &common.AppSetting{CryptoSetting: common.CryptoSetting{Enabled: true}}, db: db, ca: mockCA, ps: mockPS, cf: connectorFactory, cipher: &mockCipher{
			encryptFunc: func(value string) (string, error) {
				assert.Equal(t, "test-only-plaintext", value)
				return "test-only-ciphertext", nil
			},
		}}
		err = cs.Update(context.Background(), &interfaces.CatalogRequest{
			ID:            "catalog-1",
			Name:          "physical-catalog",
			ConnectorType: "mariadb",
			ConnectorCfg:  interfaces.ConnectorConfig{"host": "new.example.com", "password": "test-only-plaintext"},
		}, true)

		require.NoError(t, err)
		require.NoError(t, sqlMock.ExpectationsWereMet())
	})

	t.Run("allowed unhealthy connection test updates health check status", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCA := vmock.NewMockCatalogAccess(ctrl)
		mockPS := vmock.NewMockPermissionService(ctrl)
		connector := vmock.NewMockConnector(ctrl)

		connectorFactory := vmock.NewMockConnectorFactory(ctrl)
		db, sqlMock, err := sqlmock.New()
		require.NoError(t, err)
		t.Cleanup(func() { _ = db.Close() })

		sqlMock.ExpectBegin()
		sqlMock.ExpectCommit()
		mockPS.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
		mockCA.EXPECT().GetByID(gomock.Any(), "catalog-1").Return(&interfaces.Catalog{
			ID: "catalog-1", Name: "physical-catalog", ConnectorType: "mariadb",
		}, nil)
		connectorFactory.EXPECT().GetSensitiveFields("mariadb", gomock.Any()).Return(nil, nil)
		connectorFactory.EXPECT().CreateConnectorInstance(gomock.Any(), "mariadb", gomock.Any()).Return(connector, nil)
		connector.EXPECT().TestConnection(gomock.Any()).Return(errors.New("connection refused"))
		connector.EXPECT().Close(gomock.Any()).Return(nil)
		mockCA.EXPECT().Update(gomock.Any(), gomock.Not(nil), gomock.Any(), int64(0), true).DoAndReturn(
			func(_ context.Context, _ *sql.Tx, catalog *interfaces.Catalog, _ int64, _ bool) (int64, error) {
				assert.Equal(t, interfaces.CatalogHealthStatusUnhealthy, catalog.HealthCheckStatus)
				assert.Greater(t, catalog.LastCheckTime, int64(0))
				assert.Equal(t, connectionTestFailedResult, catalog.HealthCheckResult)
				return 1, nil
			},
		)

		cs := &catalogService{appSetting: &common.AppSetting{}, db: db, ca: mockCA, ps: mockPS, cf: connectorFactory}
		err = cs.Update(context.Background(), &interfaces.CatalogRequest{
			ID:            "catalog-1",
			Name:          "physical-catalog",
			ConnectorType: "mariadb",
			ConnectorCfg:  interfaces.ConnectorConfig{},
		}, true)

		require.NoError(t, err)
		require.NoError(t, sqlMock.ExpectationsWereMet())
	})

	t.Run("uses transaction", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCA := vmock.NewMockCatalogAccess(ctrl)
		mockPS := vmock.NewMockPermissionService(ctrl)
		db, sqlMock, err := sqlmock.New()
		require.NoError(t, err)
		t.Cleanup(func() { _ = db.Close() })

		sqlMock.ExpectBegin()
		sqlMock.ExpectCommit()
		mockPS.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
		mockCA.EXPECT().GetByID(gomock.Any(), "catalog-1").Return(&interfaces.Catalog{ID: "catalog-1", Name: "catalog"}, nil)
		mockCA.EXPECT().Update(gomock.Any(), gomock.Not(nil), gomock.Any(), int64(0), false).Return(int64(1), nil)

		cs := &catalogService{db: db, ca: mockCA, ps: mockPS}
		err = cs.Update(context.Background(), &interfaces.CatalogRequest{ID: "catalog-1", Name: "catalog"}, false)

		require.NoError(t, err)
		require.NoError(t, sqlMock.ExpectationsWereMet())
	})

	t.Run("returns conflict for stale catalog", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCA := vmock.NewMockCatalogAccess(ctrl)
		mockPS := vmock.NewMockPermissionService(ctrl)
		db, sqlMock, err := sqlmock.New()
		require.NoError(t, err)
		t.Cleanup(func() { _ = db.Close() })

		expectedUpdateTime := int64(42)
		sqlMock.ExpectBegin()
		sqlMock.ExpectRollback()
		mockPS.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
		mockCA.EXPECT().GetByID(gomock.Any(), "catalog-1").Return(&interfaces.Catalog{ID: "catalog-1", Name: "catalog"}, nil)
		mockCA.EXPECT().Update(gomock.Any(), gomock.Not(nil), gomock.Any(), expectedUpdateTime, false).
			DoAndReturn(func(_ context.Context, _ *sql.Tx, catalog *interfaces.Catalog, expected int64, _ bool) (int64, error) {
				assert.Equal(t, expectedUpdateTime, expected)
				assert.Greater(t, catalog.UpdateTime, expectedUpdateTime)
				return 0, nil
			})

		cs := &catalogService{db: db, ca: mockCA, ps: mockPS}
		err = cs.Update(context.Background(), &interfaces.CatalogRequest{
			ID:                 "catalog-1",
			Name:               "catalog",
			ExpectedUpdateTime: expectedUpdateTime,
		}, false)

		var httpErr *httperrors.HTTPError
		require.ErrorAs(t, err, &httpErr)
		assert.Equal(t, http.StatusConflict, httpErr.HTTPCode)
		assert.Equal(t, verrors.VegaBackend_Catalog_UpdateConflict, httpErr.ErrorCode)
		require.NoError(t, sqlMock.ExpectationsWereMet())
	})

	t.Run("returns conflict when no catalog is updated", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCA := vmock.NewMockCatalogAccess(ctrl)
		mockPS := vmock.NewMockPermissionService(ctrl)
		db, sqlMock, err := sqlmock.New()
		require.NoError(t, err)
		t.Cleanup(func() { _ = db.Close() })

		sqlMock.ExpectBegin()
		sqlMock.ExpectRollback()
		mockPS.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
		mockCA.EXPECT().GetByID(gomock.Any(), "catalog-1").Return(&interfaces.Catalog{ID: "catalog-1", Name: "catalog"}, nil)
		mockCA.EXPECT().Update(gomock.Any(), gomock.Not(nil), gomock.Any(), int64(0), false).Return(int64(0), nil)

		cs := &catalogService{db: db, ca: mockCA, ps: mockPS}
		err = cs.Update(context.Background(), &interfaces.CatalogRequest{ID: "catalog-1", Name: "catalog"}, false)

		var httpErr *httperrors.HTTPError
		require.ErrorAs(t, err, &httpErr)
		assert.Equal(t, http.StatusConflict, httpErr.HTTPCode)
		assert.Equal(t, verrors.VegaBackend_Catalog_UpdateConflict, httpErr.ErrorCode)
		require.NoError(t, sqlMock.ExpectationsWereMet())
	})

}

func TestCatalogServiceSetEnabled(t *testing.T) {
	t.Run("set enabled reenable sets health status unchecked", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCA := vmock.NewMockCatalogAccess(ctrl)
		mockPS := vmock.NewMockPermissionService(ctrl)

		mockPS.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
		mockCA.EXPECT().GetByID(gomock.Any(), "catalog-1").Return(&interfaces.Catalog{
			ID:      "catalog-1",
			Name:    "catalog",
			Enabled: false,
			CatalogHealthCheckStatus: interfaces.CatalogHealthCheckStatus{
				HealthCheckStatus: interfaces.CatalogHealthStatusHealthy,
			},
		}, nil)
		mockCA.EXPECT().UpdateEnabled(gomock.Any(), "catalog-1", true, gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, _ string, enabled bool, status interfaces.CatalogHealthCheckStatus, _ int64, _ interfaces.AccountInfo) error {
				if !enabled {
					t.Fatal("expected enabled=true")
				}
				if status.HealthCheckStatus != interfaces.CatalogHealthStatusUnchecked {
					t.Fatalf("expected unchecked status, got %s", status.HealthCheckStatus)
				}
				return nil
			},
		)

		cs := &catalogService{ca: mockCA, ps: mockPS}
		catalog, err := cs.SetEnabled(context.Background(), "catalog-1", true)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		assert.Equal(t, "catalog", catalog.Name)
	})
	t.Run("set enabled disable preserves health status", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCA := vmock.NewMockCatalogAccess(ctrl)
		mockPS := vmock.NewMockPermissionService(ctrl)

		mockPS.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
		mockCA.EXPECT().GetByID(gomock.Any(), "catalog-1").Return(&interfaces.Catalog{
			ID:      "catalog-1",
			Name:    "catalog",
			Enabled: true,
			CatalogHealthCheckStatus: interfaces.CatalogHealthCheckStatus{
				HealthCheckStatus: interfaces.CatalogHealthStatusHealthy,
			},
		}, nil)
		mockCA.EXPECT().UpdateEnabled(gomock.Any(), "catalog-1", false, gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, _ string, enabled bool, status interfaces.CatalogHealthCheckStatus, _ int64, _ interfaces.AccountInfo) error {
				if enabled {
					t.Fatal("expected enabled=false")
				}
				if status.HealthCheckStatus != interfaces.CatalogHealthStatusHealthy {
					t.Fatalf("expected preserved healthy status, got %s", status.HealthCheckStatus)
				}
				return nil
			},
		)

		cs := &catalogService{ca: mockCA, ps: mockPS}
		catalog, err := cs.SetEnabled(context.Background(), "catalog-1", false)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		assert.Equal(t, "catalog", catalog.Name)
	})
	t.Run("does not load catalog when modify permission is denied", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCA := vmock.NewMockCatalogAccess(ctrl)
		mockPS := vmock.NewMockPermissionService(ctrl)
		permissionErr := errors.New("modify permission denied")

		mockPS.EXPECT().CheckPermission(gomock.Any(), interfaces.PermissionResource{
			Type: interfaces.AUTH_RESOURCE_TYPE_CATALOG,
			ID:   "catalog-1",
		}, []string{interfaces.OPERATION_TYPE_MODIFY}).Return(permissionErr)

		cs := &catalogService{ca: mockCA, ps: mockPS}
		catalog, err := cs.SetEnabled(context.Background(), "catalog-1", true)

		require.ErrorIs(t, err, permissionErr)
		assert.Nil(t, catalog)
	})
	t.Run("already enabled catalog is idempotent", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCA := vmock.NewMockCatalogAccess(ctrl)
		mockPS := vmock.NewMockPermissionService(ctrl)
		existing := &interfaces.Catalog{ID: "catalog-1", Name: "catalog", Enabled: true}

		mockPS.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
		mockCA.EXPECT().GetByID(gomock.Any(), "catalog-1").Return(existing, nil)

		cs := &catalogService{ca: mockCA, ps: mockPS}
		catalog, err := cs.SetEnabled(context.Background(), "catalog-1", true)

		require.NoError(t, err)
		assert.Same(t, existing, catalog)
	})
}

func TestCatalogServiceInternalGetByIDs(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockCA := vmock.NewMockCatalogAccess(ctrl)
	cs := &catalogService{ca: mockCA}

	empty, err := cs.InternalGetByIDs(context.Background(), nil)
	require.NoError(t, err)
	assert.Empty(t, empty)

	ids := []string{"missing", "c2", "c1"}
	rows := map[string]*interfaces.Catalog{"c1": {ID: "c1"}, "c2": {ID: "c2"}}
	mockCA.EXPECT().GetByIDs(gomock.Any(), ids).Return(rows, nil)
	got, err := cs.InternalGetByIDs(context.Background(), ids)
	require.NoError(t, err)
	assert.Equal(t, rows, got)
	assert.NotContains(t, got, "missing")
}

func TestCatalogServiceGetByIDs(t *testing.T) {
	for _, malformed := range []bool{false, true} {
		t.Run(fmt.Sprintf("dynamic batch metadata malformed=%t", malformed), func(t *testing.T) {
			cs, ca, ps, ums := newS2SCatalogService(t)
			ctrl := gomock.NewController(t)
			cf := vmock.NewMockConnectorFactory(ctrl)
			cs.cf = cf
			cfg := interfaces.ConnectorConfig{"variables": map[string]any{"fixture_option": "ENC:nested", "normal": 0}, "sensitive_variables": []any{"fixture_option"}}
			if malformed {
				cfg["sensitive_variables"] = []any{false}
			}
			ca.EXPECT().GetByIDs(gomock.Any(), []string{"one"}).Return(map[string]*interfaces.Catalog{"one": {ID: "one", ConnectorType: "custom", ConnectorCfg: cfg}}, nil)
			var metadataErr error
			if malformed {
				metadataErr = errors.New("invalid protection metadata")
			}
			cf.EXPECT().GetSensitiveFields("custom", cfg).Return([]string{"fixture_option"}, metadataErr)
			if !malformed {
				ps.EXPECT().FilterVisibleResourcesWithOperations(gomock.Any(), gomock.Any(), []string{"one"}, gomock.Any(), interfaces.VISIBILITY_MATCH_ALL).Return(map[string]interfaces.PermissionResourceOps{"one": {}}, nil)
				ums.EXPECT().GetAccountNames(gomock.Any(), gomock.Any()).Return(nil)
			}
			got, err := cs.GetByIDs(context.Background(), []string{"one"})
			if malformed {
				require.Error(t, err)
				assert.Nil(t, got)
				return
			}
			require.NoError(t, err)
			require.Len(t, got, 1)
			assert.NotContains(t, got[0].ConnectorCfg["variables"], "fixture_option")
			assert.Equal(t, "ENC:nested", cfg["variables"].(map[string]any)["fixture_option"])
		})
	}

	for _, unknown := range []bool{false, true} {
		t.Run(fmt.Sprintf("batch protection metadata unknown=%t", unknown), func(t *testing.T) {
			ctrl := gomock.NewController(t)
			ca, cf, ps, ums := vmock.NewMockCatalogAccess(ctrl), vmock.NewMockConnectorFactory(ctrl), vmock.NewMockPermissionService(ctrl), vmock.NewMockUserMgmtService(ctrl)
			ca.EXPECT().GetByIDs(gomock.Any(), []string{"one"}).Return(map[string]*interfaces.Catalog{"one": {ID: "one", ConnectorType: "declared", ConnectorCfg: interfaces.ConnectorConfig{"base_url": "https://api.example.com/"}}}, nil)
			var metadataErr error
			if unknown {
				metadataErr = factory.ErrConnectorUnavailable
			} else {
				ps.EXPECT().FilterVisibleResourcesWithOperations(gomock.Any(), gomock.Any(), []string{"one"}, gomock.Any(), interfaces.VISIBILITY_MATCH_ALL).Return(map[string]interfaces.PermissionResourceOps{"one": {}}, nil)
				ums.EXPECT().GetAccountNames(gomock.Any(), gomock.Any()).Return(nil)
			}
			cf.EXPECT().GetSensitiveFields("declared", gomock.Any()).Return(nil, metadataErr)
			cs := &catalogService{ca: ca, cf: cf, ps: ps, ums: ums}
			got, err := cs.GetByIDs(context.Background(), []string{"one"})
			if unknown {
				require.Error(t, err)
				assert.Nil(t, got)
			} else {
				require.NoError(t, err)
				require.Len(t, got, 1)
				assert.Equal(t, "https://api.example.com/", got[0].ConnectorCfg["base_url"])
			}
		})
	}
	t.Run("preserves requested order from keyed access results", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCA := vmock.NewMockCatalogAccess(ctrl)
		mockPS := vmock.NewMockPermissionService(ctrl)
		mockUMS := vmock.NewMockUserMgmtService(ctrl)
		ids := []string{"c1", "c2"}
		mockCA.EXPECT().GetByIDs(gomock.Any(), ids).Return(map[string]*interfaces.Catalog{"c2": {ID: "c2"}, "c1": {ID: "c1"}}, nil)
		mockPS.EXPECT().FilterVisibleResourcesWithOperations(gomock.Any(), interfaces.AUTH_RESOURCE_TYPE_CATALOG, ids, gomock.Any(), interfaces.VISIBILITY_MATCH_ALL).
			Return(map[string]interfaces.PermissionResourceOps{
				"c1": {ResourceID: "c1"}, "c2": {ResourceID: "c2"},
			}, nil)
		mockUMS.EXPECT().GetAccountNames(gomock.Any(), gomock.Any()).Return(nil)

		cs := &catalogService{ca: mockCA, ps: mockPS, ums: mockUMS}
		catalogs, err := cs.GetByIDs(context.Background(), ids)
		require.NoError(t, err)
		require.Len(t, catalogs, 2)
		assert.Equal(t, ids, []string{catalogs[0].ID, catalogs[1].ID})
	})
}

func TestCatalogServiceList(t *testing.T) {
	t.Run("restores sorted page from keyed summary lookup", func(t *testing.T) {
		for _, tc := range []struct {
			name       string
			params     interfaces.CatalogsQueryParams
			refs       []interfaces.CatalogPermissionRef
			permitted  []string
			pageIDs    []string
			lookupRows map[string]*interfaces.CatalogSummary
			wantTotal  int64
		}{
			{
				name: "name asc search with middle permission denied",
				params: interfaces.CatalogsQueryParams{
					PaginationQueryParams: interfaces.PaginationQueryParams{Sort: interfaces.CatalogSortName, Direction: interfaces.ASC_DIRECTION, Limit: 2},
					Name:                  "catalog",
				},
				refs:       []interfaces.CatalogPermissionRef{{CatalogID: "c1"}, {CatalogID: "c2"}, {CatalogID: "c3"}, {CatalogID: "c4"}},
				permitted:  []string{"c1", "c3", "c4"},
				pageIDs:    []string{"c1", "c3"},
				lookupRows: map[string]*interfaces.CatalogSummary{"c3": {ID: "c3"}, "c1": {ID: "c1"}},
				wantTotal:  3,
			},
			{
				name: "name desc later page",
				params: interfaces.CatalogsQueryParams{
					PaginationQueryParams: interfaces.PaginationQueryParams{Sort: interfaces.CatalogSortName, Direction: interfaces.DESC_DIRECTION, Offset: 1, Limit: 2},
				},
				refs:       []interfaces.CatalogPermissionRef{{CatalogID: "c4"}, {CatalogID: "c3"}, {CatalogID: "c2"}, {CatalogID: "c1"}},
				permitted:  []string{"c4", "c3", "c2", "c1"},
				pageIDs:    []string{"c3", "c2"},
				lookupRows: map[string]*interfaces.CatalogSummary{"c2": {ID: "c2"}, "c3": {ID: "c3"}},
				wantTotal:  4,
			},
		} {
			t.Run(tc.name, func(t *testing.T) {
				ctrl := gomock.NewController(t)
				mockCA := vmock.NewMockCatalogAccess(ctrl)
				mockPS := vmock.NewMockPermissionService(ctrl)
				mockUMS := vmock.NewMockUserMgmtService(ctrl)
				permissions := make(map[string]interfaces.PermissionResourceOps, len(tc.permitted))
				for _, id := range tc.permitted {
					permissions[id] = interfaces.PermissionResourceOps{
						ResourceID: id,
						Operations: []string{interfaces.OPERATION_TYPE_VIEW_DETAIL},
					}
				}
				mockCA.EXPECT().ListPermissionRefs(gomock.Any(), tc.params).Return(tc.refs, nil)
				mockPS.EXPECT().FilterVisibleResourcesWithOperations(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), interfaces.VISIBILITY_MATCH_ANY).Return(permissions, nil)
				mockCA.EXPECT().GetSummariesByIDs(gomock.Any(), tc.pageIDs).Return(tc.lookupRows, nil)
				mockUMS.EXPECT().GetAccountNames(gomock.Any(), gomock.Any()).Return(nil)

				cs := &catalogService{ca: mockCA, ps: mockPS, ums: mockUMS}
				result, total, err := cs.List(context.Background(), tc.params)
				require.NoError(t, err)
				assert.Equal(t, tc.wantTotal, total)
				require.Len(t, result, len(tc.pageIDs))
				assert.Equal(t, tc.pageIDs, []string{result[0].ID, result[1].ID})
			})
		}
	})
	t.Run("includes detail and summary catalogs but preserves their effective operations", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCA := vmock.NewMockCatalogAccess(ctrl)
		mockPS := vmock.NewMockPermissionService(ctrl)
		mockUMS := vmock.NewMockUserMgmtService(ctrl)
		params := interfaces.CatalogsQueryParams{
			PaginationQueryParams: interfaces.PaginationQueryParams{Limit: -1},
		}
		refs := []interfaces.CatalogPermissionRef{
			{CatalogID: "detail-only"},
			{CatalogID: "summary-only"},
			{CatalogID: "both"},
			{CatalogID: "hidden"},
		}
		mockCA.EXPECT().ListPermissionRefs(gomock.Any(), params).Return(refs, nil)
		mockPS.EXPECT().FilterVisibleResourcesWithOperations(gomock.Any(), interfaces.AUTH_RESOURCE_TYPE_CATALOG,
			[]string{"detail-only", "summary-only", "both", "hidden"},
			[]string{interfaces.OPERATION_TYPE_VIEW_DETAIL, interfaces.OPERATION_TYPE_VIEW_SUMMARY}, interfaces.VISIBILITY_MATCH_ANY).
			Return(map[string]interfaces.PermissionResourceOps{
				"detail-only":  {ResourceID: "detail-only", Operations: []string{interfaces.OPERATION_TYPE_VIEW_DETAIL}},
				"summary-only": {ResourceID: "summary-only", Operations: []string{interfaces.OPERATION_TYPE_VIEW_SUMMARY}},
				"both":         {ResourceID: "both", Operations: []string{interfaces.OPERATION_TYPE_VIEW_DETAIL, interfaces.OPERATION_TYPE_VIEW_SUMMARY}},
			}, nil)
		mockCA.EXPECT().GetSummariesByIDs(gomock.Any(), []string{"detail-only", "summary-only", "both"}).
			Return(map[string]*interfaces.CatalogSummary{
				"detail-only":  {ID: "detail-only"},
				"summary-only": {ID: "summary-only"},
				"both":         {ID: "both"},
			}, nil)
		mockUMS.EXPECT().GetAccountNames(gomock.Any(), gomock.Any()).Return(nil)

		cs := &catalogService{ca: mockCA, ps: mockPS, ums: mockUMS}
		catalogs, total, err := cs.List(context.Background(), params)

		require.NoError(t, err)
		assert.Equal(t, int64(3), total)
		require.Len(t, catalogs, 3)
		assert.Equal(t, []string{"detail-only", "summary-only", "both"}, []string{catalogs[0].ID, catalogs[1].ID, catalogs[2].ID})
		assert.Equal(t, []string{interfaces.OPERATION_TYPE_VIEW_SUMMARY}, catalogs[1].Operations)
	})
	t.Run("keeps catalogs when account name lookup fails", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCA := vmock.NewMockCatalogAccess(ctrl)
		mockPS := vmock.NewMockPermissionService(ctrl)
		mockUMS := vmock.NewMockUserMgmtService(ctrl)

		refs := []interfaces.CatalogPermissionRef{{CatalogID: "c1"}, {CatalogID: "c2"}, {CatalogID: "c3"}}
		catalogs := []*interfaces.CatalogSummary{{ID: "c1"}, {ID: "c2"}, {ID: "c3"}}
		mockCA.EXPECT().ListPermissionRefs(gomock.Any(), gomock.Any()).Return(refs, nil)
		mockPS.EXPECT().FilterVisibleResourcesWithOperations(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), interfaces.VISIBILITY_MATCH_ANY).
			Return(map[string]interfaces.PermissionResourceOps{
				"c1": {ResourceID: "c1", Operations: []string{interfaces.OPERATION_TYPE_VIEW_DETAIL}},
				"c2": {ResourceID: "c2", Operations: []string{interfaces.OPERATION_TYPE_VIEW_DETAIL}},
				"c3": {ResourceID: "c3", Operations: []string{interfaces.OPERATION_TYPE_VIEW_DETAIL}},
			}, nil)
		mockCA.EXPECT().GetSummariesByIDs(gomock.Any(), gomock.Any()).Return(map[string]*interfaces.CatalogSummary{"c1": catalogs[0], "c2": catalogs[1], "c3": catalogs[2]}, nil)
		mockUMS.EXPECT().GetAccountNames(gomock.Any(), gomock.Any()).Return(errors.New("user management unavailable"))

		cs := &catalogService{ca: mockCA, ps: mockPS, ums: mockUMS}
		result, total, err := cs.List(context.Background(), interfaces.CatalogsQueryParams{
			PaginationQueryParams: interfaces.PaginationQueryParams{Limit: -1},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if total != 3 {
			t.Errorf("expected total 3, got %d", total)
		}
		if len(result) != 3 {
			t.Errorf("expected 3 results, got %d", len(result))
		}
	})
	t.Run("list pagination", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCA := vmock.NewMockCatalogAccess(ctrl)
		mockPS := vmock.NewMockPermissionService(ctrl)
		mockUMS := vmock.NewMockUserMgmtService(ctrl)

		refs := []interfaces.CatalogPermissionRef{{CatalogID: "c1"}, {CatalogID: "c2"}, {CatalogID: "c3"}, {CatalogID: "c4"}, {CatalogID: "c5"}}
		mockCA.EXPECT().ListPermissionRefs(gomock.Any(), gomock.Any()).Return(refs, nil)
		mockPS.EXPECT().FilterVisibleResourcesWithOperations(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), interfaces.VISIBILITY_MATCH_ANY).
			Return(map[string]interfaces.PermissionResourceOps{
				"c1": {ResourceID: "c1", Operations: []string{interfaces.OPERATION_TYPE_VIEW_DETAIL}},
				"c2": {ResourceID: "c2", Operations: []string{interfaces.OPERATION_TYPE_VIEW_DETAIL}},
				"c3": {ResourceID: "c3", Operations: []string{interfaces.OPERATION_TYPE_VIEW_DETAIL}},
				"c4": {ResourceID: "c4", Operations: []string{interfaces.OPERATION_TYPE_VIEW_DETAIL}},
				"c5": {ResourceID: "c5", Operations: []string{interfaces.OPERATION_TYPE_VIEW_DETAIL}},
			}, nil)
		catalogs := []*interfaces.CatalogSummary{{ID: "c2"}, {ID: "c3"}}
		mockCA.EXPECT().GetSummariesByIDs(gomock.Any(), []string{"c2", "c3"}).Return(map[string]*interfaces.CatalogSummary{"c2": catalogs[0], "c3": catalogs[1]}, nil)
		mockUMS.EXPECT().GetAccountNames(gomock.Any(), gomock.Any()).Return(nil)

		cs := &catalogService{ca: mockCA, ps: mockPS, ums: mockUMS}
		result, total, err := cs.List(context.Background(), interfaces.CatalogsQueryParams{
			PaginationQueryParams: interfaces.PaginationQueryParams{Offset: 1, Limit: 2},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if total != 5 {
			t.Errorf("expected total 5, got %d", total)
		}
		if len(result) != 2 {
			t.Errorf("expected 2 results (page), got %d", len(result))
		}
		if result[0].ID != "c2" {
			t.Errorf("expected first item 'c2', got '%s'", result[0].ID)
		}
	})
	t.Run("list offset beyond total", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCA := vmock.NewMockCatalogAccess(ctrl)
		mockPS := vmock.NewMockPermissionService(ctrl)

		refs := []interfaces.CatalogPermissionRef{{CatalogID: "c1"}, {CatalogID: "c2"}}
		mockCA.EXPECT().ListPermissionRefs(gomock.Any(), gomock.Any()).Return(refs, nil)
		mockPS.EXPECT().FilterVisibleResourcesWithOperations(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), interfaces.VISIBILITY_MATCH_ANY).
			Return(map[string]interfaces.PermissionResourceOps{
				"c1": {ResourceID: "c1", Operations: []string{interfaces.OPERATION_TYPE_VIEW_DETAIL}},
				"c2": {ResourceID: "c2", Operations: []string{interfaces.OPERATION_TYPE_VIEW_DETAIL}},
			}, nil)

		cs := &catalogService{ca: mockCA, ps: mockPS}
		result, total, err := cs.List(context.Background(), interfaces.CatalogsQueryParams{
			PaginationQueryParams: interfaces.PaginationQueryParams{Offset: 10, Limit: 5},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if total != 2 {
			t.Errorf("expected total 2, got %d", total)
		}
		if len(result) != 0 {
			t.Errorf("expected 0 results for offset beyond total, got %d", len(result))
		}
	})
	t.Run("list permission filters out", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCA := vmock.NewMockCatalogAccess(ctrl)
		mockPS := vmock.NewMockPermissionService(ctrl)
		mockUMS := vmock.NewMockUserMgmtService(ctrl)

		refs := []interfaces.CatalogPermissionRef{{CatalogID: "c1"}, {CatalogID: "c2"}, {CatalogID: "c3"}}
		catalogs := []*interfaces.CatalogSummary{{ID: "c1"}, {ID: "c3"}}
		mockCA.EXPECT().ListPermissionRefs(gomock.Any(), gomock.Any()).Return(refs, nil)
		// 权限只返回 c1 和 c3，c2 被过滤
		mockPS.EXPECT().FilterVisibleResourcesWithOperations(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), interfaces.VISIBILITY_MATCH_ANY).
			Return(map[string]interfaces.PermissionResourceOps{
				"c1": {ResourceID: "c1", Operations: []string{interfaces.OPERATION_TYPE_VIEW_DETAIL}},
				"c3": {ResourceID: "c3", Operations: []string{interfaces.OPERATION_TYPE_VIEW_DETAIL}},
			}, nil)
		mockCA.EXPECT().GetSummariesByIDs(gomock.Any(), []string{"c1", "c3"}).Return(map[string]*interfaces.CatalogSummary{"c1": catalogs[0], "c3": catalogs[1]}, nil)
		mockUMS.EXPECT().GetAccountNames(gomock.Any(), gomock.Any()).Return(nil)

		cs := &catalogService{ca: mockCA, ps: mockPS, ums: mockUMS}
		result, total, err := cs.List(context.Background(), interfaces.CatalogsQueryParams{
			PaginationQueryParams: interfaces.PaginationQueryParams{Limit: -1},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if total != 2 {
			t.Errorf("expected total 2 after permission filter, got %d", total)
		}
		if len(result) != 2 {
			t.Errorf("expected 2 results, got %d", len(result))
		}
	})
	t.Run("list dberror", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCA := vmock.NewMockCatalogAccess(ctrl)
		mockCA.EXPECT().ListPermissionRefs(gomock.Any(), gomock.Any()).Return(nil, fmt.Errorf("db error"))

		cs := &catalogService{ca: mockCA}
		_, _, err := cs.List(context.Background(), interfaces.CatalogsQueryParams{})
		if err == nil {
			t.Fatal("expected error")
		}
	})
	t.Run("list excludes internal catalog for non-admin", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCA := vmock.NewMockCatalogAccess(ctrl)
		mockPS := vmock.NewMockPermissionService(ctrl)
		mockUMS := vmock.NewMockUserMgmtService(ctrl)

		refs := []interfaces.CatalogPermissionRef{{CatalogID: "c1"}}
		params := interfaces.CatalogsQueryParams{
			PaginationQueryParams: interfaces.PaginationQueryParams{Limit: -1},
		}
		mockCA.EXPECT().ListPermissionRefs(gomock.Any(), params).Return(refs, nil)
		// The access query already excludes internal catalogs for non-admin callers.
		mockPS.EXPECT().FilterVisibleResourcesWithOperations(gomock.Any(), interfaces.AUTH_RESOURCE_TYPE_CATALOG,
			[]string{"c1"}, gomock.Any(), interfaces.VISIBILITY_MATCH_ANY).
			Return(map[string]interfaces.PermissionResourceOps{"c1": {
				ResourceID: "c1", Operations: []string{interfaces.OPERATION_TYPE_VIEW_DETAIL},
			}}, nil)
		mockCA.EXPECT().GetSummariesByIDs(gomock.Any(), []string{"c1"}).Return(map[string]*interfaces.CatalogSummary{"c1": {ID: "c1"}}, nil)
		mockUMS.EXPECT().GetAccountNames(gomock.Any(), gomock.Any()).Return(nil)

		cs := &catalogService{ca: mockCA, ps: mockPS, ums: mockUMS}
		result, total, err := cs.List(context.Background(), params)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if total != 1 {
			t.Errorf("expected total 1, got %d", total)
		}
		if len(result) != 1 || result[0].ID != "c1" {
			t.Errorf("expected only 'c1' visible, got %v", result)
		}
	})
	t.Run("requests internal catalogs for the built-in admin", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCA := vmock.NewMockCatalogAccess(ctrl)
		params := interfaces.CatalogsQueryParams{}
		mockCA.EXPECT().ListPermissionRefs(gomock.Any(), interfaces.CatalogsQueryParams{IncludeBuiltin: true}).Return(nil, nil)

		cs := &catalogService{ca: mockCA}
		ctx := context.WithValue(context.Background(), interfaces.ACCOUNT_INFO_KEY,
			interfaces.AccountInfo{ID: interfaces.BuiltinAdminID})
		result, total, err := cs.List(ctx, params)

		require.NoError(t, err)
		assert.Empty(t, result)
		assert.Zero(t, total)
	})
}

func TestCatalogServiceListConnectorTypeStats(t *testing.T) {
	t.Run("aggregates authorized refs", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCA := vmock.NewMockCatalogAccess(ctrl)
		mockPS := vmock.NewMockPermissionService(ctrl)
		refs := []interfaces.CatalogConnectorTypePermissionRef{
			{CatalogID: "logical-1", CatalogType: interfaces.CatalogTypeLogical, ConnectorType: ""},
			{CatalogID: "mysql-1", CatalogType: interfaces.CatalogTypePhysical, ConnectorType: "mysql"},
			{CatalogID: "mysql-2", CatalogType: interfaces.CatalogTypePhysical, ConnectorType: "mysql"},
			{CatalogID: "hidden-1", CatalogType: interfaces.CatalogTypePhysical, ConnectorType: "mariadb"},
		}
		mockCA.EXPECT().ListConnectorTypePermissionRefs(gomock.Any(), interfaces.CatalogsQueryParams{}).Return(refs, nil)
		mockPS.EXPECT().FilterVisibleResources(
			gomock.Any(),
			interfaces.AUTH_RESOURCE_TYPE_CATALOG,
			[]string{"logical-1", "mysql-1", "mysql-2", "hidden-1"},
			[]string{interfaces.OPERATION_TYPE_VIEW_DETAIL, interfaces.OPERATION_TYPE_VIEW_SUMMARY},
			interfaces.VISIBILITY_MATCH_ANY,
		).Return(map[string]interfaces.PermissionResourceOps{
			"logical-1": {ResourceID: "logical-1", Operations: []string{interfaces.OPERATION_TYPE_VIEW_DETAIL}},
			"mysql-1":   {ResourceID: "mysql-1", Operations: []string{interfaces.OPERATION_TYPE_VIEW_SUMMARY}},
			"mysql-2":   {ResourceID: "mysql-2", Operations: []string{interfaces.OPERATION_TYPE_VIEW_DETAIL, interfaces.OPERATION_TYPE_VIEW_SUMMARY}},
		}, nil)

		cs := &catalogService{ca: mockCA, ps: mockPS}
		stats, err := cs.ListConnectorTypeStats(context.Background(), interfaces.CatalogsQueryParams{})

		require.NoError(t, err)
		assert.Equal(t, []*interfaces.CatalogConnectorTypeStat{
			{CatalogType: interfaces.CatalogTypeLogical, ConnectorType: "", CatalogCount: 1},
			{CatalogType: interfaces.CatalogTypePhysical, ConnectorType: "mysql", CatalogCount: 2},
		}, stats)
	})

	t.Run("filters permissions in batches", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCA := vmock.NewMockCatalogAccess(ctrl)
		mockPS := vmock.NewMockPermissionService(ctrl)
		refs := make([]interfaces.CatalogConnectorTypePermissionRef, catalogAuthResourcePermissionBatchSize+1)
		for index := range refs {
			refs[index] = interfaces.CatalogConnectorTypePermissionRef{
				CatalogID:     fmt.Sprintf("mysql-%d", index),
				CatalogType:   interfaces.CatalogTypePhysical,
				ConnectorType: "mysql",
			}
		}
		mockCA.EXPECT().ListConnectorTypePermissionRefs(gomock.Any(), interfaces.CatalogsQueryParams{}).Return(refs, nil)
		mockPS.EXPECT().FilterVisibleResources(
			gomock.Any(), interfaces.AUTH_RESOURCE_TYPE_CATALOG, gomock.Any(),
			[]string{interfaces.OPERATION_TYPE_VIEW_DETAIL, interfaces.OPERATION_TYPE_VIEW_SUMMARY}, interfaces.VISIBILITY_MATCH_ANY,
		).DoAndReturn(func(_ context.Context, _ string, ids []string, _ []string, _ string) (map[string]interfaces.PermissionResourceOps, error) {
			require.LessOrEqual(t, len(ids), catalogAuthResourcePermissionBatchSize)
			allowed := make(map[string]interfaces.PermissionResourceOps, len(ids))
			for _, id := range ids {
				allowed[id] = interfaces.PermissionResourceOps{
					ResourceID: id, Operations: []string{interfaces.OPERATION_TYPE_VIEW_DETAIL},
				}
			}
			return allowed, nil
		}).Times(2)

		cs := &catalogService{ca: mockCA, ps: mockPS}
		stats, err := cs.ListConnectorTypeStats(context.Background(), interfaces.CatalogsQueryParams{})

		require.NoError(t, err)
		assert.Equal(t, []*interfaces.CatalogConnectorTypeStat{{
			CatalogType: interfaces.CatalogTypePhysical, ConnectorType: "mysql", CatalogCount: catalogAuthResourcePermissionBatchSize + 1,
		}}, stats)
	})
}

func TestCatalogServiceListAuthResourcesDoesNotFilterByPermission(t *testing.T) {
	ctrl := gomock.NewController(t)
	ca := vmock.NewMockCatalogAccess(ctrl)
	cs := &catalogService{ca: ca}
	params := interfaces.AuthResourceQueryParams{
		PaginationQueryParams: interfaces.PaginationQueryParams{Offset: 1, Limit: 1},
	}
	entries := []*interfaces.AuthResourceEntry{{ID: "catalog-2", Name: "Catalog 2"}}
	ca.EXPECT().ListAuthResourceEntries(gomock.Any(), params).Return(entries, int64(3), nil)

	got, total, err := cs.ListAuthResourceEntries(context.Background(), params)

	require.NoError(t, err)
	assert.Equal(t, int64(3), total)
	require.Len(t, got, 1)
	assert.Equal(t, "catalog-2", got[0].ID)
}

func TestCatalogServiceListAuthResourcesIncludesInternalForBuiltinAdmin(t *testing.T) {
	ctrl := gomock.NewController(t)
	ca := vmock.NewMockCatalogAccess(ctrl)
	cs := &catalogService{ca: ca}
	params := interfaces.AuthResourceQueryParams{}
	ca.EXPECT().ListAuthResourceEntries(gomock.Any(), interfaces.AuthResourceQueryParams{IncludeBuiltin: true}).
		Return([]*interfaces.AuthResourceEntry{}, int64(3), nil)
	ctx := context.WithValue(context.Background(), interfaces.ACCOUNT_INFO_KEY,
		interfaces.AccountInfo{ID: interfaces.BuiltinAdminID})

	entries, total, err := cs.ListAuthResourceEntries(ctx, params)

	require.NoError(t, err)
	assert.Empty(t, entries)
	assert.Equal(t, int64(3), total)
}

func TestCatalogServiceGetDeletionImpact(t *testing.T) {
	t.Run("API catalog includes its inherited health schedule", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		ca, ps, ra := vmock.NewMockCatalogAccess(ctrl), vmock.NewMockPermissionService(ctrl), vmock.NewMockResourceAccess(ctrl)
		bta, dsa, dta := vmock.NewMockBuildTaskAccess(ctrl), vmock.NewMockDiscoverScheduleAccess(ctrl), vmock.NewMockDiscoverTaskAccess(ctrl)
		suta, hcss := vmock.NewMockSemanticUnderstandingTaskAccess(ctrl), vmock.NewMockCatalogHealthCheckScheduleService(ctrl)
		ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
		ca.EXPECT().GetByID(gomock.Any(), "api-1").Return(&interfaces.Catalog{ID: "api-1", Type: interfaces.CatalogTypePhysical, ConnectorType: "http"}, nil)
		ra.EXPECT().GetByCatalogID(gomock.Any(), "api-1").Return(nil, nil)
		bta.EXPECT().List(gomock.Any(), gomock.Any()).Return(nil, int64(0), nil).Times(2)
		dsa.EXPECT().List(gomock.Any(), gomock.Any()).Return(nil, int64(0), nil)
		dta.EXPECT().List(gomock.Any(), gomock.Any()).Return(nil, int64(0), nil).Times(2)
		suta.EXPECT().List(gomock.Any(), gomock.Any()).Return(nil, int64(0), nil).Times(2)
		hcss.EXPECT().GetByCatalogID(gomock.Any(), "api-1").Return(&interfaces.CatalogHealthCheckSchedule{CatalogID: "api-1"}, nil)
		cf := vmock.NewMockConnectorFactory(ctrl)
		cf.EXPECT().GetSensitiveFields("http", gomock.Any()).Return(nil, nil)
		cs := &catalogService{ca: ca, ps: ps, ra: ra, bta: bta, dsa: dsa, dta: dta, suta: suta, hcss: hcss, cf: cf}
		impact, err := cs.GetDeletionImpact(context.Background(), "api-1")
		require.NoError(t, err)
		assert.True(t, impact.CanDelete)
		assert.Equal(t, int64(1), impact.CatalogHealthCheckSchedules)
	})
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	ra := vmock.NewMockResourceAccess(ctrl)
	ca := vmock.NewMockCatalogAccess(ctrl)
	ps := vmock.NewMockPermissionService(ctrl)
	bta := vmock.NewMockBuildTaskAccess(ctrl)
	dsa := vmock.NewMockDiscoverScheduleAccess(ctrl)
	dta := vmock.NewMockDiscoverTaskAccess(ctrl)
	suta := vmock.NewMockSemanticUnderstandingTaskAccess(ctrl)
	hcss := vmock.NewMockCatalogHealthCheckScheduleService(ctrl)
	cs := &catalogService{ca: ca, ps: ps, ra: ra, bta: bta, dsa: dsa, dta: dta, suta: suta, hcss: hcss}

	ps.EXPECT().CheckPermission(gomock.Any(), interfaces.PermissionResource{
		Type: interfaces.AUTH_RESOURCE_TYPE_CATALOG, ID: "catalog-1",
	}, []string{interfaces.OPERATION_TYPE_DELETE}).Return(nil)
	ca.EXPECT().GetByID(gomock.Any(), "catalog-1").Return(&interfaces.Catalog{
		ID: "catalog-1", Type: interfaces.CatalogTypePhysical,
	}, nil)
	ra.EXPECT().GetByCatalogID(gomock.Any(), "catalog-1").Return([]*interfaces.Resource{
		{ID: "resource-1", Category: interfaces.ResourceCategoryDataset},
	}, nil)
	bta.EXPECT().List(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, params interfaces.BuildTasksQueryParams) ([]*interfaces.BuildTask, int64, error) {
			if slices.Equal(params.Statuses, []string{
				interfaces.BuildTaskStatusRunning,
				interfaces.BuildTaskStatusStopping,
			}) {
				return nil, 2, nil
			}
			assert.Empty(t, params.Statuses)
			return nil, 4, nil
		}).Times(2)
	dsa.EXPECT().List(gomock.Any(), gomock.Any()).Return(nil, int64(2), nil)
	dta.EXPECT().List(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, params interfaces.DiscoverTaskQueryParams) ([]*interfaces.DiscoverTaskSummary, int64, error) {
			switch {
			case len(params.Statuses) == 0:
				return nil, 3, nil
			case slices.Equal(params.Statuses, []string{interfaces.DiscoverTaskStatusRunning}):
				return nil, 2, nil
			default:
				t.Fatalf("unexpected statuses: %v", params.Statuses)
				return nil, 0, nil
			}
		}).Times(2)
	suta.EXPECT().List(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, params interfaces.SemanticUnderstandingTaskQueryParams) ([]*interfaces.SemanticUnderstandingTaskSummary, int64, error) {
			if slices.Equal(params.Statuses, []string{interfaces.SemanticUnderstandingTaskStatusRunning}) {
				return nil, 1, nil
			}
			assert.Empty(t, params.Statuses)
			return nil, 2, nil
		}).Times(2)
	hcss.EXPECT().GetByCatalogID(gomock.Any(), "catalog-1").Return(&interfaces.CatalogHealthCheckSchedule{
		CatalogID: "catalog-1",
		Mode:      interfaces.CatalogHealthCheckScheduleModeInherit,
	}, nil)

	impact, err := cs.GetDeletionImpact(context.Background(), "catalog-1")
	require.NoError(t, err)
	assert.False(t, impact.CanDelete)
	assert.Equal(t, 1, impact.ProtectedResources)
	assert.Equal(t, []string{
		interfaces.CatalogDeletionBlockerProtectedResources,
		interfaces.CatalogDeletionBlockerBuildTasksRunningOrStopping,
		interfaces.CatalogDeletionBlockerDiscoverTasksRunning,
		interfaces.CatalogDeletionBlockerSemanticUnderstandingTasksRunning,
	}, impact.Blockers)
	assert.Equal(t, interfaces.CatalogDeletionTaskImpact{WillDelete: 4, Blocking: 2}, impact.BuildTasks)
	assert.Equal(t, int64(1), impact.CatalogHealthCheckSchedules)
	assert.Equal(t, int64(2), impact.DiscoverSchedules)
	assert.Equal(t, interfaces.CatalogDeletionTaskImpact{WillDelete: 3, Blocking: 2}, impact.DiscoverTasks)
	assert.Equal(t, interfaces.CatalogDeletionTaskImpact{WillDelete: 2, Blocking: 1}, impact.SemanticUnderstandingTasks)
}

func TestCatalogServiceGetDeletionImpactDependencyFailure(t *testing.T) {
	ctrl := gomock.NewController(t)
	ca := vmock.NewMockCatalogAccess(ctrl)
	ps := vmock.NewMockPermissionService(ctrl)
	sensitiveError := "dial tcp db.internal:3306: connection refused"

	ps.EXPECT().CheckPermission(gomock.Any(), interfaces.PermissionResource{
		Type: interfaces.AUTH_RESOURCE_TYPE_CATALOG, ID: "catalog-1",
	}, []string{interfaces.OPERATION_TYPE_DELETE}).Return(nil)
	ca.EXPECT().GetByID(gomock.Any(), "catalog-1").Return(nil, errors.New(sensitiveError))

	cs := &catalogService{ca: ca, ps: ps}
	impact, err := cs.GetDeletionImpact(context.Background(), "catalog-1")

	var httpErr *httperrors.HTTPError
	require.ErrorAs(t, err, &httpErr)
	assert.Nil(t, impact)
	assert.Equal(t, http.StatusInternalServerError, httpErr.HTTPCode)
	assert.Equal(t, verrors.VegaBackend_Catalog_InternalError_DeleteFailed, httpErr.ErrorCode)
	assert.NotContains(t, fmt.Sprint(httpErr.ErrorDetails), sensitiveError)
	assert.Contains(t, fmt.Sprint(httpErr.ErrorDetails), "failed to inspect catalog dependencies")
}

func TestCatalogServiceGetDeletionImpactReturnsNotFoundWhenCatalogDisappears(t *testing.T) {
	ctrl := gomock.NewController(t)
	ca := vmock.NewMockCatalogAccess(ctrl)
	ps := vmock.NewMockPermissionService(ctrl)

	ps.EXPECT().CheckPermission(gomock.Any(), interfaces.PermissionResource{
		Type: interfaces.AUTH_RESOURCE_TYPE_CATALOG, ID: "catalog-1",
	}, []string{interfaces.OPERATION_TYPE_DELETE}).Return(nil)
	ca.EXPECT().GetByID(gomock.Any(), "catalog-1").Return(nil, nil)

	cs := &catalogService{ca: ca, ps: ps}
	impact, err := cs.GetDeletionImpact(context.Background(), "catalog-1")

	var httpErr *httperrors.HTTPError
	require.ErrorAs(t, err, &httpErr)
	assert.Nil(t, impact)
	assert.Equal(t, http.StatusNotFound, httpErr.HTTPCode)
	assert.Equal(t, verrors.VegaBackend_Catalog_NotFound, httpErr.ErrorCode)
}

func expectCatalogDeletionImpact(
	ctrl *gomock.Controller,
	ra *vmock.MockResourceAccess,
	bta *vmock.MockBuildTaskAccess,
	dsa *vmock.MockDiscoverScheduleAccess,
	dta *vmock.MockDiscoverTaskAccess,
	suta *vmock.MockSemanticUnderstandingTaskAccess,
	hcss *vmock.MockCatalogHealthCheckScheduleService,
	catalogType string,
	buildTasks []*interfaces.BuildTask,
	resourceBlocked bool,
	includeBuildCascade bool,
) {
	resources := []*interfaces.Resource(nil)
	if resourceBlocked {
		resources = []*interfaces.Resource{{ID: "protected", Category: interfaces.ResourceCategoryLogicalView}}
	} else if includeBuildCascade {
		resources = []*interfaces.Resource{{ID: "r1"}, {ID: "r2"}}
	}
	ra.EXPECT().GetByCatalogID(gomock.Any(), "c1").Return(resources, nil)
	if catalogType == interfaces.CatalogTypePhysical {
		listCalls := 2
		bta.EXPECT().List(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, params interfaces.BuildTasksQueryParams) ([]*interfaces.BuildTask, int64, error) {
				if params.Limit == 1 {
					if len(params.Statuses) > 0 {
						var active int64
						for _, task := range buildTasks {
							if slices.Contains(params.Statuses, task.Status) {
								active++
							}
						}
						return nil, active, nil
					}
					return nil, int64(len(buildTasks)), nil
				}
				return buildTasks, int64(len(buildTasks)), nil
			}).Times(listCalls)
		dsa.EXPECT().List(gomock.Any(), gomock.Any()).Return(nil, int64(0), nil)
		dta.EXPECT().List(gomock.Any(), gomock.Any()).Return(nil, int64(0), nil).Times(2)
		hcss.EXPECT().GetByCatalogID(gomock.Any(), "c1").Return(nil, nil)
	}
	suta.EXPECT().List(gomock.Any(), gomock.Any()).Return(nil, int64(0), nil).Times(2)
}

func TestCatalogServiceDeleteByID(t *testing.T) {
	t.Run("deletes API catalog without a health schedule", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		ca, ps, ra := vmock.NewMockCatalogAccess(ctrl), vmock.NewMockPermissionService(ctrl), vmock.NewMockResourceAccess(ctrl)
		bta, dsa, dta := vmock.NewMockBuildTaskAccess(ctrl), vmock.NewMockDiscoverScheduleAccess(ctrl), vmock.NewMockDiscoverTaskAccess(ctrl)
		suta, hcss := vmock.NewMockSemanticUnderstandingTaskAccess(ctrl), vmock.NewMockCatalogHealthCheckScheduleService(ctrl)
		ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
		ca.EXPECT().GetByID(gomock.Any(), "api-1").Return(&interfaces.Catalog{ID: "api-1", Type: interfaces.CatalogTypePhysical, ConnectorType: "http"}, nil)
		ra.EXPECT().GetByCatalogID(gomock.Any(), "api-1").Return(nil, nil)
		bta.EXPECT().List(gomock.Any(), gomock.Any()).Return(nil, int64(0), nil).Times(2)
		dsa.EXPECT().List(gomock.Any(), gomock.Any()).Return(nil, int64(0), nil)
		dta.EXPECT().List(gomock.Any(), gomock.Any()).Return(nil, int64(0), nil).Times(2)
		suta.EXPECT().List(gomock.Any(), gomock.Any()).Return(nil, int64(0), nil).Times(2)
		hcss.EXPECT().GetByCatalogID(gomock.Any(), "api-1").Return(nil, nil)
		cf := vmock.NewMockConnectorFactory(ctrl)
		cf.EXPECT().GetSensitiveFields("http", gomock.Any()).Return(nil, nil)
		db, sqlMock, err := sqlmock.New()
		require.NoError(t, err)
		t.Cleanup(func() { _ = db.Close() })
		sqlMock.ExpectBegin()
		bta.EXPECT().DeleteByCatalogID(gomock.Any(), gomock.Any(), "api-1").Return(nil)
		dta.EXPECT().DeleteByCatalogID(gomock.Any(), gomock.Any(), "api-1").Return(nil)
		suta.EXPECT().DeleteByCatalogID(gomock.Any(), gomock.Any(), "api-1").Return(nil)
		dsa.EXPECT().DeleteByCatalogID(gomock.Any(), gomock.Any(), "api-1").Return(nil)
		hcss.EXPECT().DeleteByCatalogID(gomock.Any(), gomock.Any(), "api-1").Return(nil)
		ra.EXPECT().DeleteByCatalogID(gomock.Any(), gomock.Any(), "api-1").Return(nil)
		ca.EXPECT().DeleteByID(gomock.Any(), gomock.Any(), "api-1").Return(nil)
		sqlMock.ExpectCommit()
		ps.EXPECT().DeleteResources(gomock.Any(), interfaces.AUTH_RESOURCE_TYPE_CATALOG, []string{"api-1"}).Return(nil)
		cs := &catalogService{db: db, ca: ca, ps: ps, ra: ra, bta: bta, dsa: dsa, dta: dta, suta: suta, hcss: hcss, cf: cf}
		require.NoError(t, cs.DeleteByID(context.Background(), "api-1"))
		require.NoError(t, sqlMock.ExpectationsWereMet())
	})
	t.Run("returns not found when catalog disappears after authorization", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		ca := vmock.NewMockCatalogAccess(ctrl)
		ps := vmock.NewMockPermissionService(ctrl)

		ps.EXPECT().CheckPermission(gomock.Any(), interfaces.PermissionResource{
			Type: interfaces.AUTH_RESOURCE_TYPE_CATALOG, ID: "c1",
		}, []string{interfaces.OPERATION_TYPE_DELETE}).Return(nil)
		ca.EXPECT().GetByID(gomock.Any(), "c1").Return(nil, nil)

		cs := &catalogService{ca: ca, ps: ps}
		err := cs.DeleteByID(context.Background(), "c1")

		var httpErr *httperrors.HTTPError
		require.ErrorAs(t, err, &httpErr)
		assert.Equal(t, http.StatusNotFound, httpErr.HTTPCode)
		assert.Equal(t, verrors.VegaBackend_Catalog_NotFound, httpErr.ErrorCode)
	})

	t.Run("commits database deletion and tolerates permission cleanup failures", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		ca := vmock.NewMockCatalogAccess(ctrl)
		ps := vmock.NewMockPermissionService(ctrl)
		ra := vmock.NewMockResourceAccess(ctrl)
		hcss := vmock.NewMockCatalogHealthCheckScheduleService(ctrl)
		bta := vmock.NewMockBuildTaskAccess(ctrl)
		dsa := vmock.NewMockDiscoverScheduleAccess(ctrl)
		dta := vmock.NewMockDiscoverTaskAccess(ctrl)
		suta := vmock.NewMockSemanticUnderstandingTaskAccess(ctrl)
		db, sqlMock, err := sqlmock.New()
		require.NoError(t, err)
		t.Cleanup(func() { _ = db.Close() })

		buildTasks := []*interfaces.BuildTask{
			{ID: "completed", ResourceID: "r1", Status: interfaces.BuildTaskStatusCompleted},
			{ID: "pending", ResourceID: "r2", Status: interfaces.BuildTaskStatusPending},
		}
		ps.EXPECT().CheckPermission(gomock.Any(), interfaces.PermissionResource{
			Type: interfaces.AUTH_RESOURCE_TYPE_CATALOG, ID: "c1",
		}, []string{interfaces.OPERATION_TYPE_DELETE}).Return(nil)
		ca.EXPECT().GetByID(gomock.Any(), "c1").Return(&interfaces.Catalog{ID: "c1", Type: interfaces.CatalogTypePhysical}, nil)
		expectCatalogDeletionImpact(ctrl, ra, bta, dsa, dta, suta, hcss,
			interfaces.CatalogTypePhysical, buildTasks, false, true)
		sqlMock.ExpectBegin()
		bta.EXPECT().DeleteByCatalogID(gomock.Any(), gomock.Any(), "c1").Return(nil)
		dta.EXPECT().DeleteByCatalogID(gomock.Any(), gomock.Any(), "c1").Return(nil)
		suta.EXPECT().DeleteByCatalogID(gomock.Any(), gomock.Any(), "c1").Return(nil)
		dsa.EXPECT().DeleteByCatalogID(gomock.Any(), gomock.Any(), "c1").Return(nil)
		hcss.EXPECT().DeleteByCatalogID(gomock.Any(), gomock.Any(), "c1").Return(nil)
		ra.EXPECT().DeleteByCatalogID(gomock.Any(), gomock.Any(), "c1").Return(nil)
		ca.EXPECT().DeleteByID(gomock.Any(), gomock.Any(), "c1").Return(nil)
		sqlMock.ExpectCommit()
		ps.EXPECT().DeleteResourceParents(gomock.Any(), interfaces.AUTH_RESOURCE_TYPE_RESOURCE, []string{"r1", "r2"}).Return(errors.New("permission unavailable"))
		ps.EXPECT().DeleteResources(gomock.Any(), interfaces.AUTH_RESOURCE_TYPE_RESOURCE, []string{"r1", "r2"}).Return(errors.New("permission unavailable"))
		ps.EXPECT().DeleteResources(gomock.Any(), interfaces.AUTH_RESOURCE_TYPE_CATALOG, []string{"c1"}).Return(errors.New("permission unavailable"))

		cs := &catalogService{db: db, ca: ca, ps: ps, ra: ra, bta: bta, dsa: dsa, dta: dta, suta: suta, hcss: hcss}
		require.NoError(t, cs.DeleteByID(context.Background(), "c1"))
		require.NoError(t, sqlMock.ExpectationsWereMet())
	})

	t.Run("does not clean external state when database deletion rolls back", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		ca := vmock.NewMockCatalogAccess(ctrl)
		ps := vmock.NewMockPermissionService(ctrl)
		ra := vmock.NewMockResourceAccess(ctrl)
		hcss := vmock.NewMockCatalogHealthCheckScheduleService(ctrl)
		bta := vmock.NewMockBuildTaskAccess(ctrl)
		dsa := vmock.NewMockDiscoverScheduleAccess(ctrl)
		dta := vmock.NewMockDiscoverTaskAccess(ctrl)
		suta := vmock.NewMockSemanticUnderstandingTaskAccess(ctrl)
		db, sqlMock, err := sqlmock.New()
		require.NoError(t, err)
		t.Cleanup(func() { _ = db.Close() })

		buildTasks := []*interfaces.BuildTask{
			{ID: "completed", ResourceID: "r1", Status: interfaces.BuildTaskStatusCompleted},
		}
		ps.EXPECT().CheckPermission(gomock.Any(), interfaces.PermissionResource{
			Type: interfaces.AUTH_RESOURCE_TYPE_CATALOG, ID: "c1",
		}, []string{interfaces.OPERATION_TYPE_DELETE}).Return(nil)
		ca.EXPECT().GetByID(gomock.Any(), "c1").Return(&interfaces.Catalog{ID: "c1", Type: interfaces.CatalogTypePhysical}, nil)
		expectCatalogDeletionImpact(ctrl, ra, bta, dsa, dta, suta, hcss,
			interfaces.CatalogTypePhysical, buildTasks, false, true)
		sqlMock.ExpectBegin()
		bta.EXPECT().DeleteByCatalogID(gomock.Any(), gomock.Any(), "c1").Return(nil)
		dta.EXPECT().DeleteByCatalogID(gomock.Any(), gomock.Any(), "c1").
			Return(errors.New("database unavailable"))
		sqlMock.ExpectRollback()

		cs := &catalogService{db: db, ca: ca, ps: ps, ra: ra, bta: bta, dsa: dsa, dta: dta, suta: suta, hcss: hcss}
		err = cs.DeleteByID(context.Background(), "c1")
		require.Error(t, err)
		require.NoError(t, sqlMock.ExpectationsWereMet())
	})

	t.Run("rejects protected resources using the shared impact", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		ca := vmock.NewMockCatalogAccess(ctrl)
		ps := vmock.NewMockPermissionService(ctrl)
		ra := vmock.NewMockResourceAccess(ctrl)
		bta := vmock.NewMockBuildTaskAccess(ctrl)
		dsa := vmock.NewMockDiscoverScheduleAccess(ctrl)
		dta := vmock.NewMockDiscoverTaskAccess(ctrl)
		suta := vmock.NewMockSemanticUnderstandingTaskAccess(ctrl)
		hcss := vmock.NewMockCatalogHealthCheckScheduleService(ctrl)
		ps.EXPECT().CheckPermission(gomock.Any(), interfaces.PermissionResource{
			Type: interfaces.AUTH_RESOURCE_TYPE_CATALOG, ID: "c1",
		}, []string{interfaces.OPERATION_TYPE_DELETE}).Return(nil)
		ca.EXPECT().GetByID(gomock.Any(), "c1").Return(&interfaces.Catalog{ID: "c1", Type: interfaces.CatalogTypeLogical}, nil)
		expectCatalogDeletionImpact(ctrl, ra, bta, dsa, dta, suta, hcss,
			interfaces.CatalogTypeLogical, nil, true, false)

		cs := &catalogService{ca: ca, ps: ps, ra: ra, bta: bta, dsa: dsa, dta: dta, suta: suta, hcss: hcss}
		err := cs.DeleteByID(context.Background(), "c1")
		var httpErr *httperrors.HTTPError
		require.ErrorAs(t, err, &httpErr)
		assert.Equal(t, http.StatusConflict, httpErr.HTTPCode)
	})
}

func TestCatalogServiceGetByID(t *testing.T) {
	for _, sensitive := range []bool{false, true} {
		for _, unknown := range []bool{false, true} {
			t.Run(fmt.Sprintf("metadata lookup sensitive=%t unknown=%t", sensitive, unknown), func(t *testing.T) {
				cs, ca, ps, ums := newS2SCatalogService(t)
				ctrl := gomock.NewController(t)
				cf := vmock.NewMockConnectorFactory(ctrl)
				cs.cf = cf
				ca.EXPECT().GetByID(gomock.Any(), "catalog-1").Return(&interfaces.Catalog{ID: "catalog-1", ConnectorType: "declared", ConnectorCfg: interfaces.ConnectorConfig{"base_url": "https://api.example.com/"}}, nil)
				ps.EXPECT().FilterVisibleResourcesWithOperations(gomock.Any(), interfaces.AUTH_RESOURCE_TYPE_CATALOG, []string{"catalog-1"}, gomock.Any(), interfaces.VISIBILITY_MATCH_ALL).Return(map[string]interfaces.PermissionResourceOps{"catalog-1": {Operations: []string{interfaces.OPERATION_TYPE_VIEW_DETAIL}}}, nil)
				ums.EXPECT().GetAccountNames(gomock.Any(), gomock.Any()).Return(nil)
				var metadataErr error
				if unknown {
					metadataErr = factory.ErrConnectorUnavailable
				}
				cf.EXPECT().GetSensitiveFields("declared", gomock.Any()).Return(nil, metadataErr)
				got, err := cs.GetByID(context.Background(), "catalog-1", sensitive)
				if unknown {
					var httpErr *httperrors.HTTPError
					require.ErrorAs(t, err, &httpErr)
					assert.Equal(t, verrors.VegaBackend_Catalog_InternalError_CreateFailed, httpErr.ErrorCode)
					assert.Nil(t, got)
				} else {
					require.NoError(t, err)
					assert.Equal(t, "https://api.example.com/", got.ConnectorCfg["base_url"])
				}
			})
		}
	}
	t.Run("protected logical catalog skips connector metadata", func(t *testing.T) {
		cs, ca, ps, ums := newS2SCatalogService(t)
		ca.EXPECT().GetByID(gomock.Any(), "logical").Return(&interfaces.Catalog{ID: "logical", Type: interfaces.CatalogTypeLogical}, nil)
		ps.EXPECT().FilterVisibleResourcesWithOperations(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), interfaces.VISIBILITY_MATCH_ALL).Return(map[string]interfaces.PermissionResourceOps{"logical": {}}, nil)
		ums.EXPECT().GetAccountNames(gomock.Any(), gomock.Any()).Return(nil)
		got, err := cs.GetByID(context.Background(), "logical", true)
		require.NoError(t, err)
		assert.Equal(t, "logical", got.ID)
	})
	t.Run("trusted proxy cannot bypass internal catalog guard", func(t *testing.T) {
		cs, ca, _, _ := newS2SCatalogService(t)
		ca.EXPECT().GetByID(gomock.Any(), "c1").Return(&interfaces.Catalog{ID: "c1", Builtin: true}, nil)

		_, err := cs.GetByID(interfaces.WithTrustedProxyRead(context.Background()), "c1", false)
		require.Error(t, err)
	})

	t.Run("built-in admin reads internal catalog through catalog hierarchy", func(t *testing.T) {
		cs, ca, ps, ums := newS2SCatalogService(t)
		ca.EXPECT().GetByID(gomock.Any(), "c1").
			Return(&interfaces.Catalog{ID: "c1", Builtin: true}, nil)
		ps.EXPECT().FilterVisibleResourcesWithOperations(gomock.Any(), interfaces.AUTH_RESOURCE_TYPE_CATALOG,
			[]string{"c1"}, gomock.Any(), interfaces.VISIBILITY_MATCH_ALL).
			Return(map[string]interfaces.PermissionResourceOps{"c1": {ResourceID: "c1", Operations: interfaces.COMMON_OPERATIONS}}, nil)
		ums.EXPECT().GetAccountNames(gomock.Any(), gomock.Any()).Return(nil)

		ctx := context.WithValue(context.Background(), interfaces.ACCOUNT_INFO_KEY,
			interfaces.AccountInfo{ID: interfaces.BuiltinAdminID})
		cat, err := cs.GetByID(ctx, "c1", false)
		if err != nil {
			t.Fatalf("built-in admin should access internal catalog, got error: %v", err)
		}
		if cat == nil || len(cat.Operations) == 0 {
			t.Fatalf("expected operations to be filled, got %+v", cat)
		}
	})
	t.Run("non-admin cannot read internal catalog", func(t *testing.T) {
		cs, ca, _, _ := newS2SCatalogService(t)
		ca.EXPECT().GetByID(gomock.Any(), "c1").
			Return(&interfaces.Catalog{ID: "c1", Builtin: true}, nil)

		_, err := cs.GetByID(context.Background(), "c1", false)
		if err == nil {
			t.Fatalf("internal catalog without S2S marker should be forbidden")
		}
	})
	t.Run("catalog get by idnon internal with marker still authz", func(t *testing.T) {
		cs, ca, ps, _ := newS2SCatalogService(t)
		ca.EXPECT().GetByID(gomock.Any(), "c1").
			Return(&interfaces.Catalog{ID: "c1", Builtin: false}, nil)
		ps.EXPECT().FilterVisibleResourcesWithOperations(gomock.Any(), interfaces.AUTH_RESOURCE_TYPE_CATALOG,
			gomock.Any(), gomock.Any(), interfaces.VISIBILITY_MATCH_ALL).
			Return(map[string]interfaces.PermissionResourceOps{}, nil)

		ctx := interfaces.WithS2SInternalAccess(context.Background())
		_, err := cs.GetByID(ctx, "c1", false)
		if err == nil {
			t.Fatalf("non-internal catalog should still use per-account authorization")
		}
	})
}

// ===== List 分页逻辑 =====

// ===== DeleteByIDs empty =====

// ===== Internal catalog（系统内部目录） =====

func newS2SCatalogService(t *testing.T) (
	*catalogService, *vmock.MockCatalogAccess, *vmock.MockPermissionService, *vmock.MockUserMgmtService) {
	t.Helper()

	ctrl := gomock.NewController(t)
	ca := vmock.NewMockCatalogAccess(ctrl)
	ps := vmock.NewMockPermissionService(ctrl)
	ums := vmock.NewMockUserMgmtService(ctrl)
	cs := &catalogService{ca: ca, ps: ps, ums: ums}
	return cs, ca, ps, ums
}

func TestCatalogServiceRemoveSensitiveFields(t *testing.T) {
	t.Run("driver options are not removed when the selected field is absent", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		cf := vmock.NewMockConnectorFactory(ctrl)
		cfg := interfaces.ConnectorConfig{"variables": map[string]any{"tenant": "public"}, "options": map[string]any{"fixture_option": "driver-fixture"}}
		cf.EXPECT().GetSensitiveFields("custom", cfg).Return([]string{"fixture_option"}, nil)
		catalog := &interfaces.Catalog{ConnectorType: "custom", ConnectorCfg: cfg}
		require.NoError(t, (&catalogService{cf: cf}).removeSensitiveFields(catalog))
		assert.Equal(t, cfg, catalog.ConnectorCfg)
	})
	t.Run("hides declared variables and preserves other configuration", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		cf := vmock.NewMockConnectorFactory(ctrl)
		cfg := interfaces.ConnectorConfig{"token": "fixed-fixture", "variables": map[string]any{"api_key": "key-fixture", "tenant": "public"}, "sensitive_variables": []string{"api_key"}, "options": map[string]any{"driver_option": "unchanged"}}
		cf.EXPECT().GetSensitiveFields("http", cfg).Return([]string{"token", "api_key"}, nil)
		catalog := &interfaces.Catalog{ConnectorType: "http", ConnectorCfg: cfg}
		require.NoError(t, (&catalogService{cf: cf}).removeSensitiveFields(catalog))
		assert.NotContains(t, catalog.ConnectorCfg, "token")
		assert.NotContains(t, catalog.ConnectorCfg["variables"], "api_key")
		assert.Equal(t, "public", catalog.ConnectorCfg["variables"].(map[string]any)["tenant"])
		assert.Equal(t, cfg["options"], catalog.ConnectorCfg["options"])
		assert.Equal(t, []string{"api_key"}, catalog.ConnectorCfg["sensitive_variables"])
		assert.Equal(t, "key-fixture", cfg["variables"].(map[string]any)["api_key"])
	})
	t.Run("removes both top-level and variable fields", func(t *testing.T) {
		for _, value := range []any{"top-value", "", nil, false, 0} {
			t.Run(fmt.Sprintf("%T=%v", value, value), func(t *testing.T) {
				ctrl := gomock.NewController(t)
				cf := vmock.NewMockConnectorFactory(ctrl)
				cfg := interfaces.ConnectorConfig{"fixture_option": value, "variables": map[string]any{"fixture_option": "nested-value"}}
				cf.EXPECT().GetSensitiveFields("custom", cfg).Return([]string{"fixture_option"}, nil)
				catalog := &interfaces.Catalog{ConnectorType: "custom", ConnectorCfg: cfg}
				require.NoError(t, (&catalogService{cf: cf}).removeSensitiveFields(catalog))
				assert.NotContains(t, catalog.ConnectorCfg, "fixture_option")
				assert.NotContains(t, catalog.ConnectorCfg["variables"], "fixture_option")
				assert.Equal(t, "nested-value", cfg["variables"].(map[string]any)["fixture_option"])
				assert.Contains(t, cfg, "fixture_option")
			})
		}
	})
	t.Run("redacts fixed and dynamic fields independently and idempotently", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		cf := vmock.NewMockConnectorFactory(ctrl)
		cf.EXPECT().GetSensitiveFields("custom", gomock.Any()).Return([]string{"token", "fixture_option"}, nil).Times(2)
		original := interfaces.ConnectorConfig{"token": "fixed", "variables": map[string]any{"fixture_option": "nested", "normal": "public", "zero": 0, "flag": false, "rows": []any{map[string]any{"flag": false}}}, "sensitive_variables": []any{"fixture_option"}}
		catalog := &interfaces.Catalog{ConnectorType: "custom", ConnectorCfg: original}
		cs := &catalogService{cf: cf}
		for range 2 {
			require.NoError(t, cs.removeSensitiveFields(catalog))
		}
		assert.NotContains(t, catalog.ConnectorCfg, "token")
		variables := catalog.ConnectorCfg["variables"].(map[string]any)
		assert.NotContains(t, variables, "fixture_option")
		assert.Equal(t, "public", variables["normal"])
		assert.Equal(t, 0, variables["zero"])
		assert.Equal(t, false, variables["flag"])
		variables["rows"].([]any)[0].(map[string]any)["flag"] = true
		assert.Equal(t, false, original["variables"].(map[string]any)["rows"].([]any)[0].(map[string]any)["flag"])
		assert.Equal(t, []any{"fixture_option"}, catalog.ConnectorCfg["sensitive_variables"])
		assert.Equal(t, "fixed", original["token"])
		assert.Equal(t, "nested", original["variables"].(map[string]any)["fixture_option"])
	})
	t.Run("invalid metadata fails before modifying the catalog", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		cf := vmock.NewMockConnectorFactory(ctrl)
		cf.EXPECT().GetSensitiveFields("custom", gomock.Any()).Return(nil, errors.New("invalid protection metadata"))
		cfg := interfaces.ConnectorConfig{"token": "fixed", "variables": map[string]any{"fixture_option": "fixture"}, "sensitive_variables": []any{false}}
		catalog := &interfaces.Catalog{ConnectorType: "custom", ConnectorCfg: cfg}
		require.Error(t, (&catalogService{cf: cf}).removeSensitiveFields(catalog))
		assert.Equal(t, "fixed", catalog.ConnectorCfg["token"])
	})

	for _, fields := range [][]string{nil, {"token", "api_key", "app_secret"}} {
		t.Run(fmt.Sprintf("declared fields empty=%t", len(fields) == 0), func(t *testing.T) {
			ctrl := gomock.NewController(t)
			cf := vmock.NewMockConnectorFactory(ctrl)
			cf.EXPECT().GetSensitiveFields("declared", gomock.Any()).Return(fields, nil)
			catalog := &interfaces.Catalog{ConnectorType: "declared", ConnectorCfg: interfaces.ConnectorConfig{"base_url": "https://api.example.com/", "token": "fixture", "api_key": "fixture", "app_secret": "fixture"}}
			cs := &catalogService{cf: cf}
			require.NoError(t, cs.removeSensitiveFields(catalog))
			assert.Equal(t, "https://api.example.com/", catalog.ConnectorCfg["base_url"])
			for _, field := range fields {
				assert.NotContains(t, catalog.ConnectorCfg, field)
			}
			if len(fields) == 0 {
				assert.Len(t, catalog.ConnectorCfg, 4)
			}
		})
	}
	t.Run("nil and logical catalogs do not need connector metadata", func(t *testing.T) {
		cs := &catalogService{}
		require.NoError(t, cs.removeSensitiveFields(nil))
		require.NoError(t, cs.removeSensitiveFields(&interfaces.Catalog{}))
	})
	t.Run("unknown connector returns metadata error", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		cf := vmock.NewMockConnectorFactory(ctrl)
		cf.EXPECT().GetSensitiveFields("missing", gomock.Any()).Return(nil, factory.ErrConnectorUnavailable)
		cs := &catalogService{cf: cf}
		require.ErrorIs(t, cs.removeSensitiveFields(&interfaces.Catalog{ConnectorType: "missing"}), factory.ErrConnectorUnavailable)
	})
}

func TestCatalogServiceInternalGetByID(t *testing.T) {
	for _, protected := range []bool{false, true} {
		for _, internal := range []bool{false, true} {
			for _, malformed := range []bool{false, true} {
				t.Run(fmt.Sprintf("dynamic read protected=%t internal=%t malformed=%t", protected, internal, malformed), func(t *testing.T) {
					cs, ca, ps, ums := newS2SCatalogService(t)
					ctrl := gomock.NewController(t)
					cf := vmock.NewMockConnectorFactory(ctrl)
					cs.cf = cf
					cs.cipher = &mockCipher{decryptFunc: func(value string) (string, error) { return "plain_" + value, nil }}
					cfg := interfaces.ConnectorConfig{"token": "ENC:fixed", "variables": map[string]any{"fixture_option": "ENC:nested", "flag": false}, "sensitive_variables": []any{"fixture_option"}}
					if malformed {
						cfg["sensitive_variables"] = []any{123}
					}
					ca.EXPECT().GetByID(gomock.Any(), "one").Return(&interfaces.Catalog{ID: "one", ConnectorType: "custom", ConnectorCfg: cfg}, nil)
					var metadataErr error
					if malformed {
						metadataErr = errors.New("invalid protection metadata")
					}
					cf.EXPECT().GetSensitiveFields("custom", cfg).Return([]string{"token", "fixture_option"}, metadataErr)
					if !internal {
						ps.EXPECT().FilterVisibleResourcesWithOperations(gomock.Any(), gomock.Any(), []string{"one"}, gomock.Any(), interfaces.VISIBILITY_MATCH_ALL).Return(map[string]interfaces.PermissionResourceOps{"one": {}}, nil)
						ums.EXPECT().GetAccountNames(gomock.Any(), gomock.Any()).Return(nil)
					}
					var got *interfaces.Catalog
					var err error
					if internal {
						got, err = cs.InternalGetByID(context.Background(), "one", protected)
					} else {
						got, err = cs.GetByID(context.Background(), "one", protected)
					}
					if malformed {
						require.Error(t, err)
						assert.Nil(t, got)
						return
					}
					require.NoError(t, err)
					require.NotNil(t, got)
					variables := got.ConnectorCfg["variables"].(map[string]any)
					if protected {
						assert.Equal(t, "plain_nested", variables["fixture_option"])
						assert.Equal(t, "plain_fixed", got.ConnectorCfg["token"])
					} else {
						assert.NotContains(t, variables, "fixture_option")
						assert.NotContains(t, got.ConnectorCfg, "token")
					}
					assert.Equal(t, false, variables["flag"])
					assert.Equal(t, []any{"fixture_option"}, got.ConnectorCfg["sensitive_variables"])
					assert.Equal(t, "ENC:nested", cfg["variables"].(map[string]any)["fixture_option"])
					assert.Equal(t, "ENC:fixed", cfg["token"])
				})
			}
		}
	}

	for _, sensitive := range []bool{false, true} {
		t.Run(fmt.Sprintf("unavailable API sensitive=%t", sensitive), func(t *testing.T) {
			ctrl := gomock.NewController(t)
			ca, cf := vmock.NewMockCatalogAccess(ctrl), vmock.NewMockConnectorFactory(ctrl)
			ca.EXPECT().GetByID(gomock.Any(), "api-1").Return(&interfaces.Catalog{ID: "api-1", ConnectorType: "http", ConnectorCfg: interfaces.ConnectorConfig{"token": "fixture"}}, nil)
			cf.EXPECT().GetSensitiveFields("http", gomock.Any()).Return(nil, factory.ErrConnectorUnavailable)
			cs := &catalogService{ca: ca, cf: cf}
			got, err := cs.InternalGetByID(context.Background(), "api-1", sensitive)
			var httpErr *httperrors.HTTPError
			require.ErrorAs(t, err, &httpErr)
			assert.Equal(t, http.StatusBadRequest, httpErr.HTTPCode)
			assert.Nil(t, got)
		})
	}
	t.Run("protected logical catalog does not look up a connector", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		ca := vmock.NewMockCatalogAccess(ctrl)
		ca.EXPECT().GetByID(gomock.Any(), "logical").Return(&interfaces.Catalog{ID: "logical", Type: interfaces.CatalogTypeLogical}, nil)
		cs := &catalogService{ca: ca}
		got, err := cs.InternalGetByID(context.Background(), "logical", true)
		require.NoError(t, err)
		assert.Equal(t, "logical", got.ID)
	})
}
