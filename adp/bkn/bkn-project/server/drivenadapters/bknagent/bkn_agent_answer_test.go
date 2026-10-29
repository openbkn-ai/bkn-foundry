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
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	. "github.com/smartystreets/goconvey/convey"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// sse writes events as bkn-agent streams them: each carries run_id and sequence.
func sse(events ...[2]string) string {
	var b strings.Builder
	for i, e := range events {
		var data map[string]any
		_ = json.Unmarshal([]byte(e[1]), &data)
		data["run_id"], data["sequence"] = "r1", i+1
		payload, _ := json.Marshal(data)
		b.WriteString("event: " + e[0] + "\ndata: " + string(payload) + "\n\n")
	}
	return b.String()
}

func Test_BknAgentAccess_AnswerOnNetwork(t *testing.T) {
	Convey("Test bknAgentAccess AnswerOnNetwork\n", t, func() {
		type request struct {
			method, path, authorization string
			body                        map[string]any
		}
		var got []request
		status, contentType, respBody := http.StatusOK, "text/event-stream", ""
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			req := request{method: r.Method, path: r.URL.EscapedPath(), authorization: r.Header.Get("Authorization")}
			if data, _ := io.ReadAll(r.Body); len(data) > 0 {
				_ = json.Unmarshal(data, &req.body)
			}
			got = append(got, req)
			w.Header().Set("Content-Type", contentType)
			w.WriteHeader(status)
			_, _ = w.Write([]byte(respBody))
		}))
		defer server.Close()
		ba := &bknAgentAccess{httpClient: rest.NewHTTPClient(), baseUrl: server.URL + "/api/bkn-agent/v1"}
		ctx := context.Background()
		req := &interfaces.BknAgentAnswerRequest{
			AgentID: "studio_smart_qa", Message: "Which supplier delivers part P1?", ClientRequestID: "rel1:q1:1", KNID: "supply_v1_1_0",
		}

		Convey("An answer streamed to its end is read with the tool calls the Agent made\n", func() {
			respBody = sse(
				[2]string{"meta", `{"thread_id":"th1","agent_id":"studio_smart_qa"}`},
				[2]string{"tool_started", `{"tool_call_id":"c1","name":"search_schema","input":{}}`},
				[2]string{"tool_finished", `{"tool_call_id":"c1","name":"search_schema","status":"succeeded","output":{},"duration_ms":5}`},
				[2]string{"tool_started", `{"tool_call_id":"c2","name":"query_object_instance","input":{}}`},
				[2]string{"tool_finished", `{"tool_call_id":"c2","name":"query_object_instance","status":"failed","output":"x","duration_ms":5,"error":"x","error_kind":"unavailable"}`},
				[2]string{"tool_started", `{"tool_call_id":"c3","name":"run_cypher","input":{}}`},
				[2]string{"tool_finished", `{"tool_call_id":"c3","name":"run_cypher","status":"failed","output":"x","duration_ms":5,"error":"x","error_kind":"error"}`},
				[2]string{"token", `{"text":"Supplier "}`},
				[2]string{"usage", `{"available":true,"usage":{"input_tokens":3}}`},
				[2]string{"result", `{"content":{"answer":"Supplier S1 delivers part P1.","context":{"kn_id":"supply_v1_1_0"},"prompt_version":1,"usage":null}}`},
				[2]string{"terminal", `{"status":"succeeded"}`},
			)

			answer, err := ba.AnswerOnNetwork(ctx, "Bearer t", req)

			So(err, ShouldBeNil)
			So(got[0].method, ShouldEqual, http.MethodPost)
			So(got[0].path, ShouldEqual, "/api/bkn-agent/v1/executions")
			So(got[0].authorization, ShouldEqual, "Bearer t")
			So(got[0].body, ShouldResemble, map[string]any{
				"agent_id": "studio_smart_qa", "message": "Which supplier delivers part P1?",
				"client_request_id": "rel1:q1:1", "context": map[string]any{"kn_id": "supply_v1_1_0"},
			})
			So(answer, ShouldResemble, &interfaces.BknAgentAnswer{
				Status: interfaces.BKN_AGENT_ANSWER_SUCCEEDED, Answer: "Supplier S1 delivers part P1.",
				ToolCalls: 3, ToolsUnavailable: 1, ToolsFailed: 1,
			})
		})

		Convey("A replayed answer has its result and no tool calls\n", func() {
			respBody = sse(
				[2]string{"meta", `{"thread_id":"th1","agent_id":"studio_smart_qa"}`},
				[2]string{"result", `{"content":{"answer":"Supplier S1."}}`},
				[2]string{"terminal", `{"status":"succeeded"}`},
			)

			answer, err := ba.AnswerOnNetwork(ctx, "Bearer t", req)

			So(err, ShouldBeNil)
			So(answer, ShouldResemble, &interfaces.BknAgentAnswer{Status: interfaces.BKN_AGENT_ANSWER_SUCCEEDED, Answer: "Supplier S1."})
		})

		Convey("An answer that failed while streaming ends with bkn-agent's code\n", func() {
			for _, code := range []string{"BknAgent.Model.Unavailable", "BknAgent.Chat.Timeout", "BknAgent.Execution.Failed"} {
				got = nil
				respBody = sse(
					[2]string{"meta", `{"thread_id":"th1","agent_id":"studio_smart_qa"}`},
					[2]string{"terminal", `{"status":"failed","error":{"code":"` + code + `","detail":"d"}}`},
				)

				answer, err := ba.AnswerOnNetwork(ctx, "Bearer t", req)

				So(err, ShouldBeNil)
				So(answer, ShouldResemble, &interfaces.BknAgentAnswer{Status: interfaces.BKN_AGENT_ANSWER_FAILED, ErrorCode: code})
			}
		})

		Convey("A cancelled answer ends cancelled, without a code\n", func() {
			respBody = sse([2]string{"terminal", `{"status":"cancelled"}`})

			answer, err := ba.AnswerOnNetwork(ctx, "Bearer t", req)

			So(err, ShouldBeNil)
			So(answer.Status, ShouldEqual, interfaces.BKN_AGENT_ANSWER_CANCELLED)
			So(answer.ErrorCode, ShouldBeEmpty)
		})

		Convey("Lines ended by CRLF are read too\n", func() {
			respBody = strings.ReplaceAll(sse(
				[2]string{"result", `{"content":{"answer":"A."}}`},
				[2]string{"terminal", `{"status":"succeeded"}`},
			), "\n", "\r\n")

			answer, err := ba.AnswerOnNetwork(ctx, "Bearer t", req)

			So(err, ShouldBeNil)
			So(answer.Answer, ShouldEqual, "A.")
		})

		Convey("A stream that ends without its terminal event is an error\n", func() {
			respBody = sse(
				[2]string{"meta", `{"thread_id":"th1","agent_id":"studio_smart_qa"}`},
				[2]string{"result", `{"content":{"answer":"A."}}`},
			)

			_, err := ba.AnswerOnNetwork(ctx, "Bearer t", req)

			So(err, ShouldNotBeNil)
			var agentErr *interfaces.BknAgentError
			So(errors.As(err, &agentErr), ShouldBeFalse)
		})

		Convey("Refusals before answering come back with their status and code\n", func() {
			contentType = "application/json"
			for _, c := range []struct {
				status int
				code   string
			}{
				{http.StatusUnauthorized, "BknAgent.Auth.TokenRequired"},
				// No permission on the network, and a network that does not exist, alike.
				{http.StatusForbidden, "BknAgent.Execution.Forbidden"},
				// The Agent is missing or not published.
				{http.StatusNotFound, "BknAgent.NotFound"},
				// The same request id is still answering, or was used for another question.
				{http.StatusConflict, "BknAgent.Execution.Conflict"},
				{http.StatusServiceUnavailable, "BknAgent.Execution.Unavailable"},
			} {
				status, respBody = c.status, `{"code":"`+c.code+`","description":"d","detail":"x"}`

				_, err := ba.AnswerOnNetwork(ctx, "Bearer t", req)

				var agentErr *interfaces.BknAgentError
				So(errors.As(err, &agentErr), ShouldBeTrue)
				So(agentErr.HTTPCode, ShouldEqual, c.status)
				So(agentErr.Code, ShouldEqual, c.code)
			}
		})

		Convey("A request that does not go through is an error\n", func() {
			server.Close()

			_, err := ba.AnswerOnNetwork(ctx, "Bearer t", req)

			So(err, ShouldNotBeNil)
		})
	})
}
