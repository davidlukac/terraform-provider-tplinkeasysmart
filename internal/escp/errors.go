package escp

import (
	"errors"
	"fmt"
)

var (
	// ErrNoReply means the switch did not answer within the timeout.
	ErrNoReply = errors.New("escp: no reply from switch")
	// ErrReplyTooLarge means a fragmented reply grew past the sane limit.
	ErrReplyTooLarge = errors.New("escp: fragmented reply is larger than any switch sends")
	// ErrRejected means the switch rejected a write. Wrong credentials are one reason, but
	// any non-zero reply code lands here (a LAG with ports outside its group's range, for
	// example, is refused with code 4 although the credentials are fine); the
	// *RejectedError that wraps it carries the code.
	ErrRejected = errors.New("escp: switch rejected the request")
	// ErrInvalid means the caller's argument was refused before any write was sent: a port or
	// id out of range, a rate that is not a multiple of the step, a combination the switch
	// cannot keep. The message names the value and what is allowed; test for the class with
	// errors.Is(err, ErrInvalid). It is never returned for a reply the client cannot parse,
	// nor for a failure of the switch.
	ErrInvalid = errors.New("escp: invalid argument")
)

// invalidError is an ErrInvalid with its own message.
type invalidError struct{ msg string }

func (e *invalidError) Error() string { return e.msg }

// Is makes errors.Is(err, ErrInvalid) true for every refused argument.
func (e *invalidError) Is(target error) bool { return target == ErrInvalid }

// invalidf builds an error that matches ErrInvalid and reads as the formatted message.
func invalidf(format string, args ...any) error {
	return &invalidError{msg: fmt.Sprintf(format, args...)}
}

// RejectedError is a write the switch answered with a non-zero code. It matches
// ErrRejected (errors.Is) for callers that treat every refusal alike, and names the
// code for those that want to know which one it was. Reason, when set, says why
// the switch refused (only for codes whose cause has been established).
type RejectedError struct {
	Code   int32
	Reason string
}

// Error names the code and, when the client knows it, the reason; for an unknown code it says only that
// the credentials, the setting or the value may be at fault, because the switch does not say which.
func (e *RejectedError) Error() string {
	if e.Reason != "" {
		return fmt.Sprintf("%s (error code %d): %s", ErrRejected.Error(), e.Code, e.Reason)
	}
	return fmt.Sprintf("%s (error code %d): wrong credentials, or the switch refuses this setting or value", ErrRejected.Error(), e.Code)
}

// Is makes errors.Is(err, ErrRejected) true for every rejected write.
func (e *RejectedError) Is(target error) bool { return target == ErrRejected }
