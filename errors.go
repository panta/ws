package ws

import (
	"errors"
	"fmt"
)

// sentinelError is a private struct that implements the error "protocol".
type sentinelError struct {
	msg string
}

func (e *sentinelError) Error() string {
	return e.msg
}

// Errorf is a method analogous to fmt.Errorf() that returns an error wrapped around this sentinel.
func (e *sentinelError) Errorf(format string, a ...interface{}) error {
	err := fmt.Errorf(format, a...)
	return sentinelWrappedError{error: err, sentinel: e}
}

// sentinelWrappedError is a private struct providing a custom Is() method.
// This allows for comparisons of the wrapper errors with the sentinel errors using errors.Is().
type sentinelWrappedError struct {
	error
	sentinel *sentinelError
}

// Is enables errors.Is() to check for conformancy to one of our sentinel errors.
func (e sentinelWrappedError) Is(err error) bool {
	return (e.sentinel == err) || errors.Is(e.error, err)
}

// Implement Unwrap method to allow errors.Is and errors.As to work
func (e *sentinelWrappedError) Unwrap() error {
	return e.error
}

// WrapError wraps an error using a sentinel..
func WrapError(err error, sentinel *sentinelError) error {
	return sentinelWrappedError{error: err, sentinel: sentinel}
}

var (
	ErrConnectionClosed = &sentinelError{msg: "connection closed"} // not an error actually
	ErrContextCanceled  = &sentinelError{msg: "context canceled"}
	ErrAbnormalClose    = &sentinelError{msg: "abnormal close"}
	ErrPing             = &sentinelError{msg: "can't send ping"}
	ErrReadMessage      = &sentinelError{msg: "can't read message"}
	ErrSendMessage      = &sentinelError{msg: "can't send message"}
)
