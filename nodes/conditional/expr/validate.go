package expr

// ValidateSyntax checks whether an expression string is syntactically valid.
// Returns nil if valid, or a parse error describing the problem.
func ValidateSyntax(expression string) error {
	_, err := Parse(expression)
	return err
}
