

# 执行单元测试：
# 设置环境变量
export AUDIT_MODE_UT=true
export VERIFY_MODE_UT=true
export I18N_MODE_UT=true
export POD_IP='1.2.3.4'

mkdir report
go test -gcflags=all=-l -v -coverprofile=report/ut_coverage.out ./...
go tool cover --html=report/ut_coverage.out -o report/ut_coverage.html
golangci-lint run --out-format junit-xml ./...



过滤器结构体数据结构:

type FilterExpress struct {
	Name      string `json:"name" binding:"required_with=Value,omitempty"`
	Value     any    `json:"value" binding:"required_with=Name,omitempty"`
	Operation string `json:"operation" binding:"required_with=Name,omitempty"`
}

type logicFilter struct {
	LogicOperator string        `json:"logic_operator" binding:"omitempty"`
	FilterExpress FilterExpress `json:"filter_express"`
	Children      []logicFilter `json:"children" binding:"required_with=LogicOperator,omitempty"`
}

type FormulaItem struct {
	Level  int         `json:"level" binding:"required_with=Filter,omitempty,oneof=1 2 3 4 5 6"`
	Filter logicFilter `json:"filter" binging:"required_with=Level,omitempty"`
}




严重: (z>5) and (x<3 or y>4)

{
	"level": 1,
	"filter": {
		"logic_operator": "and",
		"filter_express": null,
		"children": [
			{
				"logic_operator": null,
				"filter_express": {
					"name": "z",
					"value": 5,
					"operation": ">"
				},
				"children": []
			},
			{
				"logic_operator": "or",
				"filter_express": {},
				"children": [
					{
						"logic_operator": null,
						"filter_express": {
							"name": "x",
							"value": 3,
							"operation": "c"
						},
						"children": []
					},
					{
						"logic_operator": null,
						"filter_express": {
							"name": "y",
							"value": 4,
							"operation": ">"
						},
						"children": []
					}
				]
			}
		]
	}
}


#