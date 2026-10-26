//go:build !linux && !darwin && !freebsd && !windows

package udp

import "syscall"

// Share is a no-op where no port-sharing option is known: the reply port
// is then bound exclusively, so only one client per host works at a time.
func Share(_, _ string, _ syscall.RawConn) error { return nil }
