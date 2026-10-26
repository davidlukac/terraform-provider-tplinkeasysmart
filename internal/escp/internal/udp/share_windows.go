//go:build windows

package udp

import "syscall"

// Share lets several processes bind the reply port at once (see the Unix
// variant); Windows has no SO_REUSEPORT, SO_REUSEADDR gives the same sharing.
func Share(_, _ string, c syscall.RawConn) error {
	var sockErr error
	if err := c.Control(func(fd uintptr) {
		sockErr = syscall.SetsockoptInt(syscall.Handle(fd), syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 1)
	}); err != nil {
		return err
	}
	return sockErr
}
