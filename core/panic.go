package core

import (
	"fmt"
	"runtime/debug"
)

// PanicError reports a panic recovered from module code.
type PanicError struct {
	Value any
	Stack []byte
}

func (e *PanicError) Error() string { return fmt.Sprintf("panic: %v", e.Value) }

// protect runs fn and converts a panic into a *PanicError.
func protect(fn func() error) (err error) {
	defer func() {
		if v := recover(); v != nil {
			err = &PanicError{Value: v, Stack: debug.Stack()}
		}
	}()
	return fn()
}
