// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package locale

import "github.com/openbkn-ai/bkn-foundry/comm-go/i18n"

var cypherEnUS = i18n.I18nMessages{
	Locale: "en-US",
	Messages: map[string]string{
		"BknBackend.Cypher.InvalidParameter":                             "Invalid Cypher query request. Please check the request body and query parameters.",
		"BknBackend.Cypher.SyntaxError":                                  "The Cypher query could not be parsed. Please correct the query at the reported position.",
		"BknBackend.Cypher.Unsupported":                                  "The Cypher query uses a construct this interface does not support. Please rewrite the query using the supported read-only subset.",
		"BknBackend.Cypher.InvalidQuery":                                 "The Cypher query does not match the knowledge network model. Please check the labels, relationship types and properties against the model.",
		"BknBackend.Cypher.LimitExceeded":                                "The requested LIMIT is above the maximum this interface returns. Please lower LIMIT and page through the result.",
		"BknBackend.Cypher.InternalError":                                "Failed to compile the Cypher query. Please contact the administrator.",
		"BknBackend.Cypher.QueryFailed":                                  "Failed to run the compiled query. Please retry later or contact the administrator.",
		"BknBackend.Cypher.Forbidden":                                    "The data source refused a resource the query reads. Please ask the administrator to publish the knowledge network and synchronize its proxy account, or to grant access to the resources behind these object types.",
		"BknBackend.Cypher.PropertyForbidden":                            "The query reads a property you may not read in full. Please remove the listed properties from the query or ask the administrator for full access to them.",
		"BknBackend.Cypher.ReadBudgetExceeded":                           "The query would read more rows than one execution may. Please add conditions on the nodes the pattern matches so that fewer rows are read.",
		"BknBackend.Cypher.PropertyForbidden.Detail.NotFull":             "Full access is required to every property the query reads; it is not granted for: {{.properties}}.",
		"BknBackend.Cypher.InvalidParameter.Detail.NonSQLSource":         "The query reads object types stored in a dataset or an index, or in more than one data source. Such data takes the basic forms only: MATCH, OPTIONAL MATCH, WHERE, returning properties and aggregates, ORDER BY / SKIP / LIMIT, and WITH to filter after aggregating or by an earlier stage's values. CASE, coalesce, totals over groups, comparing two lists, passing nodes on to the next MATCH, and collect are not available there (returning nodes and relationships is); rewrite the query without them.",
		"BknBackend.Cypher.InvalidParameter.Detail.NonSQLSourceTooLarge": "The query reads object types stored in a dataset or an index, or in more than one data source, which are read table by table and joined; this one reads more than the limit allows or runs out of time, so narrow it with conditions.",
		"BknBackend.Cypher.InvalidParameter.Detail.CartesianProduct":     "The pattern has parts that nothing connects, and at least two of them have no condition, so the result would pair every row of one table with every row of another. Connect them with a relation type or an equality such as a.code = b.code, or add a condition to one side.",
		"BknBackend.Cypher.InvalidParameter.Detail.QueryRequired":        "query is required.",
		"BknBackend.Cypher.InvalidParameter.Detail.QueryTooLong":         "query must not exceed {{.max}} characters.",
		"BknBackend.Cypher.InvalidParameter.Detail.LimitExceeded":        "LIMIT must not exceed {{.max}}.",
	},
}
