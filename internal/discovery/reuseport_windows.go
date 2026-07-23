//go:build windows

package discovery

import (
	"context"
	"net"
	"syscall"
)

// listenPacketReusable is like net.ListenPacket, but sets SO_REUSEADDR on
// the socket before binding, so more than one instance of this program can
// run on the same machine for local development/testing. See the unix
// variant for the full rationale; Windows has no SO_REUSEPORT, but
// SO_REUSEADDR alone is sufficient for multiple UDP multicast receivers.
func listenPacketReusable(ctx context.Context, network, address string) (net.PacketConn, error) {
	lc := net.ListenConfig{
		Control: func(_, _ string, c syscall.RawConn) error {
			var sockErr error
			if err := c.Control(func(fd uintptr) {
				sockErr = syscall.SetsockoptInt(syscall.Handle(fd), syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 1)
			}); err != nil {
				return err
			}
			return sockErr
		},
	}
	return lc.ListenPacket(ctx, network, address)
}
