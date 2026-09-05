// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package managedproxy_test

import (
	"errors"
	"sync"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/database"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/managedproxy"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
)

func managedProxyTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := database.Migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func TestCreateIsIdempotentAndBuildsNonCredentialedApp(t *testing.T) {
	db := managedProxyTestDB(t)
	service := managedproxy.New(db)
	req := managedproxy.CreateRequest{
		ManagedResourceType: managedproxy.ResourceKnowledgeNetwork,
		ManagedResourceID:   "kn-1",
		Name:                "Supply chain proxy",
	}

	first, created, err := service.Create(t.Context(), req)
	if err != nil || !created {
		t.Fatalf("first Create() = (%+v, %v, %v), want created", first, created, err)
	}
	second, created, err := service.Create(t.Context(), req)
	if err != nil || created {
		t.Fatalf("replayed Create() = (%+v, %v, %v), want existing", second, created, err)
	}
	if second.ProxyAccountID != first.ProxyAccountID {
		t.Fatalf("replay proxy id = %q, want %q", second.ProxyAccountID, first.ProxyAccountID)
	}
	if first.AccountType != string(model.AccountTypeApp) || first.ManagedBy != managedproxy.ManagerBKN ||
		first.LoginEnabled || first.CredentialIssuanceEnabled || !first.Enabled || first.Version != 1 {
		t.Fatalf("unexpected proxy contract: %+v", first)
	}

	var user model.User
	if err := db.First(&user, "id = ?", first.ProxyAccountID).Error; err != nil {
		t.Fatalf("load proxy user: %v", err)
	}
	if user.PasswordHash != "" || user.MustChangePassword || user.AccountType != model.AccountTypeApp {
		t.Fatalf("proxy user can carry credentials: %+v", user)
	}
	var mappings int64
	if err := db.Model(&model.ManagedProxyAccount{}).Count(&mappings).Error; err != nil {
		t.Fatalf("count mappings: %v", err)
	}
	if mappings != 1 {
		t.Fatalf("mapping count = %d, want 1", mappings)
	}
}

func TestConcurrentCreateReturnsOneProxyWithoutOrphanAccounts(t *testing.T) {
	db := managedProxyTestDB(t)
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	// Keep the in-memory SQLite database on one connection. The calls still
	// enter the service concurrently; production databases additionally use the
	// composite unique key when requests reach the database simultaneously.
	sqlDB.SetMaxOpenConns(1)
	service := managedproxy.New(db)
	req := managedproxy.CreateRequest{
		ManagedResourceType: managedproxy.ResourceKnowledgeNetwork,
		ManagedResourceID:   "kn-concurrent",
	}

	const callers = 16
	results := make(chan *managedproxy.Account, callers)
	errs := make(chan error, callers)
	created := make(chan bool, callers)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			account, wasCreated, err := service.Create(t.Context(), req)
			results <- account
			created <- wasCreated
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	close(created)
	close(errs)

	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent Create() error = %v", err)
		}
	}
	createdCount := 0
	for value := range created {
		if value {
			createdCount++
		}
	}
	if createdCount != 1 {
		t.Fatalf("created responses = %d, want 1", createdCount)
	}
	proxyID := ""
	for account := range results {
		if account == nil {
			t.Fatal("concurrent Create() returned nil account")
		}
		if proxyID == "" {
			proxyID = account.ProxyAccountID
		}
		if account.ProxyAccountID != proxyID {
			t.Fatalf("concurrent proxy id = %q, want %q", account.ProxyAccountID, proxyID)
		}
	}
	var users, mappings int64
	if err := db.Model(&model.User{}).Where("account LIKE ?", "bkn-proxy-%").Count(&users).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.ManagedProxyAccount{}).Count(&mappings).Error; err != nil {
		t.Fatal(err)
	}
	if users != 1 || mappings != 1 {
		t.Fatalf("concurrent create left users=%d mappings=%d, want 1/1", users, mappings)
	}
}

func TestCreateRejectsArbitraryManagedResource(t *testing.T) {
	service := managedproxy.New(managedProxyTestDB(t))
	_, _, err := service.Create(t.Context(), managedproxy.CreateRequest{
		ManagedResourceType: "tool_box",
		ManagedResourceID:   "box-1",
	})
	if !errors.Is(err, managedproxy.ErrInvalidManagedResource) {
		t.Fatalf("Create() error = %v, want ErrInvalidManagedResource", err)
	}
}

func TestDisableAndArchiveAreIdempotent(t *testing.T) {
	service := managedproxy.New(managedProxyTestDB(t))
	created, _, err := service.Create(t.Context(), managedproxy.CreateRequest{
		ManagedResourceType: managedproxy.ResourceKnowledgeNetwork,
		ManagedResourceID:   "kn-lifecycle",
	})
	if err != nil {
		t.Fatalf("Create(): %v", err)
	}

	disabled, err := service.Disable(t.Context(), created.ProxyAccountID)
	if err != nil {
		t.Fatalf("Disable(): %v", err)
	}
	if disabled.Enabled || disabled.LifecycleStatus != managedproxy.StatusDisabling || disabled.Version != 2 {
		t.Fatalf("disabled = %+v", disabled)
	}
	disabledAgain, err := service.Disable(t.Context(), created.ProxyAccountID)
	if err != nil || disabledAgain.Version != 2 {
		t.Fatalf("replayed Disable() = (%+v, %v), want version 2", disabledAgain, err)
	}

	archived, err := service.Archive(t.Context(), created.ProxyAccountID)
	if err != nil {
		t.Fatalf("Archive(): %v", err)
	}
	if archived.Enabled || archived.LifecycleStatus != managedproxy.StatusArchived || archived.Version != 3 {
		t.Fatalf("archived = %+v", archived)
	}
	archivedAgain, err := service.Archive(t.Context(), created.ProxyAccountID)
	if err != nil || archivedAgain.Version != 3 {
		t.Fatalf("replayed Archive() = (%+v, %v), want version 3", archivedAgain, err)
	}
}

func TestRestoreArchivedProxyForCreationRetry(t *testing.T) {
	service := managedproxy.New(managedProxyTestDB(t))
	created, _, err := service.Create(t.Context(), managedproxy.CreateRequest{
		ManagedResourceType: managedproxy.ResourceKnowledgeNetwork,
		ManagedResourceID:   "kn-retry",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Archive(t.Context(), created.ProxyAccountID); err != nil {
		t.Fatal(err)
	}

	restored, err := service.Restore(t.Context(), created.ProxyAccountID)
	if err != nil {
		t.Fatal(err)
	}
	if restored.ProxyAccountID != created.ProxyAccountID || !restored.Enabled ||
		restored.LifecycleStatus != managedproxy.StatusActive || restored.Version != 3 {
		t.Fatalf("restored proxy = %+v", restored)
	}
	replayed, err := service.Restore(t.Context(), created.ProxyAccountID)
	if err != nil || replayed.Version != restored.Version {
		t.Fatalf("replayed Restore() = (%+v, %v)", replayed, err)
	}
}

func TestRestoreRejectsDisablingProxy(t *testing.T) {
	service := managedproxy.New(managedProxyTestDB(t))
	created, _, err := service.Create(t.Context(), managedproxy.CreateRequest{
		ManagedResourceType: managedproxy.ResourceKnowledgeNetwork,
		ManagedResourceID:   "kn-deleting",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Disable(t.Context(), created.ProxyAccountID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Restore(t.Context(), created.ProxyAccountID); !errors.Is(err, managedproxy.ErrInvalidLifecycle) {
		t.Fatalf("Restore() error = %v, want ErrInvalidLifecycle", err)
	}
}

func TestGetFailsClosedForCredentialedManagedIdentity(t *testing.T) {
	db := managedProxyTestDB(t)
	service := managedproxy.New(db)
	account, _, err := service.Create(t.Context(), managedproxy.CreateRequest{
		ManagedResourceType: managedproxy.ResourceKnowledgeNetwork,
		ManagedResourceID:   "kn-corrupt",
	})
	if err != nil {
		t.Fatalf("Create(): %v", err)
	}
	if err := db.Model(&model.User{}).Where("id = ?", account.ProxyAccountID).
		Update("password_hash", "unexpected").Error; err != nil {
		t.Fatalf("corrupt user: %v", err)
	}
	_, err = service.Get(t.Context(), account.ProxyAccountID)
	if !errors.Is(err, managedproxy.ErrInconsistentAccount) {
		t.Fatalf("Get() error = %v, want ErrInconsistentAccount", err)
	}
}

func TestGetFailsClosedForLifecycleAccountStateMismatch(t *testing.T) {
	db := managedProxyTestDB(t)
	service := managedproxy.New(db)
	account, _, err := service.Create(t.Context(), managedproxy.CreateRequest{
		ManagedResourceType: managedproxy.ResourceKnowledgeNetwork,
		ManagedResourceID:   "kn-state-mismatch",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.ManagedProxyAccount{}).Where("proxy_account_id = ?", account.ProxyAccountID).
		Update("lifecycle_status", managedproxy.StatusArchived).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := service.Get(t.Context(), account.ProxyAccountID); !errors.Is(err, managedproxy.ErrInconsistentAccount) {
		t.Fatalf("Get() error = %v, want ErrInconsistentAccount", err)
	}
}

func TestDisableFailsClosedWithoutAdvancingMappingWhenUserIsMissing(t *testing.T) {
	db := managedProxyTestDB(t)
	service := managedproxy.New(db)
	account, _, err := service.Create(t.Context(), managedproxy.CreateRequest{
		ManagedResourceType: managedproxy.ResourceKnowledgeNetwork,
		ManagedResourceID:   "kn-missing-user",
	})
	if err != nil {
		t.Fatalf("Create(): %v", err)
	}
	if err := db.Where("id = ?", account.ProxyAccountID).Delete(&model.User{}).Error; err != nil {
		t.Fatalf("delete proxy user: %v", err)
	}
	if _, err := service.Disable(t.Context(), account.ProxyAccountID); !errors.Is(err, managedproxy.ErrInconsistentAccount) {
		t.Fatalf("Disable() error = %v, want ErrInconsistentAccount", err)
	}
	var mapping model.ManagedProxyAccount
	if err := db.First(&mapping, "proxy_account_id = ?", account.ProxyAccountID).Error; err != nil {
		t.Fatal(err)
	}
	if mapping.LifecycleStatus != managedproxy.StatusActive || mapping.Version != 1 {
		t.Fatalf("failed transition mutated mapping: %+v", mapping)
	}
}
