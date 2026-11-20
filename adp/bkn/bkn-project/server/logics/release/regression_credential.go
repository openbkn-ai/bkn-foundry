// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package release

import (
	"context"
	"fmt"
	"time"

	"github.com/openbkn-ai/bkn-foundry/comm-go/hydra"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/otellog"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// tokenMargin is how long the sign-in a run started with must still hold for a question to be
// asked with it: the longest one answer takes, with room to spare. Past that, the run asks with
// the credential bkn-safe issued for it.
const tokenMargin = 10 * time.Minute

// runCredential is what a regression asks its questions with: the sign-in of the person who
// started it while that still holds, then a credential bkn-safe issued, when the run started,
// for that person on the release's network through the answering Agent. The sign-in comes first
// because only it reaches every tool, Functions included; the issued one lets the rest of a long
// run go on as the same person, under that person's permissions as they are at each question.
// Neither is ever stored or logged.
type runCredential struct {
	token string
	// expires is when token stops being accepted; zero when it could not be read.
	expires time.Time
	// delegated is the issued credential, empty when none was.
	delegated string
	now       func() time.Time
}

// current is the credential the next question is asked with.
func (c *runCredential) current() string {
	if c.delegated == "" {
		return c.token
	}
	if !c.expires.IsZero() && c.now().Add(tokenMargin).Before(c.expires) {
		return c.token
	}
	return "Bearer " + c.delegated
}

// grantID is the execution grant of a run: its run id, so a run left behind by a restart can
// still be revoked.
func grantID(runID string) string { return runID }

// credential reads how long token holds and, for a run started by a person, has bkn-safe issue
// the credential the run goes on with once it lapses. A run bkn-safe issues none for asks with
// token alone, as before.
func (s *regressionService) credential(ctx context.Context, token string, release *interfaces.Release,
	r *interfaces.Regression) *runCredential {
	c := &runCredential{token: token, now: s.now}
	if s.ga == nil || r.Runner.Type != string(hydra.VisitorType_User) {
		return c
	}
	if s.aa != nil {
		expires, err := s.aa.TokenExpiry(ctx, token)
		if err != nil {
			otellog.LogWarn(ctx, fmt.Sprintf("Read when the sign-in of regression %s lapses: %v", r.RunID, err))
		}
		c.expires = expires
	}
	grant, err := s.ga.Issue(ctx, interfaces.ExecutionGrantRequest{ID: grantID(r.RunID), ProjectID: release.ProjectID, RunID: r.RunID,
		DelegatorID: r.Runner.ID, KnID: release.NetworkID, AgentID: answerAgentID, TTL: regressionStaleAfter})
	if err != nil {
		otellog.LogWarn(ctx, fmt.Sprintf("Issue the credential of regression %s: %v", r.RunID, err))
		return c
	}
	c.delegated = grant.Credential
	return c
}

// revoke ends the credential issued for a run, if one was.
func (s *regressionService) revoke(ctx context.Context, projectID string, runID string) {
	if s.ga == nil {
		return
	}
	if err := s.ga.Revoke(context.WithoutCancel(ctx), grantID(runID), projectID, runID); err != nil {
		otellog.LogWarn(ctx, fmt.Sprintf("Revoke the credential of regression %s: %v", runID, err))
	}
}
