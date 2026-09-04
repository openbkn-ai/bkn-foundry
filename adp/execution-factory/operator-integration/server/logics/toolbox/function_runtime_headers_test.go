package toolbox

import (
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	. "github.com/smartystreets/goconvey/convey"
)

// A managed call hands the Function the caller it authenticated and the
// Interaction it belongs to, so the code can read BKN as that principal.
func TestFunctionRuntimeHeadersForwardsAManagedCall(t *testing.T) {
	Convey("Function tool receives the caller and its managed Interaction", t, func() {
		headers := functionRuntimeHeaders(
			map[string]any{"X-Api-Key": "tool-own-key"},
			&interfaces.ExecuteToolReq{
				RequestAuthorization: "Bearer caller-token",
				BKNConversationID:    "conv_1",
				BKNInteractionID:     "int_1",
			},
		)

		So(headers["Authorization"], ShouldEqual, "Bearer caller-token")
		So(headers["bkn-conversation-id"], ShouldEqual, "conv_1")
		So(headers["bkn-interaction-id"], ShouldEqual, "int_1")
		So(headers["X-Api-Key"], ShouldEqual, "tool-own-key")
	})
}

// A credential without the lifecycle guard it belongs to must not reach a
// pooled sandbox, so a partial context forwards nothing at all.
func TestFunctionRuntimeHeadersRefusesAPartialContext(t *testing.T) {
	Convey("An incomplete managed context forwards neither credential nor ids", t, func() {
		for _, req := range []*interfaces.ExecuteToolReq{
			{RequestAuthorization: "Bearer caller-token", BKNConversationID: "conv_1"},
			{RequestAuthorization: "Bearer caller-token", BKNInteractionID: "int_1"},
			{BKNConversationID: "conv_1", BKNInteractionID: "int_1"},
			{},
			nil,
		} {
			headers := functionRuntimeHeaders(map[string]any{"X-Api-Key": "tool-own-key"}, req)

			So(headers["Authorization"], ShouldBeNil)
			So(headers["bkn-conversation-id"], ShouldBeNil)
			So(headers["bkn-interaction-id"], ShouldBeNil)
			So(headers["X-Api-Key"], ShouldEqual, "tool-own-key")
		}
	})
}

// The captured values are server-owned. A Tool body that names them would be
// naming whose credential and whose Interaction the Function runs under.
func TestFunctionRuntimeHeadersOverridesBodySuppliedValues(t *testing.T) {
	Convey("Server-captured context wins over anything the Tool body stated", t, func() {
		headers := functionRuntimeHeaders(
			map[string]any{
				"Authorization":       "Bearer body-token",
				"bkn-conversation-id": "conv_body",
				"bkn-interaction-id":  "int_body",
			},
			&interfaces.ExecuteToolReq{
				RequestAuthorization: "Bearer caller-token",
				BKNConversationID:    "conv_1",
				BKNInteractionID:     "int_1",
			},
		)

		So(headers["Authorization"], ShouldEqual, "Bearer caller-token")
		So(headers["bkn-conversation-id"], ShouldEqual, "conv_1")
		So(headers["bkn-interaction-id"], ShouldEqual, "int_1")
	})
}

// SourceTypeFunction is not proof that the target is this deployment's runtime.
// Registration pins the address, but import takes metadata.server_url from the
// payload verbatim, so an imported Function can name any host. Forwarding the
// caller's live credential there would hand an arbitrary external endpoint the
// token of whoever invoked the tool.
func TestOnlyThisDeploymentsFunctionRuntimeIsAPlatformTarget(t *testing.T) {
	Convey("The configured function-exec route is a platform target", t, func() {
		So(isPlatformFunctionTarget(
			interfaces.AOIServerURL+interfaces.SetAOIFuncExecPath("v1")), ShouldBeTrue)
	})

	Convey("An imported Function pointing anywhere else is not", t, func() {
		for _, rawURL := range []string{
			"http://attacker.example.com" + interfaces.SetAOIFuncExecPath("v1"),
			"https://agent-operator-integration:9000" + interfaces.SetAOIFuncExecPath("v1"),
			"http://agent-operator-integration:9001" + interfaces.SetAOIFuncExecPath("v1"),
			// Right host, but not the function-exec route.
			interfaces.AOIServerURL + "/api/agent-operator-integration/v1/tool-box/b/proxy/t",
			"://not-a-url",
			"",
		} {
			So(isPlatformFunctionTarget(rawURL), ShouldBeFalse)
		}
	})
}

// The caller's map must not be mutated: the same request parameters are read
// again by the audit and retry paths.
func TestFunctionRuntimeHeadersDoesNotMutateTheInput(t *testing.T) {
	Convey("The original header map is left untouched", t, func() {
		original := map[string]any{"X-Api-Key": "tool-own-key"}

		functionRuntimeHeaders(original, &interfaces.ExecuteToolReq{
			RequestAuthorization: "Bearer caller-token",
			BKNConversationID:    "conv_1",
			BKNInteractionID:     "int_1",
		})

		So(len(original), ShouldEqual, 1)
		So(original["Authorization"], ShouldBeNil)
	})
}
