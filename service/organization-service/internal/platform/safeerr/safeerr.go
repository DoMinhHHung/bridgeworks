package safeerr

import "fmt"

type Error struct {
	operation string
	cause     error
}

func Wrap(operation string, cause error) error {
	if cause == nil {
		return nil
	}
	return &Error{operation: operation, cause: cause}
}

func (e *Error) Error() string { return e.operation }
func (e *Error) Unwrap() error { return e.cause }
func New(operation string) error { return fmt.Errorf("%s", operation) }
