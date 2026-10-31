// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package bknagent

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

func Test_BknAgentAccess_Chat(t *testing.T) {
	Convey("Test bknAgentAccess Chat\n", t, func() {
		type request struct {
			path, accept, accountID string
			body                    map[string]any
		}
		var got []request
		status, respBody := http.StatusOK, ""
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			req := request{path: r.URL.EscapedPath(), accept: r.Header.Get("Accept"), accountID: r.Header.Get(headerAccountID)}
			if data, _ := io.ReadAll(r.Body); len(data) > 0 {
				_ = json.Unmarshal(data, &req.body)
			}
			got = append(got, req)
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(status)
			_, _ = w.Write([]byte(respBody))
		}))
		defer server.Close()
		ba := &bknAgentAccess{streamClient: &http.Client{}, baseUrl: server.URL + "/api/bkn-agent/v1"}
		ctx := context.Background()
		account := interfaces.BknAgentAccount{ID: "u1", Type: "user"}
		req := &interfaces.BknAgentChatRequest{AgentID: "bkn-project-ontology-ask", Message: "{}",
			ResponseFormat: map[string]any{"type": "object"}}
		var events []interfaces.BknAgentChatEvent
		on := func(e interfaces.BknAgentChatEvent) { events = append(events, e) }
		names := func() []string {
			out := []string{}
			for _, e := range events {
				out = append(out, e.Name)
			}
			return out
		}

		Convey("Each event is handed on as it is read, up to done, as the account\n", func() {
			respBody = "event: meta\ndata: {\"thread_id\":\"th1\"}\n\n" +
				"event: token\r\ndata: {\"content\":\"一\"}\r\n\r\n" +
				"event: token\ndata: {\"content\":\"二\"}\n\n" +
				"event: structured\ndata: {\"content\":{\"revisions\":[]}}\n\n" +
				"event: done\ndata: {\"thread_id\":\"th1\"}\n\n" +
				"event: token\ndata: {\"content\":\"after\"}\n\n"

			err := ba.Chat(ctx, account, req, on)

			So(err, ShouldBeNil)
			So(got[0].path, ShouldEqual, "/api/bkn-agent/v1/chat")
			So(got[0].accept, ShouldEqual, "text/event-stream")
			So(got[0].accountID, ShouldEqual, "u1")
			So(got[0].body["agent_id"], ShouldEqual, "bkn-project-ontology-ask")
			So(got[0].body["response_format"], ShouldResemble, map[string]any{"type": "object"})
			So(names(), ShouldResemble, []string{"meta", "token", "token", "structured", "done"})
			So(string(events[2].Data), ShouldEqual, `{"content":"二"}`)
		})

		Convey("An error event ends the turn and is handed on\n", func() {
			respBody = "event: meta\ndata: {}\n\nevent: error\ndata: {\"code\":\"BknAgent.Chat.Timeout\",\"detail\":\"x\"}\n\n"

			So(ba.Chat(ctx, account, req, on), ShouldBeNil)
			So(names(), ShouldResemble, []string{"meta", "error"})
		})

		Convey("A stream that stops before done or error is cut\n", func() {
			respBody = "event: meta\ndata: {}\n\nevent: token\ndata: {\"content\":\"一\"}\n\n"

			err := ba.Chat(ctx, account, req, on)

			So(errors.Is(err, interfaces.ErrBknAgentAnswerCut), ShouldBeTrue)
			So(names(), ShouldResemble, []string{"meta", "token"})
		})

		Convey("A refusal before the turn is bkn-agent's error, and nothing is handed on\n", func() {
			status, respBody = http.StatusBadRequest, `{"code":"BknAgent.Chat.ModeMismatch","description":"x"}`

			err := ba.Chat(ctx, account, req, on)

			var agentErr *interfaces.BknAgentError
			So(errors.As(err, &agentErr), ShouldBeTrue)
			So(agentErr.HTTPCode, ShouldEqual, http.StatusBadRequest)
			So(agentErr.Code, ShouldEqual, "BknAgent.Chat.ModeMismatch")
			So(events, ShouldBeEmpty)
		})

		Convey("An unreachable bkn-agent is an error\n", func() {
			down := &bknAgentAccess{streamClient: &http.Client{}, baseUrl: "http://127.0.0.1:1"}

			So(down.Chat(ctx, account, req, on), ShouldNotBeNil)
		})

		Convey("A caller giving up while the turn streams reads as its own context's error\n", func() {
			release := make(chan struct{})
			defer close(release)
			slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = w.Write([]byte("event: meta\ndata: {}\n\n"))
				w.(http.Flusher).Flush()
				select {
				case <-release:
				case <-r.Context().Done():
				}
			}))
			defer slow.Close()
			cut, cancel := context.WithCancel(ctx)
			streaming := &bknAgentAccess{streamClient: &http.Client{}, baseUrl: slow.URL}

			err := streaming.Chat(cut, account, req, func(interfaces.BknAgentChatEvent) { cancel() })

			So(errors.Is(err, context.Canceled), ShouldBeTrue)
		})

		Convey("A caller giving up reads as its own context's error\n", func() {
			cut, cancel := context.WithCancel(ctx)
			cancel()

			So(errors.Is(ba.Chat(cut, account, req, on), context.Canceled), ShouldBeTrue)
		})
	})
}
