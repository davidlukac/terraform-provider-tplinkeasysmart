//go:build linux || darwin || freebsd

package escp

import (
	"syscall"

	"golang.org/x/sys/unix"
)

// shareSocket lets several processes (several provider instances, the vendor
// utility) bind the reply port at once. The switch broadcasts its replies, so
// every one of them receives every reply and keeps the ones addressed to it.
func shareSocket(_, _ string, c syscall.RawConn) error {
	var sockErr error
	if err := c.Control(func(fd uintptr) { sockErr = setReuse(int(fd)) }); err != nil {
		return err
	}
	return sockErr
}

// setReuse enables address and port reuse on a socket.
func setReuse(fd int) error {
	if err := unix.SetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_REUSEADDR, 1); err != nil {
		return err
	}
	return unix.SetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_REUSEPORT, 1)
}
