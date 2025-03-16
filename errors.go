package dman

import "fmt"

type Err struct {
	err   string
	cause error
	fatal bool
}

func (fe *Err) Error() string {
	return fmt.Sprintf("%s: %s", fe.err, fe.cause.Error())
}

func (fe *Err) IsFatal() bool {
	return fe.fatal
}

func (fe *Err) Unwrap() error {
	return fe.cause
}
