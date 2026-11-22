// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package cypher

import (
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

// sharedNameSchema is a model whose relation types share names, as relation
// types named by the role their target plays do: an order and an invoice each
// have a CUSTOMER, an order has two relations named PARTY to the same customer,
// and REFERS runs both ways between an order and an invoice.
func sharedNameSchema(t *testing.T) *Schema {
	t.Helper()

	order := objectType("ot_order", "Order", resource("res_order", "orders"),
		dataProperty("id", "f_id"),
		dataProperty("customer_code", "f_cust_code"),
		dataProperty("ship_to_code", "f_ship_code"),
		dataProperty("invoice_id", "f_invoice"),
	)
	order.PrimaryKeys = []string{"id"}
	invoice := objectType("ot_invoice", "Invoice", resource("res_invoice", "invoices"),
		dataProperty("id", "f_id"),
		dataProperty("buyer_code", "f_buyer"),
		dataProperty("order_id", "f_order"),
	)
	invoice.PrimaryKeys = []string{"id"}
	customer := objectType("ot_customer", "Customer", resource("res_customer", "customers"),
		dataProperty("code", "f_code"),
	)
	customer.PrimaryKeys = []string{"code"}

	relation := func(id, name, source, target, sourceProp, targetProp string) *interfaces.RelationType {
		rt := relationType(id, name)
		rt.SourceObjectTypeID = source
		rt.TargetObjectTypeID = target
		rt.MappingRules = []interfaces.Mapping{{
			SourceProp: interfaces.SimpleProperty{Name: sourceProp},
			TargetProp: interfaces.SimpleProperty{Name: targetProp},
		}}
		return rt
	}

	return testSchema(t, &fakeSchemaSource{
		objectTypes: []*interfaces.ObjectType{order, invoice, customer},
		// The invoice's CUSTOMER is loaded last, so a name index that keeps
		// one relation type per name keeps it and loses the order's.
		relationTypes: []*interfaces.RelationType{
			relation("rt_order_customer", "CUSTOMER", "ot_order", "ot_customer", "customer_code", "code"),
			relation("rt_bill_to", "PARTY", "ot_order", "ot_customer", "customer_code", "code"),
			relation("rt_ship_to", "PARTY", "ot_order", "ot_customer", "ship_to_code", "code"),
			relation("rt_order_refers", "REFERS", "ot_order", "ot_invoice", "invoice_id", "id"),
			relation("rt_invoice_refers", "REFERS", "ot_invoice", "ot_order", "order_id", "id"),
			relation("rt_invoice_customer", "CUSTOMER", "ot_invoice", "ot_customer", "buyer_code", "code"),
		},
	})
}

func compileShared(t *testing.T, query string) (string, error) {
	t.Helper()
	tree, err := Parse(query)
	if err != nil {
		t.Fatalf("Parse(%q): %v", query, err)
	}
	analyzed, err := Analyze(tree)
	if err != nil {
		return "", err
	}
	plan, err := Compile(analyzed, sharedNameSchema(t), CompileOptions{})
	if err != nil {
		return "", err
	}
	return Generate(plan, GenerateOptions{})
}

// A name several relation types share resolves to the one that joins the
// pattern's object types the way its arrow reads.
func TestCompileSharedRelationName(t *testing.T) {
	const orderJoin = "JOIN {{res_customer}} t1 ON t0.`f_cust_code` = t1.`f_code`"
	for _, tc := range []struct {
		name  string
		query string
		want  string
	}{
		{
			name:  "from the order",
			query: "MATCH (o:Order)-[:CUSTOMER]->(c:Customer) RETURN o.id AS id",
			want:  "SELECT t0.`f_id` AS `id` FROM {{res_order}} t0 " + orderJoin,
		},
		{
			name:  "from the invoice",
			query: "MATCH (i:Invoice)-[:CUSTOMER]->(c:Customer) RETURN i.id AS id",
			want: "SELECT t0.`f_id` AS `id` FROM {{res_invoice}} t0 " +
				"JOIN {{res_customer}} t1 ON t0.`f_buyer` = t1.`f_code`",
		},
		{
			name:  "written backwards",
			query: "MATCH (c:Customer)<-[:CUSTOMER]-(o:Order) RETURN o.id AS id",
			want: "SELECT t1.`f_id` AS `id` FROM {{res_customer}} t0 " +
				"JOIN {{res_order}} t1 ON t0.`f_code` = t1.`f_cust_code`",
		},
		{
			name:  "undirected, where one relation type fits",
			query: "MATCH (o:Order)-[:CUSTOMER]-(c:Customer) RETURN o.id AS id",
			want:  "SELECT t0.`f_id` AS `id` FROM {{res_order}} t0 " + orderJoin,
		},
		{
			name:  "by id",
			query: "MATCH (o:Order)-[:rt_order_customer]->(c:Customer) RETURN o.id AS id",
			want:  "SELECT t0.`f_id` AS `id` FROM {{res_order}} t0 " + orderJoin,
		},
		{
			name:  "opposite ways, settled by the arrow",
			query: "MATCH (i:Invoice)<-[:REFERS]-(o:Order) RETURN o.id AS id",
			want: "SELECT t1.`f_id` AS `id` FROM {{res_invoice}} t0 " +
				"JOIN {{res_order}} t1 ON t0.`f_id` = t1.`f_invoice`",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := compileShared(t, tc.query)
			if err != nil {
				t.Fatalf("compile(%q): %v", tc.query, err)
			}
			if got != tc.want {
				t.Fatalf("got  %s\nwant %s", got, tc.want)
			}
		})
	}
}

// A shared name is refused, not guessed, when no relation type of that name
// fits the pattern or more than one does; the error names the candidates.
func TestCompileSharedRelationNameRefusals(t *testing.T) {
	for _, tc := range []struct {
		name  string
		query string
		want  []string
	}{
		{
			name:  "none connects",
			query: "MATCH (o:Order)-[:CUSTOMER]->(i:Invoice) RETURN o.id",
			want: []string{`no relation type named "CUSTOMER" connects`,
				`"rt_invoice_customer" (Invoice -> Customer)`, `"rt_order_customer" (Order -> Customer)`},
		},
		{
			name:  "two with the same ends",
			query: "MATCH (o:Order)-[:PARTY]->(c:Customer) RETURN o.id",
			want: []string{`relationship type "PARTY" is ambiguous`,
				`"rt_bill_to" (Order -> Customer), "rt_ship_to" (Order -> Customer)`, "use the id"},
		},
		{
			name:  "undirected over two running opposite ways",
			query: "MATCH (o:Order)-[:REFERS]-(i:Invoice) RETURN o.id",
			want: []string{`relationship type "REFERS" is ambiguous`,
				`"rt_invoice_refers" (Invoice -> Order), "rt_order_refers" (Order -> Invoice)`, "write the direction"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := compileShared(t, tc.query)
			if err == nil {
				t.Fatalf("compile(%q) succeeded, want a refusal", tc.query)
			}
			for _, want := range tc.want {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("compile(%q) = %v, want it to mention %q", tc.query, err, want)
				}
			}
		})
	}
}

// The rule against an undirected hop beside another over the same relation
// type compares the relation types the hops resolved to: a shared name is not
// the same relation type, and one relation type written by id and by name is.
func TestCompileUndirectedRepeatsCompareRelationTypes(t *testing.T) {
	t.Run("two relation types sharing a name", func(t *testing.T) {
		query := "MATCH (o:Order)-[:CUSTOMER]-(c:Customer)<-[:CUSTOMER]-(i:Invoice) RETURN o.id AS id"
		want := "SELECT t0.`f_id` AS `id` FROM {{res_order}} t0 " +
			"JOIN {{res_customer}} t1 ON t0.`f_cust_code` = t1.`f_code` " +
			"JOIN {{res_invoice}} t2 ON t1.`f_code` = t2.`f_buyer`"
		got, err := compileShared(t, query)
		if err != nil {
			t.Fatalf("compile(%q): %v", query, err)
		}
		if got != want {
			t.Fatalf("got  %s\nwant %s", got, want)
		}
	})

	t.Run("one relation type by id and by name", func(t *testing.T) {
		query := "MATCH (a:Order)-[:CUSTOMER]-(c:Customer)<-[:rt_order_customer]-(b:Order) RETURN a.id"
		_, err := compileShared(t, query)
		if err == nil || !strings.Contains(err.Error(), "cannot have one of them undirected") {
			t.Fatalf("compile(%q) = %v, want the undirected repeat refused", query, err)
		}
	})
}
