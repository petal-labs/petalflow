// Package expr provides a minimal, safe expression language for conditional
// routing in PetalFlow graphs. Expressions are stateless and side-effect-free.
package expr

import "fmt"

// Expr is the interface implemented by all AST nodes.
type Expr interface {
	expr() // marker method
	String() string
}

// BinaryExpr represents a binary operation (e.g. a == b, a && b).
type BinaryExpr struct {
	Left  Expr
	Op    TokenKind
	Right Expr
}

func (e *BinaryExpr) expr() {}
func (e *BinaryExpr) String() string {
	return fmt.Sprintf("(%s %s %s)", e.Left, e.Op, e.Right)
}

// UnaryExpr represents a unary operation (e.g. !a).
type UnaryExpr struct {
	Op      TokenKind
	Operand Expr
}

func (e *UnaryExpr) expr() {}
func (e *UnaryExpr) String() string {
	return fmt.Sprintf("(%s%s)", e.Op, e.Operand)
}

// LiteralExpr represents a literal value (number, string, bool, null).
type LiteralExpr struct {
	Value any // float64, string, bool, or nil
}

func (e *LiteralExpr) expr() {}
func (e *LiteralExpr) String() string {
	if e.Value == nil {
		return "null"
	}
	return fmt.Sprintf("%v", e.Value)
}

// IdentExpr represents an identifier (e.g. input, score).
type IdentExpr struct {
	Name string
}

func (e *IdentExpr) expr() {}
func (e *IdentExpr) String() string {
	return e.Name
}

// MemberExpr represents property access (e.g. input.score).
type MemberExpr struct {
	Object   Expr
	Property string
}

func (e *MemberExpr) expr() {}
func (e *MemberExpr) String() string {
	return fmt.Sprintf("%s.%s", e.Object, e.Property)
}

// IndexExpr represents array index access (e.g. input.tags[0]).
type IndexExpr struct {
	Object Expr
	Index  Expr
}

func (e *IndexExpr) expr() {}
func (e *IndexExpr) String() string {
	return fmt.Sprintf("%s[%s]", e.Object, e.Index)
}

// ArrayLiteral represents an inline array (e.g. ["a", "b"]).
type ArrayLiteral struct {
	Elements []Expr
}

func (e *ArrayLiteral) expr() {}
func (e *ArrayLiteral) String() string {
	return fmt.Sprintf("[%d elements]", len(e.Elements))
}
