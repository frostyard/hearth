//go:build linux

package lodge

import (
	"fmt"
	"net"
	"syscall"

	"golang.org/x/sys/unix"
)

// peerUID returns the user id of the process at the other end of 1 Unix socket
// connection.
func peerUID(conn net.Conn) (int, error) {
	syscallConn, ok := conn.(syscall.Conn)
	if !ok {
		return 0, fmt.Errorf("the connection has no file descriptor")
	}
	raw, err := syscallConn.SyscallConn()
	if err != nil {
		return 0, fmt.Errorf("read the connection's file descriptor: %w", err)
	}
	var (
		cred    *unix.Ucred
		credErr error
	)
	if err := raw.Control(func(fd uintptr) {
		cred, credErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	}); err != nil {
		return 0, fmt.Errorf("read the connection's file descriptor: %w", err)
	}
	if credErr != nil {
		return 0, fmt.Errorf("read the peer of the connection: %w", credErr)
	}
	return int(cred.Uid), nil
}
