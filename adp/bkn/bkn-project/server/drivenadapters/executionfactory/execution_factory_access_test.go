// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package executionfactory

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

const token = "Bearer test-token"

func Test_ExecutionFactoryAccess(t *testing.T) {
	Convey("Test executionFactoryAccess against a fake operator-integration\n", t, func() {
		type request struct {
			method, path, query, auth, contentType string
			form                                   map[string]string
			file                                   []byte
			body                                   []byte
		}
		var requests []request
		handlers := map[string]func(w http.ResponseWriter, r *http.Request){}
		var server *httptest.Server
		server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			req := request{method: r.Method, path: r.URL.Path, query: r.URL.RawQuery,
				auth: r.Header.Get("Authorization"), contentType: r.Header.Get("Content-Type")}
			if err := r.ParseMultipartForm(1 << 20); err == nil {
				req.form = map[string]string{}
				for k, v := range r.MultipartForm.Value {
					req.form[k] = v[0]
				}
				for _, headers := range r.MultipartForm.File {
					f, _ := headers[0].Open()
					req.file, _ = io.ReadAll(f)
				}
			} else {
				req.body, _ = io.ReadAll(r.Body)
			}
			requests = append(requests, req)
			handler, ok := handlers[r.Method+" "+r.URL.Path]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"error_code":"Common.NotFound","description":"not found"}`))
				return
			}
			handler(w, r)
		}))
		defer server.Close()
		efa := &executionFactoryAccess{httpClient: rest.NewHTTPClient(), baseUrl: server.URL + "/api/agent-operator-integration/v1"}
		ctx := context.Background()
		const prefix = "/api/agent-operator-integration/v1"

		Convey("ExportToolbox returns the impex export body with the caller's token\n", func() {
			handlers["GET "+prefix+"/impex/export/toolbox/box1"] = func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(`{"toolbox":{"configs":[]}}`))
			}

			data, err := efa.ExportToolbox(ctx, token, "box1")

			So(err, ShouldBeNil)
			So(string(data), ShouldEqual, `{"toolbox":{"configs":[]}}`)
			So(requests[0].auth, ShouldEqual, token)
		})

		Convey("A refused export comes back as an ExecutionFactoryError\n", func() {
			handlers["GET "+prefix+"/impex/export/toolbox/box1"] = func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(`{"error_code":"Common.Forbidden","description":"no view"}`))
			}

			_, err := efa.ExportToolbox(ctx, token, "box1")

			var efaErr *interfaces.ExecutionFactoryError
			So(errors.As(err, &efaErr), ShouldBeTrue)
			So(efaErr.HTTPCode, ShouldEqual, http.StatusForbidden)
			So(efaErr.ErrorCode, ShouldEqual, "Common.Forbidden")
		})

		Convey("ImportToolbox posts the mode and the export as the multipart file data\n", func() {
			handlers["POST "+prefix+"/impex/import/toolbox"] = func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusCreated)
			}

			err := efa.ImportToolbox(ctx, token, []byte(`{"toolbox":{}}`), interfaces.IMPEX_MODE_UPSERT)

			So(err, ShouldBeNil)
			So(requests[0].form, ShouldResemble, map[string]string{"mode": "upsert"})
			So(string(requests[0].file), ShouldEqual, `{"toolbox":{}}`)
			So(requests[0].auth, ShouldEqual, token)
		})

		Convey("GetReleasedSkill downloads SKILL.md and every listed file through their links\n", func() {
			handlers["GET "+prefix+"/skills/s1/content"] = func(w http.ResponseWriter, _ *http.Request) {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"skill_id": "s1", "url": server.URL + "/oss/skill-md?sig=1",
					"files": []map[string]string{{"rel_path": "SKILL.md"}, {"rel_path": "refs/spec.md"}},
				})
			}
			handlers["POST "+prefix+"/skills/s1/files/read"] = func(w http.ResponseWriter, _ *http.Request) {
				_ = json.NewEncoder(w).Encode(map[string]string{"url": server.URL + "/oss/spec"})
			}
			handlers["GET /oss/skill-md"] = func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("---\nname: s\n---\n")) }
			handlers["GET /oss/spec"] = func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("spec")) }

			files, err := efa.GetReleasedSkill(ctx, token, "s1")

			So(err, ShouldBeNil)
			So(files, ShouldResemble, map[string][]byte{"SKILL.md": []byte("---\nname: s\n---\n"), "refs/spec.md": []byte("spec")})
			So(string(requests[2].body), ShouldEqual, `{"rel_path":"refs/spec.md"}`)
			So(requests[2].auth, ShouldEqual, token)
			// The signed links carry no token.
			So(requests[1].auth, ShouldBeEmpty)
			So(requests[1].query, ShouldEqual, "sig=1")
			So(requests[3].auth, ShouldBeEmpty)
		})

		Convey("GetDraftSkill reads the same way through the management endpoints\n", func() {
			handlers["GET "+prefix+"/skills/s2/management/content"] = func(w http.ResponseWriter, _ *http.Request) {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"skill_id": "s2", "status": "unpublish", "url": server.URL + "/oss/draft-md",
					"files": []map[string]string{{"rel_path": "SKILL.md"}, {"rel_path": "refs/spec.md"}},
				})
			}
			handlers["POST "+prefix+"/skills/s2/management/files/read"] = func(w http.ResponseWriter, _ *http.Request) {
				_ = json.NewEncoder(w).Encode(map[string]string{"url": server.URL + "/oss/draft-spec"})
			}
			handlers["GET /oss/draft-md"] = func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("---\nname: d\n---\n")) }
			handlers["GET /oss/draft-spec"] = func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("draft spec")) }

			files, err := efa.GetDraftSkill(ctx, token, "s2")

			So(err, ShouldBeNil)
			So(files, ShouldResemble, map[string][]byte{"SKILL.md": []byte("---\nname: d\n---\n"), "refs/spec.md": []byte("draft spec")})
		})

		Convey("An unpublished Skill fails the read\n", func() {
			_, err := efa.GetReleasedSkill(ctx, token, "s2")

			var efaErr *interfaces.ExecutionFactoryError
			So(errors.As(err, &efaErr), ShouldBeTrue)
			So(efaErr.HTTPCode, ShouldEqual, http.StatusNotFound)
		})

		Convey("FindSkill picks the exact name out of the substring matches, published before a draft\n", func() {
			handlers["GET "+prefix+"/skills"] = func(w http.ResponseWriter, _ *http.Request) {
				_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{
					{"skill_id": "a", "name": "plan_supply_v1_0_0_x", "status": "published"},
					{"skill_id": "c", "name": "plan_supply_v1_0_0", "status": "unpublish"},
					{"skill_id": "b", "name": "plan_supply_v1_0_0", "status": "published"},
					{"skill_id": "d", "name": "plan_draft", "status": "offline"},
					{"skill_id": "e", "name": "plan_draft", "status": "editing"},
				}})
			}

			id, status, err := efa.FindSkill(ctx, token, "plan_supply_v1_0_0")
			So(err, ShouldBeNil)
			So(id, ShouldEqual, "b")
			So(status, ShouldEqual, interfaces.SKILL_STATUS_PUBLISHED)
			So(requests[0].query, ShouldNotContainSubstring, "status=")

			id, status, err = efa.FindSkill(ctx, token, "plan_draft")
			So(err, ShouldBeNil)
			So(id, ShouldEqual, "e")
			So(status, ShouldEqual, "editing")

			id, _, err = efa.FindSkill(ctx, token, "plan")
			So(err, ShouldBeNil)
			So(id, ShouldBeEmpty)
		})

		Convey("RegisterSkill uploads a zip and PublishSkill publishes the new id\n", func() {
			handlers["POST "+prefix+"/skills"] = func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(`{"skill_id":"new1","status":"unpublish"}`))
			}
			handlers["PUT "+prefix+"/skills/new1/status"] = func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(`{"skill_id":"new1","status":"published"}`))
			}

			id, err := efa.RegisterSkill(ctx, token, []byte("zip"))
			So(err, ShouldBeNil)
			So(id, ShouldEqual, "new1")
			So(requests[0].form, ShouldResemble, map[string]string{"file_type": "zip"})
			So(string(requests[0].file), ShouldEqual, "zip")

			So(efa.PublishSkill(ctx, token, "new1"), ShouldBeNil)
			So(string(requests[1].body), ShouldEqual, `{"status":"published"}`)
			So(requests[1].contentType, ShouldEqual, "application/json")
		})
	})
}
