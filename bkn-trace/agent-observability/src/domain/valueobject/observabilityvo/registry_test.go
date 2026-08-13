package observabilityvo

import "testing"

func TestRegisteredLogEventsRejectUnknownAndCategoryMismatch(t *testing.T) {
	if !IsRegisteredLogEvent(CategoryRuntimeBusiness, "knowledge.read.completed") {
		t.Fatal("registered business event was rejected")
	}
	if IsRegisteredLogEvent(CategoryRuntimeSystem, "knowledge.read.completed") {
		t.Fatal("registered event was accepted under the wrong category")
	}
	if IsRegisteredLogEvent(CategoryRuntimeSystem, "plugin.custom.event") {
		t.Fatal("unregistered extension event was accepted")
	}
}

func TestRegisteredLogEventsIncludeVoluntaryLogout(t *testing.T) {
	if !IsRegisteredLogEvent(CategoryAccessUser, "logout.succeeded") {
		t.Fatal("registered voluntary logout event was rejected")
	}
}
