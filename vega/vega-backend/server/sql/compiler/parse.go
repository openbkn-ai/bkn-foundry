// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package compiler 将 ANTLR 解析树直接转换为结构化 SQL Builder，不访问资源或数据库。
package compiler

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/antlr4-go/antlr/v4"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces/sqlbuilder"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/sql/compiler/sqlparser/generated"
)

const (
	maxSQLBytes = 1 << 20
	maxTokens   = 10000
	maxDepth    = 128
	maxNodes    = 5000
	maxINValues = 1000
)

const (
	CodeSyntax      ErrorCode = "syntax"
	CodeUnsupported ErrorCode = "unsupported"
	CodeLimit       ErrorCode = "limit"
)

type ErrorCode string

// ParseError 的行、列从 1 开始；不保存 SQL 原文或 ANTLR 的原始报错文本。
type ParseError struct {
	Code         ErrorCode
	Line, Column int
	Message      string
}

type parseErrors struct {
	*antlr.DefaultErrorListener
	err *ParseError
}

// budgetLexer 同时统计普通及隐藏 token，避免大量注释绕过 token 限制。
type budgetLexer struct {
	*generated.SqlBaseLexer
	errors *parseErrors
	count  int
}

// ParseSQL 严格消费一条 SELECT，直接返回尚未绑定资源和类型的新 Builder。
// 数字保留为 json.Number，不经过 float64；标识符和字符串仅解码一次。
// 此处检查开放函数及调用形式；源表类型规则和物理目标能力由执行阶段检查。
func ParseSQL(input string) (*sqlbuilder.SelectBuilder, error) {
	if len(input) > maxSQLBytes {
		return nil, &ParseError{Code: CodeLimit, Line: 1, Column: 1, Message: "SQL byte size exceeds the limit"}
	}
	if !utf8.ValidString(input) {
		return nil, &ParseError{Code: CodeSyntax, Line: 1, Column: 1, Message: "SQL must be valid UTF-8"}
	}
	errors := &parseErrors{DefaultErrorListener: antlr.NewDefaultErrorListener()}
	lexer := generated.NewSqlBaseLexer(antlr.NewInputStream(input))
	lexer.RemoveErrorListeners()
	lexer.AddErrorListener(errors)
	tokens := antlr.NewCommonTokenStream(&budgetLexer{SqlBaseLexer: lexer, errors: errors}, antlr.TokenDefaultChannel)
	depth, prefix := 0, 0
	// 只扫描并缓存一次 token，在调用递归语法规则前约束括号、CASE 和前缀嵌套。
	for {
		token := tokens.LT(1)
		if errors.err != nil {
			return nil, errors.err
		}
		if token.GetTokenType() == antlr.TokenEOF {
			break
		}
		switch token.GetTokenType() {
		case generated.SqlBaseLexerUNSUPPORTED, generated.SqlBaseLexerFORBIDDEN_COMMENT:
			return nil, errorAt(CodeUnsupported, token, "syntax is not supported")
		case generated.SqlBaseLexerLPAREN, generated.SqlBaseLexerCASE:
			depth++
		case generated.SqlBaseLexerRPAREN, generated.SqlBaseLexerEND:
			depth--
		case generated.SqlBaseLexerRESOURCE:
			if len(strings.TrimSpace(token.GetText()[2:len(token.GetText())-2])) > 40 {
				return nil, errorAt(CodeSyntax, token, "resource ID length exceeds the limit")
			}
		}
		if depth < 0 {
			return nil, errorAt(CodeSyntax, token, "unmatched closing delimiter")
		}
		switch token.GetTokenType() {
		case generated.SqlBaseLexerNOT, generated.SqlBaseLexerPLUS, generated.SqlBaseLexerMINUS:
			prefix++
		default:
			prefix = 0
		}
		if depth > maxDepth || prefix > maxDepth {
			return nil, errorAt(CodeLimit, token, "SQL nesting depth exceeds the limit")
		}
		tokens.Consume()
	}
	tokens.Seek(0)
	parser := generated.NewSqlBaseParser(tokens)
	parser.RemoveErrorListeners()
	parser.AddErrorListener(errors)
	statement := parser.Statement()
	if errors.err != nil {
		return nil, errors.err
	}
	b := treeBuilder{depths: make(map[sqlbuilder.SqlNode]int)}
	query := b.query(statement.SelectQuery())
	if b.err != nil {
		return nil, b.err
	}
	return query, nil
}

func (e *ParseError) Error() string {
	return fmt.Sprintf("SQL %s (line %d, column %d): %s", e.Code, e.Line, e.Column, e.Message)
}

func errorAt(code ErrorCode, token antlr.Token, message string) *ParseError {
	return &ParseError{Code: code, Line: token.GetLine(), Column: token.GetColumn() + 1, Message: message}
}

func (e *parseErrors) SyntaxError(_ antlr.Recognizer, _ interface{}, line, column int, _ string, _ antlr.RecognitionException) {
	if e.err == nil {
		e.err = &ParseError{Code: CodeSyntax, Line: line, Column: column + 1, Message: "invalid SQL syntax"}
	}
}

func (l *budgetLexer) NextToken() antlr.Token {
	token := l.SqlBaseLexer.NextToken()
	if token.GetTokenType() != antlr.TokenEOF {
		l.count++
	}
	if l.count > maxTokens && l.errors.err == nil {
		l.errors.err = errorAt(CodeLimit, token, "SQL token count exceeds the limit")
	}
	if l.errors.err != nil {
		return l.GetTokenFactory().Create(l.GetTokenSourceCharStreamPair(), antlr.TokenEOF, "", antlr.TokenDefaultChannel, token.GetStart(), token.GetStop(), token.GetLine(), token.GetColumn())
	}
	return token
}

// ResourceIDs 仅从 MySQL 词法 token 中提取路由所需的资源引用，忽略字符串和注释。
// 不代替 ParseSQL；选定 MySQL 目标后必须完成严格的 SELECT 解析。
func ResourceIDs(input string) ([]string, error) {
	if len(input) > maxSQLBytes {
		return nil, &ParseError{Code: CodeLimit, Line: 1, Column: 1, Message: "SQL byte size exceeds the limit"}
	}
	if !utf8.ValidString(input) {
		return nil, &ParseError{Code: CodeSyntax, Line: 1, Column: 1, Message: "SQL must be valid UTF-8"}
	}
	lexer := generated.NewSqlBaseLexer(antlr.NewInputStream(input))
	lexer.RemoveErrorListeners()
	seen := make(map[string]bool)
	var ids []string
	for count := 0; ; count++ {
		if count > maxTokens {
			return nil, &ParseError{Code: CodeLimit, Line: 1, Column: 1, Message: "SQL token count exceeds the limit"}
		}
		token := lexer.NextToken()
		if token.GetTokenType() == antlr.TokenEOF {
			return ids, nil
		}
		if token.GetTokenType() != generated.SqlBaseLexerRESOURCE {
			continue
		}
		text := token.GetText()
		id := strings.TrimSpace(text[2 : len(text)-2])
		if len(id) > 40 {
			return nil, errorAt(CodeSyntax, token, "resource ID length exceeds the limit")
		}
		if !seen[id] {
			ids = append(ids, id)
			seen[id] = true
		}
	}
}
