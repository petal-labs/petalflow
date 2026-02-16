package expr

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// TokenKind identifies the type of a lexer token.
type TokenKind int

const (
	// Literals and identifiers
	TokenIdent  TokenKind = iota // identifier
	TokenNumber                  // numeric literal
	TokenString                  // string literal

	// Operators
	TokenEq         // ==
	TokenNeq        // !=
	TokenGt         // >
	TokenGte        // >=
	TokenLt         // <
	TokenLte        // <=
	TokenAnd        // &&
	TokenOr         // ||
	TokenNot        // !
	TokenNullCoal   // ??
	TokenIn         // in
	TokenHas        // has
	TokenContains   // contains
	TokenStartsWith // startsWith
	TokenEndsWith   // endsWith
	TokenMatches    // matches

	// Delimiters
	TokenDot      // .
	TokenLBracket // [
	TokenRBracket // ]
	TokenLParen   // (
	TokenRParen   // )
	TokenComma    // ,

	// Special
	TokenTrue  // true
	TokenFalse // false
	TokenNull  // null
	TokenEOF
)

var tokenNames = map[TokenKind]string{
	TokenIdent:      "identifier",
	TokenNumber:     "number",
	TokenString:     "string",
	TokenEq:         "==",
	TokenNeq:        "!=",
	TokenGt:         ">",
	TokenGte:        ">=",
	TokenLt:         "<",
	TokenLte:        "<=",
	TokenAnd:        "&&",
	TokenOr:         "||",
	TokenNot:        "!",
	TokenNullCoal:   "??",
	TokenIn:         "in",
	TokenHas:        "has",
	TokenContains:   "contains",
	TokenStartsWith: "startsWith",
	TokenEndsWith:   "endsWith",
	TokenMatches:    "matches",
	TokenDot:        ".",
	TokenLBracket:   "[",
	TokenRBracket:   "]",
	TokenLParen:     "(",
	TokenRParen:     ")",
	TokenComma:      ",",
	TokenTrue:       "true",
	TokenFalse:      "false",
	TokenNull:       "null",
	TokenEOF:        "EOF",
}

func (k TokenKind) String() string {
	if name, ok := tokenNames[k]; ok {
		return name
	}
	return fmt.Sprintf("token(%d)", int(k))
}

// Token is a lexed token with position information.
type Token struct {
	Kind  TokenKind
	Value string // raw text of the token
	Pos   int    // byte offset in source
}

// keywords maps keyword strings to their token kinds.
var keywords = map[string]TokenKind{
	"in":         TokenIn,
	"has":        TokenHas,
	"contains":   TokenContains,
	"startsWith": TokenStartsWith,
	"endsWith":   TokenEndsWith,
	"matches":    TokenMatches,
	"true":       TokenTrue,
	"false":      TokenFalse,
	"null":       TokenNull,
}

// Lexer tokenizes expression strings.
type Lexer struct {
	src    string
	pos    int
	tokens []Token
}

// Lex tokenizes the input string and returns all tokens.
func Lex(src string) ([]Token, error) {
	l := &Lexer{src: src}
	if err := l.lexAll(); err != nil {
		return nil, err
	}
	return l.tokens, nil
}

func (l *Lexer) lexAll() error {
	for {
		l.skipWhitespace()
		if l.pos >= len(l.src) {
			l.tokens = append(l.tokens, Token{Kind: TokenEOF, Pos: l.pos})
			return nil
		}

		ch, _ := utf8.DecodeRuneInString(l.src[l.pos:])
		if l.tryEmitDoubleCharToken(ch) || l.tryEmitSingleCharToken(ch) {
			continue
		}

		switch {
		case ch == '"':
			if err := l.lexString(); err != nil {
				return err
			}
		case isDigit(ch) || (ch == '-' && l.isNegativeNumber()):
			l.lexNumber()
		case isIdentStart(ch):
			l.lexIdent()
		default:
			return fmt.Errorf("unexpected character %q at position %d", string(ch), l.pos)
		}
	}
}

func (l *Lexer) tryEmitDoubleCharToken(ch rune) bool {
	switch {
	case ch == '=' && l.peekNext() == '=':
		l.emit2(TokenEq)
	case ch == '!' && l.peekNext() == '=':
		l.emit2(TokenNeq)
	case ch == '>' && l.peekNext() == '=':
		l.emit2(TokenGte)
	case ch == '<' && l.peekNext() == '=':
		l.emit2(TokenLte)
	case ch == '&' && l.peekNext() == '&':
		l.emit2(TokenAnd)
	case ch == '|' && l.peekNext() == '|':
		l.emit2(TokenOr)
	case ch == '?' && l.peekNext() == '?':
		l.emit2(TokenNullCoal)
	default:
		return false
	}
	return true
}

func (l *Lexer) tryEmitSingleCharToken(ch rune) bool {
	switch ch {
	case '>':
		l.emit1(TokenGt)
	case '<':
		l.emit1(TokenLt)
	case '!':
		l.emit1(TokenNot)
	case '.':
		l.emit1(TokenDot)
	case '[':
		l.emit1(TokenLBracket)
	case ']':
		l.emit1(TokenRBracket)
	case '(':
		l.emit1(TokenLParen)
	case ')':
		l.emit1(TokenRParen)
	case ',':
		l.emit1(TokenComma)
	default:
		return false
	}
	return true
}

func (l *Lexer) peekNext() byte {
	next := l.pos + 1
	if next >= len(l.src) {
		return 0
	}
	return l.src[next]
}

func (l *Lexer) emit1(kind TokenKind) {
	l.tokens = append(l.tokens, Token{Kind: kind, Value: l.src[l.pos : l.pos+1], Pos: l.pos})
	l.pos++
}

func (l *Lexer) emit2(kind TokenKind) {
	l.tokens = append(l.tokens, Token{Kind: kind, Value: l.src[l.pos : l.pos+2], Pos: l.pos})
	l.pos += 2
}

func (l *Lexer) skipWhitespace() {
	for l.pos < len(l.src) {
		ch, size := utf8.DecodeRuneInString(l.src[l.pos:])
		if !unicode.IsSpace(ch) {
			break
		}
		l.pos += size
	}
}

func (l *Lexer) lexString() error {
	start := l.pos
	l.pos++ // skip opening quote
	var sb strings.Builder

	for l.pos < len(l.src) {
		ch := l.src[l.pos]
		if ch == '\\' {
			l.pos++
			if l.pos >= len(l.src) {
				return fmt.Errorf("unterminated string at position %d", start)
			}
			esc := l.src[l.pos]
			switch esc {
			case '"', '\\', '/':
				sb.WriteByte(esc)
			case 'n':
				sb.WriteByte('\n')
			case 't':
				sb.WriteByte('\t')
			case 'r':
				sb.WriteByte('\r')
			default:
				sb.WriteByte('\\')
				sb.WriteByte(esc)
			}
			l.pos++
			continue
		}
		if ch == '"' {
			l.pos++ // skip closing quote
			l.tokens = append(l.tokens, Token{
				Kind:  TokenString,
				Value: sb.String(),
				Pos:   start,
			})
			return nil
		}
		sb.WriteByte(ch)
		l.pos++
	}

	return fmt.Errorf("unterminated string at position %d", start)
}

func (l *Lexer) lexNumber() {
	start := l.pos
	if l.src[l.pos] == '-' {
		l.pos++
	}
	for l.pos < len(l.src) && isDigit(rune(l.src[l.pos])) {
		l.pos++
	}
	// decimal part
	if l.pos < len(l.src) && l.src[l.pos] == '.' {
		l.pos++
		for l.pos < len(l.src) && isDigit(rune(l.src[l.pos])) {
			l.pos++
		}
	}
	l.tokens = append(l.tokens, Token{Kind: TokenNumber, Value: l.src[start:l.pos], Pos: start})
}

func (l *Lexer) lexIdent() {
	start := l.pos
	for l.pos < len(l.src) {
		ch, size := utf8.DecodeRuneInString(l.src[l.pos:])
		if !isIdentPart(ch) {
			break
		}
		l.pos += size
	}
	word := l.src[start:l.pos]
	kind := TokenIdent
	if kw, ok := keywords[word]; ok {
		kind = kw
	}
	l.tokens = append(l.tokens, Token{Kind: kind, Value: word, Pos: start})
}

// isNegativeNumber checks if a '-' at current position is a negative number
// (not a subtraction). It's a negative number if preceded by an operator,
// opening paren/bracket, comma, or at the start of input.
func (l *Lexer) isNegativeNumber() bool {
	if len(l.tokens) == 0 {
		return true
	}
	last := l.tokens[len(l.tokens)-1].Kind
	switch last {
	case TokenEq, TokenNeq, TokenGt, TokenGte, TokenLt, TokenLte,
		TokenAnd, TokenOr, TokenNot, TokenNullCoal,
		TokenLParen, TokenLBracket, TokenComma,
		TokenIn, TokenHas, TokenContains, TokenStartsWith,
		TokenEndsWith, TokenMatches:
		return true
	}
	return false
}

func isDigit(ch rune) bool {
	return ch >= '0' && ch <= '9'
}

func isIdentStart(ch rune) bool {
	return ch == '_' || unicode.IsLetter(ch)
}

func isIdentPart(ch rune) bool {
	return ch == '_' || unicode.IsLetter(ch) || unicode.IsDigit(ch)
}
