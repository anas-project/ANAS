package runner

import (
	"errors"
	"fmt"
)

// unresolvedBindingError marks only a provider choice that a draft config has
// not made yet. Read-only schema boundaries may defer this one condition while
// they validate everything they can resolve deterministically. Invalid values,
// unknown or disabled providers, and incompatible interfaces remain ordinary
// errors and are never deferred.
type unresolvedBindingError struct {
	err error
}

func (e *unresolvedBindingError) Error() string {
	return e.err.Error()
}

func (e *unresolvedBindingError) Unwrap() error {
	return e.err
}

func unresolvedBindingErrorf(format string, args ...any) error {
	return &unresolvedBindingError{err: fmt.Errorf(format, args...)}
}

func canDeferUnresolvedBinding(allow bool, err error) bool {
	if !allow || err == nil {
		return false
	}
	var unresolved *unresolvedBindingError
	return errors.As(err, &unresolved)
}
