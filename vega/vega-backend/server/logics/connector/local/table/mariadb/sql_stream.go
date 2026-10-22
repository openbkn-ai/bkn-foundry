// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package mariadb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"strconv"
	"unicode/utf8"

	qm "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces/querymodel"
)

const sqlBatchRows = 128

// 流按列位置解码，不依赖源返回的列名。正常 EOF 与失败/提前关闭严格区分。
type sqlResultStream struct {
	rows       *sql.Rows
	schema     qm.Schema
	offset     int64
	limit      *int64
	read       int64
	done       bool
	closed     bool
	failure    error
	completion qm.SQLReadCompletion
}

func (s *sqlResultStream) Schema() qm.Schema {
	return cloneSQLMetadata(qm.SQLPreparedMetadata{OutputSchema: s.schema}).OutputSchema
}
func (s *sqlResultStream) Completion() qm.SQLReadCompletion {
	completion := s.completion
	if completion.NextOffset != nil {
		completion.NextOffset = new(*completion.NextOffset)
	}
	return completion
}
func (s *sqlResultStream) Next(ctx context.Context) (qm.Batch, error) {
	if s.failure != nil {
		return qm.Batch{}, s.failure
	}
	if err := ctx.Err(); err != nil {
		return s.fail(err)
	}
	if s.done {
		return qm.Batch{}, io.EOF
	}
	if s.closed {
		return qm.Batch{}, errors.New("SQL 结果流已关闭")
	}
	batch := qm.Batch{}
	for len(batch.Rows) < sqlBatchRows {
		if err := ctx.Err(); err != nil {
			return s.fail(err)
		}
		if s.limit != nil && s.read == *s.limit {
			more := s.rows.Next()
			if err := s.rows.Err(); err != nil {
				return s.fail(mariadbQueryError(err))
			}
			if err := s.finish(more); err != nil {
				return s.fail(err)
			}
			break
		}
		if !s.rows.Next() {
			if err := s.rows.Err(); err != nil {
				return s.fail(mariadbQueryError(err))
			}
			if err := s.finish(false); err != nil {
				return s.fail(err)
			}
			break
		}
		values := make([]any, len(s.schema))
		dest := make([]any, len(values))
		for i := range values {
			dest[i] = &values[i]
		}
		if err := s.rows.Scan(dest...); err != nil {
			return s.fail(fmt.Errorf("SQL 结果读取失败: %w", err))
		}
		row := make(qm.Row, len(values))
		for i, raw := range values {
			cell, err := decodeSQLCell(raw, s.schema[i])
			if err != nil {
				return s.fail(err)
			}
			row[i] = cell
		}
		s.read++
		batch.Rows = append(batch.Rows, row)
	}
	if len(batch.Rows) == 0 {
		return qm.Batch{}, io.EOF
	}
	return batch, nil
}
func (s *sqlResultStream) finish(more bool) error {
	if err := s.rows.Close(); err != nil {
		return mariadbQueryError(err)
	}
	s.done = true
	s.completion = qm.SQLReadCompletion{Kind: qm.CompletionRelationEnd, RowsRead: s.read}
	if more {
		s.completion.Kind = qm.CompletionWindowEnd
		s.completion.NextOffset = new(s.offset + s.read)
	}
	return nil
}
func (s *sqlResultStream) fail(err error) (qm.Batch, error) {
	s.failure = err
	s.completion = qm.SQLReadCompletion{}
	_ = s.Close()
	return qm.Batch{}, err
}
func (s *sqlResultStream) Close() error {
	if s.closed {
		return nil
	}
	s.closed = true
	if s.rows != nil {
		return mariadbQueryError(s.rows.Close())
	}
	return nil
}
func decodeSQLCell(raw any, column qm.OutputColumn) (qm.ResultCell, error) {
	if raw == nil {
		if !column.Nullable && column.Type.Kind != qm.TypeNull {
			return qm.ResultCell{}, errors.New("源结果出现非空列 NULL")
		}
		return qm.ResultCell{State: qm.CellValue, Value: &qm.Value{Type: column.Type, Data: qm.NullValue{}}}, nil
	}
	var text string
	switch v := raw.(type) {
	case []byte:
		text = string(v)
	case string:
		text = v
	case int64:
		text = strconv.FormatInt(v, 10)
	case uint64:
		text = strconv.FormatUint(v, 10)
	case bool:
		if v {
			text = "1"
		} else {
			text = "0"
		}
	default:
		return qm.ResultCell{}, errors.New("源结果表示与解码类型不一致")
	}
	value := qm.Value{Type: column.Type}
	switch column.Type.Kind {
	case qm.TypeInteger:
		if column.Type.Unsigned {
			if _, err := strconv.ParseUint(text, 10, 64); err != nil {
				return qm.ResultCell{}, errors.New("源结果整数超出精确值域")
			}
		} else if _, err := strconv.ParseInt(text, 10, 64); err != nil {
			return qm.ResultCell{}, errors.New("源结果整数超出精确值域")
		}
		value.Data = qm.IntegerValue(text)
	case qm.TypeBoolean:
		if text != "0" && text != "1" {
			return qm.ResultCell{}, errors.New("源结果不是布尔值")
		}
		value.Data = qm.BooleanValue(text == "1")
	case qm.TypeString, qm.TypeText:
		if _, ok := raw.(string); !ok {
			if _, ok := raw.([]byte); !ok {
				return qm.ResultCell{}, errors.New("源结果不是字符串")
			}
		}
		if !utf8.ValidString(text) {
			return qm.ResultCell{}, errors.New("源结果字符串不是有效 UTF-8")
		}
		value.Data = qm.StringValue(text)
	default:
		return qm.ResultCell{}, errors.New("源结果类型尚无精确解码支持")
	}
	return qm.ResultCell{State: qm.CellValue, Value: &value}, nil
}
