package sandbox

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/drivenadapters"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/config"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/telemetry"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/utils"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
)

const (
	sessionIDPrefix           = "sess_aoi_"
	defaultMaxSessions        = 3
	defaultMaxConcurrentTasks = 100
	defaultActiveSessions     = 1
	defaultContextTimeout     = 30 * time.Second
	// Maximum number of retries.
	maxRetryCount = 3
	// Session health check interval.
	sessionStatusRunningCheckInterval = time.Second
	// Waiting for session to run timed out.
	waitSessionRunningTimeout = 30 * time.Second
	// Background worker interval.
	backgroundWorkerInterval = time.Minute
)

// Current environment dependency information.
type DependenciesInfo struct {
	Dependencies []*interfaces.DependencyInfo `json:"dependencies"`
	SessionID    string                       `json:"session_id"`
}

// SessionPool session pool interface.
type SessionPool interface {
	ExecuteCode(ctx context.Context, req *interfaces.ExecuteCodeReq) (*interfaces.ExecuteCodeResp, error)
	// Get the list of dependent libraries.
	GetDependencies(ctx context.Context) (resp *DependenciesInfo, err error)
	// Snapshot returns read-only pool status for management and diagnostics.
	Snapshot() PoolSnapshot
	// Get available sessions.
	AcquireSession(ctx context.Context) (sessionID string, err error)
	// Return session.
	ReleaseSession(sessionID string)
}

type sessionItem struct {
	ID               string
	Dependencies     []*interfaces.DependencyInfo
	RunningTasks     int
	LastUsedAt       time.Time
	ExecutionContext map[string]any
}

type sessionPoolImpl struct {
	client             interfaces.SandBoxControlPlane
	sessions           map[string]*sessionItem // key: sessionID
	mu                 sync.Mutex
	maxSessions        int
	maxConcurrentTasks int
	activeSessions     int
	logger             interfaces.Logger
	stopCh             chan struct{}
	templateID         string
	reqConfig          config.SessionResourcesConfig
}

var (
	poolInstance *sessionPoolImpl
	poolOnce     sync.Once
)

// GetSessionPool Gets the session pool instance.
func GetSessionPool() SessionPool {
	poolOnce.Do(func() {
		conf := config.NewConfigLoader()
		client := drivenadapters.NewSandBoxControlPlaneClient()
		maxConcurrentTasks := conf.SandboxControlPlane.MaxConcurrentTasks
		if maxConcurrentTasks <= 0 {
			maxConcurrentTasks = defaultMaxConcurrentTasks
		}
		maxSessions := conf.SandboxControlPlane.MaxSessions
		if maxSessions <= 0 {
			maxSessions = defaultMaxSessions
		}
		activeSessions := conf.SandboxControlPlane.ActiveSessions
		if activeSessions <= 0 {
			activeSessions = defaultActiveSessions
		} else if activeSessions > maxSessions {
			activeSessions = maxSessions
		}

		poolInstance = &sessionPoolImpl{
			client:             client,
			sessions:           make(map[string]*sessionItem),
			maxSessions:        maxSessions,
			maxConcurrentTasks: maxConcurrentTasks,
			activeSessions:     activeSessions,
			logger:             conf.GetLogger(),
			stopCh:             make(chan struct{}),
			templateID:         conf.SandboxControlPlane.TemplateID,
			reqConfig:          conf.SandboxControlPlane.SessionResources,
		}
		// Print configuration information.
		poolInstance.logger.Infof("SessionPool initialized with maxSessions: %d, maxConcurrentTasks: %d, activeSessions: %d, templateID: %s, sessionResources: %v",
			poolInstance.maxSessions, poolInstance.maxConcurrentTasks, poolInstance.activeSessions, poolInstance.templateID, poolInstance.reqConfig)

		// Initialization: Synchronize existing deterministic sessions from the control plane and top up the number of activeSessions.
		poolInstance.initSessions()

		// Start background management tasks: health check and idle shrink/warm-up.
		go poolInstance.backgroundWorker()
	})
	return poolInstance
}

func (p *sessionPoolImpl) GetDependencies(ctx context.Context) (resp *DependenciesInfo, err error) {
	sessionID, err := p.AcquireSession(ctx)
	if err != nil {
		return nil, err
	}
	defer p.ReleaseSession(sessionID)

	exists, detail, err := p.querySessionAndCache(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, fmt.Errorf("session %s not found", sessionID)
	}

	var deps []*interfaces.DependencyInfo
	if detail != nil && detail.InstalledDependencies != nil {
		deps = detail.InstalledDependencies
	} else if item, ok := p.getSessionItem(sessionID); ok && item.Dependencies != nil {
		deps = item.Dependencies
	}
	resp = &DependenciesInfo{
		SessionID:    sessionID,
		Dependencies: deps,
	}
	return resp, nil
}

// ExecuteCode execute code.
func (p *sessionPoolImpl) ExecuteCode(ctx context.Context, req *interfaces.ExecuteCodeReq) (resp *interfaces.ExecuteCodeResp, err error) {
	// record observable.
	ctx, _ = oteltrace.StartInternalSpan(ctx)
	defer oteltrace.EndSpan(ctx, err)
	telemetry.SetSpanAttributes(ctx, map[string]interface{}{
		"language": req.Language,
		"timeout":  req.Timeout,
		"code":     req.Code,
		"event":    req.Event,
	})
	sessionID, err := p.acquireSessionWithEnv(ctx, req.EnvVars)
	if err != nil {
		return nil, err
	}
	p.recordExecutionContext(sessionID, req.EnvVars)
	defer p.ReleaseSession(sessionID)
	// Install dependent libraries.
	if len(req.Dependencies) > 0 && req.PythonPackageIndexURL != "" {
		detail, err := p.client.InstallPythonDependencies(ctx, sessionID, &interfaces.InstallDependenciesReq{
			Dependencies:          req.Dependencies,
			PythonPackageIndexURL: req.PythonPackageIndexURL,
		})
		if err != nil {
			p.logger.WithContext(ctx).Errorf("InstallPythonDependencies failed for session %s: %v", sessionID, err)
			err = errors.NewHTTPError(ctx, http.StatusInternalServerError, errors.ErrExtPypiRepoUnavailable, map[string]any{
				"error":            err.Error(),
				"session_id":       sessionID,
				"request_params":   utils.ObjectToJSON(req),
				"dependencies":     utils.ObjectToJSON(req.Dependencies),
				"dependencies_url": req.PythonPackageIndexURL,
			})
			return nil, err
		}
		p.updateSessionDependencies(sessionID, detail)
	}
	resp, err = p.client.ExecuteCodeSync(ctx, sessionID, req)
	if err != nil {
		p.logger.WithContext(ctx).Errorf("ExecuteCodeSync failed for session %s: %v", sessionID, err)
		return nil, err
	}
	return resp, nil
}

// AcquireSession Get available sessions.
func (p *sessionPoolImpl) AcquireSession(ctx context.Context) (sessionID string, err error) {
	return p.acquireSession(ctx, maxRetryCount)
}

func (p *sessionPoolImpl) acquireSessionWithEnv(ctx context.Context, envVars map[string]any) (sessionID string, err error) {
	return p.acquireSessionWithOptions(ctx, maxRetryCount, envVars)
}

func (p *sessionPoolImpl) initSessions() {
	ctx := context.Background()
	recoveredCount := 0
	for i := 0; i < p.maxSessions; i++ {
		id := fmt.Sprintf("%s%d", sessionIDPrefix, i)
		// Check if the session exists and the status is Running.
		exists, detail, err := p.querySessionAndCache(ctx, id)
		if err == nil && exists && detail != nil && detail.Status == interfaces.SessionStatusRunning {
			poolInstance.addSession(id)
			p.updateSessionDependencies(id, detail)
			recoveredCount++
		}
	}
	p.logger.Infof("Recovered %d sessions during initialization", recoveredCount)

	// Initial warm-up, supplemented to activeSessions.
	p.prewarmSessions()
}

// acquireSession Gets a session from the session pool.
func (p *sessionPoolImpl) acquireSession(ctx context.Context, retryCount int) (sessionID string, err error) {
	return p.acquireSessionWithOptions(ctx, retryCount, nil)
}

func (p *sessionPoolImpl) acquireSessionWithOptions(ctx context.Context, retryCount int, envVars map[string]any) (sessionID string, err error) {
	// record observable.
	ctx, _ = oteltrace.StartInternalSpan(ctx)
	defer oteltrace.EndSpan(ctx, err)
	telemetry.SetSpanAttributes(ctx, map[string]interface{}{
		"retryCount": retryCount,
	})
	// Do you need to retry?.
	var needRetry bool
	defer func(count int) {
		if !needRetry { // No need to retry.
			return
		}
		// Maximum number of retries reached.
		if count < 0 {
			err = fmt.Errorf("[acquireSession] retryCount %d exceeds maxRetryCount %d", count, maxRetryCount)
			return
		}
		// Pause time: Add 1 second to each retry interval.
		time.Sleep(time.Duration(count) * time.Second)
		sessionID, err = p.acquireSessionWithOptions(ctx, count-1, envVars)
	}(retryCount)
	// 1. Stack allocation strategy: Find the session with the highest load but not full.
	bestSession := p.findBestSession()
	if bestSession != nil {
		p.updateRunningTasks(bestSession.ID, 1)
		sessionID = bestSession.ID
		return
	}

	// 2. Try to find a slot that can be created.
	var targetID string
	for i := 0; i < p.maxSessions; i++ {
		id := fmt.Sprintf("%s%d", sessionIDPrefix, i)
		if _, ok := p.getSessionItem(id); !ok {
			targetID = id
			break
		}
	}

	// 3. If there are Sessions in all slots but they are all full (because they were not found in step 1), an error will be reported.
	if targetID == "" {
		if retryCount == 0 {
			return "", fmt.Errorf("all %d sessions are at max concurrency (%d)", p.maxSessions, p.maxConcurrentTasks)
		}
		// Recursive retry: If creation of the current ID fails, recursively try the next available ID.
		needRetry = true
		return
	}

	// 5. Perform remote creation.
	p.logger.Infof("Creating new session slot: %s", targetID)
	if err = p.ensureRemoteSessionWithEnv(ctx, targetID, envVars); err != nil {
		p.logger.Errorf("Failed to create session %s: %v", targetID, err)
		// Creation failed, placeholder removed.
		// Fault-tolerant retries: If the current ID fails to be created, recursively try the next available ID.
		// Note: You need to clean up the current failed placeholders first.
		p.removeSession(targetID) // Clean up placeholders (dark bottom)
		// Try again.
		needRetry = true
		return
	}
	return targetID, nil
}

func (p *sessionPoolImpl) ensureRemoteSession(ctx context.Context, sessionID string) error {
	return p.ensureRemoteSessionWithEnv(ctx, sessionID, nil)
}

func (p *sessionPoolImpl) ensureRemoteSessionWithEnv(ctx context.Context, sessionID string, envVars map[string]any) error {
	// Check existence before creating.
	exists, _, err := p.querySessionAndCache(ctx, sessionID)
	if err != nil {
		p.logger.Errorf("QuerySession failed for session %s: %v", sessionID, err)
		return err
	}
	if !exists {
		// Execute create.
		req := &interfaces.CreateSessionReq{
			ID:         sessionID,
			TemplateID: p.templateID,
			Timeout:    p.reqConfig.Timeout,
			CPU:        p.reqConfig.CPU,
			Memory:     p.reqConfig.Memory,
			Disk:       p.reqConfig.Disk,
			EnvVars:    sessionScopedEnvVars(envVars),
		}

		_, err := p.client.CreateSession(ctx, req)
		if err != nil {
			p.logger.Warnf("[ensureRemoteSession] Failed to create session %s: %v", sessionID, err)
			return err
		}
	}

	// Waiting for Running status.
	err = p.waitForSessionRunning(ctx, sessionID)
	if err != nil {
		return err
	}
	p.addSession(sessionID)
	return nil
}

// sessionScopedEnvKeys Keys allowed to be hung on the session, whitelist.
//
// The session-level env will be stored in t_session.f_env_vars by the control plane, written into the Pod spec, and passed by.
// GET /api/v1/sessions/{id} is read as is, the lifecycle is the session life (default is up to 6 hours),
// rather than the execution that initiated it.
//
// Use a whitelist instead of a blacklist that "blocks known credentials": the blacklist is allowed by default, add a new one to the execution env.
// If you forget to synchronize the credential type key, it will be silently dropped from the persisted data; the whitelist blocks it by default, and missing a tracking mark will only cause a session at most.
// There is no harm in seeing one missing field in the query.
//
// All listed here are tracking marks - their contract documents clearly state "only used as tracking marks, not involved in authentication.".
var sessionScopedEnvKeys = map[string]bool{
	"source":              true,
	"task_id":             true,
	"capability_id":       true,
	"capability_name":     true,
	"function_version_id": true,
	"user_id":             true,
	"user_name":           true,
}

// sessionScopedEnvVars retrieves the portion of environment variables that can safely be hung on the session.
//
// Filtered out keys do not affect functionality: there is another path to env for each execution (ExecuteCodeReq.EnvVars ->.
// executor -> --setenv into bwrap), and the one executed this time shall prevail when merging.
func sessionScopedEnvVars(envVars map[string]any) map[string]any {
	if len(envVars) == 0 {
		return nil
	}
	scoped := make(map[string]any, len(envVars))
	for key, value := range envVars {
		if !sessionScopedEnvKeys[key] {
			continue
		}
		scoped[key] = value
	}
	if len(scoped) == 0 {
		return nil
	}
	return scoped
}

func cloneSessionEnvVars(envVars map[string]any) map[string]any {
	if len(envVars) == 0 {
		return nil
	}
	cloned := make(map[string]any, len(envVars))
	for key, value := range envVars {
		cloned[key] = value
	}
	return cloned
}

func (p *sessionPoolImpl) recordExecutionContext(sessionID string, envVars map[string]any) {
	if len(envVars) == 0 {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if item, ok := p.sessions[sessionID]; ok && item != nil {
		item.ExecutionContext = sessionScopedEnvVars(envVars)
		item.LastUsedAt = time.Now()
	}
}

func (p *sessionPoolImpl) waitForSessionRunning(ctx context.Context, sessionID string) error {
	ticker := time.NewTicker(sessionStatusRunningCheckInterval)
	defer ticker.Stop()
	timeout := time.After(waitSessionRunningTimeout)

	for {
		select {
		case <-timeout:
			return fmt.Errorf("timeout waiting for session %s to be running", sessionID)
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			exists, detail, err := p.querySessionAndCache(ctx, sessionID)
			if err != nil {
				p.logger.Errorf("QuerySession failed for session %s: %v", sessionID, err)
				return err
			}
			if !exists {
				// Session creation failed.
				return fmt.Errorf("session %s failed to create, not found", sessionID)
			}
			switch detail.Status {
			case interfaces.SessionStatusRunning:
				return nil // Session ran successfully.
			case interfaces.SessionStatusFailed, interfaces.SessionStatusTerminated:
				err := p.client.DeleteSession(ctx, sessionID)
				if err != nil {
					p.logger.Warnf("Failed to delete session %s before creation: %v", sessionID, err)
					return err
				}
				return fmt.Errorf("session %s failed to create, status: %s", sessionID, detail.Status)
			case interfaces.SessionStatusCreating:
				// Keep waiting.
			}
		}
	}
}

// releaseSession releases the session slot to allow other tasks to use it.
func (p *sessionPoolImpl) releaseSession(sessionID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if item, ok := p.sessions[sessionID]; ok {
		item.RunningTasks--
		if item.RunningTasks < 0 {
			item.RunningTasks = 0
		}
		item.LastUsedAt = time.Now()
	}
}

// ReleaseSession returns the session.
func (p *sessionPoolImpl) ReleaseSession(sessionID string) {
	p.releaseSession(sessionID)
}

// invalidateSession removes the session slot from the session pool and deletes the remote resource asynchronously.
func (p *sessionPoolImpl) invalidateSession(sessionID string) {
	// Asynchronously delete remote resources.
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), defaultContextTimeout)
		defer cancel()
		_ = p.client.DeleteSession(ctx, sessionID)
	}()
}

func (p *sessionPoolImpl) prewarmSessions() {
	p.mu.Lock()
	currentCount := len(p.sessions)
	needed := p.activeSessions - currentCount
	p.mu.Unlock()

	if needed <= 0 {
		return
	}

	p.logger.Infof("Pre-warming %d sessions to reach activeSessions limit (%d)", needed, p.activeSessions)

	for i := 0; i < needed; i++ {
		// Use acquireSession logic to find available IDs and create.
		// Here we directly call the internal logic or reuse part of the logic.
		// For the sake of simplicity, we directly try to find free slots and create.
		p.mu.Lock()
		var targetID string
		for j := 0; j < p.maxSessions; j++ {
			id := fmt.Sprintf("%s%d", sessionIDPrefix, j)
			if _, ok := p.sessions[id]; !ok {
				targetID = id
				break
			}
		}
		p.mu.Unlock()

		if targetID == "" {
			break
		}

		go func(sid string) {
			prewarmCtx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
			defer cancel()

			if err := p.ensureRemoteSession(prewarmCtx, sid); err != nil {
				p.logger.Errorf("Failed to pre-warm session %s: %v", sid, err)
				p.removeSession(sid)
				return
			}
			p.logger.Infof("Successfully pre-warmed session: %s", sid)
		}(targetID)
	}
}

func (p *sessionPoolImpl) backgroundWorker() {
	ticker := time.NewTicker(backgroundWorkerInterval)
	defer ticker.Stop()

	for {
		select {
		case <-p.stopCh:
			return
		case <-ticker.C:
			p.maintainPool()
		}
	}
}

func (p *sessionPoolImpl) maintainPool() {
	ctx := context.Background()
	p.mu.Lock()
	// Make a copy of the current session list for checking to avoid holding locks for a long time.
	currentSessions := make([]string, 0, len(p.sessions))
	for id := range p.sessions {
		currentSessions = append(currentSessions, id)
	}
	p.mu.Unlock()

	// 1. Health check and repair.
	for _, id := range currentSessions {
		exists, detail, err := p.querySessionAndCache(ctx, id)
		if err != nil || !exists || (detail.Status != interfaces.SessionStatusRunning && detail.Status != interfaces.SessionStatusCreating) {
			p.logger.Warnf("Session %s is unhealthy or missing, removing from pool", id)
			p.removeSession(id)
			p.invalidateSession(id)
			continue
		}
	}

	// 2. Warm-up management: add to activeSessions.
	p.prewarmSessions()

	// 3. Idle management: retain active idle sessions according to activeSessions configuration.
	p.mu.Lock()
	var idleItems []*sessionItem
	for _, item := range p.sessions {
		if item.RunningTasks == 0 {
			idleItems = append(idleItems, item)
		}
	}
	if len(idleItems) > p.activeSessions {
		// Sort by last use time, keep the latest.
		// Simple approach: delete all but the first one (or find the latest one to keep)
		latestIdx := 0
		for i := 1; i < len(idleItems); i++ {
			if idleItems[i].LastUsedAt.After(idleItems[latestIdx].LastUsedAt) {
				latestIdx = i
			}
		}

		for i, item := range idleItems {
			if i == latestIdx {
				continue
			}
			p.logger.Infof("Scaling down idle session: %s", item.ID)
			// Remove session slot from session pool.
			delete(p.sessions, item.ID)
			p.invalidateSession(item.ID)
		}
	}
	p.mu.Unlock()
}

// Close Closes the global session pool.
func Close() {
	if poolInstance == nil {
		return
	}
	close(poolInstance.stopCh)
	// Concurrently close the session pool.
	waitGroup := sync.WaitGroup{}
	for _, pool := range poolInstance.sessions {
		waitGroup.Add(1)
		poolInstance.removeSession(pool.ID)
		go func(sessionID string) {
			_ = poolInstance.client.DeleteSession(context.Background(), sessionID)
			waitGroup.Done()
		}(pool.ID)
	}
	waitGroup.Wait()
}

// Add session to pool.
func (p *sessionPoolImpl) addSession(sessionID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sessions[sessionID] = &sessionItem{
		ID:           sessionID,
		RunningTasks: 0,
		LastUsedAt:   time.Now(),
	}
}

// getSessionItem Gets the session item.
func (p *sessionPoolImpl) getSessionItem(sessionID string) (sessionItem *sessionItem, ok bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	sessionItem, ok = p.sessions[sessionID]
	return
}

// Delete session.
func (p *sessionPoolImpl) removeSession(sessionID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.sessions, sessionID)
}

// Update the number of running tasks.
// updateRunningTasks updates the number of running tasks in the session.
func (p *sessionPoolImpl) updateRunningTasks(sessionID string, delta int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if item, exists := p.sessions[sessionID]; exists {
		item.RunningTasks += delta
		item.LastUsedAt = time.Now()
	}
}

// findBestSession Finds the best session: Stacked allocation strategy: Finds the session with the highest load but not full.
func (p *sessionPoolImpl) findBestSession() (bestSession *sessionItem) {
	p.mu.Lock()
	type sessionCandidate struct {
		id           string
		runningTasks int
		lastUsedAt   time.Time
	}
	candidates := make([]sessionCandidate, 0, len(p.sessions))
	for _, item := range p.sessions {
		p.logger.Infof("Session %s: RunningTasks=%d, LastUsedAt=%v\n", item.ID, item.RunningTasks, item.LastUsedAt)
		if item.RunningTasks < p.maxConcurrentTasks {
			candidates = append(candidates, sessionCandidate{
				id:           item.ID,
				runningTasks: item.RunningTasks,
				lastUsedAt:   item.LastUsedAt,
			})
		}
	}
	p.mu.Unlock()

	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].runningTasks > candidates[j].runningTasks
	})

	invalidIDs := make([]string, 0)
	for _, c := range candidates {
		exists, _, _ := p.querySessionAndCache(context.Background(), c.id)
		if !exists {
			invalidIDs = append(invalidIDs, c.id)
			continue
		}
		if item, ok := p.getSessionItem(c.id); ok {
			return item
		}
	}

	for _, sessionID := range invalidIDs {
		p.removeSession(sessionID)
	}
	return nil
}

func (p *sessionPoolImpl) querySessionAndCache(ctx context.Context, sessionID string) (exists bool, detail *interfaces.SessionDetail, err error) {
	exists, detail, err = p.client.QuerySession(ctx, sessionID)
	if err != nil || !exists || detail == nil {
		return exists, detail, err
	}
	p.updateSessionDependencies(sessionID, detail)
	return exists, detail, nil
}

func (p *sessionPoolImpl) updateSessionDependencies(sessionID string, detail *interfaces.SessionDetail) {
	if detail == nil {
		return
	}
	var deps []*interfaces.DependencyInfo
	if detail.InstalledDependencies != nil {
		deps = detail.InstalledDependencies
	} else if detail.RequestedDependencies != nil {
		deps = detail.RequestedDependencies
	} else {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if item, ok := p.sessions[sessionID]; ok {
		item.Dependencies = deps
	}
}
