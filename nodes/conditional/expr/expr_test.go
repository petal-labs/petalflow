package expr

import (
	"fmt"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

// evalExpr is a parse-then-eval integration helper.
func evalExpr(t *testing.T, input string, vars map[string]any) any {
	t.Helper()
	ast, err := Parse(input)
	if err != nil {
		t.Fatalf("Parse(%q) unexpected error: %v", input, err)
	}
	result, err := Eval(ast, vars)
	if err != nil {
		t.Fatalf("Eval(%q) unexpected error: %v", input, err)
	}
	return result
}

// evalExprErr expects evaluation to succeed after parsing; returns both value and error.
func evalExprErr(t *testing.T, input string, vars map[string]any) (any, error) {
	t.Helper()
	ast, err := Parse(input)
	if err != nil {
		t.Fatalf("Parse(%q) unexpected error: %v", input, err)
	}
	return Eval(ast, vars)
}

// assertBool asserts a value is a bool with the expected value.
func assertBool(t *testing.T, label string, got any, want bool) {
	t.Helper()
	b, ok := got.(bool)
	if !ok {
		t.Fatalf("%s: expected bool, got %T (%v)", label, got, got)
	}
	if b != want {
		t.Fatalf("%s: got %v, want %v", label, b, want)
	}
}

// assertFloat64 asserts a value is float64 with the expected value.
func assertFloat64(t *testing.T, label string, got any, want float64) {
	t.Helper()
	f, ok := got.(float64)
	if !ok {
		t.Fatalf("%s: expected float64, got %T (%v)", label, got, got)
	}
	if f != want {
		t.Fatalf("%s: got %v, want %v", label, f, want)
	}
}

// assertString asserts a value is a string with the expected value.
func assertString(t *testing.T, label string, got any, want string) {
	t.Helper()
	s, ok := got.(string)
	if !ok {
		t.Fatalf("%s: expected string, got %T (%v)", label, got, got)
	}
	if s != want {
		t.Fatalf("%s: got %q, want %q", label, s, want)
	}
}

// assertNil asserts a value is nil.
func assertNil(t *testing.T, label string, got any) {
	t.Helper()
	if got != nil {
		t.Fatalf("%s: expected nil, got %T (%v)", label, got, got)
	}
}

// ---------------------------------------------------------------------------
// 1. Lexer tests
// ---------------------------------------------------------------------------

func TestLex_Operators(t *testing.T) {
	tests := []struct {
		input string
		kinds []TokenKind
	}{
		{"==", []TokenKind{TokenEq, TokenEOF}},
		{"!=", []TokenKind{TokenNeq, TokenEOF}},
		{">", []TokenKind{TokenGt, TokenEOF}},
		{">=", []TokenKind{TokenGte, TokenEOF}},
		{"<", []TokenKind{TokenLt, TokenEOF}},
		{"<=", []TokenKind{TokenLte, TokenEOF}},
		{"&&", []TokenKind{TokenAnd, TokenEOF}},
		{"||", []TokenKind{TokenOr, TokenEOF}},
		{"!", []TokenKind{TokenNot, TokenEOF}},
		{"??", []TokenKind{TokenNullCoal, TokenEOF}},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			tokens, err := Lex(tt.input)
			if err != nil {
				t.Fatalf("Lex(%q) error: %v", tt.input, err)
			}
			if len(tokens) != len(tt.kinds) {
				t.Fatalf("Lex(%q) got %d tokens, want %d", tt.input, len(tokens), len(tt.kinds))
			}
			for i, want := range tt.kinds {
				if tokens[i].Kind != want {
					t.Errorf("token[%d] got %s, want %s", i, tokens[i].Kind, want)
				}
			}
		})
	}
}

func TestLex_Keywords(t *testing.T) {
	tests := []struct {
		input string
		kind  TokenKind
	}{
		{"in", TokenIn},
		{"has", TokenHas},
		{"contains", TokenContains},
		{"startsWith", TokenStartsWith},
		{"endsWith", TokenEndsWith},
		{"matches", TokenMatches},
		{"true", TokenTrue},
		{"false", TokenFalse},
		{"null", TokenNull},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			tokens, err := Lex(tt.input)
			if err != nil {
				t.Fatalf("Lex(%q) error: %v", tt.input, err)
			}
			if len(tokens) != 2 { // keyword + EOF
				t.Fatalf("Lex(%q) got %d tokens, want 2", tt.input, len(tokens))
			}
			if tokens[0].Kind != tt.kind {
				t.Errorf("got kind %s, want %s", tokens[0].Kind, tt.kind)
			}
			if tokens[0].Value != tt.input {
				t.Errorf("got value %q, want %q", tokens[0].Value, tt.input)
			}
		})
	}
}

func TestLex_Delimiters(t *testing.T) {
	tests := []struct {
		input string
		kind  TokenKind
	}{
		{".", TokenDot},
		{"[", TokenLBracket},
		{"]", TokenRBracket},
		{"(", TokenLParen},
		{")", TokenRParen},
		{",", TokenComma},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			tokens, err := Lex(tt.input)
			if err != nil {
				t.Fatalf("Lex(%q) error: %v", tt.input, err)
			}
			if tokens[0].Kind != tt.kind {
				t.Errorf("got %s, want %s", tokens[0].Kind, tt.kind)
			}
		})
	}
}

func TestLex_Numbers(t *testing.T) {
	tests := []struct {
		input string
		value string
	}{
		{"0", "0"},
		{"42", "42"},
		{"3.14", "3.14"},
		{"0.5", "0.5"},
		{"100.00", "100.00"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			tokens, err := Lex(tt.input)
			if err != nil {
				t.Fatalf("Lex(%q) error: %v", tt.input, err)
			}
			if tokens[0].Kind != TokenNumber {
				t.Fatalf("got kind %s, want TokenNumber", tokens[0].Kind)
			}
			if tokens[0].Value != tt.value {
				t.Errorf("got value %q, want %q", tokens[0].Value, tt.value)
			}
		})
	}
}

func TestLex_NegativeNumbers(t *testing.T) {
	// At the start of input, '-' is a negative number sign.
	tokens, err := Lex("-5")
	if err != nil {
		t.Fatalf("Lex(\"-5\") error: %v", err)
	}
	if tokens[0].Kind != TokenNumber || tokens[0].Value != "-5" {
		t.Errorf("got %s %q, want TokenNumber \"-5\"", tokens[0].Kind, tokens[0].Value)
	}

	// After an operator, '-' is also a negative number sign.
	tokens, err = Lex("x == -3")
	if err != nil {
		t.Fatalf("Lex(\"x == -3\") error: %v", err)
	}
	// x, ==, -3, EOF
	if len(tokens) != 4 {
		t.Fatalf("got %d tokens, want 4", len(tokens))
	}
	if tokens[2].Kind != TokenNumber || tokens[2].Value != "-3" {
		t.Errorf("got %s %q, want TokenNumber \"-3\"", tokens[2].Kind, tokens[2].Value)
	}
}

func TestLex_Strings(t *testing.T) {
	tests := []struct {
		name  string
		input string
		value string
	}{
		{"simple", `"hello"`, "hello"},
		{"empty", `""`, ""},
		{"with spaces", `"hello world"`, "hello world"},
		{"escape quote", `"say \"hi\""`, `say "hi"`},
		{"escape backslash", `"a\\b"`, `a\b`},
		{"escape newline", `"line1\nline2"`, "line1\nline2"},
		{"escape tab", `"col1\tcol2"`, "col1\tcol2"},
		{"escape carriage return", `"a\rb"`, "a\rb"},
		{"escape slash", `"a\/b"`, "a/b"},
		{"unknown escape passthrough", `"a\xb"`, `a\xb`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tokens, err := Lex(tt.input)
			if err != nil {
				t.Fatalf("Lex(%q) error: %v", tt.input, err)
			}
			if tokens[0].Kind != TokenString {
				t.Fatalf("got kind %s, want TokenString", tokens[0].Kind)
			}
			if tokens[0].Value != tt.value {
				t.Errorf("got value %q, want %q", tokens[0].Value, tt.value)
			}
		})
	}
}

func TestLex_UnterminatedString(t *testing.T) {
	_, err := Lex(`"unterminated`)
	if err == nil {
		t.Fatal("expected error for unterminated string, got nil")
	}
	if !strings.Contains(err.Error(), "unterminated string") {
		t.Errorf("expected 'unterminated string' in error, got: %v", err)
	}
}

func TestLex_UnterminatedStringEscapeAtEnd(t *testing.T) {
	_, err := Lex(`"abc\`)
	if err == nil {
		t.Fatal("expected error for escape at end of string, got nil")
	}
	if !strings.Contains(err.Error(), "unterminated string") {
		t.Errorf("expected 'unterminated string' in error, got: %v", err)
	}
}

func TestLex_Identifiers(t *testing.T) {
	tests := []struct {
		input string
		value string
	}{
		{"x", "x"},
		{"input", "input"},
		{"_private", "_private"},
		{"camelCase", "camelCase"},
		{"with123", "with123"},
		{"ALL_CAPS", "ALL_CAPS"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			tokens, err := Lex(tt.input)
			if err != nil {
				t.Fatalf("Lex(%q) error: %v", tt.input, err)
			}
			if tokens[0].Kind != TokenIdent {
				t.Fatalf("got kind %s, want TokenIdent", tokens[0].Kind)
			}
			if tokens[0].Value != tt.value {
				t.Errorf("got value %q, want %q", tokens[0].Value, tt.value)
			}
		})
	}
}

func TestLex_EmptyInput(t *testing.T) {
	tokens, err := Lex("")
	if err != nil {
		t.Fatalf("Lex(\"\") error: %v", err)
	}
	if len(tokens) != 1 {
		t.Fatalf("got %d tokens, want 1 (EOF only)", len(tokens))
	}
	if tokens[0].Kind != TokenEOF {
		t.Errorf("got %s, want TokenEOF", tokens[0].Kind)
	}
}

func TestLex_WhitespaceOnly(t *testing.T) {
	tokens, err := Lex("   \t\n  ")
	if err != nil {
		t.Fatalf("Lex(whitespace) error: %v", err)
	}
	if len(tokens) != 1 || tokens[0].Kind != TokenEOF {
		t.Errorf("expected only EOF token for whitespace-only input")
	}
}

func TestLex_UnexpectedCharacter(t *testing.T) {
	_, err := Lex("@")
	if err == nil {
		t.Fatal("expected error for unexpected character, got nil")
	}
	if !strings.Contains(err.Error(), "unexpected character") {
		t.Errorf("expected 'unexpected character' in error, got: %v", err)
	}
}

func TestLex_ComplexExpression(t *testing.T) {
	tokens, err := Lex(`input.score >= 0.8 && input.source != "test"`)
	if err != nil {
		t.Fatalf("Lex error: %v", err)
	}

	expected := []TokenKind{
		TokenIdent, TokenDot, TokenIdent, TokenGte, TokenNumber,
		TokenAnd,
		TokenIdent, TokenDot, TokenIdent, TokenNeq, TokenString,
		TokenEOF,
	}
	if len(tokens) != len(expected) {
		t.Fatalf("got %d tokens, want %d", len(tokens), len(expected))
	}
	for i, want := range expected {
		if tokens[i].Kind != want {
			t.Errorf("token[%d]: got %s, want %s", i, tokens[i].Kind, want)
		}
	}
}

func TestLex_PositionTracking(t *testing.T) {
	tokens, err := Lex("a == b")
	if err != nil {
		t.Fatalf("Lex error: %v", err)
	}
	// a at 0, == at 2, b at 5
	if tokens[0].Pos != 0 {
		t.Errorf("token 'a' pos: got %d, want 0", tokens[0].Pos)
	}
	if tokens[1].Pos != 2 {
		t.Errorf("token '==' pos: got %d, want 2", tokens[1].Pos)
	}
	if tokens[2].Pos != 5 {
		t.Errorf("token 'b' pos: got %d, want 5", tokens[2].Pos)
	}
}

func TestLex_TokenKindString(t *testing.T) {
	if s := TokenEq.String(); s != "==" {
		t.Errorf("TokenEq.String() = %q, want \"==\"", s)
	}
	if s := TokenIdent.String(); s != "identifier" {
		t.Errorf("TokenIdent.String() = %q, want \"identifier\"", s)
	}
	// Unknown token kind
	unknown := TokenKind(999)
	s := unknown.String()
	if !strings.Contains(s, "999") {
		t.Errorf("unknown token kind String() = %q, expected to contain 999", s)
	}
}

// ---------------------------------------------------------------------------
// 2. Parser tests
// ---------------------------------------------------------------------------

func TestParse_Literals(t *testing.T) {
	tests := []struct {
		input string
		want  string // AST String()
	}{
		{"42", "42"},
		{"3.14", "3.14"},
		{`"hello"`, "hello"},
		{"true", "true"},
		{"false", "false"},
		{"null", "null"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			ast, err := Parse(tt.input)
			if err != nil {
				t.Fatalf("Parse(%q) error: %v", tt.input, err)
			}
			if ast.String() != tt.want {
				t.Errorf("got %q, want %q", ast.String(), tt.want)
			}
		})
	}
}

func TestParse_Identifiers(t *testing.T) {
	ast, err := Parse("input")
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	ident, ok := ast.(*IdentExpr)
	if !ok {
		t.Fatalf("expected *IdentExpr, got %T", ast)
	}
	if ident.Name != "input" {
		t.Errorf("got name %q, want \"input\"", ident.Name)
	}
}

func TestParse_MemberAccess(t *testing.T) {
	ast, err := Parse("input.score")
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	member, ok := ast.(*MemberExpr)
	if !ok {
		t.Fatalf("expected *MemberExpr, got %T", ast)
	}
	if member.Property != "score" {
		t.Errorf("property: got %q, want \"score\"", member.Property)
	}
	if member.String() != "input.score" {
		t.Errorf("String(): got %q, want \"input.score\"", member.String())
	}
}

func TestParse_NestedMemberAccess(t *testing.T) {
	ast, err := Parse("input.metadata.source")
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	member, ok := ast.(*MemberExpr)
	if !ok {
		t.Fatalf("expected *MemberExpr, got %T", ast)
	}
	if member.Property != "source" {
		t.Errorf("property: got %q, want \"source\"", member.Property)
	}
	inner, ok := member.Object.(*MemberExpr)
	if !ok {
		t.Fatalf("expected inner *MemberExpr, got %T", member.Object)
	}
	if inner.Property != "metadata" {
		t.Errorf("inner property: got %q, want \"metadata\"", inner.Property)
	}
}

func TestParse_IndexAccess(t *testing.T) {
	ast, err := Parse("input.tags[0]")
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	idx, ok := ast.(*IndexExpr)
	if !ok {
		t.Fatalf("expected *IndexExpr, got %T", ast)
	}
	if idx.String() != "input.tags[0]" {
		t.Errorf("String(): got %q, want \"input.tags[0]\"", idx.String())
	}
}

func TestParse_ArrayLiteral(t *testing.T) {
	tests := []struct {
		name  string
		input string
		count int
	}{
		{"empty", "[]", 0},
		{"single", `["a"]`, 1},
		{"multiple", `["a", "b", "c"]`, 3},
		{"numbers", "[1, 2, 3]", 3},
		{"mixed", `[1, "two", true, null]`, 4},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ast, err := Parse(tt.input)
			if err != nil {
				t.Fatalf("Parse(%q) error: %v", tt.input, err)
			}
			arr, ok := ast.(*ArrayLiteral)
			if !ok {
				t.Fatalf("expected *ArrayLiteral, got %T", ast)
			}
			if len(arr.Elements) != tt.count {
				t.Errorf("got %d elements, want %d", len(arr.Elements), tt.count)
			}
		})
	}
}

func TestParse_BinaryOperators(t *testing.T) {
	tests := []struct {
		input string
		op    TokenKind
	}{
		{"a == b", TokenEq},
		{"a != b", TokenNeq},
		{"a > b", TokenGt},
		{"a >= b", TokenGte},
		{"a < b", TokenLt},
		{"a <= b", TokenLte},
		{"a && b", TokenAnd},
		{"a || b", TokenOr},
		{"a ?? b", TokenNullCoal},
		{"a in b", TokenIn},
		{"a has b", TokenHas},
		{"a contains b", TokenContains},
		{"a startsWith b", TokenStartsWith},
		{"a endsWith b", TokenEndsWith},
		{"a matches b", TokenMatches},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			ast, err := Parse(tt.input)
			if err != nil {
				t.Fatalf("Parse(%q) error: %v", tt.input, err)
			}
			bin, ok := ast.(*BinaryExpr)
			if !ok {
				t.Fatalf("expected *BinaryExpr, got %T", ast)
			}
			if bin.Op != tt.op {
				t.Errorf("operator: got %s, want %s", bin.Op, tt.op)
			}
		})
	}
}

func TestParse_UnaryNot(t *testing.T) {
	ast, err := Parse("!x")
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	unary, ok := ast.(*UnaryExpr)
	if !ok {
		t.Fatalf("expected *UnaryExpr, got %T", ast)
	}
	if unary.Op != TokenNot {
		t.Errorf("operator: got %s, want %s", unary.Op, TokenNot)
	}
	if unary.String() != "(!x)" {
		t.Errorf("String(): got %q, want \"(!x)\"", unary.String())
	}
}

func TestParse_DoubleNot(t *testing.T) {
	ast, err := Parse("!!x")
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	outer, ok := ast.(*UnaryExpr)
	if !ok {
		t.Fatalf("expected outer *UnaryExpr, got %T", ast)
	}
	inner, ok := outer.Operand.(*UnaryExpr)
	if !ok {
		t.Fatalf("expected inner *UnaryExpr, got %T", outer.Operand)
	}
	_, ok = inner.Operand.(*IdentExpr)
	if !ok {
		t.Fatalf("expected *IdentExpr, got %T", inner.Operand)
	}
}

func TestParse_Precedence_AndOverOr(t *testing.T) {
	// a || b && c should parse as a || (b && c)
	ast, err := Parse("a || b && c")
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	bin, ok := ast.(*BinaryExpr)
	if !ok {
		t.Fatalf("expected *BinaryExpr, got %T", ast)
	}
	if bin.Op != TokenOr {
		t.Errorf("top operator: got %s, want ||", bin.Op)
	}
	rightBin, ok := bin.Right.(*BinaryExpr)
	if !ok {
		t.Fatalf("right: expected *BinaryExpr, got %T", bin.Right)
	}
	if rightBin.Op != TokenAnd {
		t.Errorf("right operator: got %s, want &&", rightBin.Op)
	}
}

func TestParse_Precedence_ComparisonOverAnd(t *testing.T) {
	// a > b && c < d should parse as (a > b) && (c < d)
	ast, err := Parse("a > b && c < d")
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	bin, ok := ast.(*BinaryExpr)
	if !ok {
		t.Fatalf("expected *BinaryExpr, got %T", ast)
	}
	if bin.Op != TokenAnd {
		t.Errorf("top operator: got %s, want &&", bin.Op)
	}
	left, ok := bin.Left.(*BinaryExpr)
	if !ok {
		t.Fatalf("left: expected *BinaryExpr, got %T", bin.Left)
	}
	if left.Op != TokenGt {
		t.Errorf("left operator: got %s, want >", left.Op)
	}
	right, ok := bin.Right.(*BinaryExpr)
	if !ok {
		t.Fatalf("right: expected *BinaryExpr, got %T", bin.Right)
	}
	if right.Op != TokenLt {
		t.Errorf("right operator: got %s, want <", right.Op)
	}
}

func TestParse_Precedence_EqualityOverComparison(t *testing.T) {
	// a == b > c should not matter much, but test: a == (x > y)
	// Actually: == is lower than >/<, so: a == b is parsed at equality level,
	// and b > c at comparison level. "a == b > c" means (a == (b > c))? No.
	// The precedence chain: equality calls comparison. So a == b consumes a,
	// then sees ==, then parses RHS starting from comparison. RHS = b > c.
	// So result is a == (b > c).
	// Wait, let me re-examine: parseEquality calls parseComparison for each side.
	// So for "a == b > c": left = parseComparison("a") = a, then sees ==,
	// then right = parseComparison("b > c") = (b > c). Result: a == (b > c).
	ast, err := Parse("a == b > c")
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	bin, ok := ast.(*BinaryExpr)
	if !ok {
		t.Fatalf("expected *BinaryExpr, got %T", ast)
	}
	if bin.Op != TokenEq {
		t.Errorf("top operator: got %s, want ==", bin.Op)
	}
	right, ok := bin.Right.(*BinaryExpr)
	if !ok {
		t.Fatalf("right: expected *BinaryExpr, got %T", bin.Right)
	}
	if right.Op != TokenGt {
		t.Errorf("right operator: got %s, want >", right.Op)
	}
}

func TestParse_Precedence_NullCoalescingLowest(t *testing.T) {
	// a ?? b || c should parse as a ?? (b || c)
	ast, err := Parse("a ?? b || c")
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	bin, ok := ast.(*BinaryExpr)
	if !ok {
		t.Fatalf("expected *BinaryExpr, got %T", ast)
	}
	if bin.Op != TokenNullCoal {
		t.Errorf("top operator: got %s, want ??", bin.Op)
	}
	right, ok := bin.Right.(*BinaryExpr)
	if !ok {
		t.Fatalf("right: expected *BinaryExpr, got %T", bin.Right)
	}
	if right.Op != TokenOr {
		t.Errorf("right operator: got %s, want ||", right.Op)
	}
}

func TestParse_Precedence_NotHigherThanComparison(t *testing.T) {
	// !a == b should parse as (!a) == b
	ast, err := Parse("!a == b")
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	bin, ok := ast.(*BinaryExpr)
	if !ok {
		t.Fatalf("expected *BinaryExpr, got %T", ast)
	}
	if bin.Op != TokenEq {
		t.Errorf("top operator: got %s, want ==", bin.Op)
	}
	_, ok = bin.Left.(*UnaryExpr)
	if !ok {
		t.Fatalf("left: expected *UnaryExpr, got %T", bin.Left)
	}
}

func TestParse_Precedence_MembershipBetweenComparisonAndUnary(t *testing.T) {
	// a in b && c has d should parse as (a in b) && (c has d)
	ast, err := Parse("a in b && c has d")
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	bin, ok := ast.(*BinaryExpr)
	if !ok {
		t.Fatalf("expected *BinaryExpr, got %T", ast)
	}
	if bin.Op != TokenAnd {
		t.Errorf("top operator: got %s, want &&", bin.Op)
	}
	left, ok := bin.Left.(*BinaryExpr)
	if !ok {
		t.Fatalf("left: expected *BinaryExpr, got %T", bin.Left)
	}
	if left.Op != TokenIn {
		t.Errorf("left operator: got %s, want in", left.Op)
	}
}

func TestParse_Grouping(t *testing.T) {
	// (a || b) && c should parse && at top with || on left
	ast, err := Parse("(a || b) && c")
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	bin, ok := ast.(*BinaryExpr)
	if !ok {
		t.Fatalf("expected *BinaryExpr, got %T", ast)
	}
	if bin.Op != TokenAnd {
		t.Errorf("top operator: got %s, want &&", bin.Op)
	}
	left, ok := bin.Left.(*BinaryExpr)
	if !ok {
		t.Fatalf("left: expected *BinaryExpr, got %T", bin.Left)
	}
	if left.Op != TokenOr {
		t.Errorf("left operator: got %s, want ||", left.Op)
	}
}

func TestParse_KeywordAsProperty(t *testing.T) {
	// Keywords should be allowed as property names after dot.
	tests := []struct {
		input    string
		property string
	}{
		{"obj.in", "in"},
		{"obj.has", "has"},
		{"obj.contains", "contains"},
		{"obj.true", "true"},
		{"obj.false", "false"},
		{"obj.null", "null"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			ast, err := Parse(tt.input)
			if err != nil {
				t.Fatalf("Parse(%q) error: %v", tt.input, err)
			}
			member, ok := ast.(*MemberExpr)
			if !ok {
				t.Fatalf("expected *MemberExpr, got %T", ast)
			}
			if member.Property != tt.property {
				t.Errorf("property: got %q, want %q", member.Property, tt.property)
			}
		})
	}
}

func TestParse_SyntaxErrors(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"missing rhs", "a =="},
		{"missing rparen", "(a == b"},
		{"missing rbracket", "a[0"},
		{"double operator", "a == == b"},
		{"trailing token", "a b"},
		{"empty parens", "()"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse(tt.input)
			if err == nil {
				t.Fatalf("Parse(%q) expected error, got nil", tt.input)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// 3. Evaluator tests
// ---------------------------------------------------------------------------

func TestEval_ComparisonOperators_Numbers(t *testing.T) {
	tests := []struct {
		expr string
		want bool
	}{
		{"1 == 1", true},
		{"1 == 2", false},
		{"1 != 2", true},
		{"1 != 1", false},
		{"2 > 1", true},
		{"1 > 2", false},
		{"1 > 1", false},
		{"2 >= 1", true},
		{"1 >= 1", true},
		{"0 >= 1", false},
		{"1 < 2", true},
		{"2 < 1", false},
		{"1 < 1", false},
		{"1 <= 2", true},
		{"1 <= 1", true},
		{"2 <= 1", false},
	}

	for _, tt := range tests {
		t.Run(tt.expr, func(t *testing.T) {
			result := evalExpr(t, tt.expr, nil)
			assertBool(t, tt.expr, result, tt.want)
		})
	}
}

func TestEval_ComparisonOperators_Strings(t *testing.T) {
	tests := []struct {
		expr string
		want bool
	}{
		{`"abc" == "abc"`, true},
		{`"abc" == "def"`, false},
		{`"abc" != "def"`, true},
		{`"abc" != "abc"`, false},
		{`"b" > "a"`, true},
		{`"a" > "b"`, false},
		{`"a" < "b"`, true},
		{`"b" < "a"`, false},
		{`"a" >= "a"`, true},
		{`"a" <= "a"`, true},
	}

	for _, tt := range tests {
		t.Run(tt.expr, func(t *testing.T) {
			result := evalExpr(t, tt.expr, nil)
			assertBool(t, tt.expr, result, tt.want)
		})
	}
}

func TestEval_ComparisonOperators_Booleans(t *testing.T) {
	tests := []struct {
		expr string
		want bool
	}{
		{"true == true", true},
		{"false == false", true},
		{"true == false", false},
		{"true != false", true},
	}

	for _, tt := range tests {
		t.Run(tt.expr, func(t *testing.T) {
			result := evalExpr(t, tt.expr, nil)
			assertBool(t, tt.expr, result, tt.want)
		})
	}
}

func TestEval_NullComparisons(t *testing.T) {
	tests := []struct {
		expr string
		want bool
	}{
		{"null == null", true},
		{"null != null", false},
		{`null == ""`, false},
		{`"" == null`, false},
		{"null == false", false},
		{"null == 0", false},
	}

	for _, tt := range tests {
		t.Run(tt.expr, func(t *testing.T) {
			result := evalExpr(t, tt.expr, nil)
			assertBool(t, tt.expr, result, tt.want)
		})
	}
}

func TestEval_LogicalAnd(t *testing.T) {
	tests := []struct {
		expr string
		want bool
	}{
		{"true && true", true},
		{"true && false", false},
		{"false && true", false},
		{"false && false", false},
	}

	for _, tt := range tests {
		t.Run(tt.expr, func(t *testing.T) {
			result := evalExpr(t, tt.expr, nil)
			assertBool(t, tt.expr, result, tt.want)
		})
	}
}

func TestEval_LogicalOr(t *testing.T) {
	tests := []struct {
		expr string
		want bool
	}{
		{"true || true", true},
		{"true || false", true},
		{"false || true", true},
		{"false || false", false},
	}

	for _, tt := range tests {
		t.Run(tt.expr, func(t *testing.T) {
			result := evalExpr(t, tt.expr, nil)
			assertBool(t, tt.expr, result, tt.want)
		})
	}
}

func TestEval_LogicalNot(t *testing.T) {
	tests := []struct {
		expr string
		want bool
	}{
		{"!true", false},
		{"!false", true},
		{"!null", true},
		{`!""`, true},
		{`!"hello"`, false},
		{"!0", true},
		{"!1", false},
	}

	for _, tt := range tests {
		t.Run(tt.expr, func(t *testing.T) {
			result := evalExpr(t, tt.expr, nil)
			assertBool(t, tt.expr, result, tt.want)
		})
	}
}

func TestEval_ShortCircuit_And(t *testing.T) {
	// If left side of && is false, right side should not be evaluated.
	// We test this by relying on the fact that accessing a member on nil
	// does NOT error (returns nil), so we just confirm the result.
	vars := map[string]any{}
	result := evalExpr(t, "false && undefined_var.prop", vars)
	assertBool(t, "short-circuit &&", result, false)
}

func TestEval_ShortCircuit_Or(t *testing.T) {
	// If left side of || is true, right side should not be evaluated.
	vars := map[string]any{}
	result := evalExpr(t, "true || undefined_var.prop", vars)
	assertBool(t, "short-circuit ||", result, true)
}

func TestEval_InOperator(t *testing.T) {
	vars := map[string]any{
		"input": map[string]any{
			"tags": []any{"go", "rust", "python"},
		},
	}

	tests := []struct {
		expr string
		want bool
	}{
		{`"go" in input.tags`, true},
		{`"java" in input.tags`, false},
		{`1 in [1, 2, 3]`, true},
		{`4 in [1, 2, 3]`, false},
		{`"x" in null`, false},
	}

	for _, tt := range tests {
		t.Run(tt.expr, func(t *testing.T) {
			result := evalExpr(t, tt.expr, vars)
			assertBool(t, tt.expr, result, tt.want)
		})
	}
}

func TestEval_HasOperator(t *testing.T) {
	vars := map[string]any{
		"input": map[string]any{
			"payload": map[string]any{
				"refund_id": "ref123",
				"amount":    42.0,
			},
		},
	}

	tests := []struct {
		expr string
		want bool
	}{
		{`input.payload has "refund_id"`, true},
		{`input.payload has "amount"`, true},
		{`input.payload has "missing"`, false},
		{`input has "payload"`, true},
		{`null has "key"`, false},
	}

	for _, tt := range tests {
		t.Run(tt.expr, func(t *testing.T) {
			result := evalExpr(t, tt.expr, vars)
			assertBool(t, tt.expr, result, tt.want)
		})
	}
}

func TestEval_Contains(t *testing.T) {
	tests := []struct {
		expr string
		want bool
	}{
		{`"hello world" contains "world"`, true},
		{`"hello world" contains "xyz"`, false},
		{`"hello" contains ""`, true},
		{`"" contains ""`, true},
	}

	for _, tt := range tests {
		t.Run(tt.expr, func(t *testing.T) {
			result := evalExpr(t, tt.expr, nil)
			assertBool(t, tt.expr, result, tt.want)
		})
	}
}

func TestEval_ContainsNonString(t *testing.T) {
	// contains with non-string args returns false
	result := evalExpr(t, "1 contains 1", nil)
	assertBool(t, "1 contains 1", result, false)
}

func TestEval_StartsWith(t *testing.T) {
	tests := []struct {
		expr string
		want bool
	}{
		{`"hello world" startsWith "hello"`, true},
		{`"hello world" startsWith "world"`, false},
		{`"hello" startsWith ""`, true},
	}

	for _, tt := range tests {
		t.Run(tt.expr, func(t *testing.T) {
			result := evalExpr(t, tt.expr, nil)
			assertBool(t, tt.expr, result, tt.want)
		})
	}
}

func TestEval_EndsWith(t *testing.T) {
	tests := []struct {
		expr string
		want bool
	}{
		{`"hello world" endsWith "world"`, true},
		{`"hello world" endsWith "hello"`, false},
		{`"hello" endsWith ""`, true},
	}

	for _, tt := range tests {
		t.Run(tt.expr, func(t *testing.T) {
			result := evalExpr(t, tt.expr, nil)
			assertBool(t, tt.expr, result, tt.want)
		})
	}
}

func TestEval_Matches(t *testing.T) {
	tests := []struct {
		expr string
		want bool
	}{
		{`"test@example.com" matches "^.*@example\\.com$"`, true},
		{`"test@other.com" matches "^.*@example\\.com$"`, false},
		{`"abc123" matches "^[a-z]+[0-9]+$"`, true},
		{`"123abc" matches "^[a-z]+[0-9]+$"`, false},
	}

	for _, tt := range tests {
		t.Run(tt.expr, func(t *testing.T) {
			result := evalExpr(t, tt.expr, nil)
			assertBool(t, tt.expr, result, tt.want)
		})
	}
}

func TestEval_MatchesInvalidRegex(t *testing.T) {
	ast, err := Parse(`"test" matches "[invalid"`)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	_, err = Eval(ast, nil)
	if err == nil {
		t.Fatal("expected error for invalid regex, got nil")
	}
	if !strings.Contains(err.Error(), "invalid regex") {
		t.Errorf("expected 'invalid regex' in error, got: %v", err)
	}
}

func TestEval_MatchesNonString(t *testing.T) {
	result := evalExpr(t, "1 matches 1", nil)
	assertBool(t, "1 matches 1", result, false)
}

func TestEval_NullCoalescing(t *testing.T) {
	tests := []struct {
		name string
		expr string
		vars map[string]any
		want any
	}{
		{
			name: "null coalesces to default",
			expr: "x ?? 42",
			vars: map[string]any{},
			want: float64(42),
		},
		{
			name: "non-null keeps value",
			expr: "x ?? 42",
			vars: map[string]any{"x": float64(10)},
			want: float64(10),
		},
		{
			name: "explicit null coalesces",
			expr: "x ?? 0",
			vars: map[string]any{"x": nil},
			want: float64(0),
		},
		{
			name: "zero does not coalesce",
			expr: "x ?? 99",
			vars: map[string]any{"x": float64(0)},
			want: float64(0),
		},
		{
			name: "empty string does not coalesce",
			expr: `x ?? "default"`,
			vars: map[string]any{"x": ""},
			want: "",
		},
		{
			name: "false does not coalesce",
			expr: "x ?? true",
			vars: map[string]any{"x": false},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := evalExpr(t, tt.expr, tt.vars)
			if fmt.Sprintf("%v", result) != fmt.Sprintf("%v", tt.want) {
				t.Errorf("got %v (%T), want %v (%T)", result, result, tt.want, tt.want)
			}
		})
	}
}

func TestEval_MemberAccess(t *testing.T) {
	vars := map[string]any{
		"input": map[string]any{
			"status": "approved",
			"metadata": map[string]any{
				"source": "api",
				"nested": map[string]any{
					"deep": "value",
				},
			},
		},
	}

	tests := []struct {
		expr string
		want any
	}{
		{"input.status", "approved"},
		{"input.metadata.source", "api"},
		{"input.metadata.nested.deep", "value"},
		{"input.missing", nil},
		{"input.metadata.missing", nil},
	}

	for _, tt := range tests {
		t.Run(tt.expr, func(t *testing.T) {
			result := evalExpr(t, tt.expr, vars)
			if tt.want == nil {
				assertNil(t, tt.expr, result)
			} else {
				assertString(t, tt.expr, result, tt.want.(string))
			}
		})
	}
}

func TestEval_MemberAccessOnNil(t *testing.T) {
	vars := map[string]any{}
	result := evalExpr(t, "undefined.prop", vars)
	assertNil(t, "undefined.prop", result)
}

func TestEval_IndexAccess(t *testing.T) {
	vars := map[string]any{
		"input": map[string]any{
			"tags": []any{"first", "second", "third"},
		},
	}

	tests := []struct {
		expr string
		want any
	}{
		{"input.tags[0]", "first"},
		{"input.tags[1]", "second"},
		{"input.tags[2]", "third"},
	}

	for _, tt := range tests {
		t.Run(tt.expr, func(t *testing.T) {
			result := evalExpr(t, tt.expr, vars)
			assertString(t, tt.expr, result, tt.want.(string))
		})
	}
}

func TestEval_IndexAccessOutOfBounds(t *testing.T) {
	vars := map[string]any{
		"arr": []any{"a", "b"},
	}
	result := evalExpr(t, "arr[99]", vars)
	assertNil(t, "arr[99]", result)

	result = evalExpr(t, "arr[-1]", vars) // negative index treated as invalid int index
	// -1 < 0 => nil
	assertNil(t, "arr[-1]", result)
}

func TestEval_IndexAccessOnNil(t *testing.T) {
	result := evalExpr(t, "x[0]", map[string]any{})
	assertNil(t, "x[0]", result)
}

func TestEval_LengthOnArray(t *testing.T) {
	vars := map[string]any{
		"arr": []any{1, 2, 3},
	}
	result := evalExpr(t, "arr.length", vars)
	assertFloat64(t, "arr.length", result, 3)
}

func TestEval_LengthOnString(t *testing.T) {
	vars := map[string]any{
		"s": "hello",
	}
	result := evalExpr(t, "s.length", vars)
	assertFloat64(t, "s.length", result, 5)
}

func TestEval_LengthOnEmptyArray(t *testing.T) {
	vars := map[string]any{
		"arr": []any{},
	}
	result := evalExpr(t, "arr.length", vars)
	assertFloat64(t, "arr.length", result, 0)
}

func TestEval_LengthOnEmptyString(t *testing.T) {
	vars := map[string]any{
		"s": "",
	}
	result := evalExpr(t, "s.length", vars)
	assertFloat64(t, "s.length", result, 0)
}

func TestEval_LengthOnNil(t *testing.T) {
	result := evalExpr(t, "x.length", map[string]any{})
	assertNil(t, "x.length", result)
}

func TestEval_Truthiness(t *testing.T) {
	tests := []struct {
		name string
		expr string
		vars map[string]any
		want bool
	}{
		// Falsy values
		{"zero is falsy", "!!0", nil, false},
		{"empty string is falsy", `!!""`, nil, false},
		{"null is falsy", "!!null", nil, false},
		{"false is falsy", "!!false", nil, false},
		{"empty array is falsy", "!!x", map[string]any{"x": []any{}}, false},
		{"empty map is falsy", "!!x", map[string]any{"x": map[string]any{}}, false},

		// Truthy values
		{"nonzero is truthy", "!!1", nil, true},
		{"negative is truthy", "!!x", map[string]any{"x": float64(-1)}, true},
		{"nonempty string is truthy", `!!"hello"`, nil, true},
		{"true is truthy", "!!true", nil, true},
		{"nonempty array is truthy", "!!x", map[string]any{"x": []any{1}}, true},
		{"nonempty map is truthy", "!!x", map[string]any{"x": map[string]any{"k": "v"}}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := evalExpr(t, tt.expr, tt.vars)
			assertBool(t, tt.name, result, tt.want)
		})
	}
}

func TestEval_UndefinedVariable(t *testing.T) {
	result := evalExpr(t, "missing", map[string]any{})
	assertNil(t, "missing", result)
}

func TestEval_EmptyVarsMap(t *testing.T) {
	result := evalExpr(t, "x == null", map[string]any{})
	assertBool(t, "undefined == null", result, true)
}

func TestEval_NilVarsMap(t *testing.T) {
	// nil map should behave like empty map.
	result := evalExpr(t, "null == null", nil)
	assertBool(t, "null == null with nil vars", result, true)
}

func TestEval_ArrayLiteral(t *testing.T) {
	result := evalExpr(t, `["a", "b", "c"]`, nil)
	arr, ok := result.([]any)
	if !ok {
		t.Fatalf("expected []any, got %T", result)
	}
	if len(arr) != 3 {
		t.Fatalf("got %d elements, want 3", len(arr))
	}
	if arr[0] != "a" || arr[1] != "b" || arr[2] != "c" {
		t.Errorf("got %v, want [a b c]", arr)
	}
}

func TestEval_EmptyArrayLiteral(t *testing.T) {
	result := evalExpr(t, "[]", nil)
	arr, ok := result.([]any)
	if !ok {
		t.Fatalf("expected []any, got %T", result)
	}
	if len(arr) != 0 {
		t.Fatalf("got %d elements, want 0", len(arr))
	}
}

func TestEval_NumericEquality_IntFloat(t *testing.T) {
	// The evaluator normalizes numeric types for comparison.
	vars := map[string]any{
		"x": 42, // int, not float64
	}
	result := evalExpr(t, "x == 42", vars)
	assertBool(t, "int == float64", result, true)
}

func TestEval_ComparisonNonNumericNonString(t *testing.T) {
	// Comparing non-numeric/non-string values returns false for >, <, etc.
	result := evalExpr(t, "true > false", nil)
	assertBool(t, "true > false", result, false)
}

// ---------------------------------------------------------------------------
// 4. Integration tests (Parse -> Eval)
// ---------------------------------------------------------------------------

func TestIntegration_StatusCheck(t *testing.T) {
	vars := map[string]any{
		"input": map[string]any{
			"status": "approved",
		},
	}
	result := evalExpr(t, `input.status == "approved"`, vars)
	assertBool(t, "status check", result, true)

	result = evalExpr(t, `input.status == "rejected"`, vars)
	assertBool(t, "status check false", result, false)
}

func TestIntegration_ScoreAndSource(t *testing.T) {
	vars := map[string]any{
		"input": map[string]any{
			"score":  0.9,
			"source": "production",
		},
	}
	result := evalExpr(t, `input.score >= 0.8 && input.source != "test"`, vars)
	assertBool(t, "score >= 0.8 and source != test", result, true)

	vars["input"].(map[string]any)["source"] = "test"
	result = evalExpr(t, `input.score >= 0.8 && input.source != "test"`, vars)
	assertBool(t, "score >= 0.8 and source == test", result, false)

	vars["input"].(map[string]any)["source"] = "production"
	vars["input"].(map[string]any)["score"] = 0.5
	result = evalExpr(t, `input.score >= 0.8 && input.source != "test"`, vars)
	assertBool(t, "score < 0.8", result, false)
}

func TestIntegration_InArrayLiteral(t *testing.T) {
	vars := map[string]any{
		"input": map[string]any{
			"event_type": "payment.succeeded",
		},
	}
	result := evalExpr(t, `input.event_type in ["payment.succeeded", "payment.failed"]`, vars)
	assertBool(t, "event_type in array", result, true)

	vars["input"].(map[string]any)["event_type"] = "payment.created"
	result = evalExpr(t, `input.event_type in ["payment.succeeded", "payment.failed"]`, vars)
	assertBool(t, "event_type not in array", result, false)
}

func TestIntegration_HasKey(t *testing.T) {
	vars := map[string]any{
		"input": map[string]any{
			"payload": map[string]any{
				"refund_id": "ref_123",
			},
		},
	}
	result := evalExpr(t, `input.payload has "refund_id"`, vars)
	assertBool(t, "has refund_id", result, true)

	result = evalExpr(t, `input.payload has "charge_id"`, vars)
	assertBool(t, "has charge_id (missing)", result, false)
}

func TestIntegration_EmailMatches(t *testing.T) {
	vars := map[string]any{
		"input": map[string]any{
			"email": "user@example.com",
		},
	}
	result := evalExpr(t, `input.email matches "^.*@example\\.com$"`, vars)
	assertBool(t, "email matches example.com", result, true)

	vars["input"].(map[string]any)["email"] = "user@other.com"
	result = evalExpr(t, `input.email matches "^.*@example\\.com$"`, vars)
	assertBool(t, "email does not match example.com", result, false)
}

func TestIntegration_NullCoalescingWithComparison(t *testing.T) {
	// "input.value ?? 0 > 100" means "(input.value ?? 0) > 100"
	// because ?? is lowest precedence, actually this parses as:
	// input.value ?? (0 > 100). Let's verify the actual behavior.
	//
	// Precedence: ?? < || < && < == < > < ...
	// So ?? calls parseOr for its RHS. "0 > 100" inside parseOr
	// -> parseAnd -> parseEquality -> parseComparison which parses "0 > 100".
	// Result: input.value ?? (0 > 100) => input.value ?? false.
	//
	// If input.value is nil, result = false. If input.value is 150, result = 150.
	// The spec example might expect different behavior, but we test the actual grammar.

	// Case: value is nil -> coalesces to (0 > 100) = false
	vars := map[string]any{
		"input": map[string]any{},
	}
	result := evalExpr(t, "input.value ?? 0 > 100", vars)
	assertBool(t, "nil ?? (0 > 100)", result, false)

	// Case: value exists -> returns the value directly
	vars["input"].(map[string]any)["value"] = float64(150)
	result = evalExpr(t, "input.value ?? 0 > 100", vars)
	assertFloat64(t, "150 ?? ...", result, 150)

	// To get "(input.value ?? 0) > 100", parentheses are needed:
	vars["input"].(map[string]any)["value"] = nil
	result = evalExpr(t, "(input.value ?? 0) > 100", vars)
	assertBool(t, "(nil ?? 0) > 100", result, false)

	vars["input"].(map[string]any)["value"] = float64(150)
	result = evalExpr(t, "(input.value ?? 0) > 100", vars)
	assertBool(t, "(150 ?? 0) > 100", result, true)
}

func TestIntegration_ComplexCompound(t *testing.T) {
	vars := map[string]any{
		"input": map[string]any{
			"status":   "active",
			"priority": float64(3),
			"tags":     []any{"urgent", "billing"},
			"message":  "Error: payment failed for order #123",
		},
	}

	tests := []struct {
		name string
		expr string
		want bool
	}{
		{
			name: "status and priority",
			expr: `input.status == "active" && input.priority >= 3`,
			want: true,
		},
		{
			name: "tag membership",
			expr: `"urgent" in input.tags`,
			want: true,
		},
		{
			name: "tag not in list",
			expr: `"low" in input.tags`,
			want: false,
		},
		{
			name: "message contains",
			expr: `input.message contains "payment failed"`,
			want: true,
		},
		{
			name: "message startsWith",
			expr: `input.message startsWith "Error"`,
			want: true,
		},
		{
			name: "compound or",
			expr: `input.status == "active" || input.status == "pending"`,
			want: true,
		},
		{
			name: "negation",
			expr: `!(input.status == "closed")`,
			want: true,
		},
		{
			name: "tags length check",
			expr: "input.tags.length == 2",
			want: true,
		},
		{
			name: "first tag check",
			expr: `input.tags[0] == "urgent"`,
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := evalExpr(t, tt.expr, vars)
			assertBool(t, tt.name, result, tt.want)
		})
	}
}

func TestIntegration_NestedArrayIndex(t *testing.T) {
	vars := map[string]any{
		"data": map[string]any{
			"items": []any{
				map[string]any{"name": "first"},
				map[string]any{"name": "second"},
			},
		},
	}
	result := evalExpr(t, `data.items[0].name == "first"`, vars)
	assertBool(t, "nested array member", result, true)

	result = evalExpr(t, `data.items[1].name == "second"`, vars)
	assertBool(t, "nested array member [1]", result, true)
}

func TestIntegration_MultipleLogicalOperators(t *testing.T) {
	vars := map[string]any{
		"a": true,
		"b": false,
		"c": true,
	}

	result := evalExpr(t, "a && b || c", vars)
	// Precedence: (a && b) || c => (true && false) || true => false || true => true
	assertBool(t, "a && b || c", result, true)

	result = evalExpr(t, "a || b && c", vars)
	// Precedence: a || (b && c) => true || (false && true) => true || false => true
	assertBool(t, "a || b && c", result, true)
}

func TestIntegration_DeeplyNestedAccess(t *testing.T) {
	vars := map[string]any{
		"a": map[string]any{
			"b": map[string]any{
				"c": map[string]any{
					"d": "deep_value",
				},
			},
		},
	}
	result := evalExpr(t, `a.b.c.d == "deep_value"`, vars)
	assertBool(t, "deep nested access", result, true)
}

func TestIntegration_ArrayInArrayLiteral(t *testing.T) {
	result := evalExpr(t, `3 in [1, 2, 3, 4, 5]`, nil)
	assertBool(t, "3 in literal array", result, true)

	result = evalExpr(t, `99 in [1, 2, 3]`, nil)
	assertBool(t, "99 not in literal array", result, false)
}

func TestIntegration_ChainedNullCoalescing(t *testing.T) {
	vars := map[string]any{}
	result := evalExpr(t, `a ?? b ?? "default"`, vars)
	assertString(t, "chained ??", result, "default")

	vars["b"] = "from_b"
	result = evalExpr(t, `a ?? b ?? "default"`, vars)
	assertString(t, "chained ?? with b", result, "from_b")
}

func TestIntegration_StringIndexAccess(t *testing.T) {
	// Index with a string key on a map
	vars := map[string]any{
		"data": map[string]any{
			"key1": "value1",
		},
	}
	result := evalExpr(t, `data["key1"]`, vars)
	assertString(t, `data["key1"]`, result, "value1")
}

func TestIntegration_NegativeNumberInExpression(t *testing.T) {
	result := evalExpr(t, "-5 == -5", nil)
	assertBool(t, "-5 == -5", result, true)

	result = evalExpr(t, "-3 < 0", nil)
	assertBool(t, "-3 < 0", result, true)

	result = evalExpr(t, "-1.5 > -2.5", nil)
	assertBool(t, "-1.5 > -2.5", result, true)
}

func TestIntegration_BooleanLiterals(t *testing.T) {
	result := evalExpr(t, "true", nil)
	if result != true {
		t.Errorf("got %v, want true", result)
	}

	result = evalExpr(t, "false", nil)
	if result != false {
		t.Errorf("got %v, want false", result)
	}

	result = evalExpr(t, "null", nil)
	if result != nil {
		t.Errorf("got %v, want nil", result)
	}
}

func TestIntegration_IndexWithExpression(t *testing.T) {
	// Using an expression as an array index
	vars := map[string]any{
		"arr": []any{"a", "b", "c"},
		"idx": float64(1),
	}
	result := evalExpr(t, "arr[idx]", vars)
	assertString(t, "arr[idx]", result, "b")
}

// ---------------------------------------------------------------------------
// AST String() coverage
// ---------------------------------------------------------------------------

func TestAST_StringRepresentations(t *testing.T) {
	tests := []struct {
		name string
		expr Expr
		want string
	}{
		{
			name: "binary expr",
			expr: &BinaryExpr{
				Left: &LiteralExpr{Value: float64(1)},
				Op:   TokenEq,
				Right: &LiteralExpr{Value: float64(2)},
			},
			want: "(1 == 2)",
		},
		{
			name: "unary expr",
			expr: &UnaryExpr{
				Op:      TokenNot,
				Operand: &IdentExpr{Name: "x"},
			},
			want: "(!x)",
		},
		{
			name: "literal nil",
			expr: &LiteralExpr{Value: nil},
			want: "null",
		},
		{
			name: "literal string",
			expr: &LiteralExpr{Value: "hello"},
			want: "hello",
		},
		{
			name: "ident",
			expr: &IdentExpr{Name: "foo"},
			want: "foo",
		},
		{
			name: "member",
			expr: &MemberExpr{
				Object:   &IdentExpr{Name: "a"},
				Property: "b",
			},
			want: "a.b",
		},
		{
			name: "index",
			expr: &IndexExpr{
				Object: &IdentExpr{Name: "a"},
				Index:  &LiteralExpr{Value: float64(0)},
			},
			want: "a[0]",
		},
		{
			name: "array literal",
			expr: &ArrayLiteral{
				Elements: []Expr{
					&LiteralExpr{Value: float64(1)},
					&LiteralExpr{Value: float64(2)},
				},
			},
			want: "[2 elements]",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.expr.String()
			if got != tt.want {
				t.Errorf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Edge cases
// ---------------------------------------------------------------------------

func TestEval_HasWithNonStringKey(t *testing.T) {
	vars := map[string]any{
		"obj": map[string]any{"key": "val"},
	}
	// has with non-string RHS returns false
	result := evalExpr(t, "obj has 123", vars) // 123 is float64, not string
	assertBool(t, "has non-string key", result, false)
}

func TestEval_InWithNonArray(t *testing.T) {
	// "in" with non-array/slice RHS returns false
	result := evalExpr(t, `"x" in "xyz"`, nil)
	assertBool(t, "in non-array", result, false)
}

func TestEval_StartsWithNonString(t *testing.T) {
	result := evalExpr(t, "1 startsWith 1", nil)
	assertBool(t, "startsWith non-string", result, false)
}

func TestEval_EndsWithNonString(t *testing.T) {
	result := evalExpr(t, "1 endsWith 1", nil)
	assertBool(t, "endsWith non-string", result, false)
}

func TestEval_NegativeArrayIndex(t *testing.T) {
	vars := map[string]any{
		"arr": []any{"a", "b", "c"},
	}
	result := evalExpr(t, "arr[-1]", vars)
	assertNil(t, "arr[-1]", result)
}

func TestEval_IndexOnNonSlice(t *testing.T) {
	vars := map[string]any{
		"x": "not_a_slice",
	}
	result := evalExpr(t, "x[0]", vars)
	assertNil(t, "x[0] on string", result)
}

func TestEval_MemberOnNonMap(t *testing.T) {
	vars := map[string]any{
		"x": float64(42),
	}
	result := evalExpr(t, "x.prop", vars)
	assertNil(t, "x.prop on number", result)
}

func TestEval_LengthOnNonContainer(t *testing.T) {
	vars := map[string]any{
		"x": float64(42),
	}
	result := evalExpr(t, "x.length", vars)
	assertNil(t, "length on number", result)
}

func TestEval_CompareIncompatibleTypes(t *testing.T) {
	// Comparing string to number with > returns false (not comparable)
	result := evalExpr(t, `"abc" > 5`, nil)
	assertBool(t, "string > number", result, false)
}

func TestParse_NegativeFloat(t *testing.T) {
	ast, err := Parse("-3.14")
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	lit, ok := ast.(*LiteralExpr)
	if !ok {
		t.Fatalf("expected *LiteralExpr, got %T", ast)
	}
	f, ok := lit.Value.(float64)
	if !ok {
		t.Fatalf("expected float64, got %T", lit.Value)
	}
	if f != -3.14 {
		t.Errorf("got %v, want -3.14", f)
	}
}

func TestEval_NegativeInArray(t *testing.T) {
	result := evalExpr(t, "-1 in [-1, 0, 1]", nil)
	assertBool(t, "-1 in [-1, 0, 1]", result, true)
}
