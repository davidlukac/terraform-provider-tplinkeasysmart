//go:build linux || darwin || freebsd

package udp

import (
	"syscall"

	"golang.org/x/sys/unix"
)

// Share lets several sockets (several clients, Discover, other processes) bind the
// reply port at once. Every socket sharing the port receives the replies, and each keeps
// only those from its own switch. This was observed on macOS: clients for two switches
// got all of theirs, and so did Discover next to a client. Whether a switch addresses its
// replies to the broadcast address or to the host was not captured.
func Share(_, _ string, c syscall.RawConn) error {
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
