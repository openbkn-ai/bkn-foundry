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

	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	. "github.com/smartystreets/goconvey/convey"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

func Test_BknAgentAccess(t *testing.T) {
	Convey("Test bknAgentAccess\n", t, func() {
		type request struct {
			method, path, accountID, accountType string
			body                                 map[string]any
		}
		var got []request
		status, respBody := http.StatusOK, ""
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			req := request{method: r.Method, path: r.URL.EscapedPath(),
				accountID: r.Header.Get("x-account-id"), accountType: r.Header.Get("x-account-type")}
			if data, _ := io.ReadAll(r.Body); len(data) > 0 {
				_ = json.Unmarshal(data, &req.body)
			}
			got = append(got, req)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_, _ = w.Write([]byte(respBody))
		}))
		defer server.Close()
		ba := &bknAgentAccess{httpClient: rest.NewHTTPClient(), baseUrl: server.URL + "/api/bkn-agent/v1"}
		ctx := context.Background()
		account := interfaces.BknAgentAccount{ID: "u1", Type: "user"}

		Convey("RunTask starts the Agent as the account, with the response format\n", func() {
			respBody = `{"task_id":"t1","agent_id":"a1","status":"pending","input":{"message":"m"},"output":null,
				"failure_detail":null,"parent_thread_id":null,"create_time":5,"update_time":5}`

			task, err := ba.RunTask(ctx, account, &interfaces.BknAgentRunRequest{
				AgentID: "a1", Message: "m", ResponseFormat: map[string]any{"type": "object"},
			})

			So(err, ShouldBeNil)
			So(got[0].method, ShouldEqual, http.MethodPost)
			So(got[0].path, ShouldEqual, "/api/bkn-agent/v1/run")
			So(got[0].accountID, ShouldEqual, "u1")
			So(got[0].accountType, ShouldEqual, "user")
			So(got[0].body, ShouldResemble, map[string]any{
				"agent_id": "a1", "message": "m", "response_format": map[string]any{"type": "object"},
			})
			So(task, ShouldResemble, &interfaces.BknAgentTask{TaskID: "t1", AgentID: "a1", Status: "pending",
				CreateTime: 5, UpdateTime: 5})
			So(task.Terminal(), ShouldBeFalse)
		})

		Convey("RunTask leaves the response format out when there is none\n", func() {
			respBody = `{"task_id":"t1","agent_id":"a1","status":"pending","create_time":5,"update_time":5}`

			_, err := ba.RunTask(ctx, account, &interfaces.BknAgentRunRequest{AgentID: "a1", Message: "m"})

			So(err, ShouldBeNil)
			So(got[0].body, ShouldResemble, map[string]any{"agent_id": "a1", "message": "m"})
		})

		Convey("RunTask returns bkn-agent's refusals with their status and code\n", func() {
			for _, c := range []struct {
				status int
				body   string
				code   string
			}{
				{http.StatusNotFound, `{"code":"BknAgent.NotFound","description":"Agent a1 not found","detail":null}`, "BknAgent.NotFound"},
				{http.StatusBadRequest, `{"code":"BknAgent.Task.ModeMismatch","description":"mode is chat"}`, "BknAgent.Task.ModeMismatch"},
				{http.StatusUnauthorized, `{"code":"BknAgent.Auth.AccountRequired","description":"account required"}`, "BknAgent.Auth.AccountRequired"},
				{http.StatusBadGateway, `<html>bad gateway</html>`, ""},
			} {
				status, respBody = c.status, c.body

				_, err := ba.RunTask(ctx, account, &interfaces.BknAgentRunRequest{AgentID: "a1", Message: "m"})

				var agentErr *interfaces.BknAgentError
				So(errors.As(err, &agentErr), ShouldBeTrue)
				So(agentErr.HTTPCode, ShouldEqual, c.status)
				So(agentErr.Code, ShouldEqual, c.code)
			}
		})

		Convey("RunTask refuses a reply without a task id\n", func() {
			respBody = `{"status":"pending"}`

			_, err := ba.RunTask(ctx, account, &interfaces.BknAgentRunRequest{AgentID: "a1", Message: "m"})

			So(err, ShouldNotBeNil)
		})

		Convey("GetTask reads the task as the account\n", func() {
			respBody = `{"task_id":"t/1","agent_id":"a1","status":"succeeded","output":"{\"a\":1}","failure_detail":null,
				"create_time":5,"update_time":9}`

			task, err := ba.GetTask(ctx, account, "t/1")

			So(err, ShouldBeNil)
			So(got[0].method, ShouldEqual, http.MethodGet)
			So(got[0].path, ShouldEqual, "/api/bkn-agent/v1/tasks/t%2F1")
			So(got[0].accountID, ShouldEqual, "u1")
			So(task.Output, ShouldEqual, `{"a":1}`)
			So(task.Terminal(), ShouldBeTrue)
		})

		Convey("GetTask returns a failed task with its detail\n", func() {
			respBody = `{"task_id":"t1","agent_id":"a1","status":"failed","output":null,"failure_detail":"TimeoutError: ",
				"create_time":5,"update_time":9}`

			task, err := ba.GetTask(ctx, account, "t1")

			So(err, ShouldBeNil)
			So(task.Status, ShouldEqual, interfaces.BKN_AGENT_TASK_FAILED)
			So(task.FailureDetail, ShouldEqual, "TimeoutError: ")
			So(task.Terminal(), ShouldBeTrue)
		})

		Convey("GetTask returns 404 for a task the account did not start\n", func() {
			status, respBody = http.StatusNotFound, `{"code":"BknAgent.NotFound","description":"task t1 not found"}`

			_, err := ba.GetTask(ctx, account, "t1")

			var agentErr *interfaces.BknAgentError
			So(errors.As(err, &agentErr), ShouldBeTrue)
			So(agentErr.HTTPCode, ShouldEqual, http.StatusNotFound)
			So(agentErr.Code, ShouldEqual, "BknAgent.NotFound")
		})

		Convey("A connection failure is no bkn-agent reply\n", func() {
			server.Close()

			_, err := ba.GetTask(ctx, account, "t1")

			var agentErr *interfaces.BknAgentError
			So(err, ShouldNotBeNil)
			So(errors.As(err, &agentErr), ShouldBeFalse)
		})
	})
}
