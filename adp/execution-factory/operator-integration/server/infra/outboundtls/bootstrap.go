// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package outboundtls

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

type Registration struct{ Scope, URL string }

// Forget accompanies the authorized deletion transaction, so a later imported
// object cannot inherit a deleted registration's explicit skip exception.
func Forget(ctx context.Context, scope string, store Store) error {
	if Default() == nil {
		return nil
	}
	remover, ok := store.(interface {
		Remove(context.Context, string) error
	})
	if !ok {
		return fault("TLS_TRUST_UNAVAILABLE: transactional trust deletion is unavailable")
	}
	return remover.Remove(ctx, scope)
}

type RegistrationDB interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

// BootstrapQuery releases the registration cursor before any enrollment writes,
// so it also works with a single database connection.
func (m *Manager) BootstrapQuery(ctx context.Context, db RegistrationDB, query string) error {
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return err
	}
	var registrations []Registration
	for rows.Next() {
		var item Registration
		if err = rows.Scan(&item.Scope, &item.URL); err != nil {
			break
		}
		registrations = append(registrations, item)
	}
	if err == nil {
		err = rows.Err()
	}
	closeErr := rows.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return m.Bootstrap(ctx, registrations)
}

// Bootstrap runs before serving traffic, after the ordinary schema migrator.
// It only fills missing trust decisions. Existing pins are never refreshed by a
// restart, repeated upgrade, or a failed verification of a business request.
func (m *Manager) Bootstrap(ctx context.Context, registrations []Registration) error {
	observed := make(map[string]Policy)
	for _, registration := range registrations {
		if !strings.HasPrefix(strings.ToLower(registration.URL), "https://") {
			continue
		}
		origin, err := Origin(registration.URL)
		if err != nil {
			return err
		}
		_, ok, err := m.store.Get(ctx, registration.Scope, origin)
		if err != nil {
			return fmt.Errorf("TLS_UPGRADE_FAILED (%s): %w", registration.Scope, err)
		}
		if ok {
			continue
		}
		p, found := observed[origin]
		if !found {
			p, err = m.Discover(ctx, origin)
			if err != nil {
				return fmt.Errorf("TLS_UPGRADE_FAILED (%s): %w", registration.Scope, err)
			}
			observed[origin] = p
		}
		if err := m.store.Create(ctx, registration.Scope, origin, p); err != nil {
			return fmt.Errorf("TLS_UPGRADE_FAILED (%s): %w", registration.Scope, err)
		}
	}
	return nil
}

func Read(ctx context.Context, scope, raw string) (*Policy, error) {
	if !strings.HasPrefix(raw, "https://") {
		return nil, nil
	}
	p, err := Default().Get(ctx, scope, raw)
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// Policies exposes saved authorities through the registration's existing,
// authorized detail API, including historical and fallback metadata endpoints.
func Policies(ctx context.Context, scope string) (map[string]Policy, error) {
	m := Default()
	if m == nil {
		return nil, nil
	}
	lister, ok := m.store.(interface {
		List(context.Context, string) (map[string]Policy, error)
	})
	if !ok {
		return nil, fault("TLS_TRUST_UNAVAILABLE: persistent certificate policies cannot be read")
	}
	result, err := lister.List(ctx, scope)
	if err != nil {
		return nil, fault("TLS_TRUST_UNAVAILABLE: persistent certificate policies cannot be read")
	}
	return result, nil
}
