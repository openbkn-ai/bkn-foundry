// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package action_scheduler

import (
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces"
)

// The pin of a numbered version answers "caller identity" until bkn-backend says that number
// is running, and the instance read in the middle of an execution can tell it so. ExecuteAction
// therefore asks once, before that read, and keeps the answer for the proxy and its record.
func TestPinIdentityChangesOnceRunningIsKnown(t *testing.T) {
	pin, err := interfaces.NewKNVersionPin("3", "")
	if err != nil {
		t.Fatal(err)
	}
	if !pin.UsesCallerIdentity() {
		t.Fatal("an unresolved numbered version must run as the caller")
	}
	if err := pin.Observe("3", ""); err != nil {
		t.Fatal(err)
	}
	pin.ObserveRunning("3")
	if pin.UsesCallerIdentity() {
		t.Fatal("v3 known to run reads through the proxy")
	}
}
