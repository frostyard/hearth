//go:build !linux && !darwin

package lodge

import (
	"errors"
	"net"
)

// peerUID reports that this platform cannot read the peer of a socket
// connection. Listener refuses every connection that it cannot check, so the
// lodge socket accepts nothing here.
func peerUID(net.Conn) (int, error) {
	return 0, errors.New("this platform cannot read the user id of a socket's peer")
}
