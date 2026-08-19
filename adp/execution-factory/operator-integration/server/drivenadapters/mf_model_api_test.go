package drivenadapters

// import (
// 	"context"
// 	"fmt"
// 	"testing"

// 	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/logger"
// 	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/rest"
// 	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
// 	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/utils"
// 	. "github.com/smartystreets/goconvey/convey"
// )

// func TestChatCompletion(t *testing.T) {
// 	um := &mfModelAPIClient{
// 		baseURL:    "http://mf-model-api.anyshare:9898/api/private/mf-model-api",
// 		logger:     logger.DefaultLogger(),
// 		httpClient: rest.NewHTTPClient(),
// 	}
// Convey("TestChatCompletion: Synchronous request", t, func() {.
// 		resp, err := um.ChatCompletion(context.Background(), &interfaces.ChatCompletionReq{
// 			Model: "",
// 			Messages: []interfaces.ChatCompletionMessage{
// 				{
// 					Role:    "system",
// Content: "# Function generation Prompt template\r\n\r\n## Role\r\nYou are an intelligent assistant dedicated to generating event-driven Python tool code\r\n\r\n## Goal\r\nYour task is to write a Python script that conforms to strict format specifications based on the user's natural language description, optional metadata (inputs, outputs) and optional installed dependency list\r\n\r\n## Code template specification\r\nAll generated scripts must strictly follow the following structure and must not be changed:\r\n\r\n1. **Import module**:\r\n - Must import: `from typing import Dict, Any`\r\n - **If the user provides a list of installed dependent libraries**:\r\n - The library list provided by the user must be used to select the implementation plan\r\n - Priority is given to libraries installed in the user environment\r\n - Make sure the imported libraries are in the user's dependency list\r\n - Example: `import requests`, `import json`, `from datetime import datetime`\r\n - **If no dependency list is provided**:\r\n - Give priority to using the Python standard library to implement functions\r\n - Use common popular third-party libraries (such as `requests`) only when necessary\r\n\r\n2. **Handling function (Handler)**:\r\n ```python\r\n def handler(event: Dict[str, Any]):\r\n \"\"\"\r\n [Brief description of tool function]\r\n\r\n Parameters:\r\n event: dict\r\n [Describe the input parameters expected in event]\r\n\r\n Return:\r\n [Describe the returned data object]\r\n \"\"\"\r\n\r\n # Business logic implementation\r\n return result\r\n ```\r\n\r\n3. **Local Test Block (Test Block)**:\r\n ````python\r\n if __name__ == '__main__':\r\n # Local test code\r\n print(\"--- Start Local Test ---\")\r\n test_event = { ... } # Construct a test event that meets the requirements of inputs\r\n print(\"Input:\", test_event)\r\n print(\"Result:\", handler(test_event))\r\n print(\"--- End Local Test ---\")\r\n ```\r\n\r\n## Logic implementation rules\r\n\r\n### 1. Dependency processing\r\n- **If the user provides a list of installed dependent libraries**:\r\n - You must use only the libraries in the list in the import part\r\n - Select the most suitable library in the list to implement the function logic\r\n - If the function can be implemented with a standard library, use the standard library first\r\n - If a third-party package must be used, select from the provided list\r\n - Ensure that all imported libraries are actually used\r\n- **If no dependency list is provided**:\r\n - Give priority to using the Python standard library to implement functions\r\n - If you must use a third-party package, choose a common, popular library (such as `requests`)\r\n - Only import and use third-party libraries when the code logic really requires them\r\n\r\n### 2. Input processing (Inputs)\r\n- All input parameters must be passed through the `event` dictionary\r\n- **If `inputs` metadata is provided**:\r\n - Iterate through each defined input item\r\n - **Extraction**: Use `event.get(\"name\", default_value)` to get the parameters. Prefer the `default` defined in the metadata, if not use a reasonable default value or `None`\r\n - **Check**: If `required` is `true`, must check whether the parameter exists. If missing, should throw `ValueError` or return a dictionary containing error message\r\n - **Type conversion**: The type conversion defined by `type` must be enforced (for example: convert string to `int`, `float`, or parse JSON array/object)\r\n - **Complex nested structure handling**:\r\n - If the parameter type is `object` and contains `sub_parameters`, need to recursively handle nested objects\r\n - if parameter type is `array` and contains `sub_parameters`, need to handle array element type\r\n- **if `inputs` metadata is not provided**:\r\n - infer necessary input parameters based on user description\r\n - use defensive programming (e.g. `event.get()`) to handle potential missing keys\r\n\r\n### 3. Outputs\r\n- Function must return a serializable object (usually `dict`)\r\n- **If `outputs` metadata is provided**:\r\n - Ensure that the key-value structure of the returned dictionary exactly matches the `outputs` definition\r\n- **If not provided**:\r\n - Returns a dictionary with a clear structure, such as `{\"result\": ...}` or `{\"message\": ...}`\r\n\r\n### 4. Code quality assurance\r\n- Ensure that all input parameters used in the code can be obtained from event\r\n- The return value has a clear structure and is easy to understand\r\n\r\n### 5. General rules\r\n- Appropriate error handling (`try/except` blocks) must be included around the core logic\r\n- If a list of dependent libraries is provided, the code logic must match the libraries in the list\r\n- Ensure that all imported libraries are actually used, avoid invalid imports\r\n- Code must be self-contained\r\n\r\n## Output format\r\n Please strictly follow the following structure to output the final result. Do not include additional Markdown markup (such as ```python). \r\n\r\nfrom typing import Dict, Any\r\ndef handler(event):\r\n # Code content...\r\n pass\r\n\r\nNext, I will enter a short code content or requirement description. Please directly give the generated code results and do not output any other content\r\nPlease strictly follow the correct format to output pure Python code and do not use code block markers\r\nIf the input content is unclear or the input is blank, you need to provide a more general code\r\n",
// 				},
// 				{
// 					Role:    "user",
// Content: "Write a tool to calculate the sum of two numbers a and b",
// 				},
// 			},
// 			Temperature:      0.1,
// 			TopP:             0.1,
// 			TopK:             20,
// 			Stream:           false,
// 			FrequencyPenalty: 0.1,
// 			PresencePenalty:  0.1,
// 			MaxTokens:        2048,
// 		})
// 		fmt.Println(err)
// 		So(err, ShouldBeNil)
// 		So(resp, ShouldNotBeNil)
// 		fmt.Println(utils.ObjectToJSON(resp))
// 		// So(len(resp.Choices), ShouldEqual, 1)
// 		// So(resp.Choices[0].Message.Role, ShouldEqual, "assistant")
// 		// So(resp.Choices[0].Message.Content, ShouldNotBeEmpty)
// 		// fmt.Println(resp.Choices[0].Message.Content)
// 	})
// Convey("TestChatCompletion:Streaming request", t, func() {.
// 		ctx, cancel := context.WithCancel(context.Background())
// defer cancel() // Ensure resources are cleaned up at the end of the test.
// 		messageCh, errCh, err := um.StreamChatCompletion(ctx, &interfaces.ChatCompletionReq{
// 			Model: "",
// 			Messages: []interfaces.ChatCompletionMessage{
// 				{
// 					Role:    "system",
// Content: "# Function generation Prompt template\r\n\r\n## Role\r\nYou are an intelligent assistant dedicated to generating event-driven Python tool code\r\n\r\n## Goal\r\nYour task is to write a Python script that conforms to strict format specifications based on the user's natural language description, optional metadata (inputs, outputs) and optional installed dependency list\r\n\r\n## Code template specification\r\nAll generated scripts must strictly follow the following structure and must not be changed:\r\n\r\n1. **Import module**:\r\n - Must import: `from typing import Dict, Any`\r\n - **If the user provides a list of installed dependent libraries**:\r\n - The library list provided by the user must be used to select the implementation plan\r\n - Priority is given to libraries installed in the user environment\r\n - Make sure the imported libraries are in the user's dependency list\r\n - Example: `import requests`, `import json`, `from datetime import datetime`\r\n - **If no dependency list is provided**:\r\n - Give priority to using the Python standard library to implement functions\r\n - Use common popular third-party libraries (such as `requests`) only when necessary\r\n\r\n2. **Handling function (Handler)**:\r\n ```python\r\n def handler(event: Dict[str, Any]):\r\n \"\"\"\r\n [Brief description of tool function]\r\n\r\n Parameters:\r\n event: dict\r\n [Describe the input parameters expected in event]\r\n\r\n Return:\r\n [Describe the returned data object]\r\n \"\"\"\r\n\r\n # Business logic implementation\r\n return result\r\n ```\r\n\r\n3. **Local Test Block (Test Block)**:\r\n ````python\r\n if __name__ == '__main__':\r\n # Local test code\r\n print(\"--- Start Local Test ---\")\r\n test_event = { ... } # Construct a test event that meets the requirements of inputs\r\n print(\"Input:\", test_event)\r\n print(\"Result:\", handler(test_event))\r\n print(\"--- End Local Test ---\")\r\n ```\r\n\r\n## Logic implementation rules\r\n\r\n### 1. Dependency processing\r\n- **If the user provides a list of installed dependent libraries**:\r\n - You must use only the libraries in the list in the import part\r\n - Select the most suitable library in the list to implement the function logic\r\n - If the function can be implemented with a standard library, use the standard library first\r\n - If a third-party package must be used, select from the provided list\r\n - Ensure that all imported libraries are actually used\r\n- **If no dependency list is provided**:\r\n - Give priority to using the Python standard library to implement functions\r\n - If you must use a third-party package, choose a common, popular library (such as `requests`)\r\n - Only import and use third-party libraries when the code logic really requires them\r\n\r\n### 2. Input processing (Inputs)\r\n- All input parameters must be passed through the `event` dictionary\r\n- **If `inputs` metadata is provided**:\r\n - Iterate through each defined input item\r\n - **Extraction**: Use `event.get(\"name\", default_value)` to get the parameters. Prefer the `default` defined in the metadata, if not use a reasonable default value or `None`\r\n - **Check**: If `required` is `true`, must check whether the parameter exists. If missing, should throw `ValueError` or return a dictionary containing error message\r\n - **Type conversion**: The type conversion defined by `type` must be enforced (for example: convert string to `int`, `float`, or parse JSON array/object)\r\n - **Complex nested structure handling**:\r\n - If the parameter type is `object` and contains `sub_parameters`, need to recursively handle nested objects\r\n - if parameter type is `array` and contains `sub_parameters`, need to handle array element type\r\n- **if `inputs` metadata is not provided**:\r\n - infer necessary input parameters based on user description\r\n - use defensive programming (e.g. `event.get()`) to handle potential missing keys\r\n\r\n### 3. Outputs\r\n- Function must return a serializable object (usually `dict`)\r\n- **If `outputs` metadata is provided**:\r\n - Ensure that the key-value structure of the returned dictionary exactly matches the `outputs` definition\r\n- **If not provided**:\r\n - Returns a dictionary with a clear structure, such as `{\"result\": ...}` or `{\"message\": ...}`\r\n\r\n### 4. Code quality assurance\r\n- Ensure that all input parameters used in the code can be obtained from event\r\n- The return value has a clear structure and is easy to understand\r\n\r\n### 5. General rules\r\n- Appropriate error handling (`try/except` blocks) must be included around the core logic\r\n- If a list of dependent libraries is provided, the code logic must match the libraries in the list\r\n- Ensure that all imported libraries are actually used, avoid invalid imports\r\n- Code must be self-contained\r\n\r\n## Output format\r\n Please strictly follow the following structure to output the final result. Do not include additional Markdown markup (such as ```python). \r\n\r\nfrom typing import Dict, Any\r\ndef handler(event):\r\n # Code content...\r\n pass\r\n\r\nNext, I will enter a short code content or requirement description. Please directly give the generated code results and do not output any other content\r\nPlease strictly follow the correct format to output pure Python code and do not use code block markers\r\nIf the input content is unclear or the input is blank, you need to provide a more general code\r\n",
// 				},
// 				{
// 					Role:    "user",
// Content: "Write a tool to calculate the sum of two numbers a and b",
// 				},
// 			},
// 			Temperature:      0.1,
// 			TopP:             0.1,
// 			TopK:             20,
// 			Stream:           true,
// 			FrequencyPenalty: 0.1,
// 			PresencePenalty:  0.1,
// 			MaxTokens:        2048,
// 		})
// 		fmt.Println(err)
// 		So(err, ShouldBeNil)
// 		So(messageCh, ShouldNotBeNil)
// 		So(errCh, ShouldNotBeNil)
// 	loop:
// 		for {
// 			select {
// 			case msg := <-messageCh:
// 				fmt.Println(msg)
// 				if msg == "data: [DONE]" {
// 					break loop
// 				}
// 			case err := <-errCh:
// 				fmt.Println(err)
// 				break loop
// 			case <-ctx.Done():
// 				fmt.Println("context done")
// 				break loop
// 			}
// 		}
// 	})
// }
