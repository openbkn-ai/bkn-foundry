// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package compiler 将 SQL 解析为独立 Query Tree，隔离 ANTLR 生成器类型。
package compiler

import (
	"strings"
	"unicode/utf8"

	"github.com/antlr4-go/antlr/v4"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces/querymodel"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/query/compiler/querytree"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/query/compiler/sqlparser/generated"
)

// ParseSQL 严格消费单条 SELECT；任何 lexer/parser 错误都禁止构造查询。
// 所有限制均作用于当前调用，不创建无法取消的后台解析 goroutine。
func ParseSQL(sql string) (*querytree.Select, error) {
	if len(sql) > querymodel.MaxSQLBytes {
		return nil, compileError(querymodel.ErrorLimit, querymodel.Span{}, "SQL 字节数超限")
	}
	if !utf8.ValidString(sql) {
		return nil, compileError(querymodel.ErrorSyntax, querymodel.Span{}, "SQL 必须为有效 UTF-8")
	}
	positions := bytePositions(sql)
	listener := &syntaxErrors{DefaultErrorListener: antlr.NewDefaultErrorListener(), positions: positions}
	lexer := generated.NewSqlBaseLexer(antlr.NewInputStream(sql))
	lexer.RemoveErrorListeners()
	lexer.AddErrorListener(listener)
	source := &limitedLexer{SqlBaseLexer: lexer, errors: listener}
	tokens := antlr.NewCommonTokenStream(source, antlr.TokenDefaultChannel)
	// 先有界读取 token；括号、CASE 和前缀递归在进入 parser 之前受限。
	depth, prefix := 0, 0
	for {
		token := tokens.LT(1)
		if listener.err != nil {
			return nil, listener.err
		}
		if token.GetTokenType() == antlr.TokenEOF {
			break
		}
		switch token.GetTokenType() {
		case generated.SqlBaseLexerUNSUPPORTED, generated.SqlBaseLexerFORBIDDEN_COMMENT:
			return nil, compileError(querymodel.ErrorUnsupported, tokenSpan(token, positions), "语法未开放")
		case generated.SqlBaseLexerLPAREN, generated.SqlBaseLexerCASE:
			depth++
		case generated.SqlBaseLexerRPAREN, generated.SqlBaseLexerEND:
			depth--
			if depth < 0 {
				return nil, compileError(querymodel.ErrorSyntax, tokenSpan(token, positions), "闭合符号不匹配")
			}
		}
		switch token.GetTokenType() {
		case generated.SqlBaseLexerNOT, generated.SqlBaseLexerPLUS, generated.SqlBaseLexerMINUS:
			prefix++
		default:
			prefix = 0
		}
		if depth > querymodel.MaxDepth || prefix > querymodel.MaxDepth {
			return nil, compileError(querymodel.ErrorLimit, tokenSpan(token, positions), "SQL 嵌套深度超限")
		}
		if token.GetTokenType() == generated.SqlBaseLexerRESOURCE {
			id := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(token.GetText(), "{{"), "}}"))
			if len(id) > 40 {
				return nil, compileError(querymodel.ErrorSyntax, tokenSpan(token, positions), "资源 ID 长度超限")
			}
		}
		tokens.Consume()
	}
	tokens.Seek(0)
	parser := generated.NewSqlBaseParser(tokens)
	parser.RemoveErrorListeners()
	parser.AddErrorListener(listener)
	parsed := parser.Statement()
	if listener.err != nil {
		return nil, listener.err
	}
	b := builder{positions: positions, depths: make(map[*querytree.Expr]int)}
	query := b.selectQuery(parsed.SelectQuery())
	if b.err != nil {
		return nil, b.err
	}
	return query, nil
}

func bytePositions(sql string) []int {
	result := make([]int, 0, utf8.RuneCountInString(sql)+1)
	for offset := range sql {
		result = append(result, offset)
	}
	return append(result, len(sql))
}

func tokenSpan(token antlr.Token, positions []int) querymodel.Span {
	start, end := token.GetStart(), token.GetStop()+1
	if start < 0 || start >= len(positions) {
		start = len(positions) - 1
	}
	if end < start || end >= len(positions) {
		end = start
	}
	return querymodel.Span{Start: positions[start], End: positions[end]}
}

func compileError(kind querymodel.ErrorKind, span querymodel.Span, message string) error {
	return &querymodel.CompileError{Kind: kind, Origin: querymodel.Origin{Span: span}, Message: message}
}

type syntaxErrors struct {
	*antlr.DefaultErrorListener
	positions []int
	err       error
}

func (l *syntaxErrors) SyntaxError(recognizer antlr.Recognizer, offending interface{}, _, _ int, _ string, _ antlr.RecognitionException) {
	if l.err != nil {
		return
	}
	span := querymodel.Span{}
	if token, ok := offending.(antlr.Token); ok {
		span = tokenSpan(token, l.positions)
	} else if lexer, ok := recognizer.(antlr.Lexer); ok {
		index := lexer.GetInputStream().Index()
		if index >= 0 && index+1 < len(l.positions) {
			span = querymodel.Span{Start: l.positions[index], End: l.positions[index+1]}
		}
	}
	l.err = compileError(querymodel.ErrorSyntax, span, "SQL 语法错误")
}

// limitedLexer 在隐藏通道 token 进入缓冲区之前执行预算检查。
type limitedLexer struct {
	*generated.SqlBaseLexer
	count  int
	errors *syntaxErrors
}

type stoppedToken struct{ antlr.Token }

func (stoppedToken) GetTokenType() int { return antlr.TokenEOF }
func (stoppedToken) GetChannel() int   { return antlr.TokenDefaultChannel }

func (l *limitedLexer) NextToken() antlr.Token {
	token := l.SqlBaseLexer.NextToken()
	if token.GetTokenType() != antlr.TokenEOF {
		l.count++
	}
	if l.count > querymodel.MaxTokens && l.errors.err == nil {
		l.errors.err = compileError(querymodel.ErrorLimit, tokenSpan(token, l.errors.positions), "SQL token 数超限")
	}
	if l.errors.err != nil {
		return stoppedToken{token}
	}
	return token
}
