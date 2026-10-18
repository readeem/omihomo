// Package peercred reads who is on the other end of a unix socket.
package peercred

import (
	"fmt"
	"net"
	"syscall"
)

// UID returns the effective uid of the process that connected conn, as the
// kernel recorded it at connect time. It cannot be forged by the peer.
func UID(conn *net.UnixConn) (int, error) {
	raw, err := conn.SyscallConn()
	if err != nil {
		return 0, err
	}
	var (
		cred    *syscall.Ucred
		credErr error
	)
	if err := raw.Control(func(fd uintptr) {
		cred, credErr = syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
	}); err != nil {
		return 0, err
	}
	if credErr != nil {
		return 0, fmt.Errorf("read peer credentials: %w", credErr)
	}
	return int(cred.Uid), nil
}
