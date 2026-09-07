// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package maskrule executes the published DataProperty masking contract at the
// ontology-query result assembly boundary.
package maskrule

import (
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	KindFixed           = "fixed"
	KindPartial         = "partial"
	KindEmail           = "email"
	KindRound           = "round"
	KindDateGranularity = "date_granularity"
)

type Rule struct {
	Kind string `json:"kind" mapstructure:"kind"`

	Replacement    string `json:"replacement,omitempty" mapstructure:"replacement,omitempty"`
	KeepStart      *int   `json:"keep_start,omitempty" mapstructure:"keep_start,omitempty"`
	KeepEnd        *int   `json:"keep_end,omitempty" mapstructure:"keep_end,omitempty"`
	LocalKeepStart *int   `json:"local_keep_start,omitempty" mapstructure:"local_keep_start,omitempty"`
	PreserveDomain *bool  `json:"preserve_domain,omitempty" mapstructure:"preserve_domain,omitempty"`

	Step        *float64 `json:"step,omitempty" mapstructure:"step,omitempty"`
	Granularity string   `json:"granularity,omitempty" mapstructure:"granularity,omitempty"`
}

func Validate(propertyType string, rule *Rule) error {
	if rule == nil {
		return fmt.Errorf("mask rule is missing")
	}
	switch rule.Kind {
	case KindFixed:
		if !isStringType(propertyType) || !validReplacement(rule.Replacement) {
			return fmt.Errorf("fixed mask rule is invalid for property type")
		}
		return rejectUnexpected(rule, true, false, false, false, false, false, false)
	case KindPartial:
		if !isStringType(propertyType) || !validReplacement(rule.Replacement) ||
			!validKeep(rule.KeepStart) || !validKeep(rule.KeepEnd) {
			return fmt.Errorf("partial mask rule is invalid for property type")
		}
		return rejectUnexpected(rule, true, true, true, false, false, false, false)
	case KindEmail:
		if !isStringType(propertyType) || !validReplacement(rule.Replacement) ||
			!validKeep(rule.LocalKeepStart) || rule.PreserveDomain == nil {
			return fmt.Errorf("email mask rule is invalid for property type")
		}
		return rejectUnexpected(rule, true, false, false, true, true, false, false)
	case KindRound:
		if !isNumberType(propertyType) || rule.Step == nil || *rule.Step <= 0 ||
			math.IsInf(*rule.Step, 0) || math.IsNaN(*rule.Step) {
			return fmt.Errorf("round mask rule is invalid for property type")
		}
		return rejectUnexpected(rule, false, false, false, false, false, true, false)
	case KindDateGranularity:
		if !validGranularity(propertyType, rule.Granularity) {
			return fmt.Errorf("date granularity mask rule is invalid for property type")
		}
		return rejectUnexpected(rule, false, false, false, false, false, false, true)
	default:
		return fmt.Errorf("mask rule kind is unsupported")
	}
}

func Apply(propertyType string, rule *Rule, value any) (any, error) {
	if value == nil {
		return nil, nil
	}
	if err := Validate(propertyType, rule); err != nil {
		return nil, err
	}
	switch rule.Kind {
	case KindFixed:
		if _, ok := value.(string); !ok {
			return nil, fmt.Errorf("fixed mask input is not a string")
		}
		return rule.Replacement, nil
	case KindPartial:
		input, ok := value.(string)
		if !ok {
			return nil, fmt.Errorf("partial mask input is not a string")
		}
		return partial(input, *rule.KeepStart, *rule.KeepEnd, rule.Replacement), nil
	case KindEmail:
		input, ok := value.(string)
		if !ok {
			return nil, fmt.Errorf("email mask input is not a string")
		}
		return email(input, *rule.LocalKeepStart, *rule.PreserveDomain, rule.Replacement), nil
	case KindRound:
		return round(value, *rule.Step)
	case KindDateGranularity:
		input, ok := value.(string)
		if !ok {
			return nil, fmt.Errorf("date mask input is not a string")
		}
		return truncateDate(input, rule.Granularity)
	default:
		return nil, fmt.Errorf("mask rule kind is unsupported")
	}
}

func partial(input string, keepStart, keepEnd int, replacement string) string {
	runes := []rune(input)
	if len(runes) == 0 {
		return replacement
	}
	if keepStart > len(runes)-1 {
		keepStart = len(runes) - 1
	}
	remaining := len(runes) - keepStart
	if keepEnd > remaining-1 {
		keepEnd = remaining - 1
	}
	maskedCount := len(runes) - keepStart - keepEnd
	return string(runes[:keepStart]) + strings.Repeat(replacement, maskedCount) + string(runes[len(runes)-keepEnd:])
}

func email(input string, keepStart int, preserveDomain bool, replacement string) string {
	at := strings.LastIndexByte(input, '@')
	if at <= 0 || at == len(input)-1 || strings.Contains(input[:at], "@") || strings.ContainsAny(input, " \t\r\n") {
		return replacement
	}
	local := []rune(input[:at])
	if keepStart > len(local)-1 {
		keepStart = len(local) - 1
	}
	masked := string(local[:keepStart]) + strings.Repeat(replacement, len(local)-keepStart)
	if preserveDomain {
		masked += input[at:]
	}
	return masked
}

func round(value any, step float64) (json.Number, error) {
	valueText, err := numberText(value)
	if err != nil {
		return "", err
	}
	valueRat, ok := new(big.Rat).SetString(valueText)
	if !ok {
		return "", fmt.Errorf("round mask input is invalid")
	}
	stepText := strconv.FormatFloat(step, 'f', -1, 64)
	stepRat, ok := new(big.Rat).SetString(stepText)
	if !ok || stepRat.Sign() <= 0 {
		return "", fmt.Errorf("round mask step is invalid")
	}
	quotient := new(big.Rat).Quo(valueRat, stepRat)
	floor := new(big.Int).Quo(quotient.Num(), quotient.Denom())
	if quotient.Sign() < 0 && new(big.Int).Rem(quotient.Num(), quotient.Denom()).Sign() != 0 {
		floor.Sub(floor, big.NewInt(1))
	}
	masked := new(big.Rat).Mul(new(big.Rat).SetInt(floor), stepRat)
	scale := decimalScale(stepText)
	text := masked.FloatString(scale)
	if strings.Contains(text, ".") {
		text = strings.TrimRight(strings.TrimRight(text, "0"), ".")
	}
	if text == "-0" || text == "" {
		text = "0"
	}
	return json.Number(text), nil
}

func truncateDate(input, granularity string) (string, error) {
	layouts := []string{
		time.RFC3339Nano,
		"2006-01-02 15:04:05.999999999Z07:00",
		"2006-01-02 15:04:05Z07:00",
		"2006-01-02T15:04:05.999999999",
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05.999999999",
		"2006-01-02 15:04:05",
		"2006-01-02",
		"15:04:05.999999999Z07:00",
		"15:04:05Z07:00",
		"15:04:05.999999999",
		"15:04:05",
	}
	for _, layout := range layouts {
		parsed, err := time.Parse(layout, input)
		if err != nil {
			continue
		}
		year, month, day := parsed.Date()
		hour := parsed.Hour()
		switch granularity {
		case "year":
			month, day, hour = time.January, 1, 0
		case "month":
			day, hour = 1, 0
		case "day":
			hour = 0
		case "hour":
		default:
			return "", fmt.Errorf("date mask granularity is invalid")
		}
		masked := time.Date(year, month, day, hour, 0, 0, 0, parsed.Location())
		return masked.Format(layout), nil
	}
	return "", fmt.Errorf("date mask input is invalid")
}

func numberText(value any) (string, error) {
	switch number := value.(type) {
	case json.Number:
		return number.String(), nil
	case float64:
		if math.IsInf(number, 0) || math.IsNaN(number) {
			return "", fmt.Errorf("round mask input is invalid")
		}
		return strconv.FormatFloat(number, 'g', -1, 64), nil
	case float32:
		return strconv.FormatFloat(float64(number), 'g', -1, 32), nil
	case int:
		return strconv.FormatInt(int64(number), 10), nil
	case int8:
		return strconv.FormatInt(int64(number), 10), nil
	case int16:
		return strconv.FormatInt(int64(number), 10), nil
	case int32:
		return strconv.FormatInt(int64(number), 10), nil
	case int64:
		return strconv.FormatInt(number, 10), nil
	case uint:
		return strconv.FormatUint(uint64(number), 10), nil
	case uint8:
		return strconv.FormatUint(uint64(number), 10), nil
	case uint16:
		return strconv.FormatUint(uint64(number), 10), nil
	case uint32:
		return strconv.FormatUint(uint64(number), 10), nil
	case uint64:
		return strconv.FormatUint(number, 10), nil
	default:
		return "", fmt.Errorf("round mask input is not a number")
	}
}

func decimalScale(value string) int {
	if dot := strings.IndexByte(value, '.'); dot >= 0 {
		return len(value) - dot - 1
	}
	return 0
}

func isStringType(value string) bool {
	return value == "string" || value == "text" || value == "keyword"
}

func isNumberType(value string) bool {
	switch value {
	case "integer", "unsigned integer", "float", "decimal":
		return true
	default:
		return false
	}
}

func validReplacement(value string) bool {
	if !utf8.ValidString(value) {
		return false
	}
	count := utf8.RuneCountInString(value)
	if count < 1 || count > 8 {
		return false
	}
	for _, char := range value {
		if !unicode.IsPrint(char) {
			return false
		}
	}
	return true
}

func validKeep(value *int) bool {
	return value != nil && *value >= 0 && *value <= 64
}

func validGranularity(propertyType, granularity string) bool {
	allowed := map[string]map[string]bool{
		"date":      {"year": true, "month": true},
		"time":      {"hour": true},
		"datetime":  {"year": true, "month": true, "day": true, "hour": true},
		"timestamp": {"year": true, "month": true, "day": true, "hour": true},
	}
	return allowed[propertyType][granularity]
}

func rejectUnexpected(rule *Rule, replacement, keepStart, keepEnd, localKeepStart,
	preserveDomain, step, granularity bool) error {
	if !replacement && rule.Replacement != "" {
		return fmt.Errorf("replacement is not valid for %s", rule.Kind)
	}
	if !keepStart && rule.KeepStart != nil {
		return fmt.Errorf("keep_start is not valid for %s", rule.Kind)
	}
	if !keepEnd && rule.KeepEnd != nil {
		return fmt.Errorf("keep_end is not valid for %s", rule.Kind)
	}
	if !localKeepStart && rule.LocalKeepStart != nil {
		return fmt.Errorf("local_keep_start is not valid for %s", rule.Kind)
	}
	if !preserveDomain && rule.PreserveDomain != nil {
		return fmt.Errorf("preserve_domain is not valid for %s", rule.Kind)
	}
	if !step && rule.Step != nil {
		return fmt.Errorf("step is not valid for %s", rule.Kind)
	}
	if !granularity && rule.Granularity != "" {
		return fmt.Errorf("granularity is not valid for %s", rule.Kind)
	}
	return nil
}
