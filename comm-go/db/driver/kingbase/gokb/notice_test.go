//go:build go1.10

package gokb

import (
	"context"
	"database/sql/driver"
	"testing"
)

type fakeConnector struct{}

func (fakeConnector) Connect(context.Context) (driver.Conn, error) { return nil, nil }
func (fakeConnector) Driver() driver.Driver                        { return nil }

func TestConnectorWithNoticeHandlerWrapsForeignConnector(t *testing.T) {
	inner := fakeConnector{}
	nhc := ConnectorWithNoticeHandler(inner, func(*Error) {})

	// The returned connector must wrap the original connector. Before the
	// fix, the else-branch referenced the shadowed variable (the failed
	// type-assertion zero value), so Connector ended up as a nil
	// *NoticeHandlerConnector instead of `inner`.
	if nhc.Connector != driver.Connector(inner) {
		t.Fatalf("ConnectorWithNoticeHandler lost the underlying connector: got %v, want %v", nhc.Connector, inner)
	}
	if nhc.noticeHandler == nil {
		t.Fatal("notice handler was not set on the wrapper")
	}
}

func TestConnectorWithNoticeHandlerReusesWrappedConnector(t *testing.T) {
	inner := fakeConnector{}
	first := ConnectorWithNoticeHandler(inner, func(*Error) {})
	first.noticeHandler = nil // reset so we can observe the update below

	second := ConnectorWithNoticeHandler(first, func(*Error) {})
	if second != first {
		t.Fatal("ConnectorWithNoticeHandler should reuse an existing NoticeHandlerConnector")
	}
	if second.noticeHandler == nil {
		t.Fatal("notice handler was not updated on the reused connector")
	}
}

func TestConnectorNoticeHandler(t *testing.T) {
	if got := ConnectorNoticeHandler(fakeConnector{}); got != nil {
		t.Fatal("expected nil handler for foreign connector")
	}
	nhc := ConnectorWithNoticeHandler(fakeConnector{}, func(*Error) {})
	if ConnectorNoticeHandler(nhc) == nil {
		t.Fatal("expected handler to be readable back")
	}
}
