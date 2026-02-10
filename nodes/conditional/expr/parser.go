package expr

import (
	"fmt"
	"strconv"
)

// Parse parses an expression string into an AST.
func Parse(input string) (Expr, error) {
	tokens, err := Lex(input)
	if err != nil {
		return nil, err
	}
	p := &parser{tokens: tokens}
	expr, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	if p.current().Kind != TokenEOF {
		return nil, fmt.Errorf("unexpected token %s at position %d", p.current().Kind, p.current().Pos)
	}
	return expr, nil
}

type parser struct {
	tokens []Token
	pos    int
}

func (p *parser) current() Token {
	if p.pos >= len(p.tokens) {
		return Token{Kind: TokenEOF}
	}
	return p.tokens[p.pos]
}

func (p *parser) advance() Token {
	tok := p.current()
	if p.pos < len(p.tokens) {
		p.pos++
	}
	return tok
}

func (p *parser) expect(kind TokenKind) (Token, error) {
	tok := p.current()
	if tok.Kind != kind {
		return tok, fmt.Errorf("expected %s but got %s at position %d", kind, tok.Kind, tok.Pos)
	}
	p.advance()
	return tok, nil
}

// Precedence levels (low to high):
// 1. ??  (null coalescing)
// 2. || (logical or)
// 3. && (logical and)
// 4. ==, != (equality)
// 5. <, >, <=, >= (comparison)
// 6. in, has, contains, startsWith, endsWith, matches (membership/string)
// 7. ! (unary not)
// 8. member access, index access (postfix)

func (p *parser) parseExpr() (Expr, error) {
	return p.parseNullCoalescing()
}

func (p *parser) parseNullCoalescing() (Expr, error) {
	left, err := p.parseOr()
	if err != nil {
		return nil, err
	}
	for p.current().Kind == TokenNullCoal {
		op := p.advance()
		right, err := p.parseOr()
		if err != nil {
			return nil, err
		}
		left = &BinaryExpr{Left: left, Op: op.Kind, Right: right}
	}
	return left, nil
}

func (p *parser) parseOr() (Expr, error) {
	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	for p.current().Kind == TokenOr {
		op := p.advance()
		right, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		left = &BinaryExpr{Left: left, Op: op.Kind, Right: right}
	}
	return left, nil
}

func (p *parser) parseAnd() (Expr, error) {
	left, err := p.parseEquality()
	if err != nil {
		return nil, err
	}
	for p.current().Kind == TokenAnd {
		op := p.advance()
		right, err := p.parseEquality()
		if err != nil {
			return nil, err
		}
		left = &BinaryExpr{Left: left, Op: op.Kind, Right: right}
	}
	return left, nil
}

func (p *parser) parseEquality() (Expr, error) {
	left, err := p.parseComparison()
	if err != nil {
		return nil, err
	}
	for p.current().Kind == TokenEq || p.current().Kind == TokenNeq {
		op := p.advance()
		right, err := p.parseComparison()
		if err != nil {
			return nil, err
		}
		left = &BinaryExpr{Left: left, Op: op.Kind, Right: right}
	}
	return left, nil
}

func (p *parser) parseComparison() (Expr, error) {
	left, err := p.parseMembership()
	if err != nil {
		return nil, err
	}
	for p.current().Kind == TokenGt || p.current().Kind == TokenGte ||
		p.current().Kind == TokenLt || p.current().Kind == TokenLte {
		op := p.advance()
		right, err := p.parseMembership()
		if err != nil {
			return nil, err
		}
		left = &BinaryExpr{Left: left, Op: op.Kind, Right: right}
	}
	return left, nil
}

func (p *parser) parseMembership() (Expr, error) {
	left, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	switch p.current().Kind {
	case TokenIn, TokenHas, TokenContains, TokenStartsWith, TokenEndsWith, TokenMatches:
		op := p.advance()
		right, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		left = &BinaryExpr{Left: left, Op: op.Kind, Right: right}
	}
	return left, nil
}

func (p *parser) parseUnary() (Expr, error) {
	if p.current().Kind == TokenNot {
		op := p.advance()
		operand, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return &UnaryExpr{Op: op.Kind, Operand: operand}, nil
	}
	return p.parsePostfix()
}

func (p *parser) parsePostfix() (Expr, error) {
	expr, err := p.parsePrimary()
	if err != nil {
		return nil, err
	}

	for {
		switch p.current().Kind {
		case TokenDot:
			p.advance()
			name, err := p.expect(TokenIdent)
			if err != nil {
				// Also allow keywords as property names (e.g. input.has)
				tok := p.current()
				if isKeywordToken(tok.Kind) {
					p.advance()
					expr = &MemberExpr{Object: expr, Property: tok.Value}
					continue
				}
				return nil, err
			}
			expr = &MemberExpr{Object: expr, Property: name.Value}

		case TokenLBracket:
			p.advance()
			index, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			if _, err := p.expect(TokenRBracket); err != nil {
				return nil, err
			}
			expr = &IndexExpr{Object: expr, Index: index}

		default:
			return expr, nil
		}
	}
}

func (p *parser) parsePrimary() (Expr, error) {
	tok := p.current()

	switch tok.Kind {
	case TokenNumber:
		p.advance()
		val, err := strconv.ParseFloat(tok.Value, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid number %q at position %d", tok.Value, tok.Pos)
		}
		return &LiteralExpr{Value: val}, nil

	case TokenString:
		p.advance()
		return &LiteralExpr{Value: tok.Value}, nil

	case TokenTrue:
		p.advance()
		return &LiteralExpr{Value: true}, nil

	case TokenFalse:
		p.advance()
		return &LiteralExpr{Value: false}, nil

	case TokenNull:
		p.advance()
		return &LiteralExpr{Value: nil}, nil

	case TokenIdent:
		p.advance()
		return &IdentExpr{Name: tok.Value}, nil

	case TokenLParen:
		p.advance()
		expr, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(TokenRParen); err != nil {
			return nil, err
		}
		return expr, nil

	case TokenLBracket:
		return p.parseArrayLiteral()

	default:
		return nil, fmt.Errorf("unexpected token %s at position %d", tok.Kind, tok.Pos)
	}
}

func (p *parser) parseArrayLiteral() (Expr, error) {
	p.advance() // skip [
	var elements []Expr

	if p.current().Kind == TokenRBracket {
		p.advance()
		return &ArrayLiteral{Elements: elements}, nil
	}

	for {
		elem, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		elements = append(elements, elem)

		if p.current().Kind != TokenComma {
			break
		}
		p.advance() // skip comma
	}

	if _, err := p.expect(TokenRBracket); err != nil {
		return nil, err
	}

	return &ArrayLiteral{Elements: elements}, nil
}

func isKeywordToken(kind TokenKind) bool {
	switch kind {
	case TokenIn, TokenHas, TokenContains, TokenStartsWith,
		TokenEndsWith, TokenMatches, TokenTrue, TokenFalse, TokenNull:
		return true
	}
	return false
}
