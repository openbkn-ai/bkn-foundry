// Code generated from SqlBase.g4 by ANTLR 4.13.2. DO NOT EDIT.

package generated // SqlBase
import (
	"fmt"
	"strconv"
	"sync"

	"github.com/antlr4-go/antlr/v4"
)

// Suppress unused import errors
var _ = fmt.Printf
var _ = strconv.Itoa
var _ = sync.Once{}

type SqlBaseParser struct {
	*antlr.BaseParser
}

var SqlBaseParserStaticData struct {
	once                   sync.Once
	serializedATN          []int32
	LiteralNames           []string
	SymbolicNames          []string
	RuleNames              []string
	PredictionContextCache *antlr.PredictionContextCache
	atn                    *antlr.ATN
	decisionToDFA          []*antlr.DFA
}

func sqlbaseParserInit() {
	staticData := &SqlBaseParserStaticData
	staticData.LiteralNames = []string{
		"", "'SELECT'", "'UNION'", "'ALL'", "'DISTINCT'", "'FROM'", "'WHERE'",
		"'GROUP'", "'BY'", "'HAVING'", "'ORDER'", "'LIMIT'", "'OFFSET'", "'AS'",
		"'INNER'", "'LEFT'", "'RIGHT'", "'OUTER'", "'JOIN'", "'CROSS'", "'ON'",
		"'ASC'", "'DESC'", "'OR'", "'AND'", "'NOT'", "'IS'", "'NULL'", "'BETWEEN'",
		"'IN'", "'LIKE'", "'ESCAPE'", "'CAST'", "'CASE'", "'WHEN'", "'THEN'",
		"'ELSE'", "'END'", "'TRUE'", "'FALSE'", "'CURRENT_TIMESTAMP'", "", "",
		"'('", "')'", "','", "'.'", "';'", "'='", "", "'<='", "'>='", "'<'",
		"'>'", "'+'", "'-'", "'*'", "'/'", "'%'",
	}
	staticData.SymbolicNames = []string{
		"", "SELECT", "UNION", "ALL", "DISTINCT", "FROM", "WHERE", "GROUP",
		"BY", "HAVING", "ORDER", "LIMIT", "OFFSET", "AS", "INNER", "LEFT", "RIGHT",
		"OUTER", "JOIN", "CROSS", "ON", "ASC", "DESC", "OR", "AND", "NOT", "IS",
		"NULL", "BETWEEN", "IN", "LIKE", "ESCAPE", "CAST", "CASE", "WHEN", "THEN",
		"ELSE", "END", "TRUE", "FALSE", "CURRENT_TIMESTAMP", "UNSUPPORTED",
		"RESOURCE", "LPAREN", "RPAREN", "COMMA", "DOT", "SEMICOLON", "EQ", "NEQ",
		"LTE", "GTE", "LT", "GT", "PLUS", "MINUS", "STAR", "SLASH", "PERCENT",
		"NUMBER", "STRING", "QUOTED_IDENTIFIER", "IDENTIFIER", "FORBIDDEN_COMMENT",
		"BLOCK_COMMENT", "LINE_COMMENT", "WS",
	}
	staticData.RuleNames = []string{
		"statement", "queryExpression", "queryTerm", "unionOperator", "selectQuery",
		"selectItem", "relation", "relationPrimary", "joinPart", "sortItem",
		"limitClause", "expressionList", "expression", "orExpression", "andExpression",
		"notExpression", "predicate", "additiveExpression", "multiplicativeExpression",
		"unaryExpression", "primaryExpression", "functionCall", "typeName",
		"literal", "identifier",
	}
	staticData.PredictionContextCache = antlr.NewPredictionContextCache()
	staticData.serializedATN = []int32{
		4, 1, 66, 351, 2, 0, 7, 0, 2, 1, 7, 1, 2, 2, 7, 2, 2, 3, 7, 3, 2, 4, 7,
		4, 2, 5, 7, 5, 2, 6, 7, 6, 2, 7, 7, 7, 2, 8, 7, 8, 2, 9, 7, 9, 2, 10, 7,
		10, 2, 11, 7, 11, 2, 12, 7, 12, 2, 13, 7, 13, 2, 14, 7, 14, 2, 15, 7, 15,
		2, 16, 7, 16, 2, 17, 7, 17, 2, 18, 7, 18, 2, 19, 7, 19, 2, 20, 7, 20, 2,
		21, 7, 21, 2, 22, 7, 22, 2, 23, 7, 23, 2, 24, 7, 24, 1, 0, 1, 0, 3, 0,
		53, 8, 0, 1, 0, 1, 0, 1, 1, 1, 1, 1, 1, 1, 1, 5, 1, 61, 8, 1, 10, 1, 12,
		1, 64, 9, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 5, 1, 71, 8, 1, 10, 1, 12, 1,
		74, 9, 1, 3, 1, 76, 8, 1, 1, 1, 3, 1, 79, 8, 1, 1, 2, 1, 2, 1, 2, 1, 2,
		1, 2, 3, 2, 86, 8, 2, 1, 3, 1, 3, 3, 3, 90, 8, 3, 1, 4, 1, 4, 3, 4, 94,
		8, 4, 1, 4, 1, 4, 1, 4, 5, 4, 99, 8, 4, 10, 4, 12, 4, 102, 9, 4, 1, 4,
		1, 4, 1, 4, 1, 4, 3, 4, 108, 8, 4, 1, 4, 1, 4, 1, 4, 3, 4, 113, 8, 4, 1,
		4, 1, 4, 3, 4, 117, 8, 4, 1, 5, 1, 5, 1, 5, 3, 5, 122, 8, 5, 1, 5, 1, 5,
		1, 5, 3, 5, 127, 8, 5, 1, 5, 3, 5, 130, 8, 5, 3, 5, 132, 8, 5, 1, 6, 1,
		6, 5, 6, 136, 8, 6, 10, 6, 12, 6, 139, 9, 6, 1, 7, 1, 7, 3, 7, 143, 8,
		7, 1, 7, 3, 7, 146, 8, 7, 1, 7, 1, 7, 1, 7, 1, 7, 3, 7, 152, 8, 7, 1, 7,
		1, 7, 1, 7, 1, 7, 1, 7, 1, 7, 3, 7, 160, 8, 7, 1, 8, 1, 8, 1, 8, 3, 8,
		165, 8, 8, 1, 8, 1, 8, 3, 8, 169, 8, 8, 3, 8, 171, 8, 8, 1, 8, 1, 8, 1,
		8, 1, 8, 1, 8, 1, 8, 1, 8, 1, 8, 3, 8, 181, 8, 8, 1, 9, 1, 9, 3, 9, 185,
		8, 9, 1, 10, 1, 10, 1, 10, 1, 10, 1, 10, 1, 10, 3, 10, 193, 8, 10, 1, 11,
		1, 11, 1, 11, 5, 11, 198, 8, 11, 10, 11, 12, 11, 201, 9, 11, 1, 12, 1,
		12, 1, 13, 1, 13, 1, 13, 5, 13, 208, 8, 13, 10, 13, 12, 13, 211, 9, 13,
		1, 14, 1, 14, 1, 14, 5, 14, 216, 8, 14, 10, 14, 12, 14, 219, 9, 14, 1,
		15, 1, 15, 1, 15, 3, 15, 224, 8, 15, 1, 16, 1, 16, 1, 16, 1, 16, 1, 16,
		3, 16, 231, 8, 16, 1, 16, 1, 16, 3, 16, 235, 8, 16, 1, 16, 1, 16, 1, 16,
		1, 16, 1, 16, 1, 16, 3, 16, 243, 8, 16, 1, 16, 1, 16, 1, 16, 1, 16, 1,
		16, 1, 16, 3, 16, 251, 8, 16, 1, 16, 1, 16, 1, 16, 1, 16, 3, 16, 257, 8,
		16, 3, 16, 259, 8, 16, 1, 17, 1, 17, 1, 17, 5, 17, 264, 8, 17, 10, 17,
		12, 17, 267, 9, 17, 1, 18, 1, 18, 1, 18, 5, 18, 272, 8, 18, 10, 18, 12,
		18, 275, 9, 18, 1, 19, 1, 19, 1, 19, 3, 19, 280, 8, 19, 1, 20, 1, 20, 1,
		20, 1, 20, 3, 20, 286, 8, 20, 1, 20, 1, 20, 1, 20, 1, 20, 3, 20, 292, 8,
		20, 1, 20, 1, 20, 1, 20, 1, 20, 1, 20, 1, 20, 1, 20, 1, 20, 1, 20, 3, 20,
		303, 8, 20, 1, 20, 1, 20, 1, 20, 1, 20, 1, 20, 4, 20, 310, 8, 20, 11, 20,
		12, 20, 311, 1, 20, 1, 20, 3, 20, 316, 8, 20, 1, 20, 1, 20, 1, 20, 1, 20,
		1, 20, 1, 20, 3, 20, 324, 8, 20, 1, 21, 1, 21, 1, 21, 3, 21, 329, 8, 21,
		1, 21, 1, 21, 3, 21, 333, 8, 21, 1, 21, 1, 21, 1, 22, 1, 22, 1, 22, 1,
		22, 1, 22, 3, 22, 342, 8, 22, 1, 22, 3, 22, 345, 8, 22, 1, 23, 1, 23, 1,
		24, 1, 24, 1, 24, 0, 0, 25, 0, 2, 4, 6, 8, 10, 12, 14, 16, 18, 20, 22,
		24, 26, 28, 30, 32, 34, 36, 38, 40, 42, 44, 46, 48, 0, 7, 1, 0, 3, 4, 1,
		0, 21, 22, 1, 0, 48, 53, 1, 0, 54, 55, 1, 0, 56, 58, 3, 0, 27, 27, 38,
		39, 59, 60, 2, 0, 3, 3, 61, 62, 389, 0, 50, 1, 0, 0, 0, 2, 56, 1, 0, 0,
		0, 4, 85, 1, 0, 0, 0, 6, 87, 1, 0, 0, 0, 8, 91, 1, 0, 0, 0, 10, 131, 1,
		0, 0, 0, 12, 133, 1, 0, 0, 0, 14, 159, 1, 0, 0, 0, 16, 180, 1, 0, 0, 0,
		18, 182, 1, 0, 0, 0, 20, 186, 1, 0, 0, 0, 22, 194, 1, 0, 0, 0, 24, 202,
		1, 0, 0, 0, 26, 204, 1, 0, 0, 0, 28, 212, 1, 0, 0, 0, 30, 223, 1, 0, 0,
		0, 32, 225, 1, 0, 0, 0, 34, 260, 1, 0, 0, 0, 36, 268, 1, 0, 0, 0, 38, 279,
		1, 0, 0, 0, 40, 323, 1, 0, 0, 0, 42, 325, 1, 0, 0, 0, 44, 336, 1, 0, 0,
		0, 46, 346, 1, 0, 0, 0, 48, 348, 1, 0, 0, 0, 50, 52, 3, 2, 1, 0, 51, 53,
		5, 47, 0, 0, 52, 51, 1, 0, 0, 0, 52, 53, 1, 0, 0, 0, 53, 54, 1, 0, 0, 0,
		54, 55, 5, 0, 0, 1, 55, 1, 1, 0, 0, 0, 56, 62, 3, 4, 2, 0, 57, 58, 3, 6,
		3, 0, 58, 59, 3, 4, 2, 0, 59, 61, 1, 0, 0, 0, 60, 57, 1, 0, 0, 0, 61, 64,
		1, 0, 0, 0, 62, 60, 1, 0, 0, 0, 62, 63, 1, 0, 0, 0, 63, 75, 1, 0, 0, 0,
		64, 62, 1, 0, 0, 0, 65, 66, 5, 10, 0, 0, 66, 67, 5, 8, 0, 0, 67, 72, 3,
		18, 9, 0, 68, 69, 5, 45, 0, 0, 69, 71, 3, 18, 9, 0, 70, 68, 1, 0, 0, 0,
		71, 74, 1, 0, 0, 0, 72, 70, 1, 0, 0, 0, 72, 73, 1, 0, 0, 0, 73, 76, 1,
		0, 0, 0, 74, 72, 1, 0, 0, 0, 75, 65, 1, 0, 0, 0, 75, 76, 1, 0, 0, 0, 76,
		78, 1, 0, 0, 0, 77, 79, 3, 20, 10, 0, 78, 77, 1, 0, 0, 0, 78, 79, 1, 0,
		0, 0, 79, 3, 1, 0, 0, 0, 80, 86, 3, 8, 4, 0, 81, 82, 5, 43, 0, 0, 82, 83,
		3, 2, 1, 0, 83, 84, 5, 44, 0, 0, 84, 86, 1, 0, 0, 0, 85, 80, 1, 0, 0, 0,
		85, 81, 1, 0, 0, 0, 86, 5, 1, 0, 0, 0, 87, 89, 5, 2, 0, 0, 88, 90, 7, 0,
		0, 0, 89, 88, 1, 0, 0, 0, 89, 90, 1, 0, 0, 0, 90, 7, 1, 0, 0, 0, 91, 93,
		5, 1, 0, 0, 92, 94, 5, 4, 0, 0, 93, 92, 1, 0, 0, 0, 93, 94, 1, 0, 0, 0,
		94, 95, 1, 0, 0, 0, 95, 100, 3, 10, 5, 0, 96, 97, 5, 45, 0, 0, 97, 99,
		3, 10, 5, 0, 98, 96, 1, 0, 0, 0, 99, 102, 1, 0, 0, 0, 100, 98, 1, 0, 0,
		0, 100, 101, 1, 0, 0, 0, 101, 103, 1, 0, 0, 0, 102, 100, 1, 0, 0, 0, 103,
		104, 5, 5, 0, 0, 104, 107, 3, 12, 6, 0, 105, 106, 5, 6, 0, 0, 106, 108,
		3, 24, 12, 0, 107, 105, 1, 0, 0, 0, 107, 108, 1, 0, 0, 0, 108, 112, 1,
		0, 0, 0, 109, 110, 5, 7, 0, 0, 110, 111, 5, 8, 0, 0, 111, 113, 3, 22, 11,
		0, 112, 109, 1, 0, 0, 0, 112, 113, 1, 0, 0, 0, 113, 116, 1, 0, 0, 0, 114,
		115, 5, 9, 0, 0, 115, 117, 3, 24, 12, 0, 116, 114, 1, 0, 0, 0, 116, 117,
		1, 0, 0, 0, 117, 9, 1, 0, 0, 0, 118, 119, 3, 48, 24, 0, 119, 120, 5, 46,
		0, 0, 120, 122, 1, 0, 0, 0, 121, 118, 1, 0, 0, 0, 121, 122, 1, 0, 0, 0,
		122, 123, 1, 0, 0, 0, 123, 132, 5, 56, 0, 0, 124, 129, 3, 24, 12, 0, 125,
		127, 5, 13, 0, 0, 126, 125, 1, 0, 0, 0, 126, 127, 1, 0, 0, 0, 127, 128,
		1, 0, 0, 0, 128, 130, 3, 48, 24, 0, 129, 126, 1, 0, 0, 0, 129, 130, 1,
		0, 0, 0, 130, 132, 1, 0, 0, 0, 131, 121, 1, 0, 0, 0, 131, 124, 1, 0, 0,
		0, 132, 11, 1, 0, 0, 0, 133, 137, 3, 14, 7, 0, 134, 136, 3, 16, 8, 0, 135,
		134, 1, 0, 0, 0, 136, 139, 1, 0, 0, 0, 137, 135, 1, 0, 0, 0, 137, 138,
		1, 0, 0, 0, 138, 13, 1, 0, 0, 0, 139, 137, 1, 0, 0, 0, 140, 145, 5, 42,
		0, 0, 141, 143, 5, 13, 0, 0, 142, 141, 1, 0, 0, 0, 142, 143, 1, 0, 0, 0,
		143, 144, 1, 0, 0, 0, 144, 146, 3, 48, 24, 0, 145, 142, 1, 0, 0, 0, 145,
		146, 1, 0, 0, 0, 146, 160, 1, 0, 0, 0, 147, 148, 5, 43, 0, 0, 148, 149,
		3, 2, 1, 0, 149, 151, 5, 44, 0, 0, 150, 152, 5, 13, 0, 0, 151, 150, 1,
		0, 0, 0, 151, 152, 1, 0, 0, 0, 152, 153, 1, 0, 0, 0, 153, 154, 3, 48, 24,
		0, 154, 160, 1, 0, 0, 0, 155, 156, 5, 43, 0, 0, 156, 157, 3, 12, 6, 0,
		157, 158, 5, 44, 0, 0, 158, 160, 1, 0, 0, 0, 159, 140, 1, 0, 0, 0, 159,
		147, 1, 0, 0, 0, 159, 155, 1, 0, 0, 0, 160, 15, 1, 0, 0, 0, 161, 171, 5,
		14, 0, 0, 162, 164, 5, 15, 0, 0, 163, 165, 5, 17, 0, 0, 164, 163, 1, 0,
		0, 0, 164, 165, 1, 0, 0, 0, 165, 171, 1, 0, 0, 0, 166, 168, 5, 16, 0, 0,
		167, 169, 5, 17, 0, 0, 168, 167, 1, 0, 0, 0, 168, 169, 1, 0, 0, 0, 169,
		171, 1, 0, 0, 0, 170, 161, 1, 0, 0, 0, 170, 162, 1, 0, 0, 0, 170, 166,
		1, 0, 0, 0, 170, 171, 1, 0, 0, 0, 171, 172, 1, 0, 0, 0, 172, 173, 5, 18,
		0, 0, 173, 174, 3, 14, 7, 0, 174, 175, 5, 20, 0, 0, 175, 176, 3, 24, 12,
		0, 176, 181, 1, 0, 0, 0, 177, 178, 5, 19, 0, 0, 178, 179, 5, 18, 0, 0,
		179, 181, 3, 14, 7, 0, 180, 170, 1, 0, 0, 0, 180, 177, 1, 0, 0, 0, 181,
		17, 1, 0, 0, 0, 182, 184, 3, 24, 12, 0, 183, 185, 7, 1, 0, 0, 184, 183,
		1, 0, 0, 0, 184, 185, 1, 0, 0, 0, 185, 19, 1, 0, 0, 0, 186, 187, 5, 11,
		0, 0, 187, 192, 5, 59, 0, 0, 188, 189, 5, 12, 0, 0, 189, 193, 5, 59, 0,
		0, 190, 191, 5, 45, 0, 0, 191, 193, 5, 59, 0, 0, 192, 188, 1, 0, 0, 0,
		192, 190, 1, 0, 0, 0, 192, 193, 1, 0, 0, 0, 193, 21, 1, 0, 0, 0, 194, 199,
		3, 24, 12, 0, 195, 196, 5, 45, 0, 0, 196, 198, 3, 24, 12, 0, 197, 195,
		1, 0, 0, 0, 198, 201, 1, 0, 0, 0, 199, 197, 1, 0, 0, 0, 199, 200, 1, 0,
		0, 0, 200, 23, 1, 0, 0, 0, 201, 199, 1, 0, 0, 0, 202, 203, 3, 26, 13, 0,
		203, 25, 1, 0, 0, 0, 204, 209, 3, 28, 14, 0, 205, 206, 5, 23, 0, 0, 206,
		208, 3, 28, 14, 0, 207, 205, 1, 0, 0, 0, 208, 211, 1, 0, 0, 0, 209, 207,
		1, 0, 0, 0, 209, 210, 1, 0, 0, 0, 210, 27, 1, 0, 0, 0, 211, 209, 1, 0,
		0, 0, 212, 217, 3, 30, 15, 0, 213, 214, 5, 24, 0, 0, 214, 216, 3, 30, 15,
		0, 215, 213, 1, 0, 0, 0, 216, 219, 1, 0, 0, 0, 217, 215, 1, 0, 0, 0, 217,
		218, 1, 0, 0, 0, 218, 29, 1, 0, 0, 0, 219, 217, 1, 0, 0, 0, 220, 221, 5,
		25, 0, 0, 221, 224, 3, 30, 15, 0, 222, 224, 3, 32, 16, 0, 223, 220, 1,
		0, 0, 0, 223, 222, 1, 0, 0, 0, 224, 31, 1, 0, 0, 0, 225, 258, 3, 34, 17,
		0, 226, 227, 7, 2, 0, 0, 227, 259, 3, 34, 17, 0, 228, 230, 5, 26, 0, 0,
		229, 231, 5, 25, 0, 0, 230, 229, 1, 0, 0, 0, 230, 231, 1, 0, 0, 0, 231,
		232, 1, 0, 0, 0, 232, 259, 5, 27, 0, 0, 233, 235, 5, 25, 0, 0, 234, 233,
		1, 0, 0, 0, 234, 235, 1, 0, 0, 0, 235, 236, 1, 0, 0, 0, 236, 237, 5, 28,
		0, 0, 237, 238, 3, 34, 17, 0, 238, 239, 5, 24, 0, 0, 239, 240, 3, 34, 17,
		0, 240, 259, 1, 0, 0, 0, 241, 243, 5, 25, 0, 0, 242, 241, 1, 0, 0, 0, 242,
		243, 1, 0, 0, 0, 243, 244, 1, 0, 0, 0, 244, 245, 5, 29, 0, 0, 245, 246,
		5, 43, 0, 0, 246, 247, 3, 22, 11, 0, 247, 248, 5, 44, 0, 0, 248, 259, 1,
		0, 0, 0, 249, 251, 5, 25, 0, 0, 250, 249, 1, 0, 0, 0, 250, 251, 1, 0, 0,
		0, 251, 252, 1, 0, 0, 0, 252, 253, 5, 30, 0, 0, 253, 256, 3, 34, 17, 0,
		254, 255, 5, 31, 0, 0, 255, 257, 5, 60, 0, 0, 256, 254, 1, 0, 0, 0, 256,
		257, 1, 0, 0, 0, 257, 259, 1, 0, 0, 0, 258, 226, 1, 0, 0, 0, 258, 228,
		1, 0, 0, 0, 258, 234, 1, 0, 0, 0, 258, 242, 1, 0, 0, 0, 258, 250, 1, 0,
		0, 0, 258, 259, 1, 0, 0, 0, 259, 33, 1, 0, 0, 0, 260, 265, 3, 36, 18, 0,
		261, 262, 7, 3, 0, 0, 262, 264, 3, 36, 18, 0, 263, 261, 1, 0, 0, 0, 264,
		267, 1, 0, 0, 0, 265, 263, 1, 0, 0, 0, 265, 266, 1, 0, 0, 0, 266, 35, 1,
		0, 0, 0, 267, 265, 1, 0, 0, 0, 268, 273, 3, 38, 19, 0, 269, 270, 7, 4,
		0, 0, 270, 272, 3, 38, 19, 0, 271, 269, 1, 0, 0, 0, 272, 275, 1, 0, 0,
		0, 273, 271, 1, 0, 0, 0, 273, 274, 1, 0, 0, 0, 274, 37, 1, 0, 0, 0, 275,
		273, 1, 0, 0, 0, 276, 277, 7, 3, 0, 0, 277, 280, 3, 38, 19, 0, 278, 280,
		3, 40, 20, 0, 279, 276, 1, 0, 0, 0, 279, 278, 1, 0, 0, 0, 280, 39, 1, 0,
		0, 0, 281, 324, 3, 46, 23, 0, 282, 285, 3, 48, 24, 0, 283, 284, 5, 46,
		0, 0, 284, 286, 3, 48, 24, 0, 285, 283, 1, 0, 0, 0, 285, 286, 1, 0, 0,
		0, 286, 324, 1, 0, 0, 0, 287, 324, 3, 42, 21, 0, 288, 291, 5, 40, 0, 0,
		289, 290, 5, 43, 0, 0, 290, 292, 5, 44, 0, 0, 291, 289, 1, 0, 0, 0, 291,
		292, 1, 0, 0, 0, 292, 324, 1, 0, 0, 0, 293, 294, 5, 32, 0, 0, 294, 295,
		5, 43, 0, 0, 295, 296, 3, 24, 12, 0, 296, 297, 5, 13, 0, 0, 297, 298, 3,
		44, 22, 0, 298, 299, 5, 44, 0, 0, 299, 324, 1, 0, 0, 0, 300, 302, 5, 33,
		0, 0, 301, 303, 3, 24, 12, 0, 302, 301, 1, 0, 0, 0, 302, 303, 1, 0, 0,
		0, 303, 309, 1, 0, 0, 0, 304, 305, 5, 34, 0, 0, 305, 306, 3, 24, 12, 0,
		306, 307, 5, 35, 0, 0, 307, 308, 3, 24, 12, 0, 308, 310, 1, 0, 0, 0, 309,
		304, 1, 0, 0, 0, 310, 311, 1, 0, 0, 0, 311, 309, 1, 0, 0, 0, 311, 312,
		1, 0, 0, 0, 312, 315, 1, 0, 0, 0, 313, 314, 5, 36, 0, 0, 314, 316, 3, 24,
		12, 0, 315, 313, 1, 0, 0, 0, 315, 316, 1, 0, 0, 0, 316, 317, 1, 0, 0, 0,
		317, 318, 5, 37, 0, 0, 318, 324, 1, 0, 0, 0, 319, 320, 5, 43, 0, 0, 320,
		321, 3, 24, 12, 0, 321, 322, 5, 44, 0, 0, 322, 324, 1, 0, 0, 0, 323, 281,
		1, 0, 0, 0, 323, 282, 1, 0, 0, 0, 323, 287, 1, 0, 0, 0, 323, 288, 1, 0,
		0, 0, 323, 293, 1, 0, 0, 0, 323, 300, 1, 0, 0, 0, 323, 319, 1, 0, 0, 0,
		324, 41, 1, 0, 0, 0, 325, 326, 3, 48, 24, 0, 326, 328, 5, 43, 0, 0, 327,
		329, 5, 4, 0, 0, 328, 327, 1, 0, 0, 0, 328, 329, 1, 0, 0, 0, 329, 332,
		1, 0, 0, 0, 330, 333, 5, 56, 0, 0, 331, 333, 3, 22, 11, 0, 332, 330, 1,
		0, 0, 0, 332, 331, 1, 0, 0, 0, 332, 333, 1, 0, 0, 0, 333, 334, 1, 0, 0,
		0, 334, 335, 5, 44, 0, 0, 335, 43, 1, 0, 0, 0, 336, 344, 3, 48, 24, 0,
		337, 338, 5, 43, 0, 0, 338, 341, 5, 59, 0, 0, 339, 340, 5, 45, 0, 0, 340,
		342, 5, 59, 0, 0, 341, 339, 1, 0, 0, 0, 341, 342, 1, 0, 0, 0, 342, 343,
		1, 0, 0, 0, 343, 345, 5, 44, 0, 0, 344, 337, 1, 0, 0, 0, 344, 345, 1, 0,
		0, 0, 345, 45, 1, 0, 0, 0, 346, 347, 7, 5, 0, 0, 347, 47, 1, 0, 0, 0, 348,
		349, 7, 6, 0, 0, 349, 49, 1, 0, 0, 0, 50, 52, 62, 72, 75, 78, 85, 89, 93,
		100, 107, 112, 116, 121, 126, 129, 131, 137, 142, 145, 151, 159, 164, 168,
		170, 180, 184, 192, 199, 209, 217, 223, 230, 234, 242, 250, 256, 258, 265,
		273, 279, 285, 291, 302, 311, 315, 323, 328, 332, 341, 344,
	}
	deserializer := antlr.NewATNDeserializer(nil)
	staticData.atn = deserializer.Deserialize(staticData.serializedATN)
	atn := staticData.atn
	staticData.decisionToDFA = make([]*antlr.DFA, len(atn.DecisionToState))
	decisionToDFA := staticData.decisionToDFA
	for index, state := range atn.DecisionToState {
		decisionToDFA[index] = antlr.NewDFA(state, index)
	}
}

// SqlBaseParserInit initializes any static state used to implement SqlBaseParser. By default the
// static state used to implement the parser is lazily initialized during the first call to
// NewSqlBaseParser(). You can call this function if you wish to initialize the static state ahead
// of time.
func SqlBaseParserInit() {
	staticData := &SqlBaseParserStaticData
	staticData.once.Do(sqlbaseParserInit)
}

// NewSqlBaseParser produces a new parser instance for the optional input antlr.TokenStream.
func NewSqlBaseParser(input antlr.TokenStream) *SqlBaseParser {
	SqlBaseParserInit()
	this := new(SqlBaseParser)
	this.BaseParser = antlr.NewBaseParser(input)
	staticData := &SqlBaseParserStaticData
	this.Interpreter = antlr.NewParserATNSimulator(this, staticData.atn, staticData.decisionToDFA, staticData.PredictionContextCache)
	this.RuleNames = staticData.RuleNames
	this.LiteralNames = staticData.LiteralNames
	this.SymbolicNames = staticData.SymbolicNames
	this.GrammarFileName = "SqlBase.g4"

	return this
}

// SqlBaseParser tokens.
const (
	SqlBaseParserEOF               = antlr.TokenEOF
	SqlBaseParserSELECT            = 1
	SqlBaseParserUNION             = 2
	SqlBaseParserALL               = 3
	SqlBaseParserDISTINCT          = 4
	SqlBaseParserFROM              = 5
	SqlBaseParserWHERE             = 6
	SqlBaseParserGROUP             = 7
	SqlBaseParserBY                = 8
	SqlBaseParserHAVING            = 9
	SqlBaseParserORDER             = 10
	SqlBaseParserLIMIT             = 11
	SqlBaseParserOFFSET            = 12
	SqlBaseParserAS                = 13
	SqlBaseParserINNER             = 14
	SqlBaseParserLEFT              = 15
	SqlBaseParserRIGHT             = 16
	SqlBaseParserOUTER             = 17
	SqlBaseParserJOIN              = 18
	SqlBaseParserCROSS             = 19
	SqlBaseParserON                = 20
	SqlBaseParserASC               = 21
	SqlBaseParserDESC              = 22
	SqlBaseParserOR                = 23
	SqlBaseParserAND               = 24
	SqlBaseParserNOT               = 25
	SqlBaseParserIS                = 26
	SqlBaseParserNULL              = 27
	SqlBaseParserBETWEEN           = 28
	SqlBaseParserIN                = 29
	SqlBaseParserLIKE              = 30
	SqlBaseParserESCAPE            = 31
	SqlBaseParserCAST              = 32
	SqlBaseParserCASE              = 33
	SqlBaseParserWHEN              = 34
	SqlBaseParserTHEN              = 35
	SqlBaseParserELSE              = 36
	SqlBaseParserEND               = 37
	SqlBaseParserTRUE              = 38
	SqlBaseParserFALSE             = 39
	SqlBaseParserCURRENT_TIMESTAMP = 40
	SqlBaseParserUNSUPPORTED       = 41
	SqlBaseParserRESOURCE          = 42
	SqlBaseParserLPAREN            = 43
	SqlBaseParserRPAREN            = 44
	SqlBaseParserCOMMA             = 45
	SqlBaseParserDOT               = 46
	SqlBaseParserSEMICOLON         = 47
	SqlBaseParserEQ                = 48
	SqlBaseParserNEQ               = 49
	SqlBaseParserLTE               = 50
	SqlBaseParserGTE               = 51
	SqlBaseParserLT                = 52
	SqlBaseParserGT                = 53
	SqlBaseParserPLUS              = 54
	SqlBaseParserMINUS             = 55
	SqlBaseParserSTAR              = 56
	SqlBaseParserSLASH             = 57
	SqlBaseParserPERCENT           = 58
	SqlBaseParserNUMBER            = 59
	SqlBaseParserSTRING            = 60
	SqlBaseParserQUOTED_IDENTIFIER = 61
	SqlBaseParserIDENTIFIER        = 62
	SqlBaseParserFORBIDDEN_COMMENT = 63
	SqlBaseParserBLOCK_COMMENT     = 64
	SqlBaseParserLINE_COMMENT      = 65
	SqlBaseParserWS                = 66
)

// SqlBaseParser rules.
const (
	SqlBaseParserRULE_statement                = 0
	SqlBaseParserRULE_queryExpression          = 1
	SqlBaseParserRULE_queryTerm                = 2
	SqlBaseParserRULE_unionOperator            = 3
	SqlBaseParserRULE_selectQuery              = 4
	SqlBaseParserRULE_selectItem               = 5
	SqlBaseParserRULE_relation                 = 6
	SqlBaseParserRULE_relationPrimary          = 7
	SqlBaseParserRULE_joinPart                 = 8
	SqlBaseParserRULE_sortItem                 = 9
	SqlBaseParserRULE_limitClause              = 10
	SqlBaseParserRULE_expressionList           = 11
	SqlBaseParserRULE_expression               = 12
	SqlBaseParserRULE_orExpression             = 13
	SqlBaseParserRULE_andExpression            = 14
	SqlBaseParserRULE_notExpression            = 15
	SqlBaseParserRULE_predicate                = 16
	SqlBaseParserRULE_additiveExpression       = 17
	SqlBaseParserRULE_multiplicativeExpression = 18
	SqlBaseParserRULE_unaryExpression          = 19
	SqlBaseParserRULE_primaryExpression        = 20
	SqlBaseParserRULE_functionCall             = 21
	SqlBaseParserRULE_typeName                 = 22
	SqlBaseParserRULE_literal                  = 23
	SqlBaseParserRULE_identifier               = 24
)

// IStatementContext is an interface to support dynamic dispatch.
type IStatementContext interface {
	antlr.ParserRuleContext

	// GetParser returns the parser.
	GetParser() antlr.Parser

	// Getter signatures
	QueryExpression() IQueryExpressionContext
	EOF() antlr.TerminalNode
	SEMICOLON() antlr.TerminalNode

	// IsStatementContext differentiates from other interfaces.
	IsStatementContext()
}

type StatementContext struct {
	antlr.BaseParserRuleContext
	parser antlr.Parser
}

func NewEmptyStatementContext() *StatementContext {
	var p = new(StatementContext)
	antlr.InitBaseParserRuleContext(&p.BaseParserRuleContext, nil, -1)
	p.RuleIndex = SqlBaseParserRULE_statement
	return p
}

func InitEmptyStatementContext(p *StatementContext) {
	antlr.InitBaseParserRuleContext(&p.BaseParserRuleContext, nil, -1)
	p.RuleIndex = SqlBaseParserRULE_statement
}

func (*StatementContext) IsStatementContext() {}

func NewStatementContext(parser antlr.Parser, parent antlr.ParserRuleContext, invokingState int) *StatementContext {
	var p = new(StatementContext)

	antlr.InitBaseParserRuleContext(&p.BaseParserRuleContext, parent, invokingState)

	p.parser = parser
	p.RuleIndex = SqlBaseParserRULE_statement

	return p
}

func (s *StatementContext) GetParser() antlr.Parser { return s.parser }

func (s *StatementContext) QueryExpression() IQueryExpressionContext {
	var t antlr.RuleContext
	for _, ctx := range s.GetChildren() {
		if _, ok := ctx.(IQueryExpressionContext); ok {
			t = ctx.(antlr.RuleContext)
			break
		}
	}

	if t == nil {
		return nil
	}

	return t.(IQueryExpressionContext)
}

func (s *StatementContext) EOF() antlr.TerminalNode {
	return s.GetToken(SqlBaseParserEOF, 0)
}

func (s *StatementContext) SEMICOLON() antlr.TerminalNode {
	return s.GetToken(SqlBaseParserSEMICOLON, 0)
}

func (s *StatementContext) GetRuleContext() antlr.RuleContext {
	return s
}

func (s *StatementContext) ToStringTree(ruleNames []string, recog antlr.Recognizer) string {
	return antlr.TreesStringTree(s, ruleNames, recog)
}

func (s *StatementContext) Accept(visitor antlr.ParseTreeVisitor) interface{} {
	switch t := visitor.(type) {
	case SqlBaseVisitor:
		return t.VisitStatement(s)

	default:
		return t.VisitChildren(s)
	}
}

func (p *SqlBaseParser) Statement() (localctx IStatementContext) {
	localctx = NewStatementContext(p, p.GetParserRuleContext(), p.GetState())
	p.EnterRule(localctx, 0, SqlBaseParserRULE_statement)
	var _la int

	p.EnterOuterAlt(localctx, 1)
	{
		p.SetState(50)
		p.QueryExpression()
	}
	p.SetState(52)
	p.GetErrorHandler().Sync(p)
	if p.HasError() {
		goto errorExit
	}
	_la = p.GetTokenStream().LA(1)

	if _la == SqlBaseParserSEMICOLON {
		{
			p.SetState(51)
			p.Match(SqlBaseParserSEMICOLON)
			if p.HasError() {
				// Recognition error - abort rule
				goto errorExit
			}
		}

	}
	{
		p.SetState(54)
		p.Match(SqlBaseParserEOF)
		if p.HasError() {
			// Recognition error - abort rule
			goto errorExit
		}
	}

errorExit:
	if p.HasError() {
		v := p.GetError()
		localctx.SetException(v)
		p.GetErrorHandler().ReportError(p, v)
		p.GetErrorHandler().Recover(p, v)
		p.SetError(nil)
	}
	p.ExitRule()
	return localctx
	goto errorExit // Trick to prevent compiler error if the label is not used
}

// IQueryExpressionContext is an interface to support dynamic dispatch.
type IQueryExpressionContext interface {
	antlr.ParserRuleContext

	// GetParser returns the parser.
	GetParser() antlr.Parser

	// Getter signatures
	AllQueryTerm() []IQueryTermContext
	QueryTerm(i int) IQueryTermContext
	AllUnionOperator() []IUnionOperatorContext
	UnionOperator(i int) IUnionOperatorContext
	ORDER() antlr.TerminalNode
	BY() antlr.TerminalNode
	AllSortItem() []ISortItemContext
	SortItem(i int) ISortItemContext
	LimitClause() ILimitClauseContext
	AllCOMMA() []antlr.TerminalNode
	COMMA(i int) antlr.TerminalNode

	// IsQueryExpressionContext differentiates from other interfaces.
	IsQueryExpressionContext()
}

type QueryExpressionContext struct {
	antlr.BaseParserRuleContext
	parser antlr.Parser
}

func NewEmptyQueryExpressionContext() *QueryExpressionContext {
	var p = new(QueryExpressionContext)
	antlr.InitBaseParserRuleContext(&p.BaseParserRuleContext, nil, -1)
	p.RuleIndex = SqlBaseParserRULE_queryExpression
	return p
}

func InitEmptyQueryExpressionContext(p *QueryExpressionContext) {
	antlr.InitBaseParserRuleContext(&p.BaseParserRuleContext, nil, -1)
	p.RuleIndex = SqlBaseParserRULE_queryExpression
}

func (*QueryExpressionContext) IsQueryExpressionContext() {}

func NewQueryExpressionContext(parser antlr.Parser, parent antlr.ParserRuleContext, invokingState int) *QueryExpressionContext {
	var p = new(QueryExpressionContext)

	antlr.InitBaseParserRuleContext(&p.BaseParserRuleContext, parent, invokingState)

	p.parser = parser
	p.RuleIndex = SqlBaseParserRULE_queryExpression

	return p
}

func (s *QueryExpressionContext) GetParser() antlr.Parser { return s.parser }

func (s *QueryExpressionContext) AllQueryTerm() []IQueryTermContext {
	children := s.GetChildren()
	len := 0
	for _, ctx := range children {
		if _, ok := ctx.(IQueryTermContext); ok {
			len++
		}
	}

	tst := make([]IQueryTermContext, len)
	i := 0
	for _, ctx := range children {
		if t, ok := ctx.(IQueryTermContext); ok {
			tst[i] = t.(IQueryTermContext)
			i++
		}
	}

	return tst
}

func (s *QueryExpressionContext) QueryTerm(i int) IQueryTermContext {
	var t antlr.RuleContext
	j := 0
	for _, ctx := range s.GetChildren() {
		if _, ok := ctx.(IQueryTermContext); ok {
			if j == i {
				t = ctx.(antlr.RuleContext)
				break
			}
			j++
		}
	}

	if t == nil {
		return nil
	}

	return t.(IQueryTermContext)
}

func (s *QueryExpressionContext) AllUnionOperator() []IUnionOperatorContext {
	children := s.GetChildren()
	len := 0
	for _, ctx := range children {
		if _, ok := ctx.(IUnionOperatorContext); ok {
			len++
		}
	}

	tst := make([]IUnionOperatorContext, len)
	i := 0
	for _, ctx := range children {
		if t, ok := ctx.(IUnionOperatorContext); ok {
			tst[i] = t.(IUnionOperatorContext)
			i++
		}
	}

	return tst
}

func (s *QueryExpressionContext) UnionOperator(i int) IUnionOperatorContext {
	var t antlr.RuleContext
	j := 0
	for _, ctx := range s.GetChildren() {
		if _, ok := ctx.(IUnionOperatorContext); ok {
			if j == i {
				t = ctx.(antlr.RuleContext)
				break
			}
			j++
		}
	}

	if t == nil {
		return nil
	}

	return t.(IUnionOperatorContext)
}

func (s *QueryExpressionContext) ORDER() antlr.TerminalNode {
	return s.GetToken(SqlBaseParserORDER, 0)
}

func (s *QueryExpressionContext) BY() antlr.TerminalNode {
	return s.GetToken(SqlBaseParserBY, 0)
}

func (s *QueryExpressionContext) AllSortItem() []ISortItemContext {
	children := s.GetChildren()
	len := 0
	for _, ctx := range children {
		if _, ok := ctx.(ISortItemContext); ok {
			len++
		}
	}

	tst := make([]ISortItemContext, len)
	i := 0
	for _, ctx := range children {
		if t, ok := ctx.(ISortItemContext); ok {
			tst[i] = t.(ISortItemContext)
			i++
		}
	}

	return tst
}

func (s *QueryExpressionContext) SortItem(i int) ISortItemContext {
	var t antlr.RuleContext
	j := 0
	for _, ctx := range s.GetChildren() {
		if _, ok := ctx.(ISortItemContext); ok {
			if j == i {
				t = ctx.(antlr.RuleContext)
				break
			}
			j++
		}
	}

	if t == nil {
		return nil
	}

	return t.(ISortItemContext)
}

func (s *QueryExpressionContext) LimitClause() ILimitClauseContext {
	var t antlr.RuleContext
	for _, ctx := range s.GetChildren() {
		if _, ok := ctx.(ILimitClauseContext); ok {
			t = ctx.(antlr.RuleContext)
			break
		}
	}

	if t == nil {
		return nil
	}

	return t.(ILimitClauseContext)
}

func (s *QueryExpressionContext) AllCOMMA() []antlr.TerminalNode {
	return s.GetTokens(SqlBaseParserCOMMA)
}

func (s *QueryExpressionContext) COMMA(i int) antlr.TerminalNode {
	return s.GetToken(SqlBaseParserCOMMA, i)
}

func (s *QueryExpressionContext) GetRuleContext() antlr.RuleContext {
	return s
}

func (s *QueryExpressionContext) ToStringTree(ruleNames []string, recog antlr.Recognizer) string {
	return antlr.TreesStringTree(s, ruleNames, recog)
}

func (s *QueryExpressionContext) Accept(visitor antlr.ParseTreeVisitor) interface{} {
	switch t := visitor.(type) {
	case SqlBaseVisitor:
		return t.VisitQueryExpression(s)

	default:
		return t.VisitChildren(s)
	}
}

func (p *SqlBaseParser) QueryExpression() (localctx IQueryExpressionContext) {
	localctx = NewQueryExpressionContext(p, p.GetParserRuleContext(), p.GetState())
	p.EnterRule(localctx, 2, SqlBaseParserRULE_queryExpression)
	var _la int

	p.EnterOuterAlt(localctx, 1)
	{
		p.SetState(56)
		p.QueryTerm()
	}
	p.SetState(62)
	p.GetErrorHandler().Sync(p)
	if p.HasError() {
		goto errorExit
	}
	_la = p.GetTokenStream().LA(1)

	for _la == SqlBaseParserUNION {
		{
			p.SetState(57)
			p.UnionOperator()
		}
		{
			p.SetState(58)
			p.QueryTerm()
		}

		p.SetState(64)
		p.GetErrorHandler().Sync(p)
		if p.HasError() {
			goto errorExit
		}
		_la = p.GetTokenStream().LA(1)
	}
	p.SetState(75)
	p.GetErrorHandler().Sync(p)
	if p.HasError() {
		goto errorExit
	}
	_la = p.GetTokenStream().LA(1)

	if _la == SqlBaseParserORDER {
		{
			p.SetState(65)
			p.Match(SqlBaseParserORDER)
			if p.HasError() {
				// Recognition error - abort rule
				goto errorExit
			}
		}
		{
			p.SetState(66)
			p.Match(SqlBaseParserBY)
			if p.HasError() {
				// Recognition error - abort rule
				goto errorExit
			}
		}
		{
			p.SetState(67)
			p.SortItem()
		}
		p.SetState(72)
		p.GetErrorHandler().Sync(p)
		if p.HasError() {
			goto errorExit
		}
		_la = p.GetTokenStream().LA(1)

		for _la == SqlBaseParserCOMMA {
			{
				p.SetState(68)
				p.Match(SqlBaseParserCOMMA)
				if p.HasError() {
					// Recognition error - abort rule
					goto errorExit
				}
			}
			{
				p.SetState(69)
				p.SortItem()
			}

			p.SetState(74)
			p.GetErrorHandler().Sync(p)
			if p.HasError() {
				goto errorExit
			}
			_la = p.GetTokenStream().LA(1)
		}

	}
	p.SetState(78)
	p.GetErrorHandler().Sync(p)
	if p.HasError() {
		goto errorExit
	}
	_la = p.GetTokenStream().LA(1)

	if _la == SqlBaseParserLIMIT {
		{
			p.SetState(77)
			p.LimitClause()
		}

	}

errorExit:
	if p.HasError() {
		v := p.GetError()
		localctx.SetException(v)
		p.GetErrorHandler().ReportError(p, v)
		p.GetErrorHandler().Recover(p, v)
		p.SetError(nil)
	}
	p.ExitRule()
	return localctx
	goto errorExit // Trick to prevent compiler error if the label is not used
}

// IQueryTermContext is an interface to support dynamic dispatch.
type IQueryTermContext interface {
	antlr.ParserRuleContext

	// GetParser returns the parser.
	GetParser() antlr.Parser

	// Getter signatures
	SelectQuery() ISelectQueryContext
	LPAREN() antlr.TerminalNode
	QueryExpression() IQueryExpressionContext
	RPAREN() antlr.TerminalNode

	// IsQueryTermContext differentiates from other interfaces.
	IsQueryTermContext()
}

type QueryTermContext struct {
	antlr.BaseParserRuleContext
	parser antlr.Parser
}

func NewEmptyQueryTermContext() *QueryTermContext {
	var p = new(QueryTermContext)
	antlr.InitBaseParserRuleContext(&p.BaseParserRuleContext, nil, -1)
	p.RuleIndex = SqlBaseParserRULE_queryTerm
	return p
}

func InitEmptyQueryTermContext(p *QueryTermContext) {
	antlr.InitBaseParserRuleContext(&p.BaseParserRuleContext, nil, -1)
	p.RuleIndex = SqlBaseParserRULE_queryTerm
}

func (*QueryTermContext) IsQueryTermContext() {}

func NewQueryTermContext(parser antlr.Parser, parent antlr.ParserRuleContext, invokingState int) *QueryTermContext {
	var p = new(QueryTermContext)

	antlr.InitBaseParserRuleContext(&p.BaseParserRuleContext, parent, invokingState)

	p.parser = parser
	p.RuleIndex = SqlBaseParserRULE_queryTerm

	return p
}

func (s *QueryTermContext) GetParser() antlr.Parser { return s.parser }

func (s *QueryTermContext) SelectQuery() ISelectQueryContext {
	var t antlr.RuleContext
	for _, ctx := range s.GetChildren() {
		if _, ok := ctx.(ISelectQueryContext); ok {
			t = ctx.(antlr.RuleContext)
			break
		}
	}

	if t == nil {
		return nil
	}

	return t.(ISelectQueryContext)
}

func (s *QueryTermContext) LPAREN() antlr.TerminalNode {
	return s.GetToken(SqlBaseParserLPAREN, 0)
}

func (s *QueryTermContext) QueryExpression() IQueryExpressionContext {
	var t antlr.RuleContext
	for _, ctx := range s.GetChildren() {
		if _, ok := ctx.(IQueryExpressionContext); ok {
			t = ctx.(antlr.RuleContext)
			break
		}
	}

	if t == nil {
		return nil
	}

	return t.(IQueryExpressionContext)
}

func (s *QueryTermContext) RPAREN() antlr.TerminalNode {
	return s.GetToken(SqlBaseParserRPAREN, 0)
}

func (s *QueryTermContext) GetRuleContext() antlr.RuleContext {
	return s
}

func (s *QueryTermContext) ToStringTree(ruleNames []string, recog antlr.Recognizer) string {
	return antlr.TreesStringTree(s, ruleNames, recog)
}

func (s *QueryTermContext) Accept(visitor antlr.ParseTreeVisitor) interface{} {
	switch t := visitor.(type) {
	case SqlBaseVisitor:
		return t.VisitQueryTerm(s)

	default:
		return t.VisitChildren(s)
	}
}

func (p *SqlBaseParser) QueryTerm() (localctx IQueryTermContext) {
	localctx = NewQueryTermContext(p, p.GetParserRuleContext(), p.GetState())
	p.EnterRule(localctx, 4, SqlBaseParserRULE_queryTerm)
	p.SetState(85)
	p.GetErrorHandler().Sync(p)
	if p.HasError() {
		goto errorExit
	}

	switch p.GetTokenStream().LA(1) {
	case SqlBaseParserSELECT:
		p.EnterOuterAlt(localctx, 1)
		{
			p.SetState(80)
			p.SelectQuery()
		}

	case SqlBaseParserLPAREN:
		p.EnterOuterAlt(localctx, 2)
		{
			p.SetState(81)
			p.Match(SqlBaseParserLPAREN)
			if p.HasError() {
				// Recognition error - abort rule
				goto errorExit
			}
		}
		{
			p.SetState(82)
			p.QueryExpression()
		}
		{
			p.SetState(83)
			p.Match(SqlBaseParserRPAREN)
			if p.HasError() {
				// Recognition error - abort rule
				goto errorExit
			}
		}

	default:
		p.SetError(antlr.NewNoViableAltException(p, nil, nil, nil, nil, nil))
		goto errorExit
	}

errorExit:
	if p.HasError() {
		v := p.GetError()
		localctx.SetException(v)
		p.GetErrorHandler().ReportError(p, v)
		p.GetErrorHandler().Recover(p, v)
		p.SetError(nil)
	}
	p.ExitRule()
	return localctx
	goto errorExit // Trick to prevent compiler error if the label is not used
}

// IUnionOperatorContext is an interface to support dynamic dispatch.
type IUnionOperatorContext interface {
	antlr.ParserRuleContext

	// GetParser returns the parser.
	GetParser() antlr.Parser

	// Getter signatures
	UNION() antlr.TerminalNode
	ALL() antlr.TerminalNode
	DISTINCT() antlr.TerminalNode

	// IsUnionOperatorContext differentiates from other interfaces.
	IsUnionOperatorContext()
}

type UnionOperatorContext struct {
	antlr.BaseParserRuleContext
	parser antlr.Parser
}

func NewEmptyUnionOperatorContext() *UnionOperatorContext {
	var p = new(UnionOperatorContext)
	antlr.InitBaseParserRuleContext(&p.BaseParserRuleContext, nil, -1)
	p.RuleIndex = SqlBaseParserRULE_unionOperator
	return p
}

func InitEmptyUnionOperatorContext(p *UnionOperatorContext) {
	antlr.InitBaseParserRuleContext(&p.BaseParserRuleContext, nil, -1)
	p.RuleIndex = SqlBaseParserRULE_unionOperator
}

func (*UnionOperatorContext) IsUnionOperatorContext() {}

func NewUnionOperatorContext(parser antlr.Parser, parent antlr.ParserRuleContext, invokingState int) *UnionOperatorContext {
	var p = new(UnionOperatorContext)

	antlr.InitBaseParserRuleContext(&p.BaseParserRuleContext, parent, invokingState)

	p.parser = parser
	p.RuleIndex = SqlBaseParserRULE_unionOperator

	return p
}

func (s *UnionOperatorContext) GetParser() antlr.Parser { return s.parser }

func (s *UnionOperatorContext) UNION() antlr.TerminalNode {
	return s.GetToken(SqlBaseParserUNION, 0)
}

func (s *UnionOperatorContext) ALL() antlr.TerminalNode {
	return s.GetToken(SqlBaseParserALL, 0)
}

func (s *UnionOperatorContext) DISTINCT() antlr.TerminalNode {
	return s.GetToken(SqlBaseParserDISTINCT, 0)
}

func (s *UnionOperatorContext) GetRuleContext() antlr.RuleContext {
	return s
}

func (s *UnionOperatorContext) ToStringTree(ruleNames []string, recog antlr.Recognizer) string {
	return antlr.TreesStringTree(s, ruleNames, recog)
}

func (s *UnionOperatorContext) Accept(visitor antlr.ParseTreeVisitor) interface{} {
	switch t := visitor.(type) {
	case SqlBaseVisitor:
		return t.VisitUnionOperator(s)

	default:
		return t.VisitChildren(s)
	}
}

func (p *SqlBaseParser) UnionOperator() (localctx IUnionOperatorContext) {
	localctx = NewUnionOperatorContext(p, p.GetParserRuleContext(), p.GetState())
	p.EnterRule(localctx, 6, SqlBaseParserRULE_unionOperator)
	var _la int

	p.EnterOuterAlt(localctx, 1)
	{
		p.SetState(87)
		p.Match(SqlBaseParserUNION)
		if p.HasError() {
			// Recognition error - abort rule
			goto errorExit
		}
	}
	p.SetState(89)
	p.GetErrorHandler().Sync(p)
	if p.HasError() {
		goto errorExit
	}
	_la = p.GetTokenStream().LA(1)

	if _la == SqlBaseParserALL || _la == SqlBaseParserDISTINCT {
		{
			p.SetState(88)
			_la = p.GetTokenStream().LA(1)

			if !(_la == SqlBaseParserALL || _la == SqlBaseParserDISTINCT) {
				p.GetErrorHandler().RecoverInline(p)
			} else {
				p.GetErrorHandler().ReportMatch(p)
				p.Consume()
			}
		}

	}

errorExit:
	if p.HasError() {
		v := p.GetError()
		localctx.SetException(v)
		p.GetErrorHandler().ReportError(p, v)
		p.GetErrorHandler().Recover(p, v)
		p.SetError(nil)
	}
	p.ExitRule()
	return localctx
	goto errorExit // Trick to prevent compiler error if the label is not used
}

// ISelectQueryContext is an interface to support dynamic dispatch.
type ISelectQueryContext interface {
	antlr.ParserRuleContext

	// GetParser returns the parser.
	GetParser() antlr.Parser

	// Getter signatures
	SELECT() antlr.TerminalNode
	AllSelectItem() []ISelectItemContext
	SelectItem(i int) ISelectItemContext
	FROM() antlr.TerminalNode
	Relation() IRelationContext
	DISTINCT() antlr.TerminalNode
	AllCOMMA() []antlr.TerminalNode
	COMMA(i int) antlr.TerminalNode
	WHERE() antlr.TerminalNode
	AllExpression() []IExpressionContext
	Expression(i int) IExpressionContext
	GROUP() antlr.TerminalNode
	BY() antlr.TerminalNode
	ExpressionList() IExpressionListContext
	HAVING() antlr.TerminalNode

	// IsSelectQueryContext differentiates from other interfaces.
	IsSelectQueryContext()
}

type SelectQueryContext struct {
	antlr.BaseParserRuleContext
	parser antlr.Parser
}

func NewEmptySelectQueryContext() *SelectQueryContext {
	var p = new(SelectQueryContext)
	antlr.InitBaseParserRuleContext(&p.BaseParserRuleContext, nil, -1)
	p.RuleIndex = SqlBaseParserRULE_selectQuery
	return p
}

func InitEmptySelectQueryContext(p *SelectQueryContext) {
	antlr.InitBaseParserRuleContext(&p.BaseParserRuleContext, nil, -1)
	p.RuleIndex = SqlBaseParserRULE_selectQuery
}

func (*SelectQueryContext) IsSelectQueryContext() {}

func NewSelectQueryContext(parser antlr.Parser, parent antlr.ParserRuleContext, invokingState int) *SelectQueryContext {
	var p = new(SelectQueryContext)

	antlr.InitBaseParserRuleContext(&p.BaseParserRuleContext, parent, invokingState)

	p.parser = parser
	p.RuleIndex = SqlBaseParserRULE_selectQuery

	return p
}

func (s *SelectQueryContext) GetParser() antlr.Parser { return s.parser }

func (s *SelectQueryContext) SELECT() antlr.TerminalNode {
	return s.GetToken(SqlBaseParserSELECT, 0)
}

func (s *SelectQueryContext) AllSelectItem() []ISelectItemContext {
	children := s.GetChildren()
	len := 0
	for _, ctx := range children {
		if _, ok := ctx.(ISelectItemContext); ok {
			len++
		}
	}

	tst := make([]ISelectItemContext, len)
	i := 0
	for _, ctx := range children {
		if t, ok := ctx.(ISelectItemContext); ok {
			tst[i] = t.(ISelectItemContext)
			i++
		}
	}

	return tst
}

func (s *SelectQueryContext) SelectItem(i int) ISelectItemContext {
	var t antlr.RuleContext
	j := 0
	for _, ctx := range s.GetChildren() {
		if _, ok := ctx.(ISelectItemContext); ok {
			if j == i {
				t = ctx.(antlr.RuleContext)
				break
			}
			j++
		}
	}

	if t == nil {
		return nil
	}

	return t.(ISelectItemContext)
}

func (s *SelectQueryContext) FROM() antlr.TerminalNode {
	return s.GetToken(SqlBaseParserFROM, 0)
}

func (s *SelectQueryContext) Relation() IRelationContext {
	var t antlr.RuleContext
	for _, ctx := range s.GetChildren() {
		if _, ok := ctx.(IRelationContext); ok {
			t = ctx.(antlr.RuleContext)
			break
		}
	}

	if t == nil {
		return nil
	}

	return t.(IRelationContext)
}

func (s *SelectQueryContext) DISTINCT() antlr.TerminalNode {
	return s.GetToken(SqlBaseParserDISTINCT, 0)
}

func (s *SelectQueryContext) AllCOMMA() []antlr.TerminalNode {
	return s.GetTokens(SqlBaseParserCOMMA)
}

func (s *SelectQueryContext) COMMA(i int) antlr.TerminalNode {
	return s.GetToken(SqlBaseParserCOMMA, i)
}

func (s *SelectQueryContext) WHERE() antlr.TerminalNode {
	return s.GetToken(SqlBaseParserWHERE, 0)
}

func (s *SelectQueryContext) AllExpression() []IExpressionContext {
	children := s.GetChildren()
	len := 0
	for _, ctx := range children {
		if _, ok := ctx.(IExpressionContext); ok {
			len++
		}
	}

	tst := make([]IExpressionContext, len)
	i := 0
	for _, ctx := range children {
		if t, ok := ctx.(IExpressionContext); ok {
			tst[i] = t.(IExpressionContext)
			i++
		}
	}

	return tst
}

func (s *SelectQueryContext) Expression(i int) IExpressionContext {
	var t antlr.RuleContext
	j := 0
	for _, ctx := range s.GetChildren() {
		if _, ok := ctx.(IExpressionContext); ok {
			if j == i {
				t = ctx.(antlr.RuleContext)
				break
			}
			j++
		}
	}

	if t == nil {
		return nil
	}

	return t.(IExpressionContext)
}

func (s *SelectQueryContext) GROUP() antlr.TerminalNode {
	return s.GetToken(SqlBaseParserGROUP, 0)
}

func (s *SelectQueryContext) BY() antlr.TerminalNode {
	return s.GetToken(SqlBaseParserBY, 0)
}

func (s *SelectQueryContext) ExpressionList() IExpressionListContext {
	var t antlr.RuleContext
	for _, ctx := range s.GetChildren() {
		if _, ok := ctx.(IExpressionListContext); ok {
			t = ctx.(antlr.RuleContext)
			break
		}
	}

	if t == nil {
		return nil
	}

	return t.(IExpressionListContext)
}

func (s *SelectQueryContext) HAVING() antlr.TerminalNode {
	return s.GetToken(SqlBaseParserHAVING, 0)
}

func (s *SelectQueryContext) GetRuleContext() antlr.RuleContext {
	return s
}

func (s *SelectQueryContext) ToStringTree(ruleNames []string, recog antlr.Recognizer) string {
	return antlr.TreesStringTree(s, ruleNames, recog)
}

func (s *SelectQueryContext) Accept(visitor antlr.ParseTreeVisitor) interface{} {
	switch t := visitor.(type) {
	case SqlBaseVisitor:
		return t.VisitSelectQuery(s)

	default:
		return t.VisitChildren(s)
	}
}

func (p *SqlBaseParser) SelectQuery() (localctx ISelectQueryContext) {
	localctx = NewSelectQueryContext(p, p.GetParserRuleContext(), p.GetState())
	p.EnterRule(localctx, 8, SqlBaseParserRULE_selectQuery)
	var _la int

	p.EnterOuterAlt(localctx, 1)
	{
		p.SetState(91)
		p.Match(SqlBaseParserSELECT)
		if p.HasError() {
			// Recognition error - abort rule
			goto errorExit
		}
	}
	p.SetState(93)
	p.GetErrorHandler().Sync(p)
	if p.HasError() {
		goto errorExit
	}
	_la = p.GetTokenStream().LA(1)

	if _la == SqlBaseParserDISTINCT {
		{
			p.SetState(92)
			p.Match(SqlBaseParserDISTINCT)
			if p.HasError() {
				// Recognition error - abort rule
				goto errorExit
			}
		}

	}
	{
		p.SetState(95)
		p.SelectItem()
	}
	p.SetState(100)
	p.GetErrorHandler().Sync(p)
	if p.HasError() {
		goto errorExit
	}
	_la = p.GetTokenStream().LA(1)

	for _la == SqlBaseParserCOMMA {
		{
			p.SetState(96)
			p.Match(SqlBaseParserCOMMA)
			if p.HasError() {
				// Recognition error - abort rule
				goto errorExit
			}
		}
		{
			p.SetState(97)
			p.SelectItem()
		}

		p.SetState(102)
		p.GetErrorHandler().Sync(p)
		if p.HasError() {
			goto errorExit
		}
		_la = p.GetTokenStream().LA(1)
	}
	{
		p.SetState(103)
		p.Match(SqlBaseParserFROM)
		if p.HasError() {
			// Recognition error - abort rule
			goto errorExit
		}
	}
	{
		p.SetState(104)
		p.Relation()
	}
	p.SetState(107)
	p.GetErrorHandler().Sync(p)
	if p.HasError() {
		goto errorExit
	}
	_la = p.GetTokenStream().LA(1)

	if _la == SqlBaseParserWHERE {
		{
			p.SetState(105)
			p.Match(SqlBaseParserWHERE)
			if p.HasError() {
				// Recognition error - abort rule
				goto errorExit
			}
		}
		{
			p.SetState(106)
			p.Expression()
		}

	}
	p.SetState(112)
	p.GetErrorHandler().Sync(p)
	if p.HasError() {
		goto errorExit
	}
	_la = p.GetTokenStream().LA(1)

	if _la == SqlBaseParserGROUP {
		{
			p.SetState(109)
			p.Match(SqlBaseParserGROUP)
			if p.HasError() {
				// Recognition error - abort rule
				goto errorExit
			}
		}
		{
			p.SetState(110)
			p.Match(SqlBaseParserBY)
			if p.HasError() {
				// Recognition error - abort rule
				goto errorExit
			}
		}
		{
			p.SetState(111)
			p.ExpressionList()
		}

	}
	p.SetState(116)
	p.GetErrorHandler().Sync(p)
	if p.HasError() {
		goto errorExit
	}
	_la = p.GetTokenStream().LA(1)

	if _la == SqlBaseParserHAVING {
		{
			p.SetState(114)
			p.Match(SqlBaseParserHAVING)
			if p.HasError() {
				// Recognition error - abort rule
				goto errorExit
			}
		}
		{
			p.SetState(115)
			p.Expression()
		}

	}

errorExit:
	if p.HasError() {
		v := p.GetError()
		localctx.SetException(v)
		p.GetErrorHandler().ReportError(p, v)
		p.GetErrorHandler().Recover(p, v)
		p.SetError(nil)
	}
	p.ExitRule()
	return localctx
	goto errorExit // Trick to prevent compiler error if the label is not used
}

// ISelectItemContext is an interface to support dynamic dispatch.
type ISelectItemContext interface {
	antlr.ParserRuleContext

	// GetParser returns the parser.
	GetParser() antlr.Parser

	// Getter signatures
	STAR() antlr.TerminalNode
	Identifier() IIdentifierContext
	DOT() antlr.TerminalNode
	Expression() IExpressionContext
	AS() antlr.TerminalNode

	// IsSelectItemContext differentiates from other interfaces.
	IsSelectItemContext()
}

type SelectItemContext struct {
	antlr.BaseParserRuleContext
	parser antlr.Parser
}

func NewEmptySelectItemContext() *SelectItemContext {
	var p = new(SelectItemContext)
	antlr.InitBaseParserRuleContext(&p.BaseParserRuleContext, nil, -1)
	p.RuleIndex = SqlBaseParserRULE_selectItem
	return p
}

func InitEmptySelectItemContext(p *SelectItemContext) {
	antlr.InitBaseParserRuleContext(&p.BaseParserRuleContext, nil, -1)
	p.RuleIndex = SqlBaseParserRULE_selectItem
}

func (*SelectItemContext) IsSelectItemContext() {}

func NewSelectItemContext(parser antlr.Parser, parent antlr.ParserRuleContext, invokingState int) *SelectItemContext {
	var p = new(SelectItemContext)

	antlr.InitBaseParserRuleContext(&p.BaseParserRuleContext, parent, invokingState)

	p.parser = parser
	p.RuleIndex = SqlBaseParserRULE_selectItem

	return p
}

func (s *SelectItemContext) GetParser() antlr.Parser { return s.parser }

func (s *SelectItemContext) STAR() antlr.TerminalNode {
	return s.GetToken(SqlBaseParserSTAR, 0)
}

func (s *SelectItemContext) Identifier() IIdentifierContext {
	var t antlr.RuleContext
	for _, ctx := range s.GetChildren() {
		if _, ok := ctx.(IIdentifierContext); ok {
			t = ctx.(antlr.RuleContext)
			break
		}
	}

	if t == nil {
		return nil
	}

	return t.(IIdentifierContext)
}

func (s *SelectItemContext) DOT() antlr.TerminalNode {
	return s.GetToken(SqlBaseParserDOT, 0)
}

func (s *SelectItemContext) Expression() IExpressionContext {
	var t antlr.RuleContext
	for _, ctx := range s.GetChildren() {
		if _, ok := ctx.(IExpressionContext); ok {
			t = ctx.(antlr.RuleContext)
			break
		}
	}

	if t == nil {
		return nil
	}

	return t.(IExpressionContext)
}

func (s *SelectItemContext) AS() antlr.TerminalNode {
	return s.GetToken(SqlBaseParserAS, 0)
}

func (s *SelectItemContext) GetRuleContext() antlr.RuleContext {
	return s
}

func (s *SelectItemContext) ToStringTree(ruleNames []string, recog antlr.Recognizer) string {
	return antlr.TreesStringTree(s, ruleNames, recog)
}

func (s *SelectItemContext) Accept(visitor antlr.ParseTreeVisitor) interface{} {
	switch t := visitor.(type) {
	case SqlBaseVisitor:
		return t.VisitSelectItem(s)

	default:
		return t.VisitChildren(s)
	}
}

func (p *SqlBaseParser) SelectItem() (localctx ISelectItemContext) {
	localctx = NewSelectItemContext(p, p.GetParserRuleContext(), p.GetState())
	p.EnterRule(localctx, 10, SqlBaseParserRULE_selectItem)
	var _la int

	p.SetState(131)
	p.GetErrorHandler().Sync(p)
	if p.HasError() {
		goto errorExit
	}

	switch p.GetInterpreter().AdaptivePredict(p.BaseParser, p.GetTokenStream(), 15, p.GetParserRuleContext()) {
	case 1:
		p.EnterOuterAlt(localctx, 1)
		p.SetState(121)
		p.GetErrorHandler().Sync(p)
		if p.HasError() {
			goto errorExit
		}
		_la = p.GetTokenStream().LA(1)

		if (int64(_la) & ^0x3f) == 0 && ((int64(1)<<_la)&6917529027641081864) != 0 {
			{
				p.SetState(118)
				p.Identifier()
			}
			{
				p.SetState(119)
				p.Match(SqlBaseParserDOT)
				if p.HasError() {
					// Recognition error - abort rule
					goto errorExit
				}
			}

		}
		{
			p.SetState(123)
			p.Match(SqlBaseParserSTAR)
			if p.HasError() {
				// Recognition error - abort rule
				goto errorExit
			}
		}

	case 2:
		p.EnterOuterAlt(localctx, 2)
		{
			p.SetState(124)
			p.Expression()
		}
		p.SetState(129)
		p.GetErrorHandler().Sync(p)
		if p.HasError() {
			goto errorExit
		}
		_la = p.GetTokenStream().LA(1)

		if (int64(_la) & ^0x3f) == 0 && ((int64(1)<<_la)&6917529027641090056) != 0 {
			p.SetState(126)
			p.GetErrorHandler().Sync(p)
			if p.HasError() {
				goto errorExit
			}
			_la = p.GetTokenStream().LA(1)

			if _la == SqlBaseParserAS {
				{
					p.SetState(125)
					p.Match(SqlBaseParserAS)
					if p.HasError() {
						// Recognition error - abort rule
						goto errorExit
					}
				}

			}
			{
				p.SetState(128)
				p.Identifier()
			}

		}

	case antlr.ATNInvalidAltNumber:
		goto errorExit
	}

errorExit:
	if p.HasError() {
		v := p.GetError()
		localctx.SetException(v)
		p.GetErrorHandler().ReportError(p, v)
		p.GetErrorHandler().Recover(p, v)
		p.SetError(nil)
	}
	p.ExitRule()
	return localctx
	goto errorExit // Trick to prevent compiler error if the label is not used
}

// IRelationContext is an interface to support dynamic dispatch.
type IRelationContext interface {
	antlr.ParserRuleContext

	// GetParser returns the parser.
	GetParser() antlr.Parser

	// Getter signatures
	RelationPrimary() IRelationPrimaryContext
	AllJoinPart() []IJoinPartContext
	JoinPart(i int) IJoinPartContext

	// IsRelationContext differentiates from other interfaces.
	IsRelationContext()
}

type RelationContext struct {
	antlr.BaseParserRuleContext
	parser antlr.Parser
}

func NewEmptyRelationContext() *RelationContext {
	var p = new(RelationContext)
	antlr.InitBaseParserRuleContext(&p.BaseParserRuleContext, nil, -1)
	p.RuleIndex = SqlBaseParserRULE_relation
	return p
}

func InitEmptyRelationContext(p *RelationContext) {
	antlr.InitBaseParserRuleContext(&p.BaseParserRuleContext, nil, -1)
	p.RuleIndex = SqlBaseParserRULE_relation
}

func (*RelationContext) IsRelationContext() {}

func NewRelationContext(parser antlr.Parser, parent antlr.ParserRuleContext, invokingState int) *RelationContext {
	var p = new(RelationContext)

	antlr.InitBaseParserRuleContext(&p.BaseParserRuleContext, parent, invokingState)

	p.parser = parser
	p.RuleIndex = SqlBaseParserRULE_relation

	return p
}

func (s *RelationContext) GetParser() antlr.Parser { return s.parser }

func (s *RelationContext) RelationPrimary() IRelationPrimaryContext {
	var t antlr.RuleContext
	for _, ctx := range s.GetChildren() {
		if _, ok := ctx.(IRelationPrimaryContext); ok {
			t = ctx.(antlr.RuleContext)
			break
		}
	}

	if t == nil {
		return nil
	}

	return t.(IRelationPrimaryContext)
}

func (s *RelationContext) AllJoinPart() []IJoinPartContext {
	children := s.GetChildren()
	len := 0
	for _, ctx := range children {
		if _, ok := ctx.(IJoinPartContext); ok {
			len++
		}
	}

	tst := make([]IJoinPartContext, len)
	i := 0
	for _, ctx := range children {
		if t, ok := ctx.(IJoinPartContext); ok {
			tst[i] = t.(IJoinPartContext)
			i++
		}
	}

	return tst
}

func (s *RelationContext) JoinPart(i int) IJoinPartContext {
	var t antlr.RuleContext
	j := 0
	for _, ctx := range s.GetChildren() {
		if _, ok := ctx.(IJoinPartContext); ok {
			if j == i {
				t = ctx.(antlr.RuleContext)
				break
			}
			j++
		}
	}

	if t == nil {
		return nil
	}

	return t.(IJoinPartContext)
}

func (s *RelationContext) GetRuleContext() antlr.RuleContext {
	return s
}

func (s *RelationContext) ToStringTree(ruleNames []string, recog antlr.Recognizer) string {
	return antlr.TreesStringTree(s, ruleNames, recog)
}

func (s *RelationContext) Accept(visitor antlr.ParseTreeVisitor) interface{} {
	switch t := visitor.(type) {
	case SqlBaseVisitor:
		return t.VisitRelation(s)

	default:
		return t.VisitChildren(s)
	}
}

func (p *SqlBaseParser) Relation() (localctx IRelationContext) {
	localctx = NewRelationContext(p, p.GetParserRuleContext(), p.GetState())
	p.EnterRule(localctx, 12, SqlBaseParserRULE_relation)
	var _la int

	p.EnterOuterAlt(localctx, 1)
	{
		p.SetState(133)
		p.RelationPrimary()
	}
	p.SetState(137)
	p.GetErrorHandler().Sync(p)
	if p.HasError() {
		goto errorExit
	}
	_la = p.GetTokenStream().LA(1)

	for (int64(_la) & ^0x3f) == 0 && ((int64(1)<<_la)&901120) != 0 {
		{
			p.SetState(134)
			p.JoinPart()
		}

		p.SetState(139)
		p.GetErrorHandler().Sync(p)
		if p.HasError() {
			goto errorExit
		}
		_la = p.GetTokenStream().LA(1)
	}

errorExit:
	if p.HasError() {
		v := p.GetError()
		localctx.SetException(v)
		p.GetErrorHandler().ReportError(p, v)
		p.GetErrorHandler().Recover(p, v)
		p.SetError(nil)
	}
	p.ExitRule()
	return localctx
	goto errorExit // Trick to prevent compiler error if the label is not used
}

// IRelationPrimaryContext is an interface to support dynamic dispatch.
type IRelationPrimaryContext interface {
	antlr.ParserRuleContext

	// GetParser returns the parser.
	GetParser() antlr.Parser

	// Getter signatures
	RESOURCE() antlr.TerminalNode
	Identifier() IIdentifierContext
	AS() antlr.TerminalNode
	LPAREN() antlr.TerminalNode
	QueryExpression() IQueryExpressionContext
	RPAREN() antlr.TerminalNode
	Relation() IRelationContext

	// IsRelationPrimaryContext differentiates from other interfaces.
	IsRelationPrimaryContext()
}

type RelationPrimaryContext struct {
	antlr.BaseParserRuleContext
	parser antlr.Parser
}

func NewEmptyRelationPrimaryContext() *RelationPrimaryContext {
	var p = new(RelationPrimaryContext)
	antlr.InitBaseParserRuleContext(&p.BaseParserRuleContext, nil, -1)
	p.RuleIndex = SqlBaseParserRULE_relationPrimary
	return p
}

func InitEmptyRelationPrimaryContext(p *RelationPrimaryContext) {
	antlr.InitBaseParserRuleContext(&p.BaseParserRuleContext, nil, -1)
	p.RuleIndex = SqlBaseParserRULE_relationPrimary
}

func (*RelationPrimaryContext) IsRelationPrimaryContext() {}

func NewRelationPrimaryContext(parser antlr.Parser, parent antlr.ParserRuleContext, invokingState int) *RelationPrimaryContext {
	var p = new(RelationPrimaryContext)

	antlr.InitBaseParserRuleContext(&p.BaseParserRuleContext, parent, invokingState)

	p.parser = parser
	p.RuleIndex = SqlBaseParserRULE_relationPrimary

	return p
}

func (s *RelationPrimaryContext) GetParser() antlr.Parser { return s.parser }

func (s *RelationPrimaryContext) RESOURCE() antlr.TerminalNode {
	return s.GetToken(SqlBaseParserRESOURCE, 0)
}

func (s *RelationPrimaryContext) Identifier() IIdentifierContext {
	var t antlr.RuleContext
	for _, ctx := range s.GetChildren() {
		if _, ok := ctx.(IIdentifierContext); ok {
			t = ctx.(antlr.RuleContext)
			break
		}
	}

	if t == nil {
		return nil
	}

	return t.(IIdentifierContext)
}

func (s *RelationPrimaryContext) AS() antlr.TerminalNode {
	return s.GetToken(SqlBaseParserAS, 0)
}

func (s *RelationPrimaryContext) LPAREN() antlr.TerminalNode {
	return s.GetToken(SqlBaseParserLPAREN, 0)
}

func (s *RelationPrimaryContext) QueryExpression() IQueryExpressionContext {
	var t antlr.RuleContext
	for _, ctx := range s.GetChildren() {
		if _, ok := ctx.(IQueryExpressionContext); ok {
			t = ctx.(antlr.RuleContext)
			break
		}
	}

	if t == nil {
		return nil
	}

	return t.(IQueryExpressionContext)
}

func (s *RelationPrimaryContext) RPAREN() antlr.TerminalNode {
	return s.GetToken(SqlBaseParserRPAREN, 0)
}

func (s *RelationPrimaryContext) Relation() IRelationContext {
	var t antlr.RuleContext
	for _, ctx := range s.GetChildren() {
		if _, ok := ctx.(IRelationContext); ok {
			t = ctx.(antlr.RuleContext)
			break
		}
	}

	if t == nil {
		return nil
	}

	return t.(IRelationContext)
}

func (s *RelationPrimaryContext) GetRuleContext() antlr.RuleContext {
	return s
}

func (s *RelationPrimaryContext) ToStringTree(ruleNames []string, recog antlr.Recognizer) string {
	return antlr.TreesStringTree(s, ruleNames, recog)
}

func (s *RelationPrimaryContext) Accept(visitor antlr.ParseTreeVisitor) interface{} {
	switch t := visitor.(type) {
	case SqlBaseVisitor:
		return t.VisitRelationPrimary(s)

	default:
		return t.VisitChildren(s)
	}
}

func (p *SqlBaseParser) RelationPrimary() (localctx IRelationPrimaryContext) {
	localctx = NewRelationPrimaryContext(p, p.GetParserRuleContext(), p.GetState())
	p.EnterRule(localctx, 14, SqlBaseParserRULE_relationPrimary)
	var _la int

	p.SetState(159)
	p.GetErrorHandler().Sync(p)
	if p.HasError() {
		goto errorExit
	}

	switch p.GetInterpreter().AdaptivePredict(p.BaseParser, p.GetTokenStream(), 20, p.GetParserRuleContext()) {
	case 1:
		p.EnterOuterAlt(localctx, 1)
		{
			p.SetState(140)
			p.Match(SqlBaseParserRESOURCE)
			if p.HasError() {
				// Recognition error - abort rule
				goto errorExit
			}
		}
		p.SetState(145)
		p.GetErrorHandler().Sync(p)
		if p.HasError() {
			goto errorExit
		}
		_la = p.GetTokenStream().LA(1)

		if (int64(_la) & ^0x3f) == 0 && ((int64(1)<<_la)&6917529027641090056) != 0 {
			p.SetState(142)
			p.GetErrorHandler().Sync(p)
			if p.HasError() {
				goto errorExit
			}
			_la = p.GetTokenStream().LA(1)

			if _la == SqlBaseParserAS {
				{
					p.SetState(141)
					p.Match(SqlBaseParserAS)
					if p.HasError() {
						// Recognition error - abort rule
						goto errorExit
					}
				}

			}
			{
				p.SetState(144)
				p.Identifier()
			}

		}

	case 2:
		p.EnterOuterAlt(localctx, 2)
		{
			p.SetState(147)
			p.Match(SqlBaseParserLPAREN)
			if p.HasError() {
				// Recognition error - abort rule
				goto errorExit
			}
		}
		{
			p.SetState(148)
			p.QueryExpression()
		}
		{
			p.SetState(149)
			p.Match(SqlBaseParserRPAREN)
			if p.HasError() {
				// Recognition error - abort rule
				goto errorExit
			}
		}
		p.SetState(151)
		p.GetErrorHandler().Sync(p)
		if p.HasError() {
			goto errorExit
		}
		_la = p.GetTokenStream().LA(1)

		if _la == SqlBaseParserAS {
			{
				p.SetState(150)
				p.Match(SqlBaseParserAS)
				if p.HasError() {
					// Recognition error - abort rule
					goto errorExit
				}
			}

		}
		{
			p.SetState(153)
			p.Identifier()
		}

	case 3:
		p.EnterOuterAlt(localctx, 3)
		{
			p.SetState(155)
			p.Match(SqlBaseParserLPAREN)
			if p.HasError() {
				// Recognition error - abort rule
				goto errorExit
			}
		}
		{
			p.SetState(156)
			p.Relation()
		}
		{
			p.SetState(157)
			p.Match(SqlBaseParserRPAREN)
			if p.HasError() {
				// Recognition error - abort rule
				goto errorExit
			}
		}

	case antlr.ATNInvalidAltNumber:
		goto errorExit
	}

errorExit:
	if p.HasError() {
		v := p.GetError()
		localctx.SetException(v)
		p.GetErrorHandler().ReportError(p, v)
		p.GetErrorHandler().Recover(p, v)
		p.SetError(nil)
	}
	p.ExitRule()
	return localctx
	goto errorExit // Trick to prevent compiler error if the label is not used
}

// IJoinPartContext is an interface to support dynamic dispatch.
type IJoinPartContext interface {
	antlr.ParserRuleContext

	// GetParser returns the parser.
	GetParser() antlr.Parser

	// Getter signatures
	JOIN() antlr.TerminalNode
	RelationPrimary() IRelationPrimaryContext
	ON() antlr.TerminalNode
	Expression() IExpressionContext
	INNER() antlr.TerminalNode
	LEFT() antlr.TerminalNode
	RIGHT() antlr.TerminalNode
	OUTER() antlr.TerminalNode
	CROSS() antlr.TerminalNode

	// IsJoinPartContext differentiates from other interfaces.
	IsJoinPartContext()
}

type JoinPartContext struct {
	antlr.BaseParserRuleContext
	parser antlr.Parser
}

func NewEmptyJoinPartContext() *JoinPartContext {
	var p = new(JoinPartContext)
	antlr.InitBaseParserRuleContext(&p.BaseParserRuleContext, nil, -1)
	p.RuleIndex = SqlBaseParserRULE_joinPart
	return p
}

func InitEmptyJoinPartContext(p *JoinPartContext) {
	antlr.InitBaseParserRuleContext(&p.BaseParserRuleContext, nil, -1)
	p.RuleIndex = SqlBaseParserRULE_joinPart
}

func (*JoinPartContext) IsJoinPartContext() {}

func NewJoinPartContext(parser antlr.Parser, parent antlr.ParserRuleContext, invokingState int) *JoinPartContext {
	var p = new(JoinPartContext)

	antlr.InitBaseParserRuleContext(&p.BaseParserRuleContext, parent, invokingState)

	p.parser = parser
	p.RuleIndex = SqlBaseParserRULE_joinPart

	return p
}

func (s *JoinPartContext) GetParser() antlr.Parser { return s.parser }

func (s *JoinPartContext) JOIN() antlr.TerminalNode {
	return s.GetToken(SqlBaseParserJOIN, 0)
}

func (s *JoinPartContext) RelationPrimary() IRelationPrimaryContext {
	var t antlr.RuleContext
	for _, ctx := range s.GetChildren() {
		if _, ok := ctx.(IRelationPrimaryContext); ok {
			t = ctx.(antlr.RuleContext)
			break
		}
	}

	if t == nil {
		return nil
	}

	return t.(IRelationPrimaryContext)
}

func (s *JoinPartContext) ON() antlr.TerminalNode {
	return s.GetToken(SqlBaseParserON, 0)
}

func (s *JoinPartContext) Expression() IExpressionContext {
	var t antlr.RuleContext
	for _, ctx := range s.GetChildren() {
		if _, ok := ctx.(IExpressionContext); ok {
			t = ctx.(antlr.RuleContext)
			break
		}
	}

	if t == nil {
		return nil
	}

	return t.(IExpressionContext)
}

func (s *JoinPartContext) INNER() antlr.TerminalNode {
	return s.GetToken(SqlBaseParserINNER, 0)
}

func (s *JoinPartContext) LEFT() antlr.TerminalNode {
	return s.GetToken(SqlBaseParserLEFT, 0)
}

func (s *JoinPartContext) RIGHT() antlr.TerminalNode {
	return s.GetToken(SqlBaseParserRIGHT, 0)
}

func (s *JoinPartContext) OUTER() antlr.TerminalNode {
	return s.GetToken(SqlBaseParserOUTER, 0)
}

func (s *JoinPartContext) CROSS() antlr.TerminalNode {
	return s.GetToken(SqlBaseParserCROSS, 0)
}

func (s *JoinPartContext) GetRuleContext() antlr.RuleContext {
	return s
}

func (s *JoinPartContext) ToStringTree(ruleNames []string, recog antlr.Recognizer) string {
	return antlr.TreesStringTree(s, ruleNames, recog)
}

func (s *JoinPartContext) Accept(visitor antlr.ParseTreeVisitor) interface{} {
	switch t := visitor.(type) {
	case SqlBaseVisitor:
		return t.VisitJoinPart(s)

	default:
		return t.VisitChildren(s)
	}
}

func (p *SqlBaseParser) JoinPart() (localctx IJoinPartContext) {
	localctx = NewJoinPartContext(p, p.GetParserRuleContext(), p.GetState())
	p.EnterRule(localctx, 16, SqlBaseParserRULE_joinPart)
	var _la int

	p.SetState(180)
	p.GetErrorHandler().Sync(p)
	if p.HasError() {
		goto errorExit
	}

	switch p.GetTokenStream().LA(1) {
	case SqlBaseParserINNER, SqlBaseParserLEFT, SqlBaseParserRIGHT, SqlBaseParserJOIN:
		p.EnterOuterAlt(localctx, 1)
		p.SetState(170)
		p.GetErrorHandler().Sync(p)
		if p.HasError() {
			goto errorExit
		}
		switch p.GetTokenStream().LA(1) {
		case SqlBaseParserINNER:
			{
				p.SetState(161)
				p.Match(SqlBaseParserINNER)
				if p.HasError() {
					// Recognition error - abort rule
					goto errorExit
				}
			}

		case SqlBaseParserLEFT:
			{
				p.SetState(162)
				p.Match(SqlBaseParserLEFT)
				if p.HasError() {
					// Recognition error - abort rule
					goto errorExit
				}
			}
			p.SetState(164)
			p.GetErrorHandler().Sync(p)
			if p.HasError() {
				goto errorExit
			}
			_la = p.GetTokenStream().LA(1)

			if _la == SqlBaseParserOUTER {
				{
					p.SetState(163)
					p.Match(SqlBaseParserOUTER)
					if p.HasError() {
						// Recognition error - abort rule
						goto errorExit
					}
				}

			}

		case SqlBaseParserRIGHT:
			{
				p.SetState(166)
				p.Match(SqlBaseParserRIGHT)
				if p.HasError() {
					// Recognition error - abort rule
					goto errorExit
				}
			}
			p.SetState(168)
			p.GetErrorHandler().Sync(p)
			if p.HasError() {
				goto errorExit
			}
			_la = p.GetTokenStream().LA(1)

			if _la == SqlBaseParserOUTER {
				{
					p.SetState(167)
					p.Match(SqlBaseParserOUTER)
					if p.HasError() {
						// Recognition error - abort rule
						goto errorExit
					}
				}

			}

		case SqlBaseParserJOIN:

		default:
		}
		{
			p.SetState(172)
			p.Match(SqlBaseParserJOIN)
			if p.HasError() {
				// Recognition error - abort rule
				goto errorExit
			}
		}
		{
			p.SetState(173)
			p.RelationPrimary()
		}
		{
			p.SetState(174)
			p.Match(SqlBaseParserON)
			if p.HasError() {
				// Recognition error - abort rule
				goto errorExit
			}
		}
		{
			p.SetState(175)
			p.Expression()
		}

	case SqlBaseParserCROSS:
		p.EnterOuterAlt(localctx, 2)
		{
			p.SetState(177)
			p.Match(SqlBaseParserCROSS)
			if p.HasError() {
				// Recognition error - abort rule
				goto errorExit
			}
		}
		{
			p.SetState(178)
			p.Match(SqlBaseParserJOIN)
			if p.HasError() {
				// Recognition error - abort rule
				goto errorExit
			}
		}
		{
			p.SetState(179)
			p.RelationPrimary()
		}

	default:
		p.SetError(antlr.NewNoViableAltException(p, nil, nil, nil, nil, nil))
		goto errorExit
	}

errorExit:
	if p.HasError() {
		v := p.GetError()
		localctx.SetException(v)
		p.GetErrorHandler().ReportError(p, v)
		p.GetErrorHandler().Recover(p, v)
		p.SetError(nil)
	}
	p.ExitRule()
	return localctx
	goto errorExit // Trick to prevent compiler error if the label is not used
}

// ISortItemContext is an interface to support dynamic dispatch.
type ISortItemContext interface {
	antlr.ParserRuleContext

	// GetParser returns the parser.
	GetParser() antlr.Parser

	// Getter signatures
	Expression() IExpressionContext
	ASC() antlr.TerminalNode
	DESC() antlr.TerminalNode

	// IsSortItemContext differentiates from other interfaces.
	IsSortItemContext()
}

type SortItemContext struct {
	antlr.BaseParserRuleContext
	parser antlr.Parser
}

func NewEmptySortItemContext() *SortItemContext {
	var p = new(SortItemContext)
	antlr.InitBaseParserRuleContext(&p.BaseParserRuleContext, nil, -1)
	p.RuleIndex = SqlBaseParserRULE_sortItem
	return p
}

func InitEmptySortItemContext(p *SortItemContext) {
	antlr.InitBaseParserRuleContext(&p.BaseParserRuleContext, nil, -1)
	p.RuleIndex = SqlBaseParserRULE_sortItem
}

func (*SortItemContext) IsSortItemContext() {}

func NewSortItemContext(parser antlr.Parser, parent antlr.ParserRuleContext, invokingState int) *SortItemContext {
	var p = new(SortItemContext)

	antlr.InitBaseParserRuleContext(&p.BaseParserRuleContext, parent, invokingState)

	p.parser = parser
	p.RuleIndex = SqlBaseParserRULE_sortItem

	return p
}

func (s *SortItemContext) GetParser() antlr.Parser { return s.parser }

func (s *SortItemContext) Expression() IExpressionContext {
	var t antlr.RuleContext
	for _, ctx := range s.GetChildren() {
		if _, ok := ctx.(IExpressionContext); ok {
			t = ctx.(antlr.RuleContext)
			break
		}
	}

	if t == nil {
		return nil
	}

	return t.(IExpressionContext)
}

func (s *SortItemContext) ASC() antlr.TerminalNode {
	return s.GetToken(SqlBaseParserASC, 0)
}

func (s *SortItemContext) DESC() antlr.TerminalNode {
	return s.GetToken(SqlBaseParserDESC, 0)
}

func (s *SortItemContext) GetRuleContext() antlr.RuleContext {
	return s
}

func (s *SortItemContext) ToStringTree(ruleNames []string, recog antlr.Recognizer) string {
	return antlr.TreesStringTree(s, ruleNames, recog)
}

func (s *SortItemContext) Accept(visitor antlr.ParseTreeVisitor) interface{} {
	switch t := visitor.(type) {
	case SqlBaseVisitor:
		return t.VisitSortItem(s)

	default:
		return t.VisitChildren(s)
	}
}

func (p *SqlBaseParser) SortItem() (localctx ISortItemContext) {
	localctx = NewSortItemContext(p, p.GetParserRuleContext(), p.GetState())
	p.EnterRule(localctx, 18, SqlBaseParserRULE_sortItem)
	var _la int

	p.EnterOuterAlt(localctx, 1)
	{
		p.SetState(182)
		p.Expression()
	}
	p.SetState(184)
	p.GetErrorHandler().Sync(p)
	if p.HasError() {
		goto errorExit
	}
	_la = p.GetTokenStream().LA(1)

	if _la == SqlBaseParserASC || _la == SqlBaseParserDESC {
		{
			p.SetState(183)
			_la = p.GetTokenStream().LA(1)

			if !(_la == SqlBaseParserASC || _la == SqlBaseParserDESC) {
				p.GetErrorHandler().RecoverInline(p)
			} else {
				p.GetErrorHandler().ReportMatch(p)
				p.Consume()
			}
		}

	}

errorExit:
	if p.HasError() {
		v := p.GetError()
		localctx.SetException(v)
		p.GetErrorHandler().ReportError(p, v)
		p.GetErrorHandler().Recover(p, v)
		p.SetError(nil)
	}
	p.ExitRule()
	return localctx
	goto errorExit // Trick to prevent compiler error if the label is not used
}

// ILimitClauseContext is an interface to support dynamic dispatch.
type ILimitClauseContext interface {
	antlr.ParserRuleContext

	// GetParser returns the parser.
	GetParser() antlr.Parser

	// Getter signatures
	LIMIT() antlr.TerminalNode
	AllNUMBER() []antlr.TerminalNode
	NUMBER(i int) antlr.TerminalNode
	OFFSET() antlr.TerminalNode
	COMMA() antlr.TerminalNode

	// IsLimitClauseContext differentiates from other interfaces.
	IsLimitClauseContext()
}

type LimitClauseContext struct {
	antlr.BaseParserRuleContext
	parser antlr.Parser
}

func NewEmptyLimitClauseContext() *LimitClauseContext {
	var p = new(LimitClauseContext)
	antlr.InitBaseParserRuleContext(&p.BaseParserRuleContext, nil, -1)
	p.RuleIndex = SqlBaseParserRULE_limitClause
	return p
}

func InitEmptyLimitClauseContext(p *LimitClauseContext) {
	antlr.InitBaseParserRuleContext(&p.BaseParserRuleContext, nil, -1)
	p.RuleIndex = SqlBaseParserRULE_limitClause
}

func (*LimitClauseContext) IsLimitClauseContext() {}

func NewLimitClauseContext(parser antlr.Parser, parent antlr.ParserRuleContext, invokingState int) *LimitClauseContext {
	var p = new(LimitClauseContext)

	antlr.InitBaseParserRuleContext(&p.BaseParserRuleContext, parent, invokingState)

	p.parser = parser
	p.RuleIndex = SqlBaseParserRULE_limitClause

	return p
}

func (s *LimitClauseContext) GetParser() antlr.Parser { return s.parser }

func (s *LimitClauseContext) LIMIT() antlr.TerminalNode {
	return s.GetToken(SqlBaseParserLIMIT, 0)
}

func (s *LimitClauseContext) AllNUMBER() []antlr.TerminalNode {
	return s.GetTokens(SqlBaseParserNUMBER)
}

func (s *LimitClauseContext) NUMBER(i int) antlr.TerminalNode {
	return s.GetToken(SqlBaseParserNUMBER, i)
}

func (s *LimitClauseContext) OFFSET() antlr.TerminalNode {
	return s.GetToken(SqlBaseParserOFFSET, 0)
}

func (s *LimitClauseContext) COMMA() antlr.TerminalNode {
	return s.GetToken(SqlBaseParserCOMMA, 0)
}

func (s *LimitClauseContext) GetRuleContext() antlr.RuleContext {
	return s
}

func (s *LimitClauseContext) ToStringTree(ruleNames []string, recog antlr.Recognizer) string {
	return antlr.TreesStringTree(s, ruleNames, recog)
}

func (s *LimitClauseContext) Accept(visitor antlr.ParseTreeVisitor) interface{} {
	switch t := visitor.(type) {
	case SqlBaseVisitor:
		return t.VisitLimitClause(s)

	default:
		return t.VisitChildren(s)
	}
}

func (p *SqlBaseParser) LimitClause() (localctx ILimitClauseContext) {
	localctx = NewLimitClauseContext(p, p.GetParserRuleContext(), p.GetState())
	p.EnterRule(localctx, 20, SqlBaseParserRULE_limitClause)
	p.EnterOuterAlt(localctx, 1)
	{
		p.SetState(186)
		p.Match(SqlBaseParserLIMIT)
		if p.HasError() {
			// Recognition error - abort rule
			goto errorExit
		}
	}
	{
		p.SetState(187)
		p.Match(SqlBaseParserNUMBER)
		if p.HasError() {
			// Recognition error - abort rule
			goto errorExit
		}
	}
	p.SetState(192)
	p.GetErrorHandler().Sync(p)
	if p.HasError() {
		goto errorExit
	}
	switch p.GetTokenStream().LA(1) {
	case SqlBaseParserOFFSET:
		{
			p.SetState(188)
			p.Match(SqlBaseParserOFFSET)
			if p.HasError() {
				// Recognition error - abort rule
				goto errorExit
			}
		}
		{
			p.SetState(189)
			p.Match(SqlBaseParserNUMBER)
			if p.HasError() {
				// Recognition error - abort rule
				goto errorExit
			}
		}

	case SqlBaseParserCOMMA:
		{
			p.SetState(190)
			p.Match(SqlBaseParserCOMMA)
			if p.HasError() {
				// Recognition error - abort rule
				goto errorExit
			}
		}
		{
			p.SetState(191)
			p.Match(SqlBaseParserNUMBER)
			if p.HasError() {
				// Recognition error - abort rule
				goto errorExit
			}
		}

	case SqlBaseParserEOF, SqlBaseParserRPAREN, SqlBaseParserSEMICOLON:

	default:
	}

errorExit:
	if p.HasError() {
		v := p.GetError()
		localctx.SetException(v)
		p.GetErrorHandler().ReportError(p, v)
		p.GetErrorHandler().Recover(p, v)
		p.SetError(nil)
	}
	p.ExitRule()
	return localctx
	goto errorExit // Trick to prevent compiler error if the label is not used
}

// IExpressionListContext is an interface to support dynamic dispatch.
type IExpressionListContext interface {
	antlr.ParserRuleContext

	// GetParser returns the parser.
	GetParser() antlr.Parser

	// Getter signatures
	AllExpression() []IExpressionContext
	Expression(i int) IExpressionContext
	AllCOMMA() []antlr.TerminalNode
	COMMA(i int) antlr.TerminalNode

	// IsExpressionListContext differentiates from other interfaces.
	IsExpressionListContext()
}

type ExpressionListContext struct {
	antlr.BaseParserRuleContext
	parser antlr.Parser
}

func NewEmptyExpressionListContext() *ExpressionListContext {
	var p = new(ExpressionListContext)
	antlr.InitBaseParserRuleContext(&p.BaseParserRuleContext, nil, -1)
	p.RuleIndex = SqlBaseParserRULE_expressionList
	return p
}

func InitEmptyExpressionListContext(p *ExpressionListContext) {
	antlr.InitBaseParserRuleContext(&p.BaseParserRuleContext, nil, -1)
	p.RuleIndex = SqlBaseParserRULE_expressionList
}

func (*ExpressionListContext) IsExpressionListContext() {}

func NewExpressionListContext(parser antlr.Parser, parent antlr.ParserRuleContext, invokingState int) *ExpressionListContext {
	var p = new(ExpressionListContext)

	antlr.InitBaseParserRuleContext(&p.BaseParserRuleContext, parent, invokingState)

	p.parser = parser
	p.RuleIndex = SqlBaseParserRULE_expressionList

	return p
}

func (s *ExpressionListContext) GetParser() antlr.Parser { return s.parser }

func (s *ExpressionListContext) AllExpression() []IExpressionContext {
	children := s.GetChildren()
	len := 0
	for _, ctx := range children {
		if _, ok := ctx.(IExpressionContext); ok {
			len++
		}
	}

	tst := make([]IExpressionContext, len)
	i := 0
	for _, ctx := range children {
		if t, ok := ctx.(IExpressionContext); ok {
			tst[i] = t.(IExpressionContext)
			i++
		}
	}

	return tst
}

func (s *ExpressionListContext) Expression(i int) IExpressionContext {
	var t antlr.RuleContext
	j := 0
	for _, ctx := range s.GetChildren() {
		if _, ok := ctx.(IExpressionContext); ok {
			if j == i {
				t = ctx.(antlr.RuleContext)
				break
			}
			j++
		}
	}

	if t == nil {
		return nil
	}

	return t.(IExpressionContext)
}

func (s *ExpressionListContext) AllCOMMA() []antlr.TerminalNode {
	return s.GetTokens(SqlBaseParserCOMMA)
}

func (s *ExpressionListContext) COMMA(i int) antlr.TerminalNode {
	return s.GetToken(SqlBaseParserCOMMA, i)
}

func (s *ExpressionListContext) GetRuleContext() antlr.RuleContext {
	return s
}

func (s *ExpressionListContext) ToStringTree(ruleNames []string, recog antlr.Recognizer) string {
	return antlr.TreesStringTree(s, ruleNames, recog)
}

func (s *ExpressionListContext) Accept(visitor antlr.ParseTreeVisitor) interface{} {
	switch t := visitor.(type) {
	case SqlBaseVisitor:
		return t.VisitExpressionList(s)

	default:
		return t.VisitChildren(s)
	}
}

func (p *SqlBaseParser) ExpressionList() (localctx IExpressionListContext) {
	localctx = NewExpressionListContext(p, p.GetParserRuleContext(), p.GetState())
	p.EnterRule(localctx, 22, SqlBaseParserRULE_expressionList)
	var _la int

	p.EnterOuterAlt(localctx, 1)
	{
		p.SetState(194)
		p.Expression()
	}
	p.SetState(199)
	p.GetErrorHandler().Sync(p)
	if p.HasError() {
		goto errorExit
	}
	_la = p.GetTokenStream().LA(1)

	for _la == SqlBaseParserCOMMA {
		{
			p.SetState(195)
			p.Match(SqlBaseParserCOMMA)
			if p.HasError() {
				// Recognition error - abort rule
				goto errorExit
			}
		}
		{
			p.SetState(196)
			p.Expression()
		}

		p.SetState(201)
		p.GetErrorHandler().Sync(p)
		if p.HasError() {
			goto errorExit
		}
		_la = p.GetTokenStream().LA(1)
	}

errorExit:
	if p.HasError() {
		v := p.GetError()
		localctx.SetException(v)
		p.GetErrorHandler().ReportError(p, v)
		p.GetErrorHandler().Recover(p, v)
		p.SetError(nil)
	}
	p.ExitRule()
	return localctx
	goto errorExit // Trick to prevent compiler error if the label is not used
}

// IExpressionContext is an interface to support dynamic dispatch.
type IExpressionContext interface {
	antlr.ParserRuleContext

	// GetParser returns the parser.
	GetParser() antlr.Parser

	// Getter signatures
	OrExpression() IOrExpressionContext

	// IsExpressionContext differentiates from other interfaces.
	IsExpressionContext()
}

type ExpressionContext struct {
	antlr.BaseParserRuleContext
	parser antlr.Parser
}

func NewEmptyExpressionContext() *ExpressionContext {
	var p = new(ExpressionContext)
	antlr.InitBaseParserRuleContext(&p.BaseParserRuleContext, nil, -1)
	p.RuleIndex = SqlBaseParserRULE_expression
	return p
}

func InitEmptyExpressionContext(p *ExpressionContext) {
	antlr.InitBaseParserRuleContext(&p.BaseParserRuleContext, nil, -1)
	p.RuleIndex = SqlBaseParserRULE_expression
}

func (*ExpressionContext) IsExpressionContext() {}

func NewExpressionContext(parser antlr.Parser, parent antlr.ParserRuleContext, invokingState int) *ExpressionContext {
	var p = new(ExpressionContext)

	antlr.InitBaseParserRuleContext(&p.BaseParserRuleContext, parent, invokingState)

	p.parser = parser
	p.RuleIndex = SqlBaseParserRULE_expression

	return p
}

func (s *ExpressionContext) GetParser() antlr.Parser { return s.parser }

func (s *ExpressionContext) OrExpression() IOrExpressionContext {
	var t antlr.RuleContext
	for _, ctx := range s.GetChildren() {
		if _, ok := ctx.(IOrExpressionContext); ok {
			t = ctx.(antlr.RuleContext)
			break
		}
	}

	if t == nil {
		return nil
	}

	return t.(IOrExpressionContext)
}

func (s *ExpressionContext) GetRuleContext() antlr.RuleContext {
	return s
}

func (s *ExpressionContext) ToStringTree(ruleNames []string, recog antlr.Recognizer) string {
	return antlr.TreesStringTree(s, ruleNames, recog)
}

func (s *ExpressionContext) Accept(visitor antlr.ParseTreeVisitor) interface{} {
	switch t := visitor.(type) {
	case SqlBaseVisitor:
		return t.VisitExpression(s)

	default:
		return t.VisitChildren(s)
	}
}

func (p *SqlBaseParser) Expression() (localctx IExpressionContext) {
	localctx = NewExpressionContext(p, p.GetParserRuleContext(), p.GetState())
	p.EnterRule(localctx, 24, SqlBaseParserRULE_expression)
	p.EnterOuterAlt(localctx, 1)
	{
		p.SetState(202)
		p.OrExpression()
	}

errorExit:
	if p.HasError() {
		v := p.GetError()
		localctx.SetException(v)
		p.GetErrorHandler().ReportError(p, v)
		p.GetErrorHandler().Recover(p, v)
		p.SetError(nil)
	}
	p.ExitRule()
	return localctx
	goto errorExit // Trick to prevent compiler error if the label is not used
}

// IOrExpressionContext is an interface to support dynamic dispatch.
type IOrExpressionContext interface {
	antlr.ParserRuleContext

	// GetParser returns the parser.
	GetParser() antlr.Parser

	// Getter signatures
	AllAndExpression() []IAndExpressionContext
	AndExpression(i int) IAndExpressionContext
	AllOR() []antlr.TerminalNode
	OR(i int) antlr.TerminalNode

	// IsOrExpressionContext differentiates from other interfaces.
	IsOrExpressionContext()
}

type OrExpressionContext struct {
	antlr.BaseParserRuleContext
	parser antlr.Parser
}

func NewEmptyOrExpressionContext() *OrExpressionContext {
	var p = new(OrExpressionContext)
	antlr.InitBaseParserRuleContext(&p.BaseParserRuleContext, nil, -1)
	p.RuleIndex = SqlBaseParserRULE_orExpression
	return p
}

func InitEmptyOrExpressionContext(p *OrExpressionContext) {
	antlr.InitBaseParserRuleContext(&p.BaseParserRuleContext, nil, -1)
	p.RuleIndex = SqlBaseParserRULE_orExpression
}

func (*OrExpressionContext) IsOrExpressionContext() {}

func NewOrExpressionContext(parser antlr.Parser, parent antlr.ParserRuleContext, invokingState int) *OrExpressionContext {
	var p = new(OrExpressionContext)

	antlr.InitBaseParserRuleContext(&p.BaseParserRuleContext, parent, invokingState)

	p.parser = parser
	p.RuleIndex = SqlBaseParserRULE_orExpression

	return p
}

func (s *OrExpressionContext) GetParser() antlr.Parser { return s.parser }

func (s *OrExpressionContext) AllAndExpression() []IAndExpressionContext {
	children := s.GetChildren()
	len := 0
	for _, ctx := range children {
		if _, ok := ctx.(IAndExpressionContext); ok {
			len++
		}
	}

	tst := make([]IAndExpressionContext, len)
	i := 0
	for _, ctx := range children {
		if t, ok := ctx.(IAndExpressionContext); ok {
			tst[i] = t.(IAndExpressionContext)
			i++
		}
	}

	return tst
}

func (s *OrExpressionContext) AndExpression(i int) IAndExpressionContext {
	var t antlr.RuleContext
	j := 0
	for _, ctx := range s.GetChildren() {
		if _, ok := ctx.(IAndExpressionContext); ok {
			if j == i {
				t = ctx.(antlr.RuleContext)
				break
			}
			j++
		}
	}

	if t == nil {
		return nil
	}

	return t.(IAndExpressionContext)
}

func (s *OrExpressionContext) AllOR() []antlr.TerminalNode {
	return s.GetTokens(SqlBaseParserOR)
}

func (s *OrExpressionContext) OR(i int) antlr.TerminalNode {
	return s.GetToken(SqlBaseParserOR, i)
}

func (s *OrExpressionContext) GetRuleContext() antlr.RuleContext {
	return s
}

func (s *OrExpressionContext) ToStringTree(ruleNames []string, recog antlr.Recognizer) string {
	return antlr.TreesStringTree(s, ruleNames, recog)
}

func (s *OrExpressionContext) Accept(visitor antlr.ParseTreeVisitor) interface{} {
	switch t := visitor.(type) {
	case SqlBaseVisitor:
		return t.VisitOrExpression(s)

	default:
		return t.VisitChildren(s)
	}
}

func (p *SqlBaseParser) OrExpression() (localctx IOrExpressionContext) {
	localctx = NewOrExpressionContext(p, p.GetParserRuleContext(), p.GetState())
	p.EnterRule(localctx, 26, SqlBaseParserRULE_orExpression)
	var _la int

	p.EnterOuterAlt(localctx, 1)
	{
		p.SetState(204)
		p.AndExpression()
	}
	p.SetState(209)
	p.GetErrorHandler().Sync(p)
	if p.HasError() {
		goto errorExit
	}
	_la = p.GetTokenStream().LA(1)

	for _la == SqlBaseParserOR {
		{
			p.SetState(205)
			p.Match(SqlBaseParserOR)
			if p.HasError() {
				// Recognition error - abort rule
				goto errorExit
			}
		}
		{
			p.SetState(206)
			p.AndExpression()
		}

		p.SetState(211)
		p.GetErrorHandler().Sync(p)
		if p.HasError() {
			goto errorExit
		}
		_la = p.GetTokenStream().LA(1)
	}

errorExit:
	if p.HasError() {
		v := p.GetError()
		localctx.SetException(v)
		p.GetErrorHandler().ReportError(p, v)
		p.GetErrorHandler().Recover(p, v)
		p.SetError(nil)
	}
	p.ExitRule()
	return localctx
	goto errorExit // Trick to prevent compiler error if the label is not used
}

// IAndExpressionContext is an interface to support dynamic dispatch.
type IAndExpressionContext interface {
	antlr.ParserRuleContext

	// GetParser returns the parser.
	GetParser() antlr.Parser

	// Getter signatures
	AllNotExpression() []INotExpressionContext
	NotExpression(i int) INotExpressionContext
	AllAND() []antlr.TerminalNode
	AND(i int) antlr.TerminalNode

	// IsAndExpressionContext differentiates from other interfaces.
	IsAndExpressionContext()
}

type AndExpressionContext struct {
	antlr.BaseParserRuleContext
	parser antlr.Parser
}

func NewEmptyAndExpressionContext() *AndExpressionContext {
	var p = new(AndExpressionContext)
	antlr.InitBaseParserRuleContext(&p.BaseParserRuleContext, nil, -1)
	p.RuleIndex = SqlBaseParserRULE_andExpression
	return p
}

func InitEmptyAndExpressionContext(p *AndExpressionContext) {
	antlr.InitBaseParserRuleContext(&p.BaseParserRuleContext, nil, -1)
	p.RuleIndex = SqlBaseParserRULE_andExpression
}

func (*AndExpressionContext) IsAndExpressionContext() {}

func NewAndExpressionContext(parser antlr.Parser, parent antlr.ParserRuleContext, invokingState int) *AndExpressionContext {
	var p = new(AndExpressionContext)

	antlr.InitBaseParserRuleContext(&p.BaseParserRuleContext, parent, invokingState)

	p.parser = parser
	p.RuleIndex = SqlBaseParserRULE_andExpression

	return p
}

func (s *AndExpressionContext) GetParser() antlr.Parser { return s.parser }

func (s *AndExpressionContext) AllNotExpression() []INotExpressionContext {
	children := s.GetChildren()
	len := 0
	for _, ctx := range children {
		if _, ok := ctx.(INotExpressionContext); ok {
			len++
		}
	}

	tst := make([]INotExpressionContext, len)
	i := 0
	for _, ctx := range children {
		if t, ok := ctx.(INotExpressionContext); ok {
			tst[i] = t.(INotExpressionContext)
			i++
		}
	}

	return tst
}

func (s *AndExpressionContext) NotExpression(i int) INotExpressionContext {
	var t antlr.RuleContext
	j := 0
	for _, ctx := range s.GetChildren() {
		if _, ok := ctx.(INotExpressionContext); ok {
			if j == i {
				t = ctx.(antlr.RuleContext)
				break
			}
			j++
		}
	}

	if t == nil {
		return nil
	}

	return t.(INotExpressionContext)
}

func (s *AndExpressionContext) AllAND() []antlr.TerminalNode {
	return s.GetTokens(SqlBaseParserAND)
}

func (s *AndExpressionContext) AND(i int) antlr.TerminalNode {
	return s.GetToken(SqlBaseParserAND, i)
}

func (s *AndExpressionContext) GetRuleContext() antlr.RuleContext {
	return s
}

func (s *AndExpressionContext) ToStringTree(ruleNames []string, recog antlr.Recognizer) string {
	return antlr.TreesStringTree(s, ruleNames, recog)
}

func (s *AndExpressionContext) Accept(visitor antlr.ParseTreeVisitor) interface{} {
	switch t := visitor.(type) {
	case SqlBaseVisitor:
		return t.VisitAndExpression(s)

	default:
		return t.VisitChildren(s)
	}
}

func (p *SqlBaseParser) AndExpression() (localctx IAndExpressionContext) {
	localctx = NewAndExpressionContext(p, p.GetParserRuleContext(), p.GetState())
	p.EnterRule(localctx, 28, SqlBaseParserRULE_andExpression)
	var _la int

	p.EnterOuterAlt(localctx, 1)
	{
		p.SetState(212)
		p.NotExpression()
	}
	p.SetState(217)
	p.GetErrorHandler().Sync(p)
	if p.HasError() {
		goto errorExit
	}
	_la = p.GetTokenStream().LA(1)

	for _la == SqlBaseParserAND {
		{
			p.SetState(213)
			p.Match(SqlBaseParserAND)
			if p.HasError() {
				// Recognition error - abort rule
				goto errorExit
			}
		}
		{
			p.SetState(214)
			p.NotExpression()
		}

		p.SetState(219)
		p.GetErrorHandler().Sync(p)
		if p.HasError() {
			goto errorExit
		}
		_la = p.GetTokenStream().LA(1)
	}

errorExit:
	if p.HasError() {
		v := p.GetError()
		localctx.SetException(v)
		p.GetErrorHandler().ReportError(p, v)
		p.GetErrorHandler().Recover(p, v)
		p.SetError(nil)
	}
	p.ExitRule()
	return localctx
	goto errorExit // Trick to prevent compiler error if the label is not used
}

// INotExpressionContext is an interface to support dynamic dispatch.
type INotExpressionContext interface {
	antlr.ParserRuleContext

	// GetParser returns the parser.
	GetParser() antlr.Parser

	// Getter signatures
	NOT() antlr.TerminalNode
	NotExpression() INotExpressionContext
	Predicate() IPredicateContext

	// IsNotExpressionContext differentiates from other interfaces.
	IsNotExpressionContext()
}

type NotExpressionContext struct {
	antlr.BaseParserRuleContext
	parser antlr.Parser
}

func NewEmptyNotExpressionContext() *NotExpressionContext {
	var p = new(NotExpressionContext)
	antlr.InitBaseParserRuleContext(&p.BaseParserRuleContext, nil, -1)
	p.RuleIndex = SqlBaseParserRULE_notExpression
	return p
}

func InitEmptyNotExpressionContext(p *NotExpressionContext) {
	antlr.InitBaseParserRuleContext(&p.BaseParserRuleContext, nil, -1)
	p.RuleIndex = SqlBaseParserRULE_notExpression
}

func (*NotExpressionContext) IsNotExpressionContext() {}

func NewNotExpressionContext(parser antlr.Parser, parent antlr.ParserRuleContext, invokingState int) *NotExpressionContext {
	var p = new(NotExpressionContext)

	antlr.InitBaseParserRuleContext(&p.BaseParserRuleContext, parent, invokingState)

	p.parser = parser
	p.RuleIndex = SqlBaseParserRULE_notExpression

	return p
}

func (s *NotExpressionContext) GetParser() antlr.Parser { return s.parser }

func (s *NotExpressionContext) NOT() antlr.TerminalNode {
	return s.GetToken(SqlBaseParserNOT, 0)
}

func (s *NotExpressionContext) NotExpression() INotExpressionContext {
	var t antlr.RuleContext
	for _, ctx := range s.GetChildren() {
		if _, ok := ctx.(INotExpressionContext); ok {
			t = ctx.(antlr.RuleContext)
			break
		}
	}

	if t == nil {
		return nil
	}

	return t.(INotExpressionContext)
}

func (s *NotExpressionContext) Predicate() IPredicateContext {
	var t antlr.RuleContext
	for _, ctx := range s.GetChildren() {
		if _, ok := ctx.(IPredicateContext); ok {
			t = ctx.(antlr.RuleContext)
			break
		}
	}

	if t == nil {
		return nil
	}

	return t.(IPredicateContext)
}

func (s *NotExpressionContext) GetRuleContext() antlr.RuleContext {
	return s
}

func (s *NotExpressionContext) ToStringTree(ruleNames []string, recog antlr.Recognizer) string {
	return antlr.TreesStringTree(s, ruleNames, recog)
}

func (s *NotExpressionContext) Accept(visitor antlr.ParseTreeVisitor) interface{} {
	switch t := visitor.(type) {
	case SqlBaseVisitor:
		return t.VisitNotExpression(s)

	default:
		return t.VisitChildren(s)
	}
}

func (p *SqlBaseParser) NotExpression() (localctx INotExpressionContext) {
	localctx = NewNotExpressionContext(p, p.GetParserRuleContext(), p.GetState())
	p.EnterRule(localctx, 30, SqlBaseParserRULE_notExpression)
	p.SetState(223)
	p.GetErrorHandler().Sync(p)
	if p.HasError() {
		goto errorExit
	}

	switch p.GetTokenStream().LA(1) {
	case SqlBaseParserNOT:
		p.EnterOuterAlt(localctx, 1)
		{
			p.SetState(220)
			p.Match(SqlBaseParserNOT)
			if p.HasError() {
				// Recognition error - abort rule
				goto errorExit
			}
		}
		{
			p.SetState(221)
			p.NotExpression()
		}

	case SqlBaseParserALL, SqlBaseParserNULL, SqlBaseParserCAST, SqlBaseParserCASE, SqlBaseParserTRUE, SqlBaseParserFALSE, SqlBaseParserCURRENT_TIMESTAMP, SqlBaseParserLPAREN, SqlBaseParserPLUS, SqlBaseParserMINUS, SqlBaseParserNUMBER, SqlBaseParserSTRING, SqlBaseParserQUOTED_IDENTIFIER, SqlBaseParserIDENTIFIER:
		p.EnterOuterAlt(localctx, 2)
		{
			p.SetState(222)
			p.Predicate()
		}

	default:
		p.SetError(antlr.NewNoViableAltException(p, nil, nil, nil, nil, nil))
		goto errorExit
	}

errorExit:
	if p.HasError() {
		v := p.GetError()
		localctx.SetException(v)
		p.GetErrorHandler().ReportError(p, v)
		p.GetErrorHandler().Recover(p, v)
		p.SetError(nil)
	}
	p.ExitRule()
	return localctx
	goto errorExit // Trick to prevent compiler error if the label is not used
}

// IPredicateContext is an interface to support dynamic dispatch.
type IPredicateContext interface {
	antlr.ParserRuleContext

	// GetParser returns the parser.
	GetParser() antlr.Parser

	// Getter signatures
	AllAdditiveExpression() []IAdditiveExpressionContext
	AdditiveExpression(i int) IAdditiveExpressionContext
	IS() antlr.TerminalNode
	NULL() antlr.TerminalNode
	BETWEEN() antlr.TerminalNode
	AND() antlr.TerminalNode
	IN() antlr.TerminalNode
	LPAREN() antlr.TerminalNode
	ExpressionList() IExpressionListContext
	RPAREN() antlr.TerminalNode
	LIKE() antlr.TerminalNode
	EQ() antlr.TerminalNode
	NEQ() antlr.TerminalNode
	LT() antlr.TerminalNode
	LTE() antlr.TerminalNode
	GT() antlr.TerminalNode
	GTE() antlr.TerminalNode
	NOT() antlr.TerminalNode
	ESCAPE() antlr.TerminalNode
	STRING() antlr.TerminalNode

	// IsPredicateContext differentiates from other interfaces.
	IsPredicateContext()
}

type PredicateContext struct {
	antlr.BaseParserRuleContext
	parser antlr.Parser
}

func NewEmptyPredicateContext() *PredicateContext {
	var p = new(PredicateContext)
	antlr.InitBaseParserRuleContext(&p.BaseParserRuleContext, nil, -1)
	p.RuleIndex = SqlBaseParserRULE_predicate
	return p
}

func InitEmptyPredicateContext(p *PredicateContext) {
	antlr.InitBaseParserRuleContext(&p.BaseParserRuleContext, nil, -1)
	p.RuleIndex = SqlBaseParserRULE_predicate
}

func (*PredicateContext) IsPredicateContext() {}

func NewPredicateContext(parser antlr.Parser, parent antlr.ParserRuleContext, invokingState int) *PredicateContext {
	var p = new(PredicateContext)

	antlr.InitBaseParserRuleContext(&p.BaseParserRuleContext, parent, invokingState)

	p.parser = parser
	p.RuleIndex = SqlBaseParserRULE_predicate

	return p
}

func (s *PredicateContext) GetParser() antlr.Parser { return s.parser }

func (s *PredicateContext) AllAdditiveExpression() []IAdditiveExpressionContext {
	children := s.GetChildren()
	len := 0
	for _, ctx := range children {
		if _, ok := ctx.(IAdditiveExpressionContext); ok {
			len++
		}
	}

	tst := make([]IAdditiveExpressionContext, len)
	i := 0
	for _, ctx := range children {
		if t, ok := ctx.(IAdditiveExpressionContext); ok {
			tst[i] = t.(IAdditiveExpressionContext)
			i++
		}
	}

	return tst
}

func (s *PredicateContext) AdditiveExpression(i int) IAdditiveExpressionContext {
	var t antlr.RuleContext
	j := 0
	for _, ctx := range s.GetChildren() {
		if _, ok := ctx.(IAdditiveExpressionContext); ok {
			if j == i {
				t = ctx.(antlr.RuleContext)
				break
			}
			j++
		}
	}

	if t == nil {
		return nil
	}

	return t.(IAdditiveExpressionContext)
}

func (s *PredicateContext) IS() antlr.TerminalNode {
	return s.GetToken(SqlBaseParserIS, 0)
}

func (s *PredicateContext) NULL() antlr.TerminalNode {
	return s.GetToken(SqlBaseParserNULL, 0)
}

func (s *PredicateContext) BETWEEN() antlr.TerminalNode {
	return s.GetToken(SqlBaseParserBETWEEN, 0)
}

func (s *PredicateContext) AND() antlr.TerminalNode {
	return s.GetToken(SqlBaseParserAND, 0)
}

func (s *PredicateContext) IN() antlr.TerminalNode {
	return s.GetToken(SqlBaseParserIN, 0)
}

func (s *PredicateContext) LPAREN() antlr.TerminalNode {
	return s.GetToken(SqlBaseParserLPAREN, 0)
}

func (s *PredicateContext) ExpressionList() IExpressionListContext {
	var t antlr.RuleContext
	for _, ctx := range s.GetChildren() {
		if _, ok := ctx.(IExpressionListContext); ok {
			t = ctx.(antlr.RuleContext)
			break
		}
	}

	if t == nil {
		return nil
	}

	return t.(IExpressionListContext)
}

func (s *PredicateContext) RPAREN() antlr.TerminalNode {
	return s.GetToken(SqlBaseParserRPAREN, 0)
}

func (s *PredicateContext) LIKE() antlr.TerminalNode {
	return s.GetToken(SqlBaseParserLIKE, 0)
}

func (s *PredicateContext) EQ() antlr.TerminalNode {
	return s.GetToken(SqlBaseParserEQ, 0)
}

func (s *PredicateContext) NEQ() antlr.TerminalNode {
	return s.GetToken(SqlBaseParserNEQ, 0)
}

func (s *PredicateContext) LT() antlr.TerminalNode {
	return s.GetToken(SqlBaseParserLT, 0)
}

func (s *PredicateContext) LTE() antlr.TerminalNode {
	return s.GetToken(SqlBaseParserLTE, 0)
}

func (s *PredicateContext) GT() antlr.TerminalNode {
	return s.GetToken(SqlBaseParserGT, 0)
}

func (s *PredicateContext) GTE() antlr.TerminalNode {
	return s.GetToken(SqlBaseParserGTE, 0)
}

func (s *PredicateContext) NOT() antlr.TerminalNode {
	return s.GetToken(SqlBaseParserNOT, 0)
}

func (s *PredicateContext) ESCAPE() antlr.TerminalNode {
	return s.GetToken(SqlBaseParserESCAPE, 0)
}

func (s *PredicateContext) STRING() antlr.TerminalNode {
	return s.GetToken(SqlBaseParserSTRING, 0)
}

func (s *PredicateContext) GetRuleContext() antlr.RuleContext {
	return s
}

func (s *PredicateContext) ToStringTree(ruleNames []string, recog antlr.Recognizer) string {
	return antlr.TreesStringTree(s, ruleNames, recog)
}

func (s *PredicateContext) Accept(visitor antlr.ParseTreeVisitor) interface{} {
	switch t := visitor.(type) {
	case SqlBaseVisitor:
		return t.VisitPredicate(s)

	default:
		return t.VisitChildren(s)
	}
}

func (p *SqlBaseParser) Predicate() (localctx IPredicateContext) {
	localctx = NewPredicateContext(p, p.GetParserRuleContext(), p.GetState())
	p.EnterRule(localctx, 32, SqlBaseParserRULE_predicate)
	var _la int

	p.EnterOuterAlt(localctx, 1)
	{
		p.SetState(225)
		p.AdditiveExpression()
	}
	p.SetState(258)
	p.GetErrorHandler().Sync(p)

	if p.GetInterpreter().AdaptivePredict(p.BaseParser, p.GetTokenStream(), 36, p.GetParserRuleContext()) == 1 {
		{
			p.SetState(226)
			_la = p.GetTokenStream().LA(1)

			if !((int64(_la) & ^0x3f) == 0 && ((int64(1)<<_la)&17732923532771328) != 0) {
				p.GetErrorHandler().RecoverInline(p)
			} else {
				p.GetErrorHandler().ReportMatch(p)
				p.Consume()
			}
		}
		{
			p.SetState(227)
			p.AdditiveExpression()
		}

	} else if p.HasError() { // JIM
		goto errorExit
	} else if p.GetInterpreter().AdaptivePredict(p.BaseParser, p.GetTokenStream(), 36, p.GetParserRuleContext()) == 2 {
		{
			p.SetState(228)
			p.Match(SqlBaseParserIS)
			if p.HasError() {
				// Recognition error - abort rule
				goto errorExit
			}
		}
		p.SetState(230)
		p.GetErrorHandler().Sync(p)
		if p.HasError() {
			goto errorExit
		}
		_la = p.GetTokenStream().LA(1)

		if _la == SqlBaseParserNOT {
			{
				p.SetState(229)
				p.Match(SqlBaseParserNOT)
				if p.HasError() {
					// Recognition error - abort rule
					goto errorExit
				}
			}

		}
		{
			p.SetState(232)
			p.Match(SqlBaseParserNULL)
			if p.HasError() {
				// Recognition error - abort rule
				goto errorExit
			}
		}

	} else if p.HasError() { // JIM
		goto errorExit
	} else if p.GetInterpreter().AdaptivePredict(p.BaseParser, p.GetTokenStream(), 36, p.GetParserRuleContext()) == 3 {
		p.SetState(234)
		p.GetErrorHandler().Sync(p)
		if p.HasError() {
			goto errorExit
		}
		_la = p.GetTokenStream().LA(1)

		if _la == SqlBaseParserNOT {
			{
				p.SetState(233)
				p.Match(SqlBaseParserNOT)
				if p.HasError() {
					// Recognition error - abort rule
					goto errorExit
				}
			}

		}
		{
			p.SetState(236)
			p.Match(SqlBaseParserBETWEEN)
			if p.HasError() {
				// Recognition error - abort rule
				goto errorExit
			}
		}
		{
			p.SetState(237)
			p.AdditiveExpression()
		}
		{
			p.SetState(238)
			p.Match(SqlBaseParserAND)
			if p.HasError() {
				// Recognition error - abort rule
				goto errorExit
			}
		}
		{
			p.SetState(239)
			p.AdditiveExpression()
		}

	} else if p.HasError() { // JIM
		goto errorExit
	} else if p.GetInterpreter().AdaptivePredict(p.BaseParser, p.GetTokenStream(), 36, p.GetParserRuleContext()) == 4 {
		p.SetState(242)
		p.GetErrorHandler().Sync(p)
		if p.HasError() {
			goto errorExit
		}
		_la = p.GetTokenStream().LA(1)

		if _la == SqlBaseParserNOT {
			{
				p.SetState(241)
				p.Match(SqlBaseParserNOT)
				if p.HasError() {
					// Recognition error - abort rule
					goto errorExit
				}
			}

		}
		{
			p.SetState(244)
			p.Match(SqlBaseParserIN)
			if p.HasError() {
				// Recognition error - abort rule
				goto errorExit
			}
		}
		{
			p.SetState(245)
			p.Match(SqlBaseParserLPAREN)
			if p.HasError() {
				// Recognition error - abort rule
				goto errorExit
			}
		}
		{
			p.SetState(246)
			p.ExpressionList()
		}
		{
			p.SetState(247)
			p.Match(SqlBaseParserRPAREN)
			if p.HasError() {
				// Recognition error - abort rule
				goto errorExit
			}
		}

	} else if p.HasError() { // JIM
		goto errorExit
	} else if p.GetInterpreter().AdaptivePredict(p.BaseParser, p.GetTokenStream(), 36, p.GetParserRuleContext()) == 5 {
		p.SetState(250)
		p.GetErrorHandler().Sync(p)
		if p.HasError() {
			goto errorExit
		}
		_la = p.GetTokenStream().LA(1)

		if _la == SqlBaseParserNOT {
			{
				p.SetState(249)
				p.Match(SqlBaseParserNOT)
				if p.HasError() {
					// Recognition error - abort rule
					goto errorExit
				}
			}

		}
		{
			p.SetState(252)
			p.Match(SqlBaseParserLIKE)
			if p.HasError() {
				// Recognition error - abort rule
				goto errorExit
			}
		}
		{
			p.SetState(253)
			p.AdditiveExpression()
		}
		p.SetState(256)
		p.GetErrorHandler().Sync(p)
		if p.HasError() {
			goto errorExit
		}
		_la = p.GetTokenStream().LA(1)

		if _la == SqlBaseParserESCAPE {
			{
				p.SetState(254)
				p.Match(SqlBaseParserESCAPE)
				if p.HasError() {
					// Recognition error - abort rule
					goto errorExit
				}
			}
			{
				p.SetState(255)
				p.Match(SqlBaseParserSTRING)
				if p.HasError() {
					// Recognition error - abort rule
					goto errorExit
				}
			}

		}

	} else if p.HasError() { // JIM
		goto errorExit
	}

errorExit:
	if p.HasError() {
		v := p.GetError()
		localctx.SetException(v)
		p.GetErrorHandler().ReportError(p, v)
		p.GetErrorHandler().Recover(p, v)
		p.SetError(nil)
	}
	p.ExitRule()
	return localctx
	goto errorExit // Trick to prevent compiler error if the label is not used
}

// IAdditiveExpressionContext is an interface to support dynamic dispatch.
type IAdditiveExpressionContext interface {
	antlr.ParserRuleContext

	// GetParser returns the parser.
	GetParser() antlr.Parser

	// Getter signatures
	AllMultiplicativeExpression() []IMultiplicativeExpressionContext
	MultiplicativeExpression(i int) IMultiplicativeExpressionContext
	AllPLUS() []antlr.TerminalNode
	PLUS(i int) antlr.TerminalNode
	AllMINUS() []antlr.TerminalNode
	MINUS(i int) antlr.TerminalNode

	// IsAdditiveExpressionContext differentiates from other interfaces.
	IsAdditiveExpressionContext()
}

type AdditiveExpressionContext struct {
	antlr.BaseParserRuleContext
	parser antlr.Parser
}

func NewEmptyAdditiveExpressionContext() *AdditiveExpressionContext {
	var p = new(AdditiveExpressionContext)
	antlr.InitBaseParserRuleContext(&p.BaseParserRuleContext, nil, -1)
	p.RuleIndex = SqlBaseParserRULE_additiveExpression
	return p
}

func InitEmptyAdditiveExpressionContext(p *AdditiveExpressionContext) {
	antlr.InitBaseParserRuleContext(&p.BaseParserRuleContext, nil, -1)
	p.RuleIndex = SqlBaseParserRULE_additiveExpression
}

func (*AdditiveExpressionContext) IsAdditiveExpressionContext() {}

func NewAdditiveExpressionContext(parser antlr.Parser, parent antlr.ParserRuleContext, invokingState int) *AdditiveExpressionContext {
	var p = new(AdditiveExpressionContext)

	antlr.InitBaseParserRuleContext(&p.BaseParserRuleContext, parent, invokingState)

	p.parser = parser
	p.RuleIndex = SqlBaseParserRULE_additiveExpression

	return p
}

func (s *AdditiveExpressionContext) GetParser() antlr.Parser { return s.parser }

func (s *AdditiveExpressionContext) AllMultiplicativeExpression() []IMultiplicativeExpressionContext {
	children := s.GetChildren()
	len := 0
	for _, ctx := range children {
		if _, ok := ctx.(IMultiplicativeExpressionContext); ok {
			len++
		}
	}

	tst := make([]IMultiplicativeExpressionContext, len)
	i := 0
	for _, ctx := range children {
		if t, ok := ctx.(IMultiplicativeExpressionContext); ok {
			tst[i] = t.(IMultiplicativeExpressionContext)
			i++
		}
	}

	return tst
}

func (s *AdditiveExpressionContext) MultiplicativeExpression(i int) IMultiplicativeExpressionContext {
	var t antlr.RuleContext
	j := 0
	for _, ctx := range s.GetChildren() {
		if _, ok := ctx.(IMultiplicativeExpressionContext); ok {
			if j == i {
				t = ctx.(antlr.RuleContext)
				break
			}
			j++
		}
	}

	if t == nil {
		return nil
	}

	return t.(IMultiplicativeExpressionContext)
}

func (s *AdditiveExpressionContext) AllPLUS() []antlr.TerminalNode {
	return s.GetTokens(SqlBaseParserPLUS)
}

func (s *AdditiveExpressionContext) PLUS(i int) antlr.TerminalNode {
	return s.GetToken(SqlBaseParserPLUS, i)
}

func (s *AdditiveExpressionContext) AllMINUS() []antlr.TerminalNode {
	return s.GetTokens(SqlBaseParserMINUS)
}

func (s *AdditiveExpressionContext) MINUS(i int) antlr.TerminalNode {
	return s.GetToken(SqlBaseParserMINUS, i)
}

func (s *AdditiveExpressionContext) GetRuleContext() antlr.RuleContext {
	return s
}

func (s *AdditiveExpressionContext) ToStringTree(ruleNames []string, recog antlr.Recognizer) string {
	return antlr.TreesStringTree(s, ruleNames, recog)
}

func (s *AdditiveExpressionContext) Accept(visitor antlr.ParseTreeVisitor) interface{} {
	switch t := visitor.(type) {
	case SqlBaseVisitor:
		return t.VisitAdditiveExpression(s)

	default:
		return t.VisitChildren(s)
	}
}

func (p *SqlBaseParser) AdditiveExpression() (localctx IAdditiveExpressionContext) {
	localctx = NewAdditiveExpressionContext(p, p.GetParserRuleContext(), p.GetState())
	p.EnterRule(localctx, 34, SqlBaseParserRULE_additiveExpression)
	var _la int

	p.EnterOuterAlt(localctx, 1)
	{
		p.SetState(260)
		p.MultiplicativeExpression()
	}
	p.SetState(265)
	p.GetErrorHandler().Sync(p)
	if p.HasError() {
		goto errorExit
	}
	_la = p.GetTokenStream().LA(1)

	for _la == SqlBaseParserPLUS || _la == SqlBaseParserMINUS {
		{
			p.SetState(261)
			_la = p.GetTokenStream().LA(1)

			if !(_la == SqlBaseParserPLUS || _la == SqlBaseParserMINUS) {
				p.GetErrorHandler().RecoverInline(p)
			} else {
				p.GetErrorHandler().ReportMatch(p)
				p.Consume()
			}
		}
		{
			p.SetState(262)
			p.MultiplicativeExpression()
		}

		p.SetState(267)
		p.GetErrorHandler().Sync(p)
		if p.HasError() {
			goto errorExit
		}
		_la = p.GetTokenStream().LA(1)
	}

errorExit:
	if p.HasError() {
		v := p.GetError()
		localctx.SetException(v)
		p.GetErrorHandler().ReportError(p, v)
		p.GetErrorHandler().Recover(p, v)
		p.SetError(nil)
	}
	p.ExitRule()
	return localctx
	goto errorExit // Trick to prevent compiler error if the label is not used
}

// IMultiplicativeExpressionContext is an interface to support dynamic dispatch.
type IMultiplicativeExpressionContext interface {
	antlr.ParserRuleContext

	// GetParser returns the parser.
	GetParser() antlr.Parser

	// Getter signatures
	AllUnaryExpression() []IUnaryExpressionContext
	UnaryExpression(i int) IUnaryExpressionContext
	AllSTAR() []antlr.TerminalNode
	STAR(i int) antlr.TerminalNode
	AllSLASH() []antlr.TerminalNode
	SLASH(i int) antlr.TerminalNode
	AllPERCENT() []antlr.TerminalNode
	PERCENT(i int) antlr.TerminalNode

	// IsMultiplicativeExpressionContext differentiates from other interfaces.
	IsMultiplicativeExpressionContext()
}

type MultiplicativeExpressionContext struct {
	antlr.BaseParserRuleContext
	parser antlr.Parser
}

func NewEmptyMultiplicativeExpressionContext() *MultiplicativeExpressionContext {
	var p = new(MultiplicativeExpressionContext)
	antlr.InitBaseParserRuleContext(&p.BaseParserRuleContext, nil, -1)
	p.RuleIndex = SqlBaseParserRULE_multiplicativeExpression
	return p
}

func InitEmptyMultiplicativeExpressionContext(p *MultiplicativeExpressionContext) {
	antlr.InitBaseParserRuleContext(&p.BaseParserRuleContext, nil, -1)
	p.RuleIndex = SqlBaseParserRULE_multiplicativeExpression
}

func (*MultiplicativeExpressionContext) IsMultiplicativeExpressionContext() {}

func NewMultiplicativeExpressionContext(parser antlr.Parser, parent antlr.ParserRuleContext, invokingState int) *MultiplicativeExpressionContext {
	var p = new(MultiplicativeExpressionContext)

	antlr.InitBaseParserRuleContext(&p.BaseParserRuleContext, parent, invokingState)

	p.parser = parser
	p.RuleIndex = SqlBaseParserRULE_multiplicativeExpression

	return p
}

func (s *MultiplicativeExpressionContext) GetParser() antlr.Parser { return s.parser }

func (s *MultiplicativeExpressionContext) AllUnaryExpression() []IUnaryExpressionContext {
	children := s.GetChildren()
	len := 0
	for _, ctx := range children {
		if _, ok := ctx.(IUnaryExpressionContext); ok {
			len++
		}
	}

	tst := make([]IUnaryExpressionContext, len)
	i := 0
	for _, ctx := range children {
		if t, ok := ctx.(IUnaryExpressionContext); ok {
			tst[i] = t.(IUnaryExpressionContext)
			i++
		}
	}

	return tst
}

func (s *MultiplicativeExpressionContext) UnaryExpression(i int) IUnaryExpressionContext {
	var t antlr.RuleContext
	j := 0
	for _, ctx := range s.GetChildren() {
		if _, ok := ctx.(IUnaryExpressionContext); ok {
			if j == i {
				t = ctx.(antlr.RuleContext)
				break
			}
			j++
		}
	}

	if t == nil {
		return nil
	}

	return t.(IUnaryExpressionContext)
}

func (s *MultiplicativeExpressionContext) AllSTAR() []antlr.TerminalNode {
	return s.GetTokens(SqlBaseParserSTAR)
}

func (s *MultiplicativeExpressionContext) STAR(i int) antlr.TerminalNode {
	return s.GetToken(SqlBaseParserSTAR, i)
}

func (s *MultiplicativeExpressionContext) AllSLASH() []antlr.TerminalNode {
	return s.GetTokens(SqlBaseParserSLASH)
}

func (s *MultiplicativeExpressionContext) SLASH(i int) antlr.TerminalNode {
	return s.GetToken(SqlBaseParserSLASH, i)
}

func (s *MultiplicativeExpressionContext) AllPERCENT() []antlr.TerminalNode {
	return s.GetTokens(SqlBaseParserPERCENT)
}

func (s *MultiplicativeExpressionContext) PERCENT(i int) antlr.TerminalNode {
	return s.GetToken(SqlBaseParserPERCENT, i)
}

func (s *MultiplicativeExpressionContext) GetRuleContext() antlr.RuleContext {
	return s
}

func (s *MultiplicativeExpressionContext) ToStringTree(ruleNames []string, recog antlr.Recognizer) string {
	return antlr.TreesStringTree(s, ruleNames, recog)
}

func (s *MultiplicativeExpressionContext) Accept(visitor antlr.ParseTreeVisitor) interface{} {
	switch t := visitor.(type) {
	case SqlBaseVisitor:
		return t.VisitMultiplicativeExpression(s)

	default:
		return t.VisitChildren(s)
	}
}

func (p *SqlBaseParser) MultiplicativeExpression() (localctx IMultiplicativeExpressionContext) {
	localctx = NewMultiplicativeExpressionContext(p, p.GetParserRuleContext(), p.GetState())
	p.EnterRule(localctx, 36, SqlBaseParserRULE_multiplicativeExpression)
	var _la int

	p.EnterOuterAlt(localctx, 1)
	{
		p.SetState(268)
		p.UnaryExpression()
	}
	p.SetState(273)
	p.GetErrorHandler().Sync(p)
	if p.HasError() {
		goto errorExit
	}
	_la = p.GetTokenStream().LA(1)

	for (int64(_la) & ^0x3f) == 0 && ((int64(1)<<_la)&504403158265495552) != 0 {
		{
			p.SetState(269)
			_la = p.GetTokenStream().LA(1)

			if !((int64(_la) & ^0x3f) == 0 && ((int64(1)<<_la)&504403158265495552) != 0) {
				p.GetErrorHandler().RecoverInline(p)
			} else {
				p.GetErrorHandler().ReportMatch(p)
				p.Consume()
			}
		}
		{
			p.SetState(270)
			p.UnaryExpression()
		}

		p.SetState(275)
		p.GetErrorHandler().Sync(p)
		if p.HasError() {
			goto errorExit
		}
		_la = p.GetTokenStream().LA(1)
	}

errorExit:
	if p.HasError() {
		v := p.GetError()
		localctx.SetException(v)
		p.GetErrorHandler().ReportError(p, v)
		p.GetErrorHandler().Recover(p, v)
		p.SetError(nil)
	}
	p.ExitRule()
	return localctx
	goto errorExit // Trick to prevent compiler error if the label is not used
}

// IUnaryExpressionContext is an interface to support dynamic dispatch.
type IUnaryExpressionContext interface {
	antlr.ParserRuleContext

	// GetParser returns the parser.
	GetParser() antlr.Parser

	// Getter signatures
	UnaryExpression() IUnaryExpressionContext
	PLUS() antlr.TerminalNode
	MINUS() antlr.TerminalNode
	PrimaryExpression() IPrimaryExpressionContext

	// IsUnaryExpressionContext differentiates from other interfaces.
	IsUnaryExpressionContext()
}

type UnaryExpressionContext struct {
	antlr.BaseParserRuleContext
	parser antlr.Parser
}

func NewEmptyUnaryExpressionContext() *UnaryExpressionContext {
	var p = new(UnaryExpressionContext)
	antlr.InitBaseParserRuleContext(&p.BaseParserRuleContext, nil, -1)
	p.RuleIndex = SqlBaseParserRULE_unaryExpression
	return p
}

func InitEmptyUnaryExpressionContext(p *UnaryExpressionContext) {
	antlr.InitBaseParserRuleContext(&p.BaseParserRuleContext, nil, -1)
	p.RuleIndex = SqlBaseParserRULE_unaryExpression
}

func (*UnaryExpressionContext) IsUnaryExpressionContext() {}

func NewUnaryExpressionContext(parser antlr.Parser, parent antlr.ParserRuleContext, invokingState int) *UnaryExpressionContext {
	var p = new(UnaryExpressionContext)

	antlr.InitBaseParserRuleContext(&p.BaseParserRuleContext, parent, invokingState)

	p.parser = parser
	p.RuleIndex = SqlBaseParserRULE_unaryExpression

	return p
}

func (s *UnaryExpressionContext) GetParser() antlr.Parser { return s.parser }

func (s *UnaryExpressionContext) UnaryExpression() IUnaryExpressionContext {
	var t antlr.RuleContext
	for _, ctx := range s.GetChildren() {
		if _, ok := ctx.(IUnaryExpressionContext); ok {
			t = ctx.(antlr.RuleContext)
			break
		}
	}

	if t == nil {
		return nil
	}

	return t.(IUnaryExpressionContext)
}

func (s *UnaryExpressionContext) PLUS() antlr.TerminalNode {
	return s.GetToken(SqlBaseParserPLUS, 0)
}

func (s *UnaryExpressionContext) MINUS() antlr.TerminalNode {
	return s.GetToken(SqlBaseParserMINUS, 0)
}

func (s *UnaryExpressionContext) PrimaryExpression() IPrimaryExpressionContext {
	var t antlr.RuleContext
	for _, ctx := range s.GetChildren() {
		if _, ok := ctx.(IPrimaryExpressionContext); ok {
			t = ctx.(antlr.RuleContext)
			break
		}
	}

	if t == nil {
		return nil
	}

	return t.(IPrimaryExpressionContext)
}

func (s *UnaryExpressionContext) GetRuleContext() antlr.RuleContext {
	return s
}

func (s *UnaryExpressionContext) ToStringTree(ruleNames []string, recog antlr.Recognizer) string {
	return antlr.TreesStringTree(s, ruleNames, recog)
}

func (s *UnaryExpressionContext) Accept(visitor antlr.ParseTreeVisitor) interface{} {
	switch t := visitor.(type) {
	case SqlBaseVisitor:
		return t.VisitUnaryExpression(s)

	default:
		return t.VisitChildren(s)
	}
}

func (p *SqlBaseParser) UnaryExpression() (localctx IUnaryExpressionContext) {
	localctx = NewUnaryExpressionContext(p, p.GetParserRuleContext(), p.GetState())
	p.EnterRule(localctx, 38, SqlBaseParserRULE_unaryExpression)
	var _la int

	p.SetState(279)
	p.GetErrorHandler().Sync(p)
	if p.HasError() {
		goto errorExit
	}

	switch p.GetTokenStream().LA(1) {
	case SqlBaseParserPLUS, SqlBaseParserMINUS:
		p.EnterOuterAlt(localctx, 1)
		{
			p.SetState(276)
			_la = p.GetTokenStream().LA(1)

			if !(_la == SqlBaseParserPLUS || _la == SqlBaseParserMINUS) {
				p.GetErrorHandler().RecoverInline(p)
			} else {
				p.GetErrorHandler().ReportMatch(p)
				p.Consume()
			}
		}
		{
			p.SetState(277)
			p.UnaryExpression()
		}

	case SqlBaseParserALL, SqlBaseParserNULL, SqlBaseParserCAST, SqlBaseParserCASE, SqlBaseParserTRUE, SqlBaseParserFALSE, SqlBaseParserCURRENT_TIMESTAMP, SqlBaseParserLPAREN, SqlBaseParserNUMBER, SqlBaseParserSTRING, SqlBaseParserQUOTED_IDENTIFIER, SqlBaseParserIDENTIFIER:
		p.EnterOuterAlt(localctx, 2)
		{
			p.SetState(278)
			p.PrimaryExpression()
		}

	default:
		p.SetError(antlr.NewNoViableAltException(p, nil, nil, nil, nil, nil))
		goto errorExit
	}

errorExit:
	if p.HasError() {
		v := p.GetError()
		localctx.SetException(v)
		p.GetErrorHandler().ReportError(p, v)
		p.GetErrorHandler().Recover(p, v)
		p.SetError(nil)
	}
	p.ExitRule()
	return localctx
	goto errorExit // Trick to prevent compiler error if the label is not used
}

// IPrimaryExpressionContext is an interface to support dynamic dispatch.
type IPrimaryExpressionContext interface {
	antlr.ParserRuleContext

	// GetParser returns the parser.
	GetParser() antlr.Parser

	// Getter signatures
	Literal() ILiteralContext
	AllIdentifier() []IIdentifierContext
	Identifier(i int) IIdentifierContext
	DOT() antlr.TerminalNode
	FunctionCall() IFunctionCallContext
	CURRENT_TIMESTAMP() antlr.TerminalNode
	LPAREN() antlr.TerminalNode
	RPAREN() antlr.TerminalNode
	CAST() antlr.TerminalNode
	AllExpression() []IExpressionContext
	Expression(i int) IExpressionContext
	AS() antlr.TerminalNode
	TypeName() ITypeNameContext
	CASE() antlr.TerminalNode
	END() antlr.TerminalNode
	AllWHEN() []antlr.TerminalNode
	WHEN(i int) antlr.TerminalNode
	AllTHEN() []antlr.TerminalNode
	THEN(i int) antlr.TerminalNode
	ELSE() antlr.TerminalNode

	// IsPrimaryExpressionContext differentiates from other interfaces.
	IsPrimaryExpressionContext()
}

type PrimaryExpressionContext struct {
	antlr.BaseParserRuleContext
	parser antlr.Parser
}

func NewEmptyPrimaryExpressionContext() *PrimaryExpressionContext {
	var p = new(PrimaryExpressionContext)
	antlr.InitBaseParserRuleContext(&p.BaseParserRuleContext, nil, -1)
	p.RuleIndex = SqlBaseParserRULE_primaryExpression
	return p
}

func InitEmptyPrimaryExpressionContext(p *PrimaryExpressionContext) {
	antlr.InitBaseParserRuleContext(&p.BaseParserRuleContext, nil, -1)
	p.RuleIndex = SqlBaseParserRULE_primaryExpression
}

func (*PrimaryExpressionContext) IsPrimaryExpressionContext() {}

func NewPrimaryExpressionContext(parser antlr.Parser, parent antlr.ParserRuleContext, invokingState int) *PrimaryExpressionContext {
	var p = new(PrimaryExpressionContext)

	antlr.InitBaseParserRuleContext(&p.BaseParserRuleContext, parent, invokingState)

	p.parser = parser
	p.RuleIndex = SqlBaseParserRULE_primaryExpression

	return p
}

func (s *PrimaryExpressionContext) GetParser() antlr.Parser { return s.parser }

func (s *PrimaryExpressionContext) Literal() ILiteralContext {
	var t antlr.RuleContext
	for _, ctx := range s.GetChildren() {
		if _, ok := ctx.(ILiteralContext); ok {
			t = ctx.(antlr.RuleContext)
			break
		}
	}

	if t == nil {
		return nil
	}

	return t.(ILiteralContext)
}

func (s *PrimaryExpressionContext) AllIdentifier() []IIdentifierContext {
	children := s.GetChildren()
	len := 0
	for _, ctx := range children {
		if _, ok := ctx.(IIdentifierContext); ok {
			len++
		}
	}

	tst := make([]IIdentifierContext, len)
	i := 0
	for _, ctx := range children {
		if t, ok := ctx.(IIdentifierContext); ok {
			tst[i] = t.(IIdentifierContext)
			i++
		}
	}

	return tst
}

func (s *PrimaryExpressionContext) Identifier(i int) IIdentifierContext {
	var t antlr.RuleContext
	j := 0
	for _, ctx := range s.GetChildren() {
		if _, ok := ctx.(IIdentifierContext); ok {
			if j == i {
				t = ctx.(antlr.RuleContext)
				break
			}
			j++
		}
	}

	if t == nil {
		return nil
	}

	return t.(IIdentifierContext)
}

func (s *PrimaryExpressionContext) DOT() antlr.TerminalNode {
	return s.GetToken(SqlBaseParserDOT, 0)
}

func (s *PrimaryExpressionContext) FunctionCall() IFunctionCallContext {
	var t antlr.RuleContext
	for _, ctx := range s.GetChildren() {
		if _, ok := ctx.(IFunctionCallContext); ok {
			t = ctx.(antlr.RuleContext)
			break
		}
	}

	if t == nil {
		return nil
	}

	return t.(IFunctionCallContext)
}

func (s *PrimaryExpressionContext) CURRENT_TIMESTAMP() antlr.TerminalNode {
	return s.GetToken(SqlBaseParserCURRENT_TIMESTAMP, 0)
}

func (s *PrimaryExpressionContext) LPAREN() antlr.TerminalNode {
	return s.GetToken(SqlBaseParserLPAREN, 0)
}

func (s *PrimaryExpressionContext) RPAREN() antlr.TerminalNode {
	return s.GetToken(SqlBaseParserRPAREN, 0)
}

func (s *PrimaryExpressionContext) CAST() antlr.TerminalNode {
	return s.GetToken(SqlBaseParserCAST, 0)
}

func (s *PrimaryExpressionContext) AllExpression() []IExpressionContext {
	children := s.GetChildren()
	len := 0
	for _, ctx := range children {
		if _, ok := ctx.(IExpressionContext); ok {
			len++
		}
	}

	tst := make([]IExpressionContext, len)
	i := 0
	for _, ctx := range children {
		if t, ok := ctx.(IExpressionContext); ok {
			tst[i] = t.(IExpressionContext)
			i++
		}
	}

	return tst
}

func (s *PrimaryExpressionContext) Expression(i int) IExpressionContext {
	var t antlr.RuleContext
	j := 0
	for _, ctx := range s.GetChildren() {
		if _, ok := ctx.(IExpressionContext); ok {
			if j == i {
				t = ctx.(antlr.RuleContext)
				break
			}
			j++
		}
	}

	if t == nil {
		return nil
	}

	return t.(IExpressionContext)
}

func (s *PrimaryExpressionContext) AS() antlr.TerminalNode {
	return s.GetToken(SqlBaseParserAS, 0)
}

func (s *PrimaryExpressionContext) TypeName() ITypeNameContext {
	var t antlr.RuleContext
	for _, ctx := range s.GetChildren() {
		if _, ok := ctx.(ITypeNameContext); ok {
			t = ctx.(antlr.RuleContext)
			break
		}
	}

	if t == nil {
		return nil
	}

	return t.(ITypeNameContext)
}

func (s *PrimaryExpressionContext) CASE() antlr.TerminalNode {
	return s.GetToken(SqlBaseParserCASE, 0)
}

func (s *PrimaryExpressionContext) END() antlr.TerminalNode {
	return s.GetToken(SqlBaseParserEND, 0)
}

func (s *PrimaryExpressionContext) AllWHEN() []antlr.TerminalNode {
	return s.GetTokens(SqlBaseParserWHEN)
}

func (s *PrimaryExpressionContext) WHEN(i int) antlr.TerminalNode {
	return s.GetToken(SqlBaseParserWHEN, i)
}

func (s *PrimaryExpressionContext) AllTHEN() []antlr.TerminalNode {
	return s.GetTokens(SqlBaseParserTHEN)
}

func (s *PrimaryExpressionContext) THEN(i int) antlr.TerminalNode {
	return s.GetToken(SqlBaseParserTHEN, i)
}

func (s *PrimaryExpressionContext) ELSE() antlr.TerminalNode {
	return s.GetToken(SqlBaseParserELSE, 0)
}

func (s *PrimaryExpressionContext) GetRuleContext() antlr.RuleContext {
	return s
}

func (s *PrimaryExpressionContext) ToStringTree(ruleNames []string, recog antlr.Recognizer) string {
	return antlr.TreesStringTree(s, ruleNames, recog)
}

func (s *PrimaryExpressionContext) Accept(visitor antlr.ParseTreeVisitor) interface{} {
	switch t := visitor.(type) {
	case SqlBaseVisitor:
		return t.VisitPrimaryExpression(s)

	default:
		return t.VisitChildren(s)
	}
}

func (p *SqlBaseParser) PrimaryExpression() (localctx IPrimaryExpressionContext) {
	localctx = NewPrimaryExpressionContext(p, p.GetParserRuleContext(), p.GetState())
	p.EnterRule(localctx, 40, SqlBaseParserRULE_primaryExpression)
	var _la int

	p.SetState(323)
	p.GetErrorHandler().Sync(p)
	if p.HasError() {
		goto errorExit
	}

	switch p.GetInterpreter().AdaptivePredict(p.BaseParser, p.GetTokenStream(), 45, p.GetParserRuleContext()) {
	case 1:
		p.EnterOuterAlt(localctx, 1)
		{
			p.SetState(281)
			p.Literal()
		}

	case 2:
		p.EnterOuterAlt(localctx, 2)
		{
			p.SetState(282)
			p.Identifier()
		}
		p.SetState(285)
		p.GetErrorHandler().Sync(p)
		if p.HasError() {
			goto errorExit
		}
		_la = p.GetTokenStream().LA(1)

		if _la == SqlBaseParserDOT {
			{
				p.SetState(283)
				p.Match(SqlBaseParserDOT)
				if p.HasError() {
					// Recognition error - abort rule
					goto errorExit
				}
			}
			{
				p.SetState(284)
				p.Identifier()
			}

		}

	case 3:
		p.EnterOuterAlt(localctx, 3)
		{
			p.SetState(287)
			p.FunctionCall()
		}

	case 4:
		p.EnterOuterAlt(localctx, 4)
		{
			p.SetState(288)
			p.Match(SqlBaseParserCURRENT_TIMESTAMP)
			if p.HasError() {
				// Recognition error - abort rule
				goto errorExit
			}
		}
		p.SetState(291)
		p.GetErrorHandler().Sync(p)
		if p.HasError() {
			goto errorExit
		}
		_la = p.GetTokenStream().LA(1)

		if _la == SqlBaseParserLPAREN {
			{
				p.SetState(289)
				p.Match(SqlBaseParserLPAREN)
				if p.HasError() {
					// Recognition error - abort rule
					goto errorExit
				}
			}
			{
				p.SetState(290)
				p.Match(SqlBaseParserRPAREN)
				if p.HasError() {
					// Recognition error - abort rule
					goto errorExit
				}
			}

		}

	case 5:
		p.EnterOuterAlt(localctx, 5)
		{
			p.SetState(293)
			p.Match(SqlBaseParserCAST)
			if p.HasError() {
				// Recognition error - abort rule
				goto errorExit
			}
		}
		{
			p.SetState(294)
			p.Match(SqlBaseParserLPAREN)
			if p.HasError() {
				// Recognition error - abort rule
				goto errorExit
			}
		}
		{
			p.SetState(295)
			p.Expression()
		}
		{
			p.SetState(296)
			p.Match(SqlBaseParserAS)
			if p.HasError() {
				// Recognition error - abort rule
				goto errorExit
			}
		}
		{
			p.SetState(297)
			p.TypeName()
		}
		{
			p.SetState(298)
			p.Match(SqlBaseParserRPAREN)
			if p.HasError() {
				// Recognition error - abort rule
				goto errorExit
			}
		}

	case 6:
		p.EnterOuterAlt(localctx, 6)
		{
			p.SetState(300)
			p.Match(SqlBaseParserCASE)
			if p.HasError() {
				// Recognition error - abort rule
				goto errorExit
			}
		}
		p.SetState(302)
		p.GetErrorHandler().Sync(p)
		if p.HasError() {
			goto errorExit
		}
		_la = p.GetTokenStream().LA(1)

		if (int64(_la) & ^0x3f) == 0 && ((int64(1)<<_la)&8700965213370843144) != 0 {
			{
				p.SetState(301)
				p.Expression()
			}

		}
		p.SetState(309)
		p.GetErrorHandler().Sync(p)
		if p.HasError() {
			goto errorExit
		}
		_la = p.GetTokenStream().LA(1)

		for ok := true; ok; ok = _la == SqlBaseParserWHEN {
			{
				p.SetState(304)
				p.Match(SqlBaseParserWHEN)
				if p.HasError() {
					// Recognition error - abort rule
					goto errorExit
				}
			}
			{
				p.SetState(305)
				p.Expression()
			}
			{
				p.SetState(306)
				p.Match(SqlBaseParserTHEN)
				if p.HasError() {
					// Recognition error - abort rule
					goto errorExit
				}
			}
			{
				p.SetState(307)
				p.Expression()
			}

			p.SetState(311)
			p.GetErrorHandler().Sync(p)
			if p.HasError() {
				goto errorExit
			}
			_la = p.GetTokenStream().LA(1)
		}
		p.SetState(315)
		p.GetErrorHandler().Sync(p)
		if p.HasError() {
			goto errorExit
		}
		_la = p.GetTokenStream().LA(1)

		if _la == SqlBaseParserELSE {
			{
				p.SetState(313)
				p.Match(SqlBaseParserELSE)
				if p.HasError() {
					// Recognition error - abort rule
					goto errorExit
				}
			}
			{
				p.SetState(314)
				p.Expression()
			}

		}
		{
			p.SetState(317)
			p.Match(SqlBaseParserEND)
			if p.HasError() {
				// Recognition error - abort rule
				goto errorExit
			}
		}

	case 7:
		p.EnterOuterAlt(localctx, 7)
		{
			p.SetState(319)
			p.Match(SqlBaseParserLPAREN)
			if p.HasError() {
				// Recognition error - abort rule
				goto errorExit
			}
		}
		{
			p.SetState(320)
			p.Expression()
		}
		{
			p.SetState(321)
			p.Match(SqlBaseParserRPAREN)
			if p.HasError() {
				// Recognition error - abort rule
				goto errorExit
			}
		}

	case antlr.ATNInvalidAltNumber:
		goto errorExit
	}

errorExit:
	if p.HasError() {
		v := p.GetError()
		localctx.SetException(v)
		p.GetErrorHandler().ReportError(p, v)
		p.GetErrorHandler().Recover(p, v)
		p.SetError(nil)
	}
	p.ExitRule()
	return localctx
	goto errorExit // Trick to prevent compiler error if the label is not used
}

// IFunctionCallContext is an interface to support dynamic dispatch.
type IFunctionCallContext interface {
	antlr.ParserRuleContext

	// GetParser returns the parser.
	GetParser() antlr.Parser

	// Getter signatures
	Identifier() IIdentifierContext
	LPAREN() antlr.TerminalNode
	RPAREN() antlr.TerminalNode
	DISTINCT() antlr.TerminalNode
	STAR() antlr.TerminalNode
	ExpressionList() IExpressionListContext

	// IsFunctionCallContext differentiates from other interfaces.
	IsFunctionCallContext()
}

type FunctionCallContext struct {
	antlr.BaseParserRuleContext
	parser antlr.Parser
}

func NewEmptyFunctionCallContext() *FunctionCallContext {
	var p = new(FunctionCallContext)
	antlr.InitBaseParserRuleContext(&p.BaseParserRuleContext, nil, -1)
	p.RuleIndex = SqlBaseParserRULE_functionCall
	return p
}

func InitEmptyFunctionCallContext(p *FunctionCallContext) {
	antlr.InitBaseParserRuleContext(&p.BaseParserRuleContext, nil, -1)
	p.RuleIndex = SqlBaseParserRULE_functionCall
}

func (*FunctionCallContext) IsFunctionCallContext() {}

func NewFunctionCallContext(parser antlr.Parser, parent antlr.ParserRuleContext, invokingState int) *FunctionCallContext {
	var p = new(FunctionCallContext)

	antlr.InitBaseParserRuleContext(&p.BaseParserRuleContext, parent, invokingState)

	p.parser = parser
	p.RuleIndex = SqlBaseParserRULE_functionCall

	return p
}

func (s *FunctionCallContext) GetParser() antlr.Parser { return s.parser }

func (s *FunctionCallContext) Identifier() IIdentifierContext {
	var t antlr.RuleContext
	for _, ctx := range s.GetChildren() {
		if _, ok := ctx.(IIdentifierContext); ok {
			t = ctx.(antlr.RuleContext)
			break
		}
	}

	if t == nil {
		return nil
	}

	return t.(IIdentifierContext)
}

func (s *FunctionCallContext) LPAREN() antlr.TerminalNode {
	return s.GetToken(SqlBaseParserLPAREN, 0)
}

func (s *FunctionCallContext) RPAREN() antlr.TerminalNode {
	return s.GetToken(SqlBaseParserRPAREN, 0)
}

func (s *FunctionCallContext) DISTINCT() antlr.TerminalNode {
	return s.GetToken(SqlBaseParserDISTINCT, 0)
}

func (s *FunctionCallContext) STAR() antlr.TerminalNode {
	return s.GetToken(SqlBaseParserSTAR, 0)
}

func (s *FunctionCallContext) ExpressionList() IExpressionListContext {
	var t antlr.RuleContext
	for _, ctx := range s.GetChildren() {
		if _, ok := ctx.(IExpressionListContext); ok {
			t = ctx.(antlr.RuleContext)
			break
		}
	}

	if t == nil {
		return nil
	}

	return t.(IExpressionListContext)
}

func (s *FunctionCallContext) GetRuleContext() antlr.RuleContext {
	return s
}

func (s *FunctionCallContext) ToStringTree(ruleNames []string, recog antlr.Recognizer) string {
	return antlr.TreesStringTree(s, ruleNames, recog)
}

func (s *FunctionCallContext) Accept(visitor antlr.ParseTreeVisitor) interface{} {
	switch t := visitor.(type) {
	case SqlBaseVisitor:
		return t.VisitFunctionCall(s)

	default:
		return t.VisitChildren(s)
	}
}

func (p *SqlBaseParser) FunctionCall() (localctx IFunctionCallContext) {
	localctx = NewFunctionCallContext(p, p.GetParserRuleContext(), p.GetState())
	p.EnterRule(localctx, 42, SqlBaseParserRULE_functionCall)
	var _la int

	p.EnterOuterAlt(localctx, 1)
	{
		p.SetState(325)
		p.Identifier()
	}
	{
		p.SetState(326)
		p.Match(SqlBaseParserLPAREN)
		if p.HasError() {
			// Recognition error - abort rule
			goto errorExit
		}
	}
	p.SetState(328)
	p.GetErrorHandler().Sync(p)
	if p.HasError() {
		goto errorExit
	}
	_la = p.GetTokenStream().LA(1)

	if _la == SqlBaseParserDISTINCT {
		{
			p.SetState(327)
			p.Match(SqlBaseParserDISTINCT)
			if p.HasError() {
				// Recognition error - abort rule
				goto errorExit
			}
		}

	}
	p.SetState(332)
	p.GetErrorHandler().Sync(p)
	if p.HasError() {
		goto errorExit
	}
	switch p.GetTokenStream().LA(1) {
	case SqlBaseParserSTAR:
		{
			p.SetState(330)
			p.Match(SqlBaseParserSTAR)
			if p.HasError() {
				// Recognition error - abort rule
				goto errorExit
			}
		}

	case SqlBaseParserALL, SqlBaseParserNOT, SqlBaseParserNULL, SqlBaseParserCAST, SqlBaseParserCASE, SqlBaseParserTRUE, SqlBaseParserFALSE, SqlBaseParserCURRENT_TIMESTAMP, SqlBaseParserLPAREN, SqlBaseParserPLUS, SqlBaseParserMINUS, SqlBaseParserNUMBER, SqlBaseParserSTRING, SqlBaseParserQUOTED_IDENTIFIER, SqlBaseParserIDENTIFIER:
		{
			p.SetState(331)
			p.ExpressionList()
		}

	case SqlBaseParserRPAREN:

	default:
	}
	{
		p.SetState(334)
		p.Match(SqlBaseParserRPAREN)
		if p.HasError() {
			// Recognition error - abort rule
			goto errorExit
		}
	}

errorExit:
	if p.HasError() {
		v := p.GetError()
		localctx.SetException(v)
		p.GetErrorHandler().ReportError(p, v)
		p.GetErrorHandler().Recover(p, v)
		p.SetError(nil)
	}
	p.ExitRule()
	return localctx
	goto errorExit // Trick to prevent compiler error if the label is not used
}

// ITypeNameContext is an interface to support dynamic dispatch.
type ITypeNameContext interface {
	antlr.ParserRuleContext

	// GetParser returns the parser.
	GetParser() antlr.Parser

	// Getter signatures
	Identifier() IIdentifierContext
	LPAREN() antlr.TerminalNode
	AllNUMBER() []antlr.TerminalNode
	NUMBER(i int) antlr.TerminalNode
	RPAREN() antlr.TerminalNode
	COMMA() antlr.TerminalNode

	// IsTypeNameContext differentiates from other interfaces.
	IsTypeNameContext()
}

type TypeNameContext struct {
	antlr.BaseParserRuleContext
	parser antlr.Parser
}

func NewEmptyTypeNameContext() *TypeNameContext {
	var p = new(TypeNameContext)
	antlr.InitBaseParserRuleContext(&p.BaseParserRuleContext, nil, -1)
	p.RuleIndex = SqlBaseParserRULE_typeName
	return p
}

func InitEmptyTypeNameContext(p *TypeNameContext) {
	antlr.InitBaseParserRuleContext(&p.BaseParserRuleContext, nil, -1)
	p.RuleIndex = SqlBaseParserRULE_typeName
}

func (*TypeNameContext) IsTypeNameContext() {}

func NewTypeNameContext(parser antlr.Parser, parent antlr.ParserRuleContext, invokingState int) *TypeNameContext {
	var p = new(TypeNameContext)

	antlr.InitBaseParserRuleContext(&p.BaseParserRuleContext, parent, invokingState)

	p.parser = parser
	p.RuleIndex = SqlBaseParserRULE_typeName

	return p
}

func (s *TypeNameContext) GetParser() antlr.Parser { return s.parser }

func (s *TypeNameContext) Identifier() IIdentifierContext {
	var t antlr.RuleContext
	for _, ctx := range s.GetChildren() {
		if _, ok := ctx.(IIdentifierContext); ok {
			t = ctx.(antlr.RuleContext)
			break
		}
	}

	if t == nil {
		return nil
	}

	return t.(IIdentifierContext)
}

func (s *TypeNameContext) LPAREN() antlr.TerminalNode {
	return s.GetToken(SqlBaseParserLPAREN, 0)
}

func (s *TypeNameContext) AllNUMBER() []antlr.TerminalNode {
	return s.GetTokens(SqlBaseParserNUMBER)
}

func (s *TypeNameContext) NUMBER(i int) antlr.TerminalNode {
	return s.GetToken(SqlBaseParserNUMBER, i)
}

func (s *TypeNameContext) RPAREN() antlr.TerminalNode {
	return s.GetToken(SqlBaseParserRPAREN, 0)
}

func (s *TypeNameContext) COMMA() antlr.TerminalNode {
	return s.GetToken(SqlBaseParserCOMMA, 0)
}

func (s *TypeNameContext) GetRuleContext() antlr.RuleContext {
	return s
}

func (s *TypeNameContext) ToStringTree(ruleNames []string, recog antlr.Recognizer) string {
	return antlr.TreesStringTree(s, ruleNames, recog)
}

func (s *TypeNameContext) Accept(visitor antlr.ParseTreeVisitor) interface{} {
	switch t := visitor.(type) {
	case SqlBaseVisitor:
		return t.VisitTypeName(s)

	default:
		return t.VisitChildren(s)
	}
}

func (p *SqlBaseParser) TypeName() (localctx ITypeNameContext) {
	localctx = NewTypeNameContext(p, p.GetParserRuleContext(), p.GetState())
	p.EnterRule(localctx, 44, SqlBaseParserRULE_typeName)
	var _la int

	p.EnterOuterAlt(localctx, 1)
	{
		p.SetState(336)
		p.Identifier()
	}
	p.SetState(344)
	p.GetErrorHandler().Sync(p)
	if p.HasError() {
		goto errorExit
	}
	_la = p.GetTokenStream().LA(1)

	if _la == SqlBaseParserLPAREN {
		{
			p.SetState(337)
			p.Match(SqlBaseParserLPAREN)
			if p.HasError() {
				// Recognition error - abort rule
				goto errorExit
			}
		}
		{
			p.SetState(338)
			p.Match(SqlBaseParserNUMBER)
			if p.HasError() {
				// Recognition error - abort rule
				goto errorExit
			}
		}
		p.SetState(341)
		p.GetErrorHandler().Sync(p)
		if p.HasError() {
			goto errorExit
		}
		_la = p.GetTokenStream().LA(1)

		if _la == SqlBaseParserCOMMA {
			{
				p.SetState(339)
				p.Match(SqlBaseParserCOMMA)
				if p.HasError() {
					// Recognition error - abort rule
					goto errorExit
				}
			}
			{
				p.SetState(340)
				p.Match(SqlBaseParserNUMBER)
				if p.HasError() {
					// Recognition error - abort rule
					goto errorExit
				}
			}

		}
		{
			p.SetState(343)
			p.Match(SqlBaseParserRPAREN)
			if p.HasError() {
				// Recognition error - abort rule
				goto errorExit
			}
		}

	}

errorExit:
	if p.HasError() {
		v := p.GetError()
		localctx.SetException(v)
		p.GetErrorHandler().ReportError(p, v)
		p.GetErrorHandler().Recover(p, v)
		p.SetError(nil)
	}
	p.ExitRule()
	return localctx
	goto errorExit // Trick to prevent compiler error if the label is not used
}

// ILiteralContext is an interface to support dynamic dispatch.
type ILiteralContext interface {
	antlr.ParserRuleContext

	// GetParser returns the parser.
	GetParser() antlr.Parser

	// Getter signatures
	NUMBER() antlr.TerminalNode
	STRING() antlr.TerminalNode
	NULL() antlr.TerminalNode
	TRUE() antlr.TerminalNode
	FALSE() antlr.TerminalNode

	// IsLiteralContext differentiates from other interfaces.
	IsLiteralContext()
}

type LiteralContext struct {
	antlr.BaseParserRuleContext
	parser antlr.Parser
}

func NewEmptyLiteralContext() *LiteralContext {
	var p = new(LiteralContext)
	antlr.InitBaseParserRuleContext(&p.BaseParserRuleContext, nil, -1)
	p.RuleIndex = SqlBaseParserRULE_literal
	return p
}

func InitEmptyLiteralContext(p *LiteralContext) {
	antlr.InitBaseParserRuleContext(&p.BaseParserRuleContext, nil, -1)
	p.RuleIndex = SqlBaseParserRULE_literal
}

func (*LiteralContext) IsLiteralContext() {}

func NewLiteralContext(parser antlr.Parser, parent antlr.ParserRuleContext, invokingState int) *LiteralContext {
	var p = new(LiteralContext)

	antlr.InitBaseParserRuleContext(&p.BaseParserRuleContext, parent, invokingState)

	p.parser = parser
	p.RuleIndex = SqlBaseParserRULE_literal

	return p
}

func (s *LiteralContext) GetParser() antlr.Parser { return s.parser }

func (s *LiteralContext) NUMBER() antlr.TerminalNode {
	return s.GetToken(SqlBaseParserNUMBER, 0)
}

func (s *LiteralContext) STRING() antlr.TerminalNode {
	return s.GetToken(SqlBaseParserSTRING, 0)
}

func (s *LiteralContext) NULL() antlr.TerminalNode {
	return s.GetToken(SqlBaseParserNULL, 0)
}

func (s *LiteralContext) TRUE() antlr.TerminalNode {
	return s.GetToken(SqlBaseParserTRUE, 0)
}

func (s *LiteralContext) FALSE() antlr.TerminalNode {
	return s.GetToken(SqlBaseParserFALSE, 0)
}

func (s *LiteralContext) GetRuleContext() antlr.RuleContext {
	return s
}

func (s *LiteralContext) ToStringTree(ruleNames []string, recog antlr.Recognizer) string {
	return antlr.TreesStringTree(s, ruleNames, recog)
}

func (s *LiteralContext) Accept(visitor antlr.ParseTreeVisitor) interface{} {
	switch t := visitor.(type) {
	case SqlBaseVisitor:
		return t.VisitLiteral(s)

	default:
		return t.VisitChildren(s)
	}
}

func (p *SqlBaseParser) Literal() (localctx ILiteralContext) {
	localctx = NewLiteralContext(p, p.GetParserRuleContext(), p.GetState())
	p.EnterRule(localctx, 46, SqlBaseParserRULE_literal)
	var _la int

	p.EnterOuterAlt(localctx, 1)
	{
		p.SetState(346)
		_la = p.GetTokenStream().LA(1)

		if !((int64(_la) & ^0x3f) == 0 && ((int64(1)<<_la)&1729383081678209024) != 0) {
			p.GetErrorHandler().RecoverInline(p)
		} else {
			p.GetErrorHandler().ReportMatch(p)
			p.Consume()
		}
	}

errorExit:
	if p.HasError() {
		v := p.GetError()
		localctx.SetException(v)
		p.GetErrorHandler().ReportError(p, v)
		p.GetErrorHandler().Recover(p, v)
		p.SetError(nil)
	}
	p.ExitRule()
	return localctx
	goto errorExit // Trick to prevent compiler error if the label is not used
}

// IIdentifierContext is an interface to support dynamic dispatch.
type IIdentifierContext interface {
	antlr.ParserRuleContext

	// GetParser returns the parser.
	GetParser() antlr.Parser

	// Getter signatures
	IDENTIFIER() antlr.TerminalNode
	QUOTED_IDENTIFIER() antlr.TerminalNode
	ALL() antlr.TerminalNode

	// IsIdentifierContext differentiates from other interfaces.
	IsIdentifierContext()
}

type IdentifierContext struct {
	antlr.BaseParserRuleContext
	parser antlr.Parser
}

func NewEmptyIdentifierContext() *IdentifierContext {
	var p = new(IdentifierContext)
	antlr.InitBaseParserRuleContext(&p.BaseParserRuleContext, nil, -1)
	p.RuleIndex = SqlBaseParserRULE_identifier
	return p
}

func InitEmptyIdentifierContext(p *IdentifierContext) {
	antlr.InitBaseParserRuleContext(&p.BaseParserRuleContext, nil, -1)
	p.RuleIndex = SqlBaseParserRULE_identifier
}

func (*IdentifierContext) IsIdentifierContext() {}

func NewIdentifierContext(parser antlr.Parser, parent antlr.ParserRuleContext, invokingState int) *IdentifierContext {
	var p = new(IdentifierContext)

	antlr.InitBaseParserRuleContext(&p.BaseParserRuleContext, parent, invokingState)

	p.parser = parser
	p.RuleIndex = SqlBaseParserRULE_identifier

	return p
}

func (s *IdentifierContext) GetParser() antlr.Parser { return s.parser }

func (s *IdentifierContext) IDENTIFIER() antlr.TerminalNode {
	return s.GetToken(SqlBaseParserIDENTIFIER, 0)
}

func (s *IdentifierContext) QUOTED_IDENTIFIER() antlr.TerminalNode {
	return s.GetToken(SqlBaseParserQUOTED_IDENTIFIER, 0)
}

func (s *IdentifierContext) ALL() antlr.TerminalNode {
	return s.GetToken(SqlBaseParserALL, 0)
}

func (s *IdentifierContext) GetRuleContext() antlr.RuleContext {
	return s
}

func (s *IdentifierContext) ToStringTree(ruleNames []string, recog antlr.Recognizer) string {
	return antlr.TreesStringTree(s, ruleNames, recog)
}

func (s *IdentifierContext) Accept(visitor antlr.ParseTreeVisitor) interface{} {
	switch t := visitor.(type) {
	case SqlBaseVisitor:
		return t.VisitIdentifier(s)

	default:
		return t.VisitChildren(s)
	}
}

func (p *SqlBaseParser) Identifier() (localctx IIdentifierContext) {
	localctx = NewIdentifierContext(p, p.GetParserRuleContext(), p.GetState())
	p.EnterRule(localctx, 48, SqlBaseParserRULE_identifier)
	var _la int

	p.EnterOuterAlt(localctx, 1)
	{
		p.SetState(348)
		_la = p.GetTokenStream().LA(1)

		if !((int64(_la) & ^0x3f) == 0 && ((int64(1)<<_la)&6917529027641081864) != 0) {
			p.GetErrorHandler().RecoverInline(p)
		} else {
			p.GetErrorHandler().ReportMatch(p)
			p.Consume()
		}
	}

errorExit:
	if p.HasError() {
		v := p.GetError()
		localctx.SetException(v)
		p.GetErrorHandler().ReportError(p, v)
		p.GetErrorHandler().Recover(p, v)
		p.SetError(nil)
	}
	p.ExitRule()
	return localctx
	goto errorExit // Trick to prevent compiler error if the label is not used
}
