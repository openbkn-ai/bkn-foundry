// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package service

import (
	"context"
	"errors"
	"oss-gateway/internal/model"
	"oss-gateway/pkg/outboundtls"
	"strings"
)

type trustRegistrationRepository interface {
	SaveWithTrust(context.Context, *model.StorageConfig, bool, func(outboundtls.Store) error) error
}

func (s *storageService) saveWithTrust(ctx context.Context, storage *model.StorageConfig, create bool, external, internal *outboundtls.Change) error {
	needsTrust := strings.HasPrefix(storage.Endpoint, "https://") || strings.HasPrefix(storage.InternalEndpoint, "https://") || external != nil || internal != nil
	if !needsTrust {
		if create {
			return s.repo.Create(ctx, storage)
		}
		return s.repo.Update(ctx, storage)
	}
	repo, ok := s.repo.(trustRegistrationRepository)
	if !ok {
		return errors.New("TLS_TRUST_UNAVAILABLE: repository does not support transactional trust")
	}
	return repo.SaveWithTrust(ctx, storage, create, func(store outboundtls.Store) error {
		if err := outboundtls.Apply(ctx, "storage:"+storage.StorageID, storage.Endpoint, external, store); err != nil {
			return err
		}
		return outboundtls.Apply(ctx, "storage:"+storage.StorageID, storage.InternalEndpoint, internal, store)
	})
}
