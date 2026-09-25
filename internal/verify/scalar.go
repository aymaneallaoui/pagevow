package verify

import (
	"fmt"
	"math"
	"math/big"
	"regexp"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

// ScalarKind is the Python type a YAML scalar resolves to under PyYAML's YAML 1.1 rules.
type ScalarKind int

// Scalar kinds.
const (
	KindString ScalarKind = iota
	KindBool
	KindInt
	KindFloat
	KindNull
)

// Scalar is a YAML value with Python's str() and repr() renderings, so comparisons match the Python verifiers.
type Scalar struct {
	kind ScalarKind
	text string
}

// NewString returns a string scalar.
func NewString(s string) Scalar { return Scalar{kind: KindString, text: s} }

// NewBool returns a boolean scalar.
func NewBool(b bool) Scalar {
	if b {
		return Scalar{kind: KindBool, text: "True"}
	}
	return Scalar{kind: KindBool, text: "False"}
}

// NewInt returns an integer scalar.
func NewInt(n int64) Scalar { return Scalar{kind: KindInt, text: strconv.FormatInt(n, 10)} }

// Kind returns the scalar's kind.
func (s Scalar) Kind() ScalarKind { return s.kind }

// String is Python's str(value).
func (s Scalar) String() string { return s.text }

// Repr is Python's repr(value).
func (s Scalar) Repr() string {
	if s.kind == KindString {
		return reprString(s.text)
	}
	return s.text
}

// Bool returns the boolean value and whether the scalar is a boolean.
func (s Scalar) Bool() (value, ok bool) {
	return s.text == "True", s.kind == KindBool
}

var (
	yamlBool  = regexp.MustCompile(`^(?:yes|Yes|YES|no|No|NO|true|True|TRUE|false|False|FALSE|on|On|ON|off|Off|OFF)$`)
	yamlNull  = regexp.MustCompile(`^(?:~|null|Null|NULL|)$`)
	yamlFloat = regexp.MustCompile(`^(?:[-+]?(?:[0-9][0-9_]*)\.[0-9_]*(?:[eE][-+][0-9]+)?` +
		`|\.[0-9][0-9_]*(?:[eE][-+][0-9]+)?` +
		`|[-+]?[0-9][0-9_]*(?::[0-5]?[0-9])+\.[0-9_]*` +
		`|[-+]?\.(?:inf|Inf|INF)` +
		`|\.(?:nan|NaN|NAN))$`)
	yamlInt = regexp.MustCompile(`^(?:[-+]?0b[0-1_]+` +
		`|[-+]?0[0-7_]+` +
		`|[-+]?(?:0|[1-9][0-9_]*)` +
		`|[-+]?0x[0-9a-fA-F_]+` +
		`|[-+]?[1-9][0-9_]*(?::[0-5]?[0-9])+)$`)
)

const quotedStyles = yaml.DoubleQuotedStyle | yaml.SingleQuotedStyle | yaml.LiteralStyle | yaml.FoldedStyle

func scalarOf(node *yaml.Node) (Scalar, error) {
	for node.Kind == yaml.AliasNode && node.Alias != nil {
		node = node.Alias
	}
	if node.Kind != yaml.ScalarNode {
		return Scalar{}, fmt.Errorf("must be a single value")
	}
	if node.Style&quotedStyles != 0 {
		return NewString(node.Value), nil
	}
	return resolvePlain(node.Value), nil
}

func resolvePlain(value string) Scalar {
	switch {
	case yamlBool.MatchString(value):
		switch value {
		case "yes", "Yes", "YES", "true", "True", "TRUE", "on", "On", "ON":
			return NewBool(true)
		}
		return NewBool(false)
	case yamlFloat.MatchString(value):
		return Scalar{kind: KindFloat, text: reprFloat(pyFloat(value))}
	case yamlInt.MatchString(value):
		return Scalar{kind: KindInt, text: pyInt(value)}
	case yamlNull.MatchString(value):
		return Scalar{kind: KindNull, text: "None"}
	}
	return NewString(value)
}

func splitSign(value string) (sign int, rest string) {
	switch {
	case strings.HasPrefix(value, "-"):
		return -1, value[1:]
	case strings.HasPrefix(value, "+"):
		return 1, value[1:]
	}
	return 1, value
}

func pyInt(raw string) string {
	sign, value := splitSign(strings.ReplaceAll(raw, "_", ""))
	result := new(big.Int)
	switch {
	case value == "0":
	case strings.HasPrefix(value, "0b"):
		result.SetString(value[2:], 2)
	case strings.HasPrefix(value, "0x"):
		result.SetString(value[2:], 16)
	case value[0] == '0':
		result.SetString(value, 8)
	case strings.Contains(value, ":"):
		parts := strings.Split(value, ":")
		unit := big.NewInt(1)
		for i := len(parts) - 1; i >= 0; i-- {
			digit, _ := new(big.Int).SetString(parts[i], 10)
			result.Add(result, digit.Mul(digit, unit))
			unit.Mul(unit, big.NewInt(60))
		}
	default:
		result.SetString(value, 10)
	}
	if sign < 0 {
		result.Neg(result)
	}
	return result.String()
}

func pyFloat(raw string) float64 {
	sign, value := splitSign(strings.ToLower(strings.ReplaceAll(raw, "_", "")))
	switch value {
	case ".inf":
		return math.Inf(sign)
	case ".nan":
		return math.NaN()
	}
	total := 0.0
	if strings.Contains(value, ":") {
		parts := strings.Split(value, ":")
		unit := 1.0
		for i := len(parts) - 1; i >= 0; i-- {
			digit, _ := strconv.ParseFloat(parts[i], 64)
			total += digit * unit
			unit *= 60
		}
	} else {
		total, _ = strconv.ParseFloat(value, 64)
	}
	return float64(sign) * total
}
