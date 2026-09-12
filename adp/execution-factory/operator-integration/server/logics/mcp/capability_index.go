// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package mcp

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
)

// MCP tools are indexed from here rather than from the capability index package, because listing
// them is not a table read: an MCP Server's tools live on the server, and this package owns the
// client that can ask. Putting the sync here also keeps the import graph acyclic — the capability
// index package is imported by the tool box package, which this package already imports.

// syncMCPCapabilities makes the capability index match what one MCP Server currently exposes.
//
// It lists the server live and reconciles the whole server at once rather than taking a caller's
// tool list, so it is correct after any change: a tool added, renamed, removed, or a server whose
// remote definition drifted without anyone here being told.
func (s *mcpServiceImpl) syncMCPCapabilities(ctx context.Context, mcpID string) error {
	mcpID = strings.TrimSpace(mcpID)
	if mcpID == "" || s.CapabilityIndex == nil {
		return nil
	}

	config, err := s.DBMCPServerConfig.SelectByID(ctx, nil, mcpID)
	if err != nil {
		return err
	}
	if config == nil {
		// The server is gone: nothing to describe the tools with, and nothing should remain.
		return s.CapabilityIndex.DeleteOwner(ctx, interfaces.CapabilityTypeMCPTool, mcpID)
	}
	// Only a published server's tools are callable, so only a published server is indexed
	// (#1443). Anything else — a draft, an offline server — is purged rather than skipped:
	// skipping would leave the documents written while it was published, and the next full
	// pass would keep them alive for as long as the server existed. The remote listing is not
	// attempted for such a server; there is nothing to write.
	if config.Status != string(interfaces.BizStatusPublished) {
		return s.CapabilityIndex.DeleteOwner(ctx, interfaces.CapabilityTypeMCPTool, mcpID)
	}

	resp, err := s.GetMCPTools(ctx, &interfaces.MCPProxyToolListRequest{MCPID: mcpID})
	if err != nil {
		return err
	}

	desired := make(map[string]*interfaces.CapabilityDocument)
	if resp != nil {
		for _, tool := range resp.Tools {
			name := strings.TrimSpace(tool.Name)
			if name == "" {
				continue
			}
			desired[name] = &interfaces.CapabilityDocument{
				CapabilityRef: interfaces.CapabilityRef{
					CapabilityType: interfaces.CapabilityTypeMCPTool,
					OwnerID:        mcpID,
					CapabilityID:   name,
				},
				Name:        name,
				Description: tool.Description,
				// A remote tool carries no audit fields of its own; the server's stand in.
				CreateUser: config.CreateUser,
				CreateTime: config.CreateTime,
				UpdateUser: config.UpdateUser,
				UpdateTime: config.UpdateTime,
			}
		}
	}

	indexed, err := s.CapabilityIndex.ListIndexedByOwner(ctx, interfaces.CapabilityTypeMCPTool, mcpID)
	if err != nil {
		return err
	}

	var errs []error
	for _, doc := range desired {
		if err := s.CapabilityIndex.UpsertCapability(ctx, doc); err != nil {
			errs = append(errs, err)
		}
	}
	for _, entry := range indexed {
		if _, ok := desired[entry.CapabilityID]; ok {
			continue
		}
		if err := s.CapabilityIndex.DeleteCapability(ctx, entry.CapabilityRef); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// syncMCPCapabilitiesAsync runs the sync off the request path.
//
// Listing a remote server and embedding each tool does not belong in the latency of registering
// one. Cancellation is dropped but the context values are kept: the request's context is done as
// soon as its response is written, and the downstream clients read their headers from there.
func (s *mcpServiceImpl) syncMCPCapabilitiesAsync(ctx context.Context, mcpID string) {
	if s.CapabilityIndex == nil {
		return
	}
	detached := context.WithoutCancel(ctx)
	go func() {
		ctx, cancel := context.WithTimeout(detached, mcpCapabilitySyncTimeout)
		defer cancel()
		ctx, _ = oteltrace.StartInternalSpan(ctx)
		var err error
		defer func() { oteltrace.EndSpan(ctx, err) }()
		if err = s.syncMCPCapabilities(ctx, mcpID); err != nil {
			// Not fatal to the write that triggered it. An unreachable server leaves the index as
			// it was, and the tools stay searchable until something says otherwise.
			s.logger.WithContext(ctx).Warnf("sync MCP tools into capability index failed, mcp_id=%s, err=%v", mcpID, err)
		}
	}()
}

// ReconcileCapabilityIndexAsync is the importer's hook: an imported (and possibly published)
// server never passes through the register/update syncs, so every server is re-listed once the
// import has committed (#1483).
func (s *mcpServiceImpl) ReconcileCapabilityIndexAsync(ctx context.Context) {
	if s.CapabilityIndex == nil {
		return
	}
	detached := context.WithoutCancel(ctx)
	go s.reconcileCapabilities(detached)
}

// forgetMCPCapabilitiesAsync removes a deleted server's tools from the index.
func (s *mcpServiceImpl) forgetMCPCapabilitiesAsync(ctx context.Context, mcpID string) {
	if s.CapabilityIndex == nil || strings.TrimSpace(mcpID) == "" {
		return
	}
	detached := context.WithoutCancel(ctx)
	go func() {
		ctx, cancel := context.WithTimeout(detached, mcpCapabilitySyncTimeout)
		defer cancel()
		if err := s.CapabilityIndex.DeleteOwner(ctx, interfaces.CapabilityTypeMCPTool, mcpID); err != nil {
			s.logger.WithContext(ctx).Warnf("purge MCP tools from capability index failed, mcp_id=%s, err=%v", mcpID, err)
		}
	}()
}

// mcpCapabilitySyncTimeout bounds one detached sync. It talks to a remote server and then embeds
// every tool it returned, so it needs room, but not unbounded room.
const mcpCapabilitySyncTimeout = 2 * time.Minute

// StartCapabilityReconciler keeps the MCP half of the capability index honest over time.
//
// The write hooks above cover changes made through this service. They cannot cover the change that
// matters most for a remote server: its own tool list moving underneath us, which nothing here is
// told about. This pass re-lists every registered server on an interval.
//
// A server that cannot be reached keeps the tools it already has indexed. Unreachable is not
// empty, and treating it as empty would drop a network's mounted tools out of search until the
// server came back.
func StartCapabilityReconciler(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		return
	}
	service, ok := NewMCPServiceImpl().(*mcpServiceImpl)
	if !ok {
		return
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			service.reconcileCapabilities(ctx)
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

// reconcileCapabilities re-lists every registered MCP Server and syncs each one.
func (s *mcpServiceImpl) reconcileCapabilities(ctx context.Context) {
	if s.CapabilityIndex == nil {
		return
	}
	if err := s.CapabilityIndex.EnsureInitialized(ctx); err != nil {
		s.logger.WithContext(ctx).Warnf("capability index not ready, skipping MCP reconcile: %v", err)
		return
	}
	servers, err := s.DBMCPServerConfig.SelectListPage(ctx, nil, map[string]interface{}{"all": true}, nil, nil)
	if err != nil {
		s.logger.WithContext(ctx).Warnf("list MCP servers for capability reconcile failed: %v", err)
		return
	}
	for _, server := range servers {
		if server == nil || strings.TrimSpace(server.MCPID) == "" {
			continue
		}
		if err := s.syncMCPCapabilities(ctx, server.MCPID); err != nil {
			s.logger.WithContext(ctx).Warnf("reconcile MCP capabilities failed, mcp_id=%s, err=%v", server.MCPID, err)
		}
	}
	s.logger.WithContext(ctx).Infof("MCP capability reconcile finished, servers=%d", len(servers))
}
