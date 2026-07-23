//go:build !windows

package discovery

import (
	"context"
	"net"
	"syscall"
)

// listenPacketReusable is like net.ListenPacket, but sets SO_REUSEADDR and
// SO_REUSEPORT on the socket before binding. On the BSD/Darwin/Linux socket
// model, that lets more than one process bind the same UDP port and each
// still receive its own copy of every multicast/broadcast datagram sent to
// it (unlike TCP, where SO_REUSEPORT load-balances connections across
// listeners, multicast/broadcast traffic is delivered to every one of
// them). Without this, a second instance of this program on the same
// machine fails outright with "address already in use" on Port -- fine for
// real deployment (one instance per machine), but it also blocks running
// two local instances against each other for development/testing.
func listenPacketReusable(ctx context.Context, network, address string) (net.PacketConn, error) {
	lc := net.ListenConfig{
		Control: func(_, _ string, c syscall.RawConn) error {
			var sockErr error
			if err := c.Control(func(fd uintptr) {
				_ = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 1)
				sockErr = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_REUSEPORT, 1)
			}); err != nil {
				return err
			}
			return sockErr
		},
	}
	return lc.ListenPacket(ctx, network, address)
}
