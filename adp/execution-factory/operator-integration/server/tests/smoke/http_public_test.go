package smoke

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/utils"
)

type ExecuteToolReq struct {
	Headers     map[string]string `json:"header"`
	Body        interface{}       `json:"body"`
	QueryParams map[string]string `json:"query"`
	PathParams  map[string]string `json:"path"`
}

func TestPublicSmokeExecuteTool(t *testing.T) {
	local := "/root/go/src/github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/tests/file/auth.json"
	client := NewPublicSmokeClient("127.0.0.1:9000")
	data, err := os.ReadFile(local)
	if err != nil {
		fmt.Printf("ReadFile failed: %v", err)
		return
	}
	token := "ory_at_RvKfV0CHK_TCxoa8mXZj5lT0Zw5WSfHe0ot1GuasEBY.FLajysFAPyUyQesRPbIHOROB9GAO1QyE1__57ztdw9U"
	reqBody := map[string]interface{}{
		"metadata_type": "openapi",
		"data":          data,
	}
	reqHeaders := map[string]string{
		"Content-Type": "multipart/form-data",
	}
	reqHeaders["Authorization"] = fmt.Sprintf("Bearer %s", token)
	req := &ExecuteToolReq{
		Body:    reqBody,
		Headers: reqHeaders,
	}
	result, err := client.ExecuteTool(context.Background(), "49d0ac32-0976-4560-a6b8-72dd232b543b", "c2395a45-fdf0-49f8-a996-8d51762753eb", req, token)
	if err != nil {
		fmt.Printf("ExecuteTool failed: %v", err)
		return
	}
	fmt.Println(utils.ObjectToJSON(result))
}
