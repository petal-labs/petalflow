package cli

import "fmt"

// ExitError is an error that carries a specific process exit code.
// Cobra's RunE returns this to signal the desired exit code to main.
type ExitError struct {
	Code    int
	Message string
}

func (e *ExitError) Error() string {
	return e.Message
}

// exitError creates a new ExitError with the given code and formatted message.
func exitError(code int, format string, args ...any) *ExitError {
	return &ExitError{
		Code:    code,
		Message: fmt.Sprintf(format, args...),
	}
}
