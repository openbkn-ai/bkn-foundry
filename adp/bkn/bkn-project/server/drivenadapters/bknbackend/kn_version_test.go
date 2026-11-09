// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package bknbackend

import (
	"archive/tar"
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

func Test_KNVersionAccess(t *testing.T) {
	Convey("Test the knowledge network version calls\n", t, func() {
		type request struct {
			method, path, query, auth, contentType string
			body                                   map[string]any
			files                                  map[string]string
		}
		var got []request
		status, respBody := http.StatusOK, ""
		replies := []string{}
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			req := request{method: r.Method, path: r.URL.Path, query: r.URL.RawQuery,
				auth: r.Header.Get("Authorization"), contentType: r.Header.Get("Content-Type")}
			if file, _, err := r.FormFile("file"); err == nil {
				data, _ := io.ReadAll(file)
				req.files = readTar(data)
			} else if data, _ := io.ReadAll(r.Body); len(data) > 0 {
				_ = json.Unmarshal(data, &req.body)
			}
			got = append(got, req)
			body := respBody
			if len(replies) > 0 {
				body, replies = replies[0], replies[1:]
			}
			w.WriteHeader(status)
			_, _ = w.Write([]byte(body))
		}))
		defer server.Close()
		bba := &bknBackendAccess{httpClient: rest.NewHTTPClient(), baseUrl: server.URL + "/api/bkn-backend/v1"}
		ctx := context.Background()
		const networkPath = "/api/bkn-backend/v1/knowledge-networks/orders"

		Convey("CreateVersionedKN posts a versioned network on main and returns its id\n", func() {
			status, respBody = http.StatusCreated, `{"id":"orders"}`

			id, err := bba.CreateVersionedKN(ctx, token, "", "Orders")

			So(err, ShouldBeNil)
			So(id, ShouldEqual, "orders")
			So(got[0].method, ShouldEqual, http.MethodPost)
			So(got[0].path, ShouldEqual, "/api/bkn-backend/v1/knowledge-networks")
			So(got[0].auth, ShouldEqual, token)
			So(got[0].contentType, ShouldEqual, "application/json")
			So(got[0].body, ShouldResemble, map[string]any{"id": "", "name": "Orders", "branch": "main", "versioning": true})
		})

		Convey("CreateVersionedKN without an id in the reply, or with another status, fails\n", func() {
			status, respBody = http.StatusCreated, `{}`
			_, err := bba.CreateVersionedKN(ctx, token, "orders", "Orders")
			So(err, ShouldNotBeNil)

			status, respBody = http.StatusOK, `{"id":"orders"}`
			_, err = bba.CreateVersionedKN(ctx, token, "orders", "Orders")
			var backendErr *interfaces.BknBackendError
			So(errors.As(err, &backendErr), ShouldBeTrue)
			So(backendErr.HTTPCode, ShouldEqual, http.StatusOK)
		})

		Convey("EnableVersioning posts the notes to the network's versioning\n", func() {
			err := bba.EnableVersioning(ctx, token, "orders", "turn on")

			So(err, ShouldBeNil)
			So(got[0].method, ShouldEqual, http.MethodPost)
			So(got[0].path, ShouldEqual, networkPath+"/versioning")
			So(got[0].body, ShouldResemble, map[string]any{"notes": "turn on"})
		})

		Convey("GetVersioning reads the running version and the draft\n", func() {
			respBody = `{"enabled":true,"running_version":3,"previous_version":2,"full_history":false,` +
				`"draft":{"base_version":3,"has_changes":true,"outdated":false,"revision":12}}`

			v, err := bba.GetVersioning(ctx, token, "orders")

			So(err, ShouldBeNil)
			So(got[0].method, ShouldEqual, http.MethodGet)
			So(got[0].path, ShouldEqual, networkPath+"/versioning")
			So(v, ShouldResemble, &interfaces.KNVersioning{Enabled: true, RunningVersion: 3, PreviousVersion: 2,
				Draft: interfaces.KNDraftState{BaseVersion: 3, HasChanges: true, Revision: 12}})
		})

		Convey("ListVersions reads page after page until a short one\n", func() {
			page := `{"entries":[`
			for i := 0; i < 100; i++ {
				if i > 0 {
					page += ","
				}
				page += `{"version":1}`
			}
			page += `]}`
			replies = []string{page, `{"entries":[{"version":2,"label":"v1.0.0","status":"running","source":{"release_id":"r1"}}]}`}

			versions, err := bba.ListVersions(ctx, token, "orders")

			So(err, ShouldBeNil)
			So(versions, ShouldHaveLength, 101)
			So(versions[100], ShouldResemble, &interfaces.KNVersion{Version: 2, Label: "v1.0.0", State: "running",
				Source: interfaces.KNVersionSource{ReleaseID: "r1"}})
			So(got, ShouldHaveLength, 2)
			So(got[0].path, ShouldEqual, networkPath+"/versions")
			So(got[0].query, ShouldEqual, "limit=100&offset=0")
			So(got[1].query, ShouldEqual, "limit=100&offset=100")
		})

		Convey("ListVersions of a network without versions is empty\n", func() {
			respBody = `{"entries":null}`

			versions, err := bba.ListVersions(ctx, token, "orders")

			So(err, ShouldBeNil)
			So(versions, ShouldNotBeNil)
			So(versions, ShouldBeEmpty)
		})

		Convey("GetVersion reads one version by number\n", func() {
			respBody = `{"version":4,"status":"history"}`

			v, err := bba.GetVersion(ctx, token, "orders", 4)

			So(err, ShouldBeNil)
			So(got[0].path, ShouldEqual, networkPath+"/versions/4")
			So(v.State, ShouldEqual, interfaces.KN_VERSION_STATE_HISTORY)
		})

		Convey("ReplaceDraft posts the files as a tar with the expected draft revision\n", func() {
			respBody = `{"kn_id":"orders","capabilities":{"bound":1,"skipped":[]}}`
			files := map[string][]byte{"network.bkn": []byte("network"), "object_types/order.bkn": []byte("order")}

			result, err := bba.ReplaceDraft(ctx, token, files, interfaces.KNReplaceImport{ExpectedDraftRevision: 7})

			So(err, ShouldBeNil)
			So(got[0].method, ShouldEqual, http.MethodPost)
			So(got[0].path, ShouldEqual, "/api/bkn-backend/v1/bkns")
			So(got[0].query, ShouldEqual, "expected_draft_revision=7&import_mode=replace")
			So(got[0].auth, ShouldEqual, token)
			So(got[0].files, ShouldResemble, map[string]string{"network.bkn": "network", "object_types/order.bkn": "order"})
			So(result.KNID, ShouldEqual, "orders")
			So(result.Capabilities.Bound, ShouldEqual, 1)
		})

		Convey("ReplaceDraft confirms overwriting unpublished edits when asked\n", func() {
			respBody = `{"kn_id":"orders"}`

			_, err := bba.ReplaceDraft(ctx, token, map[string][]byte{"network.bkn": []byte("n")},
				interfaces.KNReplaceImport{ExpectedDraftRevision: 0, Overwrite: true})

			So(err, ShouldBeNil)
			So(got[0].query, ShouldEqual, "expected_draft_revision=0&import_mode=replace&overwrite_changes=true")
		})

		Convey("ReplaceDraft keeps a revision conflict's status and code\n", func() {
			status, respBody = http.StatusConflict, `{"error_code":"BknBackend.KNVersion.DraftRevisionMismatch"}`

			_, err := bba.ReplaceDraft(ctx, token, map[string][]byte{"network.bkn": []byte("n")}, interfaces.KNReplaceImport{})

			var backendErr *interfaces.BknBackendError
			So(errors.As(err, &backendErr), ShouldBeTrue)
			So(backendErr.HTTPCode, ShouldEqual, http.StatusConflict)
			So(backendErr.ErrorCode, ShouldEqual, interfaces.KN_VERSION_ERROR_DRAFT_REVISION_MISMATCH)
		})

		Convey("ExportVersion reads the draft, the running version and a number as BKN 3.0\n", func() {
			respBody = string(tarFixture(
				tarEntry{name: "./", typeflag: tar.TypeDir},
				tarEntry{name: "./network.bkn", typeflag: tar.TypeReg, content: "network"},
				tarEntry{name: "object_types/order.bkn", typeflag: tar.TypeReg, content: "order"},
				tarEntry{name: "CHECKSUM", typeflag: tar.TypeReg, content: "# generated: now"},
			))

			files, err := bba.ExportVersion(ctx, token, "orders", interfaces.KN_VERSION_DRAFT)
			So(err, ShouldBeNil)
			So(files, ShouldResemble, map[string][]byte{
				"network.bkn":            []byte("network"),
				"object_types/order.bkn": []byte("order"),
			})
			_, err = bba.ExportVersion(ctx, token, "orders", interfaces.KN_VERSION_RUNNING)
			So(err, ShouldBeNil)
			_, err = bba.ExportVersion(ctx, token, "orders", "3")
			So(err, ShouldBeNil)

			So(got[0].method, ShouldEqual, http.MethodGet)
			So(got[0].path, ShouldEqual, "/api/bkn-backend/v1/bkns/orders")
			So(got[0].auth, ShouldEqual, token)
			So(got[0].query, ShouldEqual, "bkn_spec=3.0&branch=draft")
			So(got[1].query, ShouldEqual, "bkn_spec=3.0&branch=main")
			So(got[2].query, ShouldEqual, "bkn_spec=3.0&kn_version=3")
		})

		Convey("ExportVersion refuses an entry escaping the archive and passes an error on\n", func() {
			respBody = string(tarFixture(tarEntry{name: "../evil.bkn", typeflag: tar.TypeReg, content: "x"}))
			_, err := bba.ExportVersion(ctx, token, "orders", interfaces.KN_VERSION_DRAFT)
			So(err, ShouldNotBeNil)

			status, respBody = http.StatusNotFound, `{"error_code":"BknBackend.KNVersion.VersionPurged"}`
			_, err = bba.ExportVersion(ctx, token, "orders", "1")
			var backendErr *interfaces.BknBackendError
			So(errors.As(err, &backendErr), ShouldBeTrue)
			So(backendErr.ErrorCode, ShouldEqual, interfaces.KN_VERSION_ERROR_PURGED)
		})

		Convey("Publish posts the expectations and source and reads 201\n", func() {
			status = http.StatusCreated
			respBody = `{"version":5,"label":"v1.1.0","status":"running","warnings":[{"category":"metric","id":"m1",` +
				`"description":"late"}],"published_metrics":[{"id":"m1","version":2}],"purged_version":3}`

			published, err := bba.Publish(ctx, token, "orders", interfaces.KNVersionPublish{
				ExpectedRunningVersion: 4, ExpectedDraftRevision: 9, Notes: "n", Label: "v1.1.0",
				Source: interfaces.KNVersionSource{Type: interfaces.KN_VERSION_SOURCE_TYPE, ProjectID: "p1", ReleaseID: "r1"},
			})

			So(err, ShouldBeNil)
			So(got[0].method, ShouldEqual, http.MethodPost)
			So(got[0].path, ShouldEqual, networkPath+"/versions")
			So(got[0].body, ShouldResemble, map[string]any{
				"expected_running_version": float64(4), "expected_draft_revision": float64(9), "notes": "n", "label": "v1.1.0",
				"source": map[string]any{"type": "bkn-project", "project_id": "p1", "release_id": "r1"},
			})
			So(published.Version, ShouldEqual, 5)
			So(published.Warnings, ShouldResemble, []interfaces.KNVersionWarning{{Category: "metric", ID: "m1", Description: "late"}})
			So(published.PublishedMetrics, ShouldResemble, []interfaces.KNPublishedMetric{{ID: "m1", Version: 2}})
			So(published.PurgedVersion, ShouldEqual, 3)
		})

		Convey("Publish decodes a refusal into its code\n", func() {
			status, respBody = http.StatusConflict, `{"error_code":"BknBackend.KNVersion.NothingToPublish","description":"same"}`

			_, err := bba.Publish(ctx, token, "orders", interfaces.KNVersionPublish{})

			var backendErr *interfaces.BknBackendError
			So(errors.As(err, &backendErr), ShouldBeTrue)
			So(backendErr.HTTPCode, ShouldEqual, http.StatusConflict)
			So(backendErr.ErrorCode, ShouldEqual, interfaces.KN_VERSION_ERROR_NOTHING_TO_PUBLISH)
		})

		Convey("Activate posts the expected running version to the version's activate\n", func() {
			respBody = `{"running_version":2,"warnings":[]}`

			activated, err := bba.Activate(ctx, token, "orders", 2, 3, "back")

			So(err, ShouldBeNil)
			So(got[0].path, ShouldEqual, networkPath+"/versions/2/activate")
			So(got[0].body, ShouldResemble, map[string]any{"expected_running_version": float64(3), "notes": "back"})
			So(activated.RunningVersion, ShouldEqual, 2)
		})

		Convey("ResetDraft posts the version and revision to draft/reset\n", func() {
			err := bba.ResetDraft(ctx, token, "orders", 2, 11, true)

			So(err, ShouldBeNil)
			So(got[0].path, ShouldEqual, networkPath+"/draft/reset")
			So(got[0].body, ShouldResemble, map[string]any{
				"from_version": float64(2), "expected_draft_revision": float64(11), "overwrite_changes": true,
			})
		})

		Convey("ReferencedResources returns the resource ids\n", func() {
			respBody = `{"entries":[{"id":"ds1"},{"id":"ds2"}]}`

			ids, err := bba.ReferencedResources(ctx, token, "orders")

			So(err, ShouldBeNil)
			So(got[0].method, ShouldEqual, http.MethodGet)
			So(got[0].path, ShouldEqual, networkPath+"/versions/referenced-resources")
			So(ids, ShouldResemble, []string{"ds1", "ds2"})
		})

		Convey("A reply that is not JSON fails, and a non-JSON error still keeps its status\n", func() {
			respBody = `not json`
			_, err := bba.GetVersioning(ctx, token, "orders")
			So(err, ShouldNotBeNil)

			status = http.StatusBadGateway
			_, err = bba.GetVersioning(ctx, token, "orders")
			var backendErr *interfaces.BknBackendError
			So(errors.As(err, &backendErr), ShouldBeTrue)
			So(backendErr.HTTPCode, ShouldEqual, http.StatusBadGateway)
			So(backendErr.ErrorCode, ShouldBeEmpty)
		})
	})
}
