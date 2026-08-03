package safeerr

// Error preserves an operational error's cause while exposing only a stable,
// sanitized message through the error interface.
type Error struct {
	message string
	cause   error
}

func (e *Error) Error() string {
	return e.message
}

func (e *Error) Unwrap() error {
	return e.cause
}

// Wrap returns nil when cause is nil. Otherwise it returns an error whose
// public message is sanitized while errors.Is and errors.As can still inspect
// the original cause.
func Wrap(message string, cause error) error {
	if cause == nil {
		return nil
	}

	return &Error{
		message: message,
		cause:   cause,
	}
}
