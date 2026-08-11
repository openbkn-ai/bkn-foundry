// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package catalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/agiledragon/gomonkey/v2"
	"github.com/openbkn-ai/bkn-comm-go/rest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"vega-backend/common"
	"vega-backend/drivenadapters/entityextension"
	verrors "vega-backend/errors"
	"vega-backend/interfaces"
	mock_interfaces "vega-backend/interfaces/mock"
)

// mockCipher 实现 kwcrypto.Cipher 接口用于测试
// 注：kwcrypto.Cipher 是外部库接口，无 mockgen 生成的版本，手写 mock 是合理的
type mockCipher struct {
	decryptFunc func(ciphertext string) (string, error)
}

func (m *mockCipher) Encrypt(plaintext string) (string, error) {
	return "encrypted_" + plaintext, nil
}

func (m *mockCipher) Decrypt(ciphertext string) (string, error) {
	return m.decryptFunc(ciphertext)
}

func (m *mockCipher) Signature(data string) (string, error) {
	return "", nil
}

func requireRedactedConnectorInitializationError(t *testing.T, err error, sensitiveError string) {
	t.Helper()

	var httpErr *rest.HTTPError
	require.ErrorAs(t, err, &httpErr)
	assert.Equal(t, http.StatusBadRequest, httpErr.HTTPCode)
	assert.Equal(t, verrors.VegaBackend_Catalog_InternalError_CreateFailed, httpErr.BaseError.ErrorCode)
	assert.NotContains(t, fmt.Sprint(httpErr.BaseError.ErrorDetails), sensitiveError)
	assert.Contains(t, fmt.Sprint(httpErr.BaseError.ErrorDetails), connectorInitializationFailedResult)
}

func TestValidateAndDecryptSensitiveFields(t *testing.T) {
	t.Run("validate and decrypt no cipher", func(t *testing.T) {
		cs := &catalogService{cipher: nil}
		config := map[string]any{"password": "secret123", "host": "localhost"}

		decrypted, err := cs.validateAndDecryptSensitiveFields([]string{"password"}, config)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if decrypted["password"] != "secret123" {
			t.Errorf("expected 'secret123', got '%v'", decrypted["password"])
		}
		if config["password"] != "secret123" {
			t.Errorf("original config should not be modified")
		}
	})
	t.Run("validate and decrypt with cipher success", func(t *testing.T) {
		cs := &catalogService{
			cipher: &mockCipher{
				decryptFunc: func(ciphertext string) (string, error) {
					return "decrypted_" + ciphertext, nil
				},
			},
		}
		config := map[string]any{"password": "rsa_ciphertext", "host": "localhost"}

		decrypted, err := cs.validateAndDecryptSensitiveFields([]string{"password"}, config)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if decrypted["password"] != "decrypted_rsa_ciphertext" {
			t.Errorf("expected 'decrypted_rsa_ciphertext', got '%v'", decrypted["password"])
		}
		if config["password"] != EncryptedPrefix+"rsa_ciphertext" {
			t.Errorf("expected ENC: prefix, got '%v'", config["password"])
		}
		if decrypted["host"] != "localhost" {
			t.Errorf("non-sensitive field should be unchanged")
		}
	})
	t.Run("validate and decrypt with cipher decrypt fails", func(t *testing.T) {
		cs := &catalogService{
			cipher: &mockCipher{
				decryptFunc: func(ciphertext string) (string, error) {
					return "", fmt.Errorf("invalid ciphertext")
				},
			},
		}
		config := map[string]any{"password": "bad_data"}

		_, err := cs.validateAndDecryptSensitiveFields([]string{"password"}, config)
		if err == nil {
			t.Fatal("expected error for invalid ciphertext")
		}
		if !strings.Contains(err.Error(), "password") {
			t.Errorf("error should mention field name, got: %v", err)
		}
	})
	t.Run("validate and decrypt empty value", func(t *testing.T) {
		cs := &catalogService{
			cipher: &mockCipher{
				decryptFunc: func(ciphertext string) (string, error) {
					t.Fatal("should not be called for empty value")
					return "", nil
				},
			},
		}
		config := map[string]any{"password": ""}

		_, err := cs.validateAndDecryptSensitiveFields([]string{"password"}, config)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	t.Run("validate and decrypt non string value", func(t *testing.T) {
		cs := &catalogService{
			cipher: &mockCipher{
				decryptFunc: func(ciphertext string) (string, error) {
					t.Fatal("should not be called for non-string value")
					return "", nil
				},
			},
		}
		config := map[string]any{"password": 12345}

		_, err := cs.validateAndDecryptSensitiveFields([]string{"password"}, config)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

func TestDecryptSensitiveFields(t *testing.T) {
	t.Run("decrypt no cipher", func(t *testing.T) {
		cs := &catalogService{cipher: nil}
		config := map[string]any{"password": "ENC:ciphertext"}

		decrypted, err := cs.decryptSensitiveFields([]string{"password"}, config)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if decrypted["password"] != "ENC:ciphertext" {
			t.Errorf("expected original value, got '%v'", decrypted["password"])
		}
	})
	t.Run("decrypt with cipher success", func(t *testing.T) {
		cs := &catalogService{
			cipher: &mockCipher{
				decryptFunc: func(ciphertext string) (string, error) {
					return "plaintext_" + ciphertext, nil
				},
			},
		}
		config := map[string]any{"password": "ENC:rsa_data"}

		decrypted, err := cs.decryptSensitiveFields([]string{"password"}, config)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if decrypted["password"] != "plaintext_rsa_data" {
			t.Errorf("expected 'plaintext_rsa_data', got '%v'", decrypted["password"])
		}
	})
	t.Run("decrypt missing enc prefix", func(t *testing.T) {
		cs := &catalogService{
			cipher: &mockCipher{
				decryptFunc: func(ciphertext string) (string, error) {
					return "", nil
				},
			},
		}
		config := map[string]any{"password": "no_prefix_value"}

		_, err := cs.decryptSensitiveFields([]string{"password"}, config)
		if err == nil {
			t.Fatal("expected error for missing ENC: prefix")
		}
		if !strings.Contains(err.Error(), "not encrypted") {
			t.Errorf("expected 'not encrypted' error, got: %v", err)
		}
	})
	t.Run("decrypt decrypt fails", func(t *testing.T) {
		cs := &catalogService{
			cipher: &mockCipher{
				decryptFunc: func(ciphertext string) (string, error) {
					return "", fmt.Errorf("corrupted data")
				},
			},
		}
		config := map[string]any{"password": "ENC:bad_data"}

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
		mockCA := mock_interfaces.NewMockCatalogAccess(ctrl)
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
		mockCA := mock_interfaces.NewMockCatalogAccess(ctrl)
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
		mockCA := mock_interfaces.NewMockCatalogAccess(ctrl)
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
		mockCA := mock_interfaces.NewMockCatalogAccess(ctrl)
		mockCA.EXPECT().GetByName(gomock.Any(), "test").
			Return(&interfaces.Catalog{Name: "test"}, nil)

		cs := &catalogService{ca: mockCA}
		exists, err := cs.CheckExistByName(context.Background(), "test")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !exists {
			t.Error("expected catalog to exist")
		}
	})
}

func TestCatalogServiceCreate(t *testing.T) {
	t.Run("does not expose connector initialization error", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockPS := mock_interfaces.NewMockPermissionService(ctrl)
		sensitiveError := "invalid endpoint db.internal with token secret"

		mockPS.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
		connectorFactory := mock_interfaces.NewMockConnectorFactory(ctrl)
		connectorFactory.EXPECT().GetSensitiveFields("mariadb").Return(nil)
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
			Internal:      true,
			ConnectorType: interfaces.ConnectorTypePostgreSQL,
		}, false)

		var httpErr *rest.HTTPError
		require.ErrorAs(t, err, &httpErr)
		assert.Equal(t, http.StatusBadRequest, httpErr.HTTPCode)
		assert.Equal(t, verrors.VegaBackend_Catalog_InvalidParameter, httpErr.BaseError.ErrorCode)
		assert.Contains(t, fmt.Sprint(httpErr.BaseError.ErrorDetails), "internal catalogs must be logical")
	})

	t.Run("does not expose connector error when connection test fails", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockPS := mock_interfaces.NewMockPermissionService(ctrl)
		connector := mock_interfaces.NewMockConnector(ctrl)
		sensitiveError := "dial tcp db.internal:3306 with password secret failed"

		mockPS.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
		connector.EXPECT().TestConnection(gomock.Any()).Return(errors.New(sensitiveError))
		connector.EXPECT().Close(gomock.Any()).Return(nil)
		connectorFactory := mock_interfaces.NewMockConnectorFactory(ctrl)
		connectorFactory.EXPECT().GetSensitiveFields("mariadb").Return(nil)
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

		var httpErr *rest.HTTPError
		require.ErrorAs(t, err, &httpErr)
		assert.Equal(t, http.StatusBadRequest, httpErr.HTTPCode)
		assert.Equal(t, verrors.VegaBackend_Catalog_InternalError_TestConnectionFailed, httpErr.BaseError.ErrorCode)
		assert.NotContains(t, fmt.Sprint(httpErr.BaseError.ErrorDetails), sensitiveError)
		assert.Contains(t, fmt.Sprint(httpErr.BaseError.ErrorDetails), "Connection test failed")
	})

	t.Run("create logical catalog defaults to disabled and unchecked", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCA := mock_interfaces.NewMockCatalogAccess(ctrl)
		mockPS := mock_interfaces.NewMockPermissionService(ctrl)
		db, sqlMock, err := sqlmock.New()
		require.NoError(t, err)
		t.Cleanup(func() { _ = db.Close() })

		sqlMock.ExpectBegin()
		sqlMock.ExpectCommit()
		mockPS.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
		mockCA.EXPECT().Create(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, tx *sql.Tx, catalog *interfaces.Catalog) error {
				require.NotNil(t, tx)
				if catalog.Enabled {
					t.Fatal("expected catalog to be disabled by default")
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
			Name: "catalog",
		}, false)
		require.NoError(t, err)
		require.NoError(t, sqlMock.ExpectationsWereMet())
	})

	t.Run("rolls back transaction when catalog creation fails", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCA := mock_interfaces.NewMockCatalogAccess(ctrl)
		mockPS := mock_interfaces.NewMockPermissionService(ctrl)
		db, sqlMock, err := sqlmock.New()
		require.NoError(t, err)
		t.Cleanup(func() { _ = db.Close() })

		sensitiveError := "insert into t_catalog failed at db.internal"
		createErr := errors.New(sensitiveError)
		sqlMock.ExpectBegin()
		sqlMock.ExpectRollback()
		mockPS.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
		mockCA.EXPECT().Create(gomock.Any(), gomock.Any(), gomock.Any()).Return(createErr)

		cs := &catalogService{db: db, ca: mockCA, ps: mockPS}
		_, err = cs.Create(context.Background(), &interfaces.CatalogRequest{Name: "catalog"}, false)

		var httpErr *rest.HTTPError
		require.ErrorAs(t, err, &httpErr)
		assert.Equal(t, http.StatusInternalServerError, httpErr.HTTPCode)
		assert.Equal(t, verrors.VegaBackend_Catalog_InternalError_CreateFailed, httpErr.BaseError.ErrorCode)
		assert.Contains(t, fmt.Sprint(httpErr.BaseError.ErrorDetails), "failed to create catalog")
		assert.NotContains(t, fmt.Sprint(httpErr.BaseError.ErrorDetails), sensitiveError)
		require.NoError(t, sqlMock.ExpectationsWereMet())
	})

	t.Run("rejects health check schedule for logical catalog before persistence", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockPS := mock_interfaces.NewMockPermissionService(ctrl)
		mockPS.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)

		cs := &catalogService{ps: mockPS}
		_, err := cs.Create(context.Background(), &interfaces.CatalogRequest{
			Name: "logical-catalog",
			HealthCheckSchedule: &interfaces.CatalogHealthCheckScheduleRequest{
				Mode:     interfaces.CatalogHealthCheckScheduleModeEnabled,
				CronExpr: "0 * * * *",
			},
		}, false)

		var httpErr *rest.HTTPError
		require.ErrorAs(t, err, &httpErr)
		assert.Equal(t, http.StatusBadRequest, httpErr.HTTPCode)
		assert.Equal(t, verrors.VegaBackend_Catalog_InvalidParameter, httpErr.BaseError.ErrorCode)
		assert.Contains(t, fmt.Sprint(httpErr.BaseError.ErrorDetails), "only supported for physical catalogs")
	})

	t.Run("create enabled true", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCA := mock_interfaces.NewMockCatalogAccess(ctrl)
		mockPS := mock_interfaces.NewMockPermissionService(ctrl)
		db, sqlMock, err := sqlmock.New()
		require.NoError(t, err)
		t.Cleanup(func() { _ = db.Close() })

		sqlMock.ExpectBegin()
		sqlMock.ExpectCommit()
		mockPS.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
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
	t.Run("create internal uses internal auth type", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCA := mock_interfaces.NewMockCatalogAccess(ctrl)
		mockPS := mock_interfaces.NewMockPermissionService(ctrl)
		db, sqlMock, err := sqlmock.New()
		require.NoError(t, err)
		t.Cleanup(func() { _ = db.Close() })

		sqlMock.ExpectBegin()
		sqlMock.ExpectCommit()
		mockPS.EXPECT().CheckPermission(gomock.Any(), interfaces.PermissionResource{
			Type: interfaces.AUTH_RESOURCE_TYPE_INTERNAL_CATALOG,
			ID:   interfaces.RESOURCE_ID_ALL,
		}, []string{interfaces.OPERATION_TYPE_CREATE}).Return(nil)
		mockCA.EXPECT().Create(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, _ *sql.Tx, catalog *interfaces.Catalog) error {
				if !catalog.Internal {
					t.Fatal("expected catalog.Internal=true")
				}
				return nil
			},
		)
		mockPS.EXPECT().CreateResources(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, resources []interfaces.PermissionResource, _ []string) error {
				if resources[0].Type != interfaces.AUTH_RESOURCE_TYPE_INTERNAL_CATALOG {
					t.Fatalf("expected internal_catalog auth type, got %s", resources[0].Type)
				}
				return nil
			},
		)

		cs := &catalogService{db: db, ca: mockCA, ps: mockPS}
		_, err = cs.Create(context.Background(), &interfaces.CatalogRequest{
			Name:     "internal-catalog",
			Internal: true,
		}, false)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		require.NoError(t, sqlMock.ExpectationsWereMet())
	})
	t.Run("physical catalog creates default inherit schedule", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockHCSS := mock_interfaces.NewMockCatalogHealthCheckScheduleService(ctrl)
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
		mockHCSS := mock_interfaces.NewMockCatalogHealthCheckScheduleService(ctrl)
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
	t.Run("does not expose connector initialization error during preflight", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockPS := mock_interfaces.NewMockPermissionService(ctrl)
		sensitiveError := "invalid endpoint db.internal with token secret"

		mockPS.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
		connectorFactory := mock_interfaces.NewMockConnectorFactory(ctrl)
		connectorFactory.EXPECT().GetSensitiveFields("mariadb").Return(nil)
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
		ca := mock_interfaces.NewMockCatalogAccess(ctrl)
		ps := mock_interfaces.NewMockPermissionService(ctrl)
		ps.EXPECT().CheckPermission(gomock.Any(), interfaces.PermissionResource{
			Type: interfaces.AUTH_RESOURCE_TYPE_CATALOG,
			ID:   "missing",
		}, []string{interfaces.OPERATION_TYPE_MODIFY}).Return(nil)
		ca.EXPECT().GetByID(gomock.Any(), "missing").Return(nil, nil)

		cs := &catalogService{ca: ca, ps: ps}
		result, err := cs.TestConnection(context.Background(), "missing")

		httpErr, ok := err.(*rest.HTTPError)
		require.True(t, ok)
		assert.Equal(t, http.StatusNotFound, httpErr.HTTPCode)
		assert.Nil(t, result)
	})
	t.Run("redacts catalog query error", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		ca := mock_interfaces.NewMockCatalogAccess(ctrl)
		ps := mock_interfaces.NewMockPermissionService(ctrl)
		sensitiveError := "select t_catalog failed at db.internal"
		ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
		ca.EXPECT().GetByID(gomock.Any(), "catalog-1").Return(nil, errors.New(sensitiveError))

		cs := &catalogService{ca: ca, ps: ps}
		result, err := cs.TestConnection(context.Background(), "catalog-1")

		var httpErr *rest.HTTPError
		require.ErrorAs(t, err, &httpErr)
		assert.Equal(t, http.StatusInternalServerError, httpErr.HTTPCode)
		assert.Equal(t, verrors.VegaBackend_Catalog_InternalError_GetFailed, httpErr.BaseError.ErrorCode)
		assert.Contains(t, fmt.Sprint(httpErr.BaseError.ErrorDetails), "failed to get catalog for connection test")
		assert.NotContains(t, fmt.Sprint(httpErr.BaseError.ErrorDetails), sensitiveError)
		assert.Nil(t, result)
	})
	t.Run("redacts health status update error", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		ca := mock_interfaces.NewMockCatalogAccess(ctrl)
		ps := mock_interfaces.NewMockPermissionService(ctrl)
		connector := mock_interfaces.NewMockConnector(ctrl)
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
		connectorFactory := mock_interfaces.NewMockConnectorFactory(ctrl)
		connectorFactory.EXPECT().GetSensitiveFields("mariadb").Return(nil)
		connectorFactory.EXPECT().CreateConnectorInstance(gomock.Any(), "mariadb", gomock.Any()).Return(connector, nil)

		cs := &catalogService{
			appSetting: &common.AppSetting{},
			ca:         ca,
			ps:         ps,
			cf:         connectorFactory,
		}
		result, err := cs.TestConnection(context.Background(), "catalog-1")

		var httpErr *rest.HTTPError
		require.ErrorAs(t, err, &httpErr)
		assert.Equal(t, http.StatusInternalServerError, httpErr.HTTPCode)
		assert.Equal(t, verrors.VegaBackend_Catalog_InternalError_UpdateFailed, httpErr.BaseError.ErrorCode)
		assert.Contains(t, fmt.Sprint(httpErr.BaseError.ErrorDetails), "failed to update catalog health check status")
		assert.NotContains(t, fmt.Sprint(httpErr.BaseError.ErrorDetails), sensitiveError)
		assert.Nil(t, result)
	})
	t.Run("test connection logical catalog returns an explicit failure", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCA := mock_interfaces.NewMockCatalogAccess(ctrl)
		mockPS := mock_interfaces.NewMockPermissionService(ctrl)
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
		mockPS := mock_interfaces.NewMockPermissionService(ctrl)
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
		mockCA := mock_interfaces.NewMockCatalogAccess(ctrl)
		mockCA.EXPECT().GetByID(gomock.Any(), "catalog-1").Return(&interfaces.Catalog{ID: "catalog-1"}, nil)

		cs := &catalogService{ca: mockCA}
		result, err := cs.InternalTestConnection(context.Background(), "catalog-1")

		require.NoError(t, err)
		assert.Equal(t, interfaces.CatalogHealthStatusUnhealthy, result.HealthCheckStatus)
	})
}

func TestCatalogServiceTestConnectorConnection(t *testing.T) {
	t.Run("passes configured timeout to connector", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		connector := mock_interfaces.NewMockConnector(ctrl)
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
		connector := mock_interfaces.NewMockConnector(ctrl)
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
	t.Run("does not expose connector initialization error", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockPS := mock_interfaces.NewMockPermissionService(ctrl)
		sensitiveError := "invalid endpoint db.internal with token secret"

		mockPS.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
		connectorFactory := mock_interfaces.NewMockConnectorFactory(ctrl)
		connectorFactory.EXPECT().GetSensitiveFields("mariadb").Return(nil)
		connectorFactory.EXPECT().CreateConnectorInstance(gomock.Any(), "mariadb", gomock.Any()).
			Return(nil, errors.New(sensitiveError))
		cs := &catalogService{
			ps: mockPS,
			cf: connectorFactory,
		}
		err := cs.Update(context.Background(), &interfaces.Catalog{
			ID:   "catalog-1",
			Name: "physical-catalog",
		}, &interfaces.CatalogRequest{
			Name:          "physical-catalog",
			ConnectorType: "mariadb",
		}, false)

		requireRedactedConnectorInitializationError(t, err, sensitiveError)
	})

	t.Run("does not expose connector error when connection test fails", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockPS := mock_interfaces.NewMockPermissionService(ctrl)
		connector := mock_interfaces.NewMockConnector(ctrl)
		sensitiveError := "dial tcp db.internal:3306 with password secret failed"

		mockPS.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
		connector.EXPECT().TestConnection(gomock.Any()).Return(errors.New(sensitiveError))
		connector.EXPECT().Close(gomock.Any()).Return(nil)
		connectorFactory := mock_interfaces.NewMockConnectorFactory(ctrl)
		connectorFactory.EXPECT().GetSensitiveFields("mariadb").Return(nil)
		connectorFactory.EXPECT().CreateConnectorInstance(gomock.Any(), "mariadb", gomock.Any()).Return(connector, nil)

		cs := &catalogService{
			appSetting: &common.AppSetting{},
			ps:         mockPS,
			cf:         connectorFactory,
		}
		err := cs.Update(context.Background(), &interfaces.Catalog{
			ID:   "catalog-1",
			Name: "physical-catalog",
		}, &interfaces.CatalogRequest{
			Name:          "physical-catalog",
			ConnectorType: "mariadb",
		}, false)

		var httpErr *rest.HTTPError
		require.ErrorAs(t, err, &httpErr)
		assert.Equal(t, http.StatusBadRequest, httpErr.HTTPCode)
		assert.Equal(t, verrors.VegaBackend_Catalog_InternalError_TestConnectionFailed, httpErr.BaseError.ErrorCode)
		assert.NotContains(t, fmt.Sprint(httpErr.BaseError.ErrorDetails), sensitiveError)
		assert.Contains(t, fmt.Sprint(httpErr.BaseError.ErrorDetails), "Connection test failed")
	})

	t.Run("uses transaction when extensions are omitted", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCA := mock_interfaces.NewMockCatalogAccess(ctrl)
		mockPS := mock_interfaces.NewMockPermissionService(ctrl)
		db, sqlMock, err := sqlmock.New()
		require.NoError(t, err)
		t.Cleanup(func() { _ = db.Close() })

		sqlMock.ExpectBegin()
		sqlMock.ExpectCommit()
		mockPS.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
		mockCA.EXPECT().Update(gomock.Any(), gomock.Not(nil), gomock.Any()).Return(nil)

		cs := &catalogService{db: db, ca: mockCA, ps: mockPS}
		err = cs.Update(context.Background(), &interfaces.Catalog{
			ID:   "catalog-1",
			Name: "catalog",
		}, &interfaces.CatalogRequest{Name: "catalog"}, false)

		require.NoError(t, err)
		require.NoError(t, sqlMock.ExpectationsWereMet())
	})

	t.Run("rolls back catalog and extensions when extension replacement fails", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCA := mock_interfaces.NewMockCatalogAccess(ctrl)
		mockPS := mock_interfaces.NewMockPermissionService(ctrl)
		db, sqlMock, err := sqlmock.New()
		require.NoError(t, err)
		t.Cleanup(func() { _ = db.Close() })

		sensitiveError := "replace t_entity_extension failed at db.internal"
		replaceErr := errors.New(sensitiveError)
		patches := gomonkey.NewPatches()
		t.Cleanup(patches.Reset)
		patches.ApplyFunc(entityextension.NewStore, func(_ *common.AppSetting) *entityextension.Store {
			return &entityextension.Store{}
		})
		patches.ApplyMethod(&entityextension.Store{}, "Replace",
			func(_ *entityextension.Store, _ context.Context, tx *sql.Tx, kind, entityID string, _ map[string]string) error {
				require.NotNil(t, tx)
				assert.Equal(t, entityextension.KindCatalog, kind)
				assert.Equal(t, "catalog-1", entityID)
				return replaceErr
			})

		sqlMock.ExpectBegin()
		sqlMock.ExpectRollback()
		mockPS.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
		mockCA.EXPECT().Update(gomock.Any(), gomock.Not(nil), gomock.Any()).Return(nil)

		extensionValues := map[string]string{"owner": "team-a"}
		cs := &catalogService{
			appSetting: &common.AppSetting{},
			db:         db,
			ca:         mockCA,
			ps:         mockPS,
		}
		err = cs.Update(context.Background(), &interfaces.Catalog{
			ID:   "catalog-1",
			Name: "catalog",
		}, &interfaces.CatalogRequest{
			Name:       "catalog",
			Extensions: &extensionValues,
		}, false)

		var httpErr *rest.HTTPError
		require.ErrorAs(t, err, &httpErr)
		assert.Equal(t, http.StatusInternalServerError, httpErr.HTTPCode)
		assert.Equal(t, verrors.VegaBackend_Catalog_InternalError_UpdateFailed, httpErr.BaseError.ErrorCode)
		assert.Contains(t, fmt.Sprint(httpErr.BaseError.ErrorDetails), "failed to update catalog")
		assert.NotContains(t, fmt.Sprint(httpErr.BaseError.ErrorDetails), sensitiveError)
		require.NoError(t, sqlMock.ExpectationsWereMet())
	})
}

func TestCatalogServiceSetEnabled(t *testing.T) {
	t.Run("set enabled reenable sets health status unchecked", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCA := mock_interfaces.NewMockCatalogAccess(ctrl)
		mockPS := mock_interfaces.NewMockPermissionService(ctrl)

		mockPS.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
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
		err := cs.SetEnabled(context.Background(), &interfaces.Catalog{
			ID:      "catalog-1",
			Name:    "catalog",
			Enabled: false,
			CatalogHealthCheckStatus: interfaces.CatalogHealthCheckStatus{
				HealthCheckStatus: interfaces.CatalogHealthStatusHealthy,
			},
		}, true)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	t.Run("set enabled disable preserves health status", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCA := mock_interfaces.NewMockCatalogAccess(ctrl)
		mockPS := mock_interfaces.NewMockPermissionService(ctrl)

		mockPS.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
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
		err := cs.SetEnabled(context.Background(), &interfaces.Catalog{
			ID:      "catalog-1",
			Name:    "catalog",
			Enabled: true,
			CatalogHealthCheckStatus: interfaces.CatalogHealthCheckStatus{
				HealthCheckStatus: interfaces.CatalogHealthStatusHealthy,
			},
		}, false)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

func TestCatalogServiceList(t *testing.T) {
	t.Run("keeps catalogs when account name lookup fails", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCA := mock_interfaces.NewMockCatalogAccess(ctrl)
		mockPS := mock_interfaces.NewMockPermissionService(ctrl)
		mockUMS := mock_interfaces.NewMockUserMgmtService(ctrl)

		ids := []string{"c1", "c2", "c3"}
		catalogs := []*interfaces.Catalog{{ID: "c1"}, {ID: "c2"}, {ID: "c3"}}
		mockCA.EXPECT().ListIDs(gomock.Any(), gomock.Any()).Return(ids, nil)
		mockCA.EXPECT().ListInternalIDs(gomock.Any()).Return([]string{}, nil)
		mockPS.EXPECT().FilterResources(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), true, gomock.Any()).
			Return(map[string]interfaces.PermissionResourceOps{
				"c1": {ResourceID: "c1"}, "c2": {ResourceID: "c2"}, "c3": {ResourceID: "c3"},
			}, nil)
		mockCA.EXPECT().GetByIDs(gomock.Any(), gomock.Any()).Return(catalogs, nil)
		mockCA.EXPECT().AttachListExtensions(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
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
		mockCA := mock_interfaces.NewMockCatalogAccess(ctrl)
		mockPS := mock_interfaces.NewMockPermissionService(ctrl)
		mockUMS := mock_interfaces.NewMockUserMgmtService(ctrl)

		ids := []string{"c1", "c2", "c3", "c4", "c5"}
		mockCA.EXPECT().ListIDs(gomock.Any(), gomock.Any()).Return(ids, nil)
		mockCA.EXPECT().ListInternalIDs(gomock.Any()).Return([]string{}, nil)
		mockPS.EXPECT().FilterResources(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), true, gomock.Any()).
			Return(map[string]interfaces.PermissionResourceOps{
				"c1": {ResourceID: "c1"}, "c2": {ResourceID: "c2"}, "c3": {ResourceID: "c3"}, "c4": {ResourceID: "c4"}, "c5": {ResourceID: "c5"},
			}, nil)
		catalogs := []*interfaces.Catalog{{ID: "c2"}, {ID: "c3"}}
		mockCA.EXPECT().GetByIDs(gomock.Any(), []string{"c2", "c3"}).Return(catalogs, nil)
		mockCA.EXPECT().AttachListExtensions(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
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
		mockCA := mock_interfaces.NewMockCatalogAccess(ctrl)
		mockPS := mock_interfaces.NewMockPermissionService(ctrl)

		ids := []string{"c1", "c2"}
		mockCA.EXPECT().ListIDs(gomock.Any(), gomock.Any()).Return(ids, nil)
		mockCA.EXPECT().ListInternalIDs(gomock.Any()).Return([]string{}, nil)
		mockPS.EXPECT().FilterResources(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), true, gomock.Any()).
			Return(map[string]interfaces.PermissionResourceOps{
				"c1": {ResourceID: "c1"}, "c2": {ResourceID: "c2"},
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
		mockCA := mock_interfaces.NewMockCatalogAccess(ctrl)
		mockPS := mock_interfaces.NewMockPermissionService(ctrl)
		mockUMS := mock_interfaces.NewMockUserMgmtService(ctrl)

		ids := []string{"c1", "c2", "c3"}
		catalogs := []*interfaces.Catalog{{ID: "c1"}, {ID: "c3"}}
		mockCA.EXPECT().ListIDs(gomock.Any(), gomock.Any()).Return(ids, nil)
		mockCA.EXPECT().ListInternalIDs(gomock.Any()).Return([]string{}, nil)
		// 权限只返回 c1 和 c3，c2 被过滤
		mockPS.EXPECT().FilterResources(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), true, gomock.Any()).
			Return(map[string]interfaces.PermissionResourceOps{
				"c1": {ResourceID: "c1"}, "c3": {ResourceID: "c3"},
			}, nil)
		mockCA.EXPECT().GetByIDs(gomock.Any(), []string{"c1", "c3"}).Return(catalogs, nil)
		mockCA.EXPECT().AttachListExtensions(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
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
		mockCA := mock_interfaces.NewMockCatalogAccess(ctrl)
		mockCA.EXPECT().ListIDs(gomock.Any(), gomock.Any()).Return(nil, fmt.Errorf("db error"))

		cs := &catalogService{ca: mockCA}
		_, _, err := cs.List(context.Background(), interfaces.CatalogsQueryParams{})
		if err == nil {
			t.Fatal("expected error")
		}
	})
	t.Run("list internal catalog checked separately", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCA := mock_interfaces.NewMockCatalogAccess(ctrl)
		mockPS := mock_interfaces.NewMockPermissionService(ctrl)
		mockUMS := mock_interfaces.NewMockUserMgmtService(ctrl)

		ids := []string{"c1", "c2"}
		mockCA.EXPECT().ListIDs(gomock.Any(), gomock.Any()).Return(ids, nil)
		mockCA.EXPECT().ListInternalIDs(gomock.Any()).Return([]string{"c2"}, nil)
		// 普通目录按 catalog 类型校验
		mockPS.EXPECT().FilterResources(gomock.Any(), interfaces.AUTH_RESOURCE_TYPE_CATALOG,
			[]string{"c1"}, gomock.Any(), true, gomock.Any()).
			Return(map[string]interfaces.PermissionResourceOps{"c1": {ResourceID: "c1"}}, nil)
		// 内部目录按 internal_catalog 类型校验；数据管理员等业务角色无授权 → 被过滤
		mockPS.EXPECT().FilterResources(gomock.Any(), interfaces.AUTH_RESOURCE_TYPE_INTERNAL_CATALOG,
			[]string{"c2"}, gomock.Any(), true, gomock.Any()).
			Return(map[string]interfaces.PermissionResourceOps{}, nil)
		mockCA.EXPECT().GetByIDs(gomock.Any(), []string{"c1"}).Return([]*interfaces.Catalog{{ID: "c1"}}, nil)
		mockCA.EXPECT().AttachListExtensions(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
		mockUMS.EXPECT().GetAccountNames(gomock.Any(), gomock.Any()).Return(nil)

		cs := &catalogService{ca: mockCA, ps: mockPS, ums: mockUMS}
		result, total, err := cs.List(context.Background(), interfaces.CatalogsQueryParams{
			PaginationQueryParams: interfaces.PaginationQueryParams{Limit: -1},
		})
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
}

func TestCatalogServiceGetDeletionImpact(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	ra := mock_interfaces.NewMockResourceAccess(ctrl)
	ca := mock_interfaces.NewMockCatalogAccess(ctrl)
	ps := mock_interfaces.NewMockPermissionService(ctrl)
	bta := mock_interfaces.NewMockBuildTaskAccess(ctrl)
	dsa := mock_interfaces.NewMockDiscoverScheduleAccess(ctrl)
	dta := mock_interfaces.NewMockDiscoverTaskAccess(ctrl)
	suta := mock_interfaces.NewMockSemanticUnderstandingTaskAccess(ctrl)
	cs := &catalogService{ca: ca, ps: ps, ra: ra, bta: bta, dsa: dsa, dta: dta, suta: suta}

	ca.EXPECT().ListInternalIDs(gomock.Any()).Return(nil, nil)
	ps.EXPECT().FilterResources(gomock.Any(), interfaces.AUTH_RESOURCE_TYPE_CATALOG,
		[]string{"catalog-1"}, gomock.Any(), true, gomock.Any()).
		Return(map[string]interfaces.PermissionResourceOps{"catalog-1": {ResourceID: "catalog-1"}}, nil)
	ra.EXPECT().GetByCatalogID(gomock.Any(), "catalog-1").Return([]*interfaces.Resource{{ID: "resource-1"}}, nil)
	bta.EXPECT().InternalList(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, params interfaces.BuildTasksQueryParams) ([]*interfaces.BuildTask, int64, error) {
			if len(params.Statuses) == 0 {
				return nil, 3, nil
			}
			if slices.Equal(params.Statuses, []string{
				interfaces.BuildTaskStatusRunning,
				interfaces.BuildTaskStatusStopping,
			}) {
				return nil, 2, nil
			}
			assert.Equal(t, []string{
				interfaces.BuildTaskStatusPending,
				interfaces.BuildTaskStatusRunning,
				interfaces.BuildTaskStatusStopping,
			}, params.Statuses)
			return nil, 3, nil
		}).Times(3)
	dsa.EXPECT().List(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, params interfaces.DiscoverScheduleQueryParams) ([]*interfaces.DiscoverSchedule, int64, error) {
			if params.Enabled == nil {
				return nil, 2, nil
			}
			return nil, 1, nil
		}).Times(2)
	dta.EXPECT().List(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, params interfaces.DiscoverTaskQueryParams) ([]*interfaces.DiscoverTaskSummary, int64, error) {
			switch {
			case len(params.Statuses) == 0:
				return nil, 4, nil
			case slices.Equal(params.Statuses, []string{interfaces.DiscoverTaskStatusPending}):
				return nil, 1, nil
			case slices.Equal(params.Statuses, []string{interfaces.DiscoverTaskStatusRunning}):
				return nil, 2, nil
			default:
				t.Fatalf("unexpected statuses: %v", params.Statuses)
				return nil, 0, nil
			}
		}).Times(3)
	suta.EXPECT().List(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, params interfaces.SemanticUnderstandingTaskQueryParams) ([]*interfaces.SemanticUnderstandingTaskSummary, int64, error) {
			if len(params.Statuses) == 0 {
				return nil, 5, nil
			}
			if slices.Equal(params.Statuses, []string{interfaces.SemanticUnderstandingTaskStatusRunning}) {
				return nil, 1, nil
			}
			assert.Equal(t, interfaces.SemanticUnderstandingTaskActiveStatuses, params.Statuses)
			return nil, 2, nil
		}).Times(3)
	ra.EXPECT().CheckExistByCategories(gomock.Any(), "catalog-1", []string{
		interfaces.ResourceCategoryDataset,
		interfaces.ResourceCategoryLogicView,
	}).Return(false, nil)

	impact, err := cs.GetDeletionImpact(context.Background(), "catalog-1")
	require.NoError(t, err)
	assert.False(t, impact.CanDelete)
	assert.Equal(t, interfaces.CatalogDeletionTaskImpact{Active: 3, Total: 3}, impact.BuildTasks)
	assert.Equal(t, interfaces.CatalogDeletionScheduleImpact{Enabled: 1, Total: 2}, impact.DiscoverSchedules)
	assert.Equal(t, interfaces.CatalogDeletionTaskImpact{Active: 3, Total: 4}, impact.DiscoverTasks)
	assert.Equal(t, interfaces.CatalogDeletionTaskImpact{Active: 2, Total: 5}, impact.SemanticUnderstandingTasks)
}

func expectCatalogDeletionImpact(
	ctrl *gomock.Controller,
	ra *mock_interfaces.MockResourceAccess,
	bta *mock_interfaces.MockBuildTaskAccess,
	dsa *mock_interfaces.MockDiscoverScheduleAccess,
	dta *mock_interfaces.MockDiscoverTaskAccess,
	suta *mock_interfaces.MockSemanticUnderstandingTaskAccess,
	buildTasks []*interfaces.BuildTask,
	resourceBlocked bool,
	includeBuildCascade bool,
) {
	ra.EXPECT().GetByCatalogID(gomock.Any(), "c1").Return(nil, nil)
	listCalls := 3
	if includeBuildCascade {
		listCalls++
	}
	bta.EXPECT().InternalList(gomock.Any(), gomock.Any()).
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
	dsa.EXPECT().List(gomock.Any(), gomock.Any()).Return(nil, int64(0), nil).Times(2)
	dta.EXPECT().List(gomock.Any(), gomock.Any()).Return(nil, int64(0), nil).Times(3)
	suta.EXPECT().List(gomock.Any(), gomock.Any()).Return(nil, int64(0), nil).Times(3)
	ra.EXPECT().CheckExistByCategories(gomock.Any(), "c1", gomock.Any()).Return(resourceBlocked, nil)
}

func TestCatalogServiceDeleteByID(t *testing.T) {
	t.Run("retains tasks, cancels queued tasks, and deletes schedules", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		ca := mock_interfaces.NewMockCatalogAccess(ctrl)
		ps := mock_interfaces.NewMockPermissionService(ctrl)
		ra := mock_interfaces.NewMockResourceAccess(ctrl)
		hcss := mock_interfaces.NewMockCatalogHealthCheckScheduleService(ctrl)
		bta := mock_interfaces.NewMockBuildTaskAccess(ctrl)
		lim := mock_interfaces.NewMockLocalIndexManager(ctrl)
		dsa := mock_interfaces.NewMockDiscoverScheduleAccess(ctrl)
		dta := mock_interfaces.NewMockDiscoverTaskAccess(ctrl)
		suta := mock_interfaces.NewMockSemanticUnderstandingTaskAccess(ctrl)
		db, sqlMock, err := sqlmock.New()
		require.NoError(t, err)
		t.Cleanup(func() { _ = db.Close() })

		buildTasks := []*interfaces.BuildTask{
			{ID: "completed", ResourceID: "r1", Status: interfaces.BuildTaskStatusCompleted},
			{ID: "pending", ResourceID: "r2", Status: interfaces.BuildTaskStatusPending},
		}
		ca.EXPECT().ListInternalIDs(gomock.Any()).Return(nil, nil)
		ps.EXPECT().FilterResources(gomock.Any(), interfaces.AUTH_RESOURCE_TYPE_CATALOG,
			[]string{"c1"}, gomock.Any(), true, gomock.Any()).
			Return(map[string]interfaces.PermissionResourceOps{"c1": {ResourceID: "c1"}}, nil)
		expectCatalogDeletionImpact(ctrl, ra, bta, dsa, dta, suta, buildTasks, false, true)
		lim.EXPECT().DeleteIndex(gomock.Any(), interfaces.BuildIndexName("r1", "completed")).Return(nil)
		lim.EXPECT().DeleteIndex(gomock.Any(), interfaces.BuildIndexName("r2", "pending")).Return(nil)
		sqlMock.ExpectBegin()
		bta.EXPECT().UpdateStatus(gomock.Any(), gomock.Any(), "pending", gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, _ *sql.Tx, _ string, update interfaces.BuildTaskUpdate, _ ...string) (bool, error) {
				require.Equal(t, interfaces.BuildTaskStatusCancelled, *update.Status)
				require.Equal(t, catalogDeletedTaskMessage, *update.ErrorMsg)
				return true, nil
			})
		dta.EXPECT().MarkCancelledByCatalogID(gomock.Any(), gomock.Any(), "c1", catalogDeletedTaskMessage, gomock.Any()).Return(nil)
		suta.EXPECT().MarkCancelledByCatalogID(gomock.Any(), gomock.Any(), "c1", catalogDeletedTaskMessage, gomock.Any()).Return(nil)
		dsa.EXPECT().DeleteByCatalogID(gomock.Any(), gomock.Any(), "c1").Return(nil)
		hcss.EXPECT().DeleteByCatalogID(gomock.Any(), gomock.Any(), "c1").Return(nil)
		ra.EXPECT().DeleteByCatalogID(gomock.Any(), gomock.Any(), "c1").Return(nil)
		ca.EXPECT().DeleteByID(gomock.Any(), gomock.Any(), "c1").Return(nil)
		sqlMock.ExpectCommit()
		ps.EXPECT().DeleteResources(gomock.Any(), interfaces.AUTH_RESOURCE_TYPE_CATALOG, []string{"c1"}).Return(nil)

		cs := &catalogService{db: db, ca: ca, ps: ps, ra: ra, bta: bta, lim: lim, dsa: dsa, dta: dta, suta: suta, hcss: hcss}
		require.NoError(t, cs.DeleteByID(context.Background(), "c1"))
		require.NoError(t, sqlMock.ExpectationsWereMet())
	})

	t.Run("rejects protected resources using the shared impact", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		ca := mock_interfaces.NewMockCatalogAccess(ctrl)
		ps := mock_interfaces.NewMockPermissionService(ctrl)
		ra := mock_interfaces.NewMockResourceAccess(ctrl)
		bta := mock_interfaces.NewMockBuildTaskAccess(ctrl)
		dsa := mock_interfaces.NewMockDiscoverScheduleAccess(ctrl)
		dta := mock_interfaces.NewMockDiscoverTaskAccess(ctrl)
		suta := mock_interfaces.NewMockSemanticUnderstandingTaskAccess(ctrl)
		ca.EXPECT().ListInternalIDs(gomock.Any()).Return(nil, nil)
		ps.EXPECT().FilterResources(gomock.Any(), interfaces.AUTH_RESOURCE_TYPE_CATALOG,
			[]string{"c1"}, gomock.Any(), true, gomock.Any()).
			Return(map[string]interfaces.PermissionResourceOps{"c1": {ResourceID: "c1"}}, nil)
		expectCatalogDeletionImpact(ctrl, ra, bta, dsa, dta, suta, nil, true, false)

		cs := &catalogService{ca: ca, ps: ps, ra: ra, bta: bta, dsa: dsa, dta: dta, suta: suta}
		err := cs.DeleteByID(context.Background(), "c1")
		var httpErr *rest.HTTPError
		require.ErrorAs(t, err, &httpErr)
		assert.Equal(t, http.StatusConflict, httpErr.HTTPCode)
	})
}

func TestCatalogServiceGetByID(t *testing.T) {
	t.Run("catalog get by ids2 sinternal bypass", func(t *testing.T) {
		cs, ca, _, ums := newS2SCatalogService(t)
		ca.EXPECT().GetByID(gomock.Any(), "c1").
			Return(&interfaces.Catalog{ID: "c1", Internal: true}, nil)
		ums.EXPECT().GetAccountNames(gomock.Any(), gomock.Any()).Return(nil)

		ctx := interfaces.WithS2SInternalAccess(context.Background())
		cat, err := cs.GetByID(ctx, "c1", false)
		if err != nil {
			t.Fatalf("internal catalog S2S access should pass, got error: %v", err)
		}
		if cat == nil || len(cat.Operations) == 0 {
			t.Fatalf("expected operations to be filled, got %+v", cat)
		}
	})
	t.Run("catalog get by idinternal no marker forbidden", func(t *testing.T) {
		cs, ca, ps, _ := newS2SCatalogService(t)
		ca.EXPECT().GetByID(gomock.Any(), "c1").
			Return(&interfaces.Catalog{ID: "c1", Internal: true}, nil)
		ps.EXPECT().FilterResources(gomock.Any(), interfaces.AUTH_RESOURCE_TYPE_INTERNAL_CATALOG,
			gomock.Any(), gomock.Any(), true, gomock.Any()).
			Return(map[string]interfaces.PermissionResourceOps{}, nil)

		_, err := cs.GetByID(context.Background(), "c1", false)
		if err == nil {
			t.Fatalf("internal catalog without S2S marker should be forbidden")
		}
	})
	t.Run("catalog get by idnon internal with marker still authz", func(t *testing.T) {
		cs, ca, ps, _ := newS2SCatalogService(t)
		ca.EXPECT().GetByID(gomock.Any(), "c1").
			Return(&interfaces.Catalog{ID: "c1", Internal: false}, nil)
		ps.EXPECT().FilterResources(gomock.Any(), interfaces.AUTH_RESOURCE_TYPE_CATALOG,
			gomock.Any(), gomock.Any(), true, gomock.Any()).
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

// 删 catalog 应级联清掉其下资源的构建任务 + OpenSearch 索引，不留孤儿。
// 删 catalog 时其下有运行中任务 → 级联拒绝，catalog/资源都不删。
// ===== Internal catalog（系统内部目录） =====

func newS2SCatalogService(t *testing.T) (
	*catalogService, *mock_interfaces.MockCatalogAccess, *mock_interfaces.MockPermissionService, *mock_interfaces.MockUserMgmtService) {
	t.Helper()

	ctrl := gomock.NewController(t)
	ca := mock_interfaces.NewMockCatalogAccess(ctrl)
	ps := mock_interfaces.NewMockPermissionService(ctrl)
	ums := mock_interfaces.NewMockUserMgmtService(ctrl)
	cs := &catalogService{ca: ca, ps: ps, ums: ums}
	return cs, ca, ps, ums
}
