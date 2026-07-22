//go:build !windows

package discovery

import (
	"fmt"
	"net"
	"syscall"
)

// enableBroadcast sets SO_BROADCAST on pc's underlying socket, required
// before sending to any broadcast address (both a subnet-directed broadcast
// like 192.168.0.255 and the global 255.255.255.255 are rejected by the OS
// otherwise).
func enableBroadcast(pc net.PacketConn) error {
	sc, ok := pc.(interface {
		SyscallConn() (syscall.RawConn, error)
	})
	if !ok {
		return fmt.Errorf("discovery: connection has no raw syscall access")
	}
	raw, err := sc.SyscallConn()
	if err != nil {
		return err
	}
	var sockErr error
	if err := raw.Control(func(fd uintptr) {
		sockErr = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_BROADCAST, 1)
	}); err != nil {
		return err
	}
	return sockErr
}
