package escp

import (
	"errors"
	"fmt"
	"net/netip"
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
	// ErrWrongSwitch means the device that answered is not the one the configuration names:
	// Config.MAC is set and the reply carries another MAC address. Nothing was written to it.
	// The *WrongSwitchError that wraps it names both addresses.
	ErrWrongSwitch = errors.New("escp: the device that answered is not the configured switch")
	// ErrNotFound means no switch with the wanted MAC address answered a discovery probe on
	// this network (Locate, or a client configured with a MAC and no working address).
	ErrNotFound = errors.New("escp: no switch with that MAC address answered")
)

// WrongSwitchError is the refusal to talk to a device whose MAC address is not Config.MAC. It
// matches ErrWrongSwitch.
type WrongSwitchError struct {
	Want, Got MAC
	IP        netip.Addr // where the other device answered from; invalid when unknown
}

// Error names both addresses and where the other device was.
func (e *WrongSwitchError) Error() string {
	where := ""
	if e.IP.IsValid() {
		where = " at " + e.IP.String()
	}
	return fmt.Sprintf("%s: the device%s has MAC %s, the configuration names %s", ErrWrongSwitch.Error(), where, e.Got, e.Want)
}

// Is makes errors.Is(err, ErrWrongSwitch) true.
func (e *WrongSwitchError) Is(target error) bool { return target == ErrWrongSwitch }

// invalidError is an ErrInvalid with its own message.
type invalidError struct{ msg string }

func (e *invalidError) Error() string { return e.msg }

// Is makes errors.Is(err, ErrInvalid) true for every refused argument.
func (e *invalidError) Is(target error) bool { return target == ErrInvalid }

// invalidf builds an error that matches ErrInvalid and reads as the formatted message.
func invalidf(format string, args ...any) error {
	return &invalidError{msg: fmt.Sprintf(format, args...)}
}

// ReplyCode is the result code of a switch reply: zero when the request was accepted,
// otherwise the code the switch gives for refusing it. Only some codes have a known cause
// (see RejectedError.Reason); the rest are reported as they came.
type ReplyCode int32

// RejectedError is a write the switch answered with a non-zero code. It matches
// ErrRejected (errors.Is) for callers that treat every refusal alike, and names the
// code for those that want to know which one it was. Reason, when set, says why
// the switch refused (only for codes whose cause has been established).
type RejectedError struct {
	Code   ReplyCode
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
