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
		"", "'SELECT'", "'DISTINCT'", "'FROM'", "'WHERE'", "'GROUP'", "'BY'",
		"'HAVING'", "'ORDER'", "'LIMIT'", "'OFFSET'", "'AS'", "'INNER'", "'LEFT'",
		"'RIGHT'", "'OUTER'", "'JOIN'", "'CROSS'", "'ON'", "'ASC'", "'DESC'",
		"'OR'", "'AND'", "'NOT'", "'IS'", "'NULL'", "'BETWEEN'", "'IN'", "'LIKE'",
		"'ESCAPE'", "'CAST'", "'CASE'", "'WHEN'", "'THEN'", "'ELSE'", "'END'",
		"'TRUE'", "'FALSE'", "'CURRENT_TIMESTAMP'", "", "", "'('", "')'", "','",
		"'.'", "';'", "'='", "", "'<='", "'>='", "'<'", "'>'", "'+'", "'-'",
		"'*'", "'/'", "'%'",
	}
	staticData.SymbolicNames = []string{
		"", "SELECT", "DISTINCT", "FROM", "WHERE", "GROUP", "BY", "HAVING",
		"ORDER", "LIMIT", "OFFSET", "AS", "INNER", "LEFT", "RIGHT", "OUTER",
		"JOIN", "CROSS", "ON", "ASC", "DESC", "OR", "AND", "NOT", "IS", "NULL",
		"BETWEEN", "IN", "LIKE", "ESCAPE", "CAST", "CASE", "WHEN", "THEN", "ELSE",
		"END", "TRUE", "FALSE", "CURRENT_TIMESTAMP", "UNSUPPORTED", "RESOURCE",
		"LPAREN", "RPAREN", "COMMA", "DOT", "SEMICOLON", "EQ", "NEQ", "LTE",
		"GTE", "LT", "GT", "PLUS", "MINUS", "STAR", "SLASH", "PERCENT", "NUMBER",
		"STRING", "QUOTED_IDENTIFIER", "IDENTIFIER", "FORBIDDEN_COMMENT", "BLOCK_COMMENT",
		"LINE_COMMENT", "WS",
	}
	staticData.RuleNames = []string{
		"statement", "selectQuery", "selectItem", "relation", "relationPrimary",
		"joinPart", "sortItem", "limitClause", "expressionList", "expression",
		"orExpression", "andExpression", "notExpression", "predicate", "additiveExpression",
		"multiplicativeExpression", "unaryExpression", "primaryExpression",
		"functionCall", "typeName", "literal", "identifier",
	}
	staticData.PredictionContextCache = antlr.NewPredictionContextCache()
	staticData.serializedATN = []int32{
		4, 1, 64, 321, 2, 0, 7, 0, 2, 1, 7, 1, 2, 2, 7, 2, 2, 3, 7, 3, 2, 4, 7,
		4, 2, 5, 7, 5, 2, 6, 7, 6, 2, 7, 7, 7, 2, 8, 7, 8, 2, 9, 7, 9, 2, 10, 7,
		10, 2, 11, 7, 11, 2, 12, 7, 12, 2, 13, 7, 13, 2, 14, 7, 14, 2, 15, 7, 15,
		2, 16, 7, 16, 2, 17, 7, 17, 2, 18, 7, 18, 2, 19, 7, 19, 2, 20, 7, 20, 2,
		21, 7, 21, 1, 0, 1, 0, 3, 0, 47, 8, 0, 1, 0, 1, 0, 1, 1, 1, 1, 3, 1, 53,
		8, 1, 1, 1, 1, 1, 1, 1, 5, 1, 58, 8, 1, 10, 1, 12, 1, 61, 9, 1, 1, 1, 1,
		1, 1, 1, 1, 1, 3, 1, 67, 8, 1, 1, 1, 1, 1, 1, 1, 3, 1, 72, 8, 1, 1, 1,
		1, 1, 3, 1, 76, 8, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 5, 1, 83, 8, 1, 10,
		1, 12, 1, 86, 9, 1, 3, 1, 88, 8, 1, 1, 1, 3, 1, 91, 8, 1, 1, 2, 1, 2, 1,
		2, 3, 2, 96, 8, 2, 1, 2, 1, 2, 1, 2, 3, 2, 101, 8, 2, 1, 2, 3, 2, 104,
		8, 2, 3, 2, 106, 8, 2, 1, 3, 1, 3, 5, 3, 110, 8, 3, 10, 3, 12, 3, 113,
		9, 3, 1, 4, 1, 4, 3, 4, 117, 8, 4, 1, 4, 3, 4, 120, 8, 4, 1, 4, 1, 4, 1,
		4, 1, 4, 3, 4, 126, 8, 4, 1, 4, 1, 4, 3, 4, 130, 8, 4, 1, 5, 1, 5, 1, 5,
		3, 5, 135, 8, 5, 1, 5, 1, 5, 3, 5, 139, 8, 5, 3, 5, 141, 8, 5, 1, 5, 1,
		5, 1, 5, 1, 5, 1, 5, 1, 5, 1, 5, 1, 5, 3, 5, 151, 8, 5, 1, 6, 1, 6, 3,
		6, 155, 8, 6, 1, 7, 1, 7, 1, 7, 1, 7, 1, 7, 1, 7, 3, 7, 163, 8, 7, 1, 8,
		1, 8, 1, 8, 5, 8, 168, 8, 8, 10, 8, 12, 8, 171, 9, 8, 1, 9, 1, 9, 1, 10,
		1, 10, 1, 10, 5, 10, 178, 8, 10, 10, 10, 12, 10, 181, 9, 10, 1, 11, 1,
		11, 1, 11, 5, 11, 186, 8, 11, 10, 11, 12, 11, 189, 9, 11, 1, 12, 1, 12,
		1, 12, 3, 12, 194, 8, 12, 1, 13, 1, 13, 1, 13, 1, 13, 1, 13, 3, 13, 201,
		8, 13, 1, 13, 1, 13, 3, 13, 205, 8, 13, 1, 13, 1, 13, 1, 13, 1, 13, 1,
		13, 1, 13, 3, 13, 213, 8, 13, 1, 13, 1, 13, 1, 13, 1, 13, 1, 13, 1, 13,
		3, 13, 221, 8, 13, 1, 13, 1, 13, 1, 13, 1, 13, 3, 13, 227, 8, 13, 3, 13,
		229, 8, 13, 1, 14, 1, 14, 1, 14, 5, 14, 234, 8, 14, 10, 14, 12, 14, 237,
		9, 14, 1, 15, 1, 15, 1, 15, 5, 15, 242, 8, 15, 10, 15, 12, 15, 245, 9,
		15, 1, 16, 1, 16, 1, 16, 3, 16, 250, 8, 16, 1, 17, 1, 17, 1, 17, 1, 17,
		3, 17, 256, 8, 17, 1, 17, 1, 17, 1, 17, 1, 17, 3, 17, 262, 8, 17, 1, 17,
		1, 17, 1, 17, 1, 17, 1, 17, 1, 17, 1, 17, 1, 17, 1, 17, 3, 17, 273, 8,
		17, 1, 17, 1, 17, 1, 17, 1, 17, 1, 17, 4, 17, 280, 8, 17, 11, 17, 12, 17,
		281, 1, 17, 1, 17, 3, 17, 286, 8, 17, 1, 17, 1, 17, 1, 17, 1, 17, 1, 17,
		1, 17, 3, 17, 294, 8, 17, 1, 18, 1, 18, 1, 18, 3, 18, 299, 8, 18, 1, 18,
		1, 18, 3, 18, 303, 8, 18, 1, 18, 1, 18, 1, 19, 1, 19, 1, 19, 1, 19, 1,
		19, 3, 19, 312, 8, 19, 1, 19, 3, 19, 315, 8, 19, 1, 20, 1, 20, 1, 21, 1,
		21, 1, 21, 0, 0, 22, 0, 2, 4, 6, 8, 10, 12, 14, 16, 18, 20, 22, 24, 26,
		28, 30, 32, 34, 36, 38, 40, 42, 0, 6, 1, 0, 19, 20, 1, 0, 46, 51, 1, 0,
		52, 53, 1, 0, 54, 56, 3, 0, 25, 25, 36, 37, 57, 58, 1, 0, 59, 60, 358,
		0, 44, 1, 0, 0, 0, 2, 50, 1, 0, 0, 0, 4, 105, 1, 0, 0, 0, 6, 107, 1, 0,
		0, 0, 8, 129, 1, 0, 0, 0, 10, 150, 1, 0, 0, 0, 12, 152, 1, 0, 0, 0, 14,
		156, 1, 0, 0, 0, 16, 164, 1, 0, 0, 0, 18, 172, 1, 0, 0, 0, 20, 174, 1,
		0, 0, 0, 22, 182, 1, 0, 0, 0, 24, 193, 1, 0, 0, 0, 26, 195, 1, 0, 0, 0,
		28, 230, 1, 0, 0, 0, 30, 238, 1, 0, 0, 0, 32, 249, 1, 0, 0, 0, 34, 293,
		1, 0, 0, 0, 36, 295, 1, 0, 0, 0, 38, 306, 1, 0, 0, 0, 40, 316, 1, 0, 0,
		0, 42, 318, 1, 0, 0, 0, 44, 46, 3, 2, 1, 0, 45, 47, 5, 45, 0, 0, 46, 45,
		1, 0, 0, 0, 46, 47, 1, 0, 0, 0, 47, 48, 1, 0, 0, 0, 48, 49, 5, 0, 0, 1,
		49, 1, 1, 0, 0, 0, 50, 52, 5, 1, 0, 0, 51, 53, 5, 2, 0, 0, 52, 51, 1, 0,
		0, 0, 52, 53, 1, 0, 0, 0, 53, 54, 1, 0, 0, 0, 54, 59, 3, 4, 2, 0, 55, 56,
		5, 43, 0, 0, 56, 58, 3, 4, 2, 0, 57, 55, 1, 0, 0, 0, 58, 61, 1, 0, 0, 0,
		59, 57, 1, 0, 0, 0, 59, 60, 1, 0, 0, 0, 60, 62, 1, 0, 0, 0, 61, 59, 1,
		0, 0, 0, 62, 63, 5, 3, 0, 0, 63, 66, 3, 6, 3, 0, 64, 65, 5, 4, 0, 0, 65,
		67, 3, 18, 9, 0, 66, 64, 1, 0, 0, 0, 66, 67, 1, 0, 0, 0, 67, 71, 1, 0,
		0, 0, 68, 69, 5, 5, 0, 0, 69, 70, 5, 6, 0, 0, 70, 72, 3, 16, 8, 0, 71,
		68, 1, 0, 0, 0, 71, 72, 1, 0, 0, 0, 72, 75, 1, 0, 0, 0, 73, 74, 5, 7, 0,
		0, 74, 76, 3, 18, 9, 0, 75, 73, 1, 0, 0, 0, 75, 76, 1, 0, 0, 0, 76, 87,
		1, 0, 0, 0, 77, 78, 5, 8, 0, 0, 78, 79, 5, 6, 0, 0, 79, 84, 3, 12, 6, 0,
		80, 81, 5, 43, 0, 0, 81, 83, 3, 12, 6, 0, 82, 80, 1, 0, 0, 0, 83, 86, 1,
		0, 0, 0, 84, 82, 1, 0, 0, 0, 84, 85, 1, 0, 0, 0, 85, 88, 1, 0, 0, 0, 86,
		84, 1, 0, 0, 0, 87, 77, 1, 0, 0, 0, 87, 88, 1, 0, 0, 0, 88, 90, 1, 0, 0,
		0, 89, 91, 3, 14, 7, 0, 90, 89, 1, 0, 0, 0, 90, 91, 1, 0, 0, 0, 91, 3,
		1, 0, 0, 0, 92, 93, 3, 42, 21, 0, 93, 94, 5, 44, 0, 0, 94, 96, 1, 0, 0,
		0, 95, 92, 1, 0, 0, 0, 95, 96, 1, 0, 0, 0, 96, 97, 1, 0, 0, 0, 97, 106,
		5, 54, 0, 0, 98, 103, 3, 18, 9, 0, 99, 101, 5, 11, 0, 0, 100, 99, 1, 0,
		0, 0, 100, 101, 1, 0, 0, 0, 101, 102, 1, 0, 0, 0, 102, 104, 3, 42, 21,
		0, 103, 100, 1, 0, 0, 0, 103, 104, 1, 0, 0, 0, 104, 106, 1, 0, 0, 0, 105,
		95, 1, 0, 0, 0, 105, 98, 1, 0, 0, 0, 106, 5, 1, 0, 0, 0, 107, 111, 3, 8,
		4, 0, 108, 110, 3, 10, 5, 0, 109, 108, 1, 0, 0, 0, 110, 113, 1, 0, 0, 0,
		111, 109, 1, 0, 0, 0, 111, 112, 1, 0, 0, 0, 112, 7, 1, 0, 0, 0, 113, 111,
		1, 0, 0, 0, 114, 119, 5, 40, 0, 0, 115, 117, 5, 11, 0, 0, 116, 115, 1,
		0, 0, 0, 116, 117, 1, 0, 0, 0, 117, 118, 1, 0, 0, 0, 118, 120, 3, 42, 21,
		0, 119, 116, 1, 0, 0, 0, 119, 120, 1, 0, 0, 0, 120, 130, 1, 0, 0, 0, 121,
		122, 5, 41, 0, 0, 122, 123, 3, 2, 1, 0, 123, 125, 5, 42, 0, 0, 124, 126,
		5, 11, 0, 0, 125, 124, 1, 0, 0, 0, 125, 126, 1, 0, 0, 0, 126, 127, 1, 0,
		0, 0, 127, 128, 3, 42, 21, 0, 128, 130, 1, 0, 0, 0, 129, 114, 1, 0, 0,
		0, 129, 121, 1, 0, 0, 0, 130, 9, 1, 0, 0, 0, 131, 141, 5, 12, 0, 0, 132,
		134, 5, 13, 0, 0, 133, 135, 5, 15, 0, 0, 134, 133, 1, 0, 0, 0, 134, 135,
		1, 0, 0, 0, 135, 141, 1, 0, 0, 0, 136, 138, 5, 14, 0, 0, 137, 139, 5, 15,
		0, 0, 138, 137, 1, 0, 0, 0, 138, 139, 1, 0, 0, 0, 139, 141, 1, 0, 0, 0,
		140, 131, 1, 0, 0, 0, 140, 132, 1, 0, 0, 0, 140, 136, 1, 0, 0, 0, 140,
		141, 1, 0, 0, 0, 141, 142, 1, 0, 0, 0, 142, 143, 5, 16, 0, 0, 143, 144,
		3, 8, 4, 0, 144, 145, 5, 18, 0, 0, 145, 146, 3, 18, 9, 0, 146, 151, 1,
		0, 0, 0, 147, 148, 5, 17, 0, 0, 148, 149, 5, 16, 0, 0, 149, 151, 3, 8,
		4, 0, 150, 140, 1, 0, 0, 0, 150, 147, 1, 0, 0, 0, 151, 11, 1, 0, 0, 0,
		152, 154, 3, 18, 9, 0, 153, 155, 7, 0, 0, 0, 154, 153, 1, 0, 0, 0, 154,
		155, 1, 0, 0, 0, 155, 13, 1, 0, 0, 0, 156, 157, 5, 9, 0, 0, 157, 162, 5,
		57, 0, 0, 158, 159, 5, 10, 0, 0, 159, 163, 5, 57, 0, 0, 160, 161, 5, 43,
		0, 0, 161, 163, 5, 57, 0, 0, 162, 158, 1, 0, 0, 0, 162, 160, 1, 0, 0, 0,
		162, 163, 1, 0, 0, 0, 163, 15, 1, 0, 0, 0, 164, 169, 3, 18, 9, 0, 165,
		166, 5, 43, 0, 0, 166, 168, 3, 18, 9, 0, 167, 165, 1, 0, 0, 0, 168, 171,
		1, 0, 0, 0, 169, 167, 1, 0, 0, 0, 169, 170, 1, 0, 0, 0, 170, 17, 1, 0,
		0, 0, 171, 169, 1, 0, 0, 0, 172, 173, 3, 20, 10, 0, 173, 19, 1, 0, 0, 0,
		174, 179, 3, 22, 11, 0, 175, 176, 5, 21, 0, 0, 176, 178, 3, 22, 11, 0,
		177, 175, 1, 0, 0, 0, 178, 181, 1, 0, 0, 0, 179, 177, 1, 0, 0, 0, 179,
		180, 1, 0, 0, 0, 180, 21, 1, 0, 0, 0, 181, 179, 1, 0, 0, 0, 182, 187, 3,
		24, 12, 0, 183, 184, 5, 22, 0, 0, 184, 186, 3, 24, 12, 0, 185, 183, 1,
		0, 0, 0, 186, 189, 1, 0, 0, 0, 187, 185, 1, 0, 0, 0, 187, 188, 1, 0, 0,
		0, 188, 23, 1, 0, 0, 0, 189, 187, 1, 0, 0, 0, 190, 191, 5, 23, 0, 0, 191,
		194, 3, 24, 12, 0, 192, 194, 3, 26, 13, 0, 193, 190, 1, 0, 0, 0, 193, 192,
		1, 0, 0, 0, 194, 25, 1, 0, 0, 0, 195, 228, 3, 28, 14, 0, 196, 197, 7, 1,
		0, 0, 197, 229, 3, 28, 14, 0, 198, 200, 5, 24, 0, 0, 199, 201, 5, 23, 0,
		0, 200, 199, 1, 0, 0, 0, 200, 201, 1, 0, 0, 0, 201, 202, 1, 0, 0, 0, 202,
		229, 5, 25, 0, 0, 203, 205, 5, 23, 0, 0, 204, 203, 1, 0, 0, 0, 204, 205,
		1, 0, 0, 0, 205, 206, 1, 0, 0, 0, 206, 207, 5, 26, 0, 0, 207, 208, 3, 28,
		14, 0, 208, 209, 5, 22, 0, 0, 209, 210, 3, 28, 14, 0, 210, 229, 1, 0, 0,
		0, 211, 213, 5, 23, 0, 0, 212, 211, 1, 0, 0, 0, 212, 213, 1, 0, 0, 0, 213,
		214, 1, 0, 0, 0, 214, 215, 5, 27, 0, 0, 215, 216, 5, 41, 0, 0, 216, 217,
		3, 16, 8, 0, 217, 218, 5, 42, 0, 0, 218, 229, 1, 0, 0, 0, 219, 221, 5,
		23, 0, 0, 220, 219, 1, 0, 0, 0, 220, 221, 1, 0, 0, 0, 221, 222, 1, 0, 0,
		0, 222, 223, 5, 28, 0, 0, 223, 226, 3, 28, 14, 0, 224, 225, 5, 29, 0, 0,
		225, 227, 5, 58, 0, 0, 226, 224, 1, 0, 0, 0, 226, 227, 1, 0, 0, 0, 227,
		229, 1, 0, 0, 0, 228, 196, 1, 0, 0, 0, 228, 198, 1, 0, 0, 0, 228, 204,
		1, 0, 0, 0, 228, 212, 1, 0, 0, 0, 228, 220, 1, 0, 0, 0, 228, 229, 1, 0,
		0, 0, 229, 27, 1, 0, 0, 0, 230, 235, 3, 30, 15, 0, 231, 232, 7, 2, 0, 0,
		232, 234, 3, 30, 15, 0, 233, 231, 1, 0, 0, 0, 234, 237, 1, 0, 0, 0, 235,
		233, 1, 0, 0, 0, 235, 236, 1, 0, 0, 0, 236, 29, 1, 0, 0, 0, 237, 235, 1,
		0, 0, 0, 238, 243, 3, 32, 16, 0, 239, 240, 7, 3, 0, 0, 240, 242, 3, 32,
		16, 0, 241, 239, 1, 0, 0, 0, 242, 245, 1, 0, 0, 0, 243, 241, 1, 0, 0, 0,
		243, 244, 1, 0, 0, 0, 244, 31, 1, 0, 0, 0, 245, 243, 1, 0, 0, 0, 246, 247,
		7, 2, 0, 0, 247, 250, 3, 32, 16, 0, 248, 250, 3, 34, 17, 0, 249, 246, 1,
		0, 0, 0, 249, 248, 1, 0, 0, 0, 250, 33, 1, 0, 0, 0, 251, 294, 3, 40, 20,
		0, 252, 255, 3, 42, 21, 0, 253, 254, 5, 44, 0, 0, 254, 256, 3, 42, 21,
		0, 255, 253, 1, 0, 0, 0, 255, 256, 1, 0, 0, 0, 256, 294, 1, 0, 0, 0, 257,
		294, 3, 36, 18, 0, 258, 261, 5, 38, 0, 0, 259, 260, 5, 41, 0, 0, 260, 262,
		5, 42, 0, 0, 261, 259, 1, 0, 0, 0, 261, 262, 1, 0, 0, 0, 262, 294, 1, 0,
		0, 0, 263, 264, 5, 30, 0, 0, 264, 265, 5, 41, 0, 0, 265, 266, 3, 18, 9,
		0, 266, 267, 5, 11, 0, 0, 267, 268, 3, 38, 19, 0, 268, 269, 5, 42, 0, 0,
		269, 294, 1, 0, 0, 0, 270, 272, 5, 31, 0, 0, 271, 273, 3, 18, 9, 0, 272,
		271, 1, 0, 0, 0, 272, 273, 1, 0, 0, 0, 273, 279, 1, 0, 0, 0, 274, 275,
		5, 32, 0, 0, 275, 276, 3, 18, 9, 0, 276, 277, 5, 33, 0, 0, 277, 278, 3,
		18, 9, 0, 278, 280, 1, 0, 0, 0, 279, 274, 1, 0, 0, 0, 280, 281, 1, 0, 0,
		0, 281, 279, 1, 0, 0, 0, 281, 282, 1, 0, 0, 0, 282, 285, 1, 0, 0, 0, 283,
		284, 5, 34, 0, 0, 284, 286, 3, 18, 9, 0, 285, 283, 1, 0, 0, 0, 285, 286,
		1, 0, 0, 0, 286, 287, 1, 0, 0, 0, 287, 288, 5, 35, 0, 0, 288, 294, 1, 0,
		0, 0, 289, 290, 5, 41, 0, 0, 290, 291, 3, 18, 9, 0, 291, 292, 5, 42, 0,
		0, 292, 294, 1, 0, 0, 0, 293, 251, 1, 0, 0, 0, 293, 252, 1, 0, 0, 0, 293,
		257, 1, 0, 0, 0, 293, 258, 1, 0, 0, 0, 293, 263, 1, 0, 0, 0, 293, 270,
		1, 0, 0, 0, 293, 289, 1, 0, 0, 0, 294, 35, 1, 0, 0, 0, 295, 296, 3, 42,
		21, 0, 296, 298, 5, 41, 0, 0, 297, 299, 5, 2, 0, 0, 298, 297, 1, 0, 0,
		0, 298, 299, 1, 0, 0, 0, 299, 302, 1, 0, 0, 0, 300, 303, 5, 54, 0, 0, 301,
		303, 3, 16, 8, 0, 302, 300, 1, 0, 0, 0, 302, 301, 1, 0, 0, 0, 302, 303,
		1, 0, 0, 0, 303, 304, 1, 0, 0, 0, 304, 305, 5, 42, 0, 0, 305, 37, 1, 0,
		0, 0, 306, 314, 3, 42, 21, 0, 307, 308, 5, 41, 0, 0, 308, 311, 5, 57, 0,
		0, 309, 310, 5, 43, 0, 0, 310, 312, 5, 57, 0, 0, 311, 309, 1, 0, 0, 0,
		311, 312, 1, 0, 0, 0, 312, 313, 1, 0, 0, 0, 313, 315, 5, 42, 0, 0, 314,
		307, 1, 0, 0, 0, 314, 315, 1, 0, 0, 0, 315, 39, 1, 0, 0, 0, 316, 317, 7,
		4, 0, 0, 317, 41, 1, 0, 0, 0, 318, 319, 7, 5, 0, 0, 319, 43, 1, 0, 0, 0,
		47, 46, 52, 59, 66, 71, 75, 84, 87, 90, 95, 100, 103, 105, 111, 116, 119,
		125, 129, 134, 138, 140, 150, 154, 162, 169, 179, 187, 193, 200, 204, 212,
		220, 226, 228, 235, 243, 249, 255, 261, 272, 281, 285, 293, 298, 302, 311,
		314,
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
	SqlBaseParserDISTINCT          = 2
	SqlBaseParserFROM              = 3
	SqlBaseParserWHERE             = 4
	SqlBaseParserGROUP             = 5
	SqlBaseParserBY                = 6
	SqlBaseParserHAVING            = 7
	SqlBaseParserORDER             = 8
	SqlBaseParserLIMIT             = 9
	SqlBaseParserOFFSET            = 10
	SqlBaseParserAS                = 11
	SqlBaseParserINNER             = 12
	SqlBaseParserLEFT              = 13
	SqlBaseParserRIGHT             = 14
	SqlBaseParserOUTER             = 15
	SqlBaseParserJOIN              = 16
	SqlBaseParserCROSS             = 17
	SqlBaseParserON                = 18
	SqlBaseParserASC               = 19
	SqlBaseParserDESC              = 20
	SqlBaseParserOR                = 21
	SqlBaseParserAND               = 22
	SqlBaseParserNOT               = 23
	SqlBaseParserIS                = 24
	SqlBaseParserNULL              = 25
	SqlBaseParserBETWEEN           = 26
	SqlBaseParserIN                = 27
	SqlBaseParserLIKE              = 28
	SqlBaseParserESCAPE            = 29
	SqlBaseParserCAST              = 30
	SqlBaseParserCASE              = 31
	SqlBaseParserWHEN              = 32
	SqlBaseParserTHEN              = 33
	SqlBaseParserELSE              = 34
	SqlBaseParserEND               = 35
	SqlBaseParserTRUE              = 36
	SqlBaseParserFALSE             = 37
	SqlBaseParserCURRENT_TIMESTAMP = 38
	SqlBaseParserUNSUPPORTED       = 39
	SqlBaseParserRESOURCE          = 40
	SqlBaseParserLPAREN            = 41
	SqlBaseParserRPAREN            = 42
	SqlBaseParserCOMMA             = 43
	SqlBaseParserDOT               = 44
	SqlBaseParserSEMICOLON         = 45
	SqlBaseParserEQ                = 46
	SqlBaseParserNEQ               = 47
	SqlBaseParserLTE               = 48
	SqlBaseParserGTE               = 49
	SqlBaseParserLT                = 50
	SqlBaseParserGT                = 51
	SqlBaseParserPLUS              = 52
	SqlBaseParserMINUS             = 53
	SqlBaseParserSTAR              = 54
	SqlBaseParserSLASH             = 55
	SqlBaseParserPERCENT           = 56
	SqlBaseParserNUMBER            = 57
	SqlBaseParserSTRING            = 58
	SqlBaseParserQUOTED_IDENTIFIER = 59
	SqlBaseParserIDENTIFIER        = 60
	SqlBaseParserFORBIDDEN_COMMENT = 61
	SqlBaseParserBLOCK_COMMENT     = 62
	SqlBaseParserLINE_COMMENT      = 63
	SqlBaseParserWS                = 64
)

// SqlBaseParser rules.
const (
	SqlBaseParserRULE_statement                = 0
	SqlBaseParserRULE_selectQuery              = 1
	SqlBaseParserRULE_selectItem               = 2
	SqlBaseParserRULE_relation                 = 3
	SqlBaseParserRULE_relationPrimary          = 4
	SqlBaseParserRULE_joinPart                 = 5
	SqlBaseParserRULE_sortItem                 = 6
	SqlBaseParserRULE_limitClause              = 7
	SqlBaseParserRULE_expressionList           = 8
	SqlBaseParserRULE_expression               = 9
	SqlBaseParserRULE_orExpression             = 10
	SqlBaseParserRULE_andExpression            = 11
	SqlBaseParserRULE_notExpression            = 12
	SqlBaseParserRULE_predicate                = 13
	SqlBaseParserRULE_additiveExpression       = 14
	SqlBaseParserRULE_multiplicativeExpression = 15
	SqlBaseParserRULE_unaryExpression          = 16
	SqlBaseParserRULE_primaryExpression        = 17
	SqlBaseParserRULE_functionCall             = 18
	SqlBaseParserRULE_typeName                 = 19
	SqlBaseParserRULE_literal                  = 20
	SqlBaseParserRULE_identifier               = 21
)

// IStatementContext is an interface to support dynamic dispatch.
type IStatementContext interface {
	antlr.ParserRuleContext

	// GetParser returns the parser.
	GetParser() antlr.Parser

	// Getter signatures
	SelectQuery() ISelectQueryContext
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

func (s *StatementContext) SelectQuery() ISelectQueryContext {
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
		p.SetState(44)
		p.SelectQuery()
	}
	p.SetState(46)
	p.GetErrorHandler().Sync(p)
	if p.HasError() {
		goto errorExit
	}
	_la = p.GetTokenStream().LA(1)

	if _la == SqlBaseParserSEMICOLON {
		{
			p.SetState(45)
			p.Match(SqlBaseParserSEMICOLON)
			if p.HasError() {
				// Recognition error - abort rule
				goto errorExit
			}
		}

	}
	{
		p.SetState(48)
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
	AllBY() []antlr.TerminalNode
	BY(i int) antlr.TerminalNode
	ExpressionList() IExpressionListContext
	HAVING() antlr.TerminalNode
	ORDER() antlr.TerminalNode
	AllSortItem() []ISortItemContext
	SortItem(i int) ISortItemContext
	LimitClause() ILimitClauseContext

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

func (s *SelectQueryContext) AllBY() []antlr.TerminalNode {
	return s.GetTokens(SqlBaseParserBY)
}

func (s *SelectQueryContext) BY(i int) antlr.TerminalNode {
	return s.GetToken(SqlBaseParserBY, i)
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

func (s *SelectQueryContext) ORDER() antlr.TerminalNode {
	return s.GetToken(SqlBaseParserORDER, 0)
}

func (s *SelectQueryContext) AllSortItem() []ISortItemContext {
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

func (s *SelectQueryContext) SortItem(i int) ISortItemContext {
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

func (s *SelectQueryContext) LimitClause() ILimitClauseContext {
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
	p.EnterRule(localctx, 2, SqlBaseParserRULE_selectQuery)
	var _la int

	p.EnterOuterAlt(localctx, 1)
	{
		p.SetState(50)
		p.Match(SqlBaseParserSELECT)
		if p.HasError() {
			// Recognition error - abort rule
			goto errorExit
		}
	}
	p.SetState(52)
	p.GetErrorHandler().Sync(p)
	if p.HasError() {
		goto errorExit
	}
	_la = p.GetTokenStream().LA(1)

	if _la == SqlBaseParserDISTINCT {
		{
			p.SetState(51)
			p.Match(SqlBaseParserDISTINCT)
			if p.HasError() {
				// Recognition error - abort rule
				goto errorExit
			}
		}

	}
	{
		p.SetState(54)
		p.SelectItem()
	}
	p.SetState(59)
	p.GetErrorHandler().Sync(p)
	if p.HasError() {
		goto errorExit
	}
	_la = p.GetTokenStream().LA(1)

	for _la == SqlBaseParserCOMMA {
		{
			p.SetState(55)
			p.Match(SqlBaseParserCOMMA)
			if p.HasError() {
				// Recognition error - abort rule
				goto errorExit
			}
		}
		{
			p.SetState(56)
			p.SelectItem()
		}

		p.SetState(61)
		p.GetErrorHandler().Sync(p)
		if p.HasError() {
			goto errorExit
		}
		_la = p.GetTokenStream().LA(1)
	}
	{
		p.SetState(62)
		p.Match(SqlBaseParserFROM)
		if p.HasError() {
			// Recognition error - abort rule
			goto errorExit
		}
	}
	{
		p.SetState(63)
		p.Relation()
	}
	p.SetState(66)
	p.GetErrorHandler().Sync(p)
	if p.HasError() {
		goto errorExit
	}
	_la = p.GetTokenStream().LA(1)

	if _la == SqlBaseParserWHERE {
		{
			p.SetState(64)
			p.Match(SqlBaseParserWHERE)
			if p.HasError() {
				// Recognition error - abort rule
				goto errorExit
			}
		}
		{
			p.SetState(65)
			p.Expression()
		}

	}
	p.SetState(71)
	p.GetErrorHandler().Sync(p)
	if p.HasError() {
		goto errorExit
	}
	_la = p.GetTokenStream().LA(1)

	if _la == SqlBaseParserGROUP {
		{
			p.SetState(68)
			p.Match(SqlBaseParserGROUP)
			if p.HasError() {
				// Recognition error - abort rule
				goto errorExit
			}
		}
		{
			p.SetState(69)
			p.Match(SqlBaseParserBY)
			if p.HasError() {
				// Recognition error - abort rule
				goto errorExit
			}
		}
		{
			p.SetState(70)
			p.ExpressionList()
		}

	}
	p.SetState(75)
	p.GetErrorHandler().Sync(p)
	if p.HasError() {
		goto errorExit
	}
	_la = p.GetTokenStream().LA(1)

	if _la == SqlBaseParserHAVING {
		{
			p.SetState(73)
			p.Match(SqlBaseParserHAVING)
			if p.HasError() {
				// Recognition error - abort rule
				goto errorExit
			}
		}
		{
			p.SetState(74)
			p.Expression()
		}

	}
	p.SetState(87)
	p.GetErrorHandler().Sync(p)
	if p.HasError() {
		goto errorExit
	}
	_la = p.GetTokenStream().LA(1)

	if _la == SqlBaseParserORDER {
		{
			p.SetState(77)
			p.Match(SqlBaseParserORDER)
			if p.HasError() {
				// Recognition error - abort rule
				goto errorExit
			}
		}
		{
			p.SetState(78)
			p.Match(SqlBaseParserBY)
			if p.HasError() {
				// Recognition error - abort rule
				goto errorExit
			}
		}
		{
			p.SetState(79)
			p.SortItem()
		}
		p.SetState(84)
		p.GetErrorHandler().Sync(p)
		if p.HasError() {
			goto errorExit
		}
		_la = p.GetTokenStream().LA(1)

		for _la == SqlBaseParserCOMMA {
			{
				p.SetState(80)
				p.Match(SqlBaseParserCOMMA)
				if p.HasError() {
					// Recognition error - abort rule
					goto errorExit
				}
			}
			{
				p.SetState(81)
				p.SortItem()
			}

			p.SetState(86)
			p.GetErrorHandler().Sync(p)
			if p.HasError() {
				goto errorExit
			}
			_la = p.GetTokenStream().LA(1)
		}

	}
	p.SetState(90)
	p.GetErrorHandler().Sync(p)
	if p.HasError() {
		goto errorExit
	}
	_la = p.GetTokenStream().LA(1)

	if _la == SqlBaseParserLIMIT {
		{
			p.SetState(89)
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
	p.EnterRule(localctx, 4, SqlBaseParserRULE_selectItem)
	var _la int

	p.SetState(105)
	p.GetErrorHandler().Sync(p)
	if p.HasError() {
		goto errorExit
	}

	switch p.GetInterpreter().AdaptivePredict(p.BaseParser, p.GetTokenStream(), 12, p.GetParserRuleContext()) {
	case 1:
		p.EnterOuterAlt(localctx, 1)
		p.SetState(95)
		p.GetErrorHandler().Sync(p)
		if p.HasError() {
			goto errorExit
		}
		_la = p.GetTokenStream().LA(1)

		if _la == SqlBaseParserQUOTED_IDENTIFIER || _la == SqlBaseParserIDENTIFIER {
			{
				p.SetState(92)
				p.Identifier()
			}
			{
				p.SetState(93)
				p.Match(SqlBaseParserDOT)
				if p.HasError() {
					// Recognition error - abort rule
					goto errorExit
				}
			}

		}
		{
			p.SetState(97)
			p.Match(SqlBaseParserSTAR)
			if p.HasError() {
				// Recognition error - abort rule
				goto errorExit
			}
		}

	case 2:
		p.EnterOuterAlt(localctx, 2)
		{
			p.SetState(98)
			p.Expression()
		}
		p.SetState(103)
		p.GetErrorHandler().Sync(p)
		if p.HasError() {
			goto errorExit
		}
		_la = p.GetTokenStream().LA(1)

		if (int64(_la) & ^0x3f) == 0 && ((int64(1)<<_la)&1729382256910272512) != 0 {
			p.SetState(100)
			p.GetErrorHandler().Sync(p)
			if p.HasError() {
				goto errorExit
			}
			_la = p.GetTokenStream().LA(1)

			if _la == SqlBaseParserAS {
				{
					p.SetState(99)
					p.Match(SqlBaseParserAS)
					if p.HasError() {
						// Recognition error - abort rule
						goto errorExit
					}
				}

			}
			{
				p.SetState(102)
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
	p.EnterRule(localctx, 6, SqlBaseParserRULE_relation)
	var _la int

	p.EnterOuterAlt(localctx, 1)
	{
		p.SetState(107)
		p.RelationPrimary()
	}
	p.SetState(111)
	p.GetErrorHandler().Sync(p)
	if p.HasError() {
		goto errorExit
	}
	_la = p.GetTokenStream().LA(1)

	for (int64(_la) & ^0x3f) == 0 && ((int64(1)<<_la)&225280) != 0 {
		{
			p.SetState(108)
			p.JoinPart()
		}

		p.SetState(113)
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
	SelectQuery() ISelectQueryContext
	RPAREN() antlr.TerminalNode

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

func (s *RelationPrimaryContext) SelectQuery() ISelectQueryContext {
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

func (s *RelationPrimaryContext) RPAREN() antlr.TerminalNode {
	return s.GetToken(SqlBaseParserRPAREN, 0)
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
	p.EnterRule(localctx, 8, SqlBaseParserRULE_relationPrimary)
	var _la int

	p.SetState(129)
	p.GetErrorHandler().Sync(p)
	if p.HasError() {
		goto errorExit
	}

	switch p.GetTokenStream().LA(1) {
	case SqlBaseParserRESOURCE:
		p.EnterOuterAlt(localctx, 1)
		{
			p.SetState(114)
			p.Match(SqlBaseParserRESOURCE)
			if p.HasError() {
				// Recognition error - abort rule
				goto errorExit
			}
		}
		p.SetState(119)
		p.GetErrorHandler().Sync(p)
		if p.HasError() {
			goto errorExit
		}
		_la = p.GetTokenStream().LA(1)

		if (int64(_la) & ^0x3f) == 0 && ((int64(1)<<_la)&1729382256910272512) != 0 {
			p.SetState(116)
			p.GetErrorHandler().Sync(p)
			if p.HasError() {
				goto errorExit
			}
			_la = p.GetTokenStream().LA(1)

			if _la == SqlBaseParserAS {
				{
					p.SetState(115)
					p.Match(SqlBaseParserAS)
					if p.HasError() {
						// Recognition error - abort rule
						goto errorExit
					}
				}

			}
			{
				p.SetState(118)
				p.Identifier()
			}

		}

	case SqlBaseParserLPAREN:
		p.EnterOuterAlt(localctx, 2)
		{
			p.SetState(121)
			p.Match(SqlBaseParserLPAREN)
			if p.HasError() {
				// Recognition error - abort rule
				goto errorExit
			}
		}
		{
			p.SetState(122)
			p.SelectQuery()
		}
		{
			p.SetState(123)
			p.Match(SqlBaseParserRPAREN)
			if p.HasError() {
				// Recognition error - abort rule
				goto errorExit
			}
		}
		p.SetState(125)
		p.GetErrorHandler().Sync(p)
		if p.HasError() {
			goto errorExit
		}
		_la = p.GetTokenStream().LA(1)

		if _la == SqlBaseParserAS {
			{
				p.SetState(124)
				p.Match(SqlBaseParserAS)
				if p.HasError() {
					// Recognition error - abort rule
					goto errorExit
				}
			}

		}
		{
			p.SetState(127)
			p.Identifier()
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
	p.EnterRule(localctx, 10, SqlBaseParserRULE_joinPart)
	var _la int

	p.SetState(150)
	p.GetErrorHandler().Sync(p)
	if p.HasError() {
		goto errorExit
	}

	switch p.GetTokenStream().LA(1) {
	case SqlBaseParserINNER, SqlBaseParserLEFT, SqlBaseParserRIGHT, SqlBaseParserJOIN:
		p.EnterOuterAlt(localctx, 1)
		p.SetState(140)
		p.GetErrorHandler().Sync(p)
		if p.HasError() {
			goto errorExit
		}
		switch p.GetTokenStream().LA(1) {
		case SqlBaseParserINNER:
			{
				p.SetState(131)
				p.Match(SqlBaseParserINNER)
				if p.HasError() {
					// Recognition error - abort rule
					goto errorExit
				}
			}

		case SqlBaseParserLEFT:
			{
				p.SetState(132)
				p.Match(SqlBaseParserLEFT)
				if p.HasError() {
					// Recognition error - abort rule
					goto errorExit
				}
			}
			p.SetState(134)
			p.GetErrorHandler().Sync(p)
			if p.HasError() {
				goto errorExit
			}
			_la = p.GetTokenStream().LA(1)

			if _la == SqlBaseParserOUTER {
				{
					p.SetState(133)
					p.Match(SqlBaseParserOUTER)
					if p.HasError() {
						// Recognition error - abort rule
						goto errorExit
					}
				}

			}

		case SqlBaseParserRIGHT:
			{
				p.SetState(136)
				p.Match(SqlBaseParserRIGHT)
				if p.HasError() {
					// Recognition error - abort rule
					goto errorExit
				}
			}
			p.SetState(138)
			p.GetErrorHandler().Sync(p)
			if p.HasError() {
				goto errorExit
			}
			_la = p.GetTokenStream().LA(1)

			if _la == SqlBaseParserOUTER {
				{
					p.SetState(137)
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
			p.SetState(142)
			p.Match(SqlBaseParserJOIN)
			if p.HasError() {
				// Recognition error - abort rule
				goto errorExit
			}
		}
		{
			p.SetState(143)
			p.RelationPrimary()
		}
		{
			p.SetState(144)
			p.Match(SqlBaseParserON)
			if p.HasError() {
				// Recognition error - abort rule
				goto errorExit
			}
		}
		{
			p.SetState(145)
			p.Expression()
		}

	case SqlBaseParserCROSS:
		p.EnterOuterAlt(localctx, 2)
		{
			p.SetState(147)
			p.Match(SqlBaseParserCROSS)
			if p.HasError() {
				// Recognition error - abort rule
				goto errorExit
			}
		}
		{
			p.SetState(148)
			p.Match(SqlBaseParserJOIN)
			if p.HasError() {
				// Recognition error - abort rule
				goto errorExit
			}
		}
		{
			p.SetState(149)
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
	p.EnterRule(localctx, 12, SqlBaseParserRULE_sortItem)
	var _la int

	p.EnterOuterAlt(localctx, 1)
	{
		p.SetState(152)
		p.Expression()
	}
	p.SetState(154)
	p.GetErrorHandler().Sync(p)
	if p.HasError() {
		goto errorExit
	}
	_la = p.GetTokenStream().LA(1)

	if _la == SqlBaseParserASC || _la == SqlBaseParserDESC {
		{
			p.SetState(153)
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
	p.EnterRule(localctx, 14, SqlBaseParserRULE_limitClause)
	p.EnterOuterAlt(localctx, 1)
	{
		p.SetState(156)
		p.Match(SqlBaseParserLIMIT)
		if p.HasError() {
			// Recognition error - abort rule
			goto errorExit
		}
	}
	{
		p.SetState(157)
		p.Match(SqlBaseParserNUMBER)
		if p.HasError() {
			// Recognition error - abort rule
			goto errorExit
		}
	}
	p.SetState(162)
	p.GetErrorHandler().Sync(p)
	if p.HasError() {
		goto errorExit
	}
	switch p.GetTokenStream().LA(1) {
	case SqlBaseParserOFFSET:
		{
			p.SetState(158)
			p.Match(SqlBaseParserOFFSET)
			if p.HasError() {
				// Recognition error - abort rule
				goto errorExit
			}
		}
		{
			p.SetState(159)
			p.Match(SqlBaseParserNUMBER)
			if p.HasError() {
				// Recognition error - abort rule
				goto errorExit
			}
		}

	case SqlBaseParserCOMMA:
		{
			p.SetState(160)
			p.Match(SqlBaseParserCOMMA)
			if p.HasError() {
				// Recognition error - abort rule
				goto errorExit
			}
		}
		{
			p.SetState(161)
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
	p.EnterRule(localctx, 16, SqlBaseParserRULE_expressionList)
	var _la int

	p.EnterOuterAlt(localctx, 1)
	{
		p.SetState(164)
		p.Expression()
	}
	p.SetState(169)
	p.GetErrorHandler().Sync(p)
	if p.HasError() {
		goto errorExit
	}
	_la = p.GetTokenStream().LA(1)

	for _la == SqlBaseParserCOMMA {
		{
			p.SetState(165)
			p.Match(SqlBaseParserCOMMA)
			if p.HasError() {
				// Recognition error - abort rule
				goto errorExit
			}
		}
		{
			p.SetState(166)
			p.Expression()
		}

		p.SetState(171)
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
	p.EnterRule(localctx, 18, SqlBaseParserRULE_expression)
	p.EnterOuterAlt(localctx, 1)
	{
		p.SetState(172)
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
	p.EnterRule(localctx, 20, SqlBaseParserRULE_orExpression)
	var _la int

	p.EnterOuterAlt(localctx, 1)
	{
		p.SetState(174)
		p.AndExpression()
	}
	p.SetState(179)
	p.GetErrorHandler().Sync(p)
	if p.HasError() {
		goto errorExit
	}
	_la = p.GetTokenStream().LA(1)

	for _la == SqlBaseParserOR {
		{
			p.SetState(175)
			p.Match(SqlBaseParserOR)
			if p.HasError() {
				// Recognition error - abort rule
				goto errorExit
			}
		}
		{
			p.SetState(176)
			p.AndExpression()
		}

		p.SetState(181)
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
	p.EnterRule(localctx, 22, SqlBaseParserRULE_andExpression)
	var _la int

	p.EnterOuterAlt(localctx, 1)
	{
		p.SetState(182)
		p.NotExpression()
	}
	p.SetState(187)
	p.GetErrorHandler().Sync(p)
	if p.HasError() {
		goto errorExit
	}
	_la = p.GetTokenStream().LA(1)

	for _la == SqlBaseParserAND {
		{
			p.SetState(183)
			p.Match(SqlBaseParserAND)
			if p.HasError() {
				// Recognition error - abort rule
				goto errorExit
			}
		}
		{
			p.SetState(184)
			p.NotExpression()
		}

		p.SetState(189)
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
	p.EnterRule(localctx, 24, SqlBaseParserRULE_notExpression)
	p.SetState(193)
	p.GetErrorHandler().Sync(p)
	if p.HasError() {
		goto errorExit
	}

	switch p.GetTokenStream().LA(1) {
	case SqlBaseParserNOT:
		p.EnterOuterAlt(localctx, 1)
		{
			p.SetState(190)
			p.Match(SqlBaseParserNOT)
			if p.HasError() {
				// Recognition error - abort rule
				goto errorExit
			}
		}
		{
			p.SetState(191)
			p.NotExpression()
		}

	case SqlBaseParserNULL, SqlBaseParserCAST, SqlBaseParserCASE, SqlBaseParserTRUE, SqlBaseParserFALSE, SqlBaseParserCURRENT_TIMESTAMP, SqlBaseParserLPAREN, SqlBaseParserPLUS, SqlBaseParserMINUS, SqlBaseParserNUMBER, SqlBaseParserSTRING, SqlBaseParserQUOTED_IDENTIFIER, SqlBaseParserIDENTIFIER:
		p.EnterOuterAlt(localctx, 2)
		{
			p.SetState(192)
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
	p.EnterRule(localctx, 26, SqlBaseParserRULE_predicate)
	var _la int

	p.EnterOuterAlt(localctx, 1)
	{
		p.SetState(195)
		p.AdditiveExpression()
	}
	p.SetState(228)
	p.GetErrorHandler().Sync(p)

	if p.GetInterpreter().AdaptivePredict(p.BaseParser, p.GetTokenStream(), 33, p.GetParserRuleContext()) == 1 {
		{
			p.SetState(196)
			_la = p.GetTokenStream().LA(1)

			if !((int64(_la) & ^0x3f) == 0 && ((int64(1)<<_la)&4433230883192832) != 0) {
				p.GetErrorHandler().RecoverInline(p)
			} else {
				p.GetErrorHandler().ReportMatch(p)
				p.Consume()
			}
		}
		{
			p.SetState(197)
			p.AdditiveExpression()
		}

	} else if p.HasError() { // JIM
		goto errorExit
	} else if p.GetInterpreter().AdaptivePredict(p.BaseParser, p.GetTokenStream(), 33, p.GetParserRuleContext()) == 2 {
		{
			p.SetState(198)
			p.Match(SqlBaseParserIS)
			if p.HasError() {
				// Recognition error - abort rule
				goto errorExit
			}
		}
		p.SetState(200)
		p.GetErrorHandler().Sync(p)
		if p.HasError() {
			goto errorExit
		}
		_la = p.GetTokenStream().LA(1)

		if _la == SqlBaseParserNOT {
			{
				p.SetState(199)
				p.Match(SqlBaseParserNOT)
				if p.HasError() {
					// Recognition error - abort rule
					goto errorExit
				}
			}

		}
		{
			p.SetState(202)
			p.Match(SqlBaseParserNULL)
			if p.HasError() {
				// Recognition error - abort rule
				goto errorExit
			}
		}

	} else if p.HasError() { // JIM
		goto errorExit
	} else if p.GetInterpreter().AdaptivePredict(p.BaseParser, p.GetTokenStream(), 33, p.GetParserRuleContext()) == 3 {
		p.SetState(204)
		p.GetErrorHandler().Sync(p)
		if p.HasError() {
			goto errorExit
		}
		_la = p.GetTokenStream().LA(1)

		if _la == SqlBaseParserNOT {
			{
				p.SetState(203)
				p.Match(SqlBaseParserNOT)
				if p.HasError() {
					// Recognition error - abort rule
					goto errorExit
				}
			}

		}
		{
			p.SetState(206)
			p.Match(SqlBaseParserBETWEEN)
			if p.HasError() {
				// Recognition error - abort rule
				goto errorExit
			}
		}
		{
			p.SetState(207)
			p.AdditiveExpression()
		}
		{
			p.SetState(208)
			p.Match(SqlBaseParserAND)
			if p.HasError() {
				// Recognition error - abort rule
				goto errorExit
			}
		}
		{
			p.SetState(209)
			p.AdditiveExpression()
		}

	} else if p.HasError() { // JIM
		goto errorExit
	} else if p.GetInterpreter().AdaptivePredict(p.BaseParser, p.GetTokenStream(), 33, p.GetParserRuleContext()) == 4 {
		p.SetState(212)
		p.GetErrorHandler().Sync(p)
		if p.HasError() {
			goto errorExit
		}
		_la = p.GetTokenStream().LA(1)

		if _la == SqlBaseParserNOT {
			{
				p.SetState(211)
				p.Match(SqlBaseParserNOT)
				if p.HasError() {
					// Recognition error - abort rule
					goto errorExit
				}
			}

		}
		{
			p.SetState(214)
			p.Match(SqlBaseParserIN)
			if p.HasError() {
				// Recognition error - abort rule
				goto errorExit
			}
		}
		{
			p.SetState(215)
			p.Match(SqlBaseParserLPAREN)
			if p.HasError() {
				// Recognition error - abort rule
				goto errorExit
			}
		}
		{
			p.SetState(216)
			p.ExpressionList()
		}
		{
			p.SetState(217)
			p.Match(SqlBaseParserRPAREN)
			if p.HasError() {
				// Recognition error - abort rule
				goto errorExit
			}
		}

	} else if p.HasError() { // JIM
		goto errorExit
	} else if p.GetInterpreter().AdaptivePredict(p.BaseParser, p.GetTokenStream(), 33, p.GetParserRuleContext()) == 5 {
		p.SetState(220)
		p.GetErrorHandler().Sync(p)
		if p.HasError() {
			goto errorExit
		}
		_la = p.GetTokenStream().LA(1)

		if _la == SqlBaseParserNOT {
			{
				p.SetState(219)
				p.Match(SqlBaseParserNOT)
				if p.HasError() {
					// Recognition error - abort rule
					goto errorExit
				}
			}

		}
		{
			p.SetState(222)
			p.Match(SqlBaseParserLIKE)
			if p.HasError() {
				// Recognition error - abort rule
				goto errorExit
			}
		}
		{
			p.SetState(223)
			p.AdditiveExpression()
		}
		p.SetState(226)
		p.GetErrorHandler().Sync(p)
		if p.HasError() {
			goto errorExit
		}
		_la = p.GetTokenStream().LA(1)

		if _la == SqlBaseParserESCAPE {
			{
				p.SetState(224)
				p.Match(SqlBaseParserESCAPE)
				if p.HasError() {
					// Recognition error - abort rule
					goto errorExit
				}
			}
			{
				p.SetState(225)
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
	p.EnterRule(localctx, 28, SqlBaseParserRULE_additiveExpression)
	var _la int

	p.EnterOuterAlt(localctx, 1)
	{
		p.SetState(230)
		p.MultiplicativeExpression()
	}
	p.SetState(235)
	p.GetErrorHandler().Sync(p)
	if p.HasError() {
		goto errorExit
	}
	_la = p.GetTokenStream().LA(1)

	for _la == SqlBaseParserPLUS || _la == SqlBaseParserMINUS {
		{
			p.SetState(231)
			_la = p.GetTokenStream().LA(1)

			if !(_la == SqlBaseParserPLUS || _la == SqlBaseParserMINUS) {
				p.GetErrorHandler().RecoverInline(p)
			} else {
				p.GetErrorHandler().ReportMatch(p)
				p.Consume()
			}
		}
		{
			p.SetState(232)
			p.MultiplicativeExpression()
		}

		p.SetState(237)
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
	p.EnterRule(localctx, 30, SqlBaseParserRULE_multiplicativeExpression)
	var _la int

	p.EnterOuterAlt(localctx, 1)
	{
		p.SetState(238)
		p.UnaryExpression()
	}
	p.SetState(243)
	p.GetErrorHandler().Sync(p)
	if p.HasError() {
		goto errorExit
	}
	_la = p.GetTokenStream().LA(1)

	for (int64(_la) & ^0x3f) == 0 && ((int64(1)<<_la)&126100789566373888) != 0 {
		{
			p.SetState(239)
			_la = p.GetTokenStream().LA(1)

			if !((int64(_la) & ^0x3f) == 0 && ((int64(1)<<_la)&126100789566373888) != 0) {
				p.GetErrorHandler().RecoverInline(p)
			} else {
				p.GetErrorHandler().ReportMatch(p)
				p.Consume()
			}
		}
		{
			p.SetState(240)
			p.UnaryExpression()
		}

		p.SetState(245)
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
	p.EnterRule(localctx, 32, SqlBaseParserRULE_unaryExpression)
	var _la int

	p.SetState(249)
	p.GetErrorHandler().Sync(p)
	if p.HasError() {
		goto errorExit
	}

	switch p.GetTokenStream().LA(1) {
	case SqlBaseParserPLUS, SqlBaseParserMINUS:
		p.EnterOuterAlt(localctx, 1)
		{
			p.SetState(246)
			_la = p.GetTokenStream().LA(1)

			if !(_la == SqlBaseParserPLUS || _la == SqlBaseParserMINUS) {
				p.GetErrorHandler().RecoverInline(p)
			} else {
				p.GetErrorHandler().ReportMatch(p)
				p.Consume()
			}
		}
		{
			p.SetState(247)
			p.UnaryExpression()
		}

	case SqlBaseParserNULL, SqlBaseParserCAST, SqlBaseParserCASE, SqlBaseParserTRUE, SqlBaseParserFALSE, SqlBaseParserCURRENT_TIMESTAMP, SqlBaseParserLPAREN, SqlBaseParserNUMBER, SqlBaseParserSTRING, SqlBaseParserQUOTED_IDENTIFIER, SqlBaseParserIDENTIFIER:
		p.EnterOuterAlt(localctx, 2)
		{
			p.SetState(248)
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
	p.EnterRule(localctx, 34, SqlBaseParserRULE_primaryExpression)
	var _la int

	p.SetState(293)
	p.GetErrorHandler().Sync(p)
	if p.HasError() {
		goto errorExit
	}

	switch p.GetInterpreter().AdaptivePredict(p.BaseParser, p.GetTokenStream(), 42, p.GetParserRuleContext()) {
	case 1:
		p.EnterOuterAlt(localctx, 1)
		{
			p.SetState(251)
			p.Literal()
		}

	case 2:
		p.EnterOuterAlt(localctx, 2)
		{
			p.SetState(252)
			p.Identifier()
		}
		p.SetState(255)
		p.GetErrorHandler().Sync(p)
		if p.HasError() {
			goto errorExit
		}
		_la = p.GetTokenStream().LA(1)

		if _la == SqlBaseParserDOT {
			{
				p.SetState(253)
				p.Match(SqlBaseParserDOT)
				if p.HasError() {
					// Recognition error - abort rule
					goto errorExit
				}
			}
			{
				p.SetState(254)
				p.Identifier()
			}

		}

	case 3:
		p.EnterOuterAlt(localctx, 3)
		{
			p.SetState(257)
			p.FunctionCall()
		}

	case 4:
		p.EnterOuterAlt(localctx, 4)
		{
			p.SetState(258)
			p.Match(SqlBaseParserCURRENT_TIMESTAMP)
			if p.HasError() {
				// Recognition error - abort rule
				goto errorExit
			}
		}
		p.SetState(261)
		p.GetErrorHandler().Sync(p)
		if p.HasError() {
			goto errorExit
		}
		_la = p.GetTokenStream().LA(1)

		if _la == SqlBaseParserLPAREN {
			{
				p.SetState(259)
				p.Match(SqlBaseParserLPAREN)
				if p.HasError() {
					// Recognition error - abort rule
					goto errorExit
				}
			}
			{
				p.SetState(260)
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
			p.SetState(263)
			p.Match(SqlBaseParserCAST)
			if p.HasError() {
				// Recognition error - abort rule
				goto errorExit
			}
		}
		{
			p.SetState(264)
			p.Match(SqlBaseParserLPAREN)
			if p.HasError() {
				// Recognition error - abort rule
				goto errorExit
			}
		}
		{
			p.SetState(265)
			p.Expression()
		}
		{
			p.SetState(266)
			p.Match(SqlBaseParserAS)
			if p.HasError() {
				// Recognition error - abort rule
				goto errorExit
			}
		}
		{
			p.SetState(267)
			p.TypeName()
		}
		{
			p.SetState(268)
			p.Match(SqlBaseParserRPAREN)
			if p.HasError() {
				// Recognition error - abort rule
				goto errorExit
			}
		}

	case 6:
		p.EnterOuterAlt(localctx, 6)
		{
			p.SetState(270)
			p.Match(SqlBaseParserCASE)
			if p.HasError() {
				// Recognition error - abort rule
				goto errorExit
			}
		}
		p.SetState(272)
		p.GetErrorHandler().Sync(p)
		if p.HasError() {
			goto errorExit
		}
		_la = p.GetTokenStream().LA(1)

		if (int64(_la) & ^0x3f) == 0 && ((int64(1)<<_la)&2175241303342710784) != 0 {
			{
				p.SetState(271)
				p.Expression()
			}

		}
		p.SetState(279)
		p.GetErrorHandler().Sync(p)
		if p.HasError() {
			goto errorExit
		}
		_la = p.GetTokenStream().LA(1)

		for ok := true; ok; ok = _la == SqlBaseParserWHEN {
			{
				p.SetState(274)
				p.Match(SqlBaseParserWHEN)
				if p.HasError() {
					// Recognition error - abort rule
					goto errorExit
				}
			}
			{
				p.SetState(275)
				p.Expression()
			}
			{
				p.SetState(276)
				p.Match(SqlBaseParserTHEN)
				if p.HasError() {
					// Recognition error - abort rule
					goto errorExit
				}
			}
			{
				p.SetState(277)
				p.Expression()
			}

			p.SetState(281)
			p.GetErrorHandler().Sync(p)
			if p.HasError() {
				goto errorExit
			}
			_la = p.GetTokenStream().LA(1)
		}
		p.SetState(285)
		p.GetErrorHandler().Sync(p)
		if p.HasError() {
			goto errorExit
		}
		_la = p.GetTokenStream().LA(1)

		if _la == SqlBaseParserELSE {
			{
				p.SetState(283)
				p.Match(SqlBaseParserELSE)
				if p.HasError() {
					// Recognition error - abort rule
					goto errorExit
				}
			}
			{
				p.SetState(284)
				p.Expression()
			}

		}
		{
			p.SetState(287)
			p.Match(SqlBaseParserEND)
			if p.HasError() {
				// Recognition error - abort rule
				goto errorExit
			}
		}

	case 7:
		p.EnterOuterAlt(localctx, 7)
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
			p.Expression()
		}
		{
			p.SetState(291)
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
	p.EnterRule(localctx, 36, SqlBaseParserRULE_functionCall)
	var _la int

	p.EnterOuterAlt(localctx, 1)
	{
		p.SetState(295)
		p.Identifier()
	}
	{
		p.SetState(296)
		p.Match(SqlBaseParserLPAREN)
		if p.HasError() {
			// Recognition error - abort rule
			goto errorExit
		}
	}
	p.SetState(298)
	p.GetErrorHandler().Sync(p)
	if p.HasError() {
		goto errorExit
	}
	_la = p.GetTokenStream().LA(1)

	if _la == SqlBaseParserDISTINCT {
		{
			p.SetState(297)
			p.Match(SqlBaseParserDISTINCT)
			if p.HasError() {
				// Recognition error - abort rule
				goto errorExit
			}
		}

	}
	p.SetState(302)
	p.GetErrorHandler().Sync(p)
	if p.HasError() {
		goto errorExit
	}
	switch p.GetTokenStream().LA(1) {
	case SqlBaseParserSTAR:
		{
			p.SetState(300)
			p.Match(SqlBaseParserSTAR)
			if p.HasError() {
				// Recognition error - abort rule
				goto errorExit
			}
		}

	case SqlBaseParserNOT, SqlBaseParserNULL, SqlBaseParserCAST, SqlBaseParserCASE, SqlBaseParserTRUE, SqlBaseParserFALSE, SqlBaseParserCURRENT_TIMESTAMP, SqlBaseParserLPAREN, SqlBaseParserPLUS, SqlBaseParserMINUS, SqlBaseParserNUMBER, SqlBaseParserSTRING, SqlBaseParserQUOTED_IDENTIFIER, SqlBaseParserIDENTIFIER:
		{
			p.SetState(301)
			p.ExpressionList()
		}

	case SqlBaseParserRPAREN:

	default:
	}
	{
		p.SetState(304)
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
	p.EnterRule(localctx, 38, SqlBaseParserRULE_typeName)
	var _la int

	p.EnterOuterAlt(localctx, 1)
	{
		p.SetState(306)
		p.Identifier()
	}
	p.SetState(314)
	p.GetErrorHandler().Sync(p)
	if p.HasError() {
		goto errorExit
	}
	_la = p.GetTokenStream().LA(1)

	if _la == SqlBaseParserLPAREN {
		{
			p.SetState(307)
			p.Match(SqlBaseParserLPAREN)
			if p.HasError() {
				// Recognition error - abort rule
				goto errorExit
			}
		}
		{
			p.SetState(308)
			p.Match(SqlBaseParserNUMBER)
			if p.HasError() {
				// Recognition error - abort rule
				goto errorExit
			}
		}
		p.SetState(311)
		p.GetErrorHandler().Sync(p)
		if p.HasError() {
			goto errorExit
		}
		_la = p.GetTokenStream().LA(1)

		if _la == SqlBaseParserCOMMA {
			{
				p.SetState(309)
				p.Match(SqlBaseParserCOMMA)
				if p.HasError() {
					// Recognition error - abort rule
					goto errorExit
				}
			}
			{
				p.SetState(310)
				p.Match(SqlBaseParserNUMBER)
				if p.HasError() {
					// Recognition error - abort rule
					goto errorExit
				}
			}

		}
		{
			p.SetState(313)
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
	p.EnterRule(localctx, 40, SqlBaseParserRULE_literal)
	var _la int

	p.EnterOuterAlt(localctx, 1)
	{
		p.SetState(316)
		_la = p.GetTokenStream().LA(1)

		if !((int64(_la) & ^0x3f) == 0 && ((int64(1)<<_la)&432345770419552256) != 0) {
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
	p.EnterRule(localctx, 42, SqlBaseParserRULE_identifier)
	var _la int

	p.EnterOuterAlt(localctx, 1)
	{
		p.SetState(318)
		_la = p.GetTokenStream().LA(1)

		if !(_la == SqlBaseParserQUOTED_IDENTIFIER || _la == SqlBaseParserIDENTIFIER) {
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
