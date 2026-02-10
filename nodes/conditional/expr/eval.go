package expr

import (
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"sync"
)

// Eval evaluates a parsed expression against a variable map.
// The vars map is the top-level namespace (e.g. {"input": {...}}).
func Eval(e Expr, vars map[string]any) (any, error) {
	ev := &evaluator{vars: vars}
	return ev.eval(e)
}

type evaluator struct {
	vars map[string]any
}

func (ev *evaluator) eval(e Expr) (any, error) {
	switch n := e.(type) {
	case *LiteralExpr:
		return n.Value, nil

	case *IdentExpr:
		val, ok := ev.vars[n.Name]
		if !ok {
			return nil, nil // undefined variables resolve to nil
		}
		return val, nil

	case *MemberExpr:
		obj, err := ev.eval(n.Object)
		if err != nil {
			return nil, err
		}
		return accessMember(obj, n.Property)

	case *IndexExpr:
		obj, err := ev.eval(n.Object)
		if err != nil {
			return nil, err
		}
		idx, err := ev.eval(n.Index)
		if err != nil {
			return nil, err
		}
		return accessIndex(obj, idx)

	case *ArrayLiteral:
		result := make([]any, len(n.Elements))
		for i, elem := range n.Elements {
			val, err := ev.eval(elem)
			if err != nil {
				return nil, err
			}
			result[i] = val
		}
		return result, nil

	case *UnaryExpr:
		return ev.evalUnary(n)

	case *BinaryExpr:
		return ev.evalBinary(n)

	default:
		return nil, fmt.Errorf("unknown expression type %T", e)
	}
}

func (ev *evaluator) evalUnary(n *UnaryExpr) (any, error) {
	val, err := ev.eval(n.Operand)
	if err != nil {
		return nil, err
	}
	switch n.Op {
	case TokenNot:
		return !IsTruthy(val), nil
	default:
		return nil, fmt.Errorf("unknown unary operator %s", n.Op)
	}
}

func (ev *evaluator) evalBinary(n *BinaryExpr) (any, error) {
	// Short-circuit for logical operators
	switch n.Op {
	case TokenAnd:
		left, err := ev.eval(n.Left)
		if err != nil {
			return nil, err
		}
		if !IsTruthy(left) {
			return false, nil
		}
		right, err := ev.eval(n.Right)
		if err != nil {
			return nil, err
		}
		return IsTruthy(right), nil

	case TokenOr:
		left, err := ev.eval(n.Left)
		if err != nil {
			return nil, err
		}
		if IsTruthy(left) {
			return true, nil
		}
		right, err := ev.eval(n.Right)
		if err != nil {
			return nil, err
		}
		return IsTruthy(right), nil

	case TokenNullCoal:
		left, err := ev.eval(n.Left)
		if err != nil {
			return nil, err
		}
		if left != nil {
			return left, nil
		}
		return ev.eval(n.Right)
	}

	// Non-short-circuit: evaluate both sides
	left, err := ev.eval(n.Left)
	if err != nil {
		return nil, err
	}
	right, err := ev.eval(n.Right)
	if err != nil {
		return nil, err
	}

	switch n.Op {
	case TokenEq:
		return isEqual(left, right), nil
	case TokenNeq:
		return !isEqual(left, right), nil
	case TokenGt:
		cmp, ok := compareNumeric(left, right)
		if !ok {
			return false, nil
		}
		return cmp > 0, nil
	case TokenGte:
		cmp, ok := compareNumeric(left, right)
		if !ok {
			return false, nil
		}
		return cmp >= 0, nil
	case TokenLt:
		cmp, ok := compareNumeric(left, right)
		if !ok {
			return false, nil
		}
		return cmp < 0, nil
	case TokenLte:
		cmp, ok := compareNumeric(left, right)
		if !ok {
			return false, nil
		}
		return cmp <= 0, nil
	case TokenIn:
		return checkIn(left, right), nil
	case TokenHas:
		return checkHas(left, right), nil
	case TokenContains:
		return checkContains(left, right), nil
	case TokenStartsWith:
		return checkStartsWith(left, right), nil
	case TokenEndsWith:
		return checkEndsWith(left, right), nil
	case TokenMatches:
		return checkMatches(left, right)
	default:
		return nil, fmt.Errorf("unknown binary operator %s", n.Op)
	}
}

// IsTruthy implements the spec's boolean coercion rules.
// Falsy: 0, "", null, false, empty array, empty object.
func IsTruthy(val any) bool {
	if val == nil {
		return false
	}
	switch v := val.(type) {
	case bool:
		return v
	case float64:
		return v != 0
	case int:
		return v != 0
	case string:
		return v != ""
	default:
		rv := reflect.ValueOf(val)
		switch rv.Kind() {
		case reflect.Slice, reflect.Array:
			return rv.Len() > 0
		case reflect.Map:
			return rv.Len() > 0
		}
		return true
	}
}

// isEqual follows reflect.DeepEqual semantics with numeric normalization.
func isEqual(a, b any) bool {
	// Normalize both to float64 if numeric
	af, aOK := toFloat64(a)
	bf, bOK := toFloat64(b)
	if aOK && bOK {
		return af == bf
	}
	return reflect.DeepEqual(a, b)
}

// compareNumeric compares two values numerically.
// Returns (comparison, ok). ok is false if values aren't comparable.
func compareNumeric(a, b any) (int, bool) {
	af, aOK := toFloat64(a)
	bf, bOK := toFloat64(b)
	if !aOK || !bOK {
		// Try string comparison
		as, aStr := a.(string)
		bs, bStr := b.(string)
		if aStr && bStr {
			return strings.Compare(as, bs), true
		}
		return 0, false
	}
	if af < bf {
		return -1, true
	}
	if af > bf {
		return 1, true
	}
	return 0, true
}

func toFloat64(val any) (float64, bool) {
	switch v := val.(type) {
	case float64:
		return v, true
	case float32:
		return float64(v), true
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	case int32:
		return float64(v), true
	}
	return 0, false
}

// accessMember accesses a property on an object.
func accessMember(obj any, prop string) (any, error) {
	if obj == nil {
		return nil, nil
	}

	// Special built-in: .length
	if prop == "length" {
		return getLength(obj)
	}

	switch v := obj.(type) {
	case map[string]any:
		return v[prop], nil
	default:
		// Try reflection for other map types
		rv := reflect.ValueOf(obj)
		if rv.Kind() == reflect.Map {
			key := reflect.ValueOf(prop)
			val := rv.MapIndex(key)
			if val.IsValid() {
				return val.Interface(), nil
			}
			return nil, nil
		}
		return nil, nil
	}
}

func getLength(obj any) (any, error) {
	switch v := obj.(type) {
	case string:
		return float64(len(v)), nil
	case []any:
		return float64(len(v)), nil
	default:
		rv := reflect.ValueOf(obj)
		switch rv.Kind() {
		case reflect.Slice, reflect.Array, reflect.Map, reflect.String:
			return float64(rv.Len()), nil
		}
		return nil, nil
	}
}

// accessIndex accesses an element by index.
func accessIndex(obj any, idx any) (any, error) {
	if obj == nil {
		return nil, nil
	}

	// String index for maps
	if key, ok := idx.(string); ok {
		return accessMember(obj, key)
	}

	// Numeric index for arrays
	i, ok := toFloat64(idx)
	if !ok {
		return nil, fmt.Errorf("invalid index type %T", idx)
	}
	index := int(i)

	switch v := obj.(type) {
	case []any:
		if index < 0 || index >= len(v) {
			return nil, nil
		}
		return v[index], nil
	default:
		rv := reflect.ValueOf(obj)
		if rv.Kind() == reflect.Slice || rv.Kind() == reflect.Array {
			if index < 0 || index >= rv.Len() {
				return nil, nil
			}
			return rv.Index(index).Interface(), nil
		}
		return nil, nil
	}
}

// checkIn checks if left value exists in right array.
func checkIn(left, right any) bool {
	if right == nil {
		return false
	}
	rv := reflect.ValueOf(right)
	if rv.Kind() != reflect.Slice && rv.Kind() != reflect.Array {
		return false
	}
	for i := 0; i < rv.Len(); i++ {
		if isEqual(left, rv.Index(i).Interface()) {
			return true
		}
	}
	return false
}

// checkHas checks if left object has right key.
func checkHas(left, right any) bool {
	if left == nil {
		return false
	}
	key, ok := right.(string)
	if !ok {
		return false
	}
	rv := reflect.ValueOf(left)
	if rv.Kind() != reflect.Map {
		return false
	}
	return rv.MapIndex(reflect.ValueOf(key)).IsValid()
}

// checkContains checks if left string contains right string.
func checkContains(left, right any) bool {
	ls, lok := left.(string)
	rs, rok := right.(string)
	if !lok || !rok {
		return false
	}
	return strings.Contains(ls, rs)
}

// checkStartsWith checks if left string starts with right string.
func checkStartsWith(left, right any) bool {
	ls, lok := left.(string)
	rs, rok := right.(string)
	if !lok || !rok {
		return false
	}
	return strings.HasPrefix(ls, rs)
}

// checkEndsWith checks if left string ends with right string.
func checkEndsWith(left, right any) bool {
	ls, lok := left.(string)
	rs, rok := right.(string)
	if !lok || !rok {
		return false
	}
	return strings.HasSuffix(ls, rs)
}

// regexCache caches compiled regexes for matches operations.
var regexCache sync.Map

func checkMatches(left, right any) (bool, error) {
	ls, lok := left.(string)
	rs, rok := right.(string)
	if !lok || !rok {
		return false, nil
	}

	// Check cache
	if cached, ok := regexCache.Load(rs); ok {
		re := cached.(*regexp.Regexp)
		return re.MatchString(ls), nil
	}

	re, err := regexp.Compile(rs)
	if err != nil {
		return false, fmt.Errorf("invalid regex %q: %w", rs, err)
	}
	regexCache.Store(rs, re)
	return re.MatchString(ls), nil
}
