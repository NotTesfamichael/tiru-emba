//go:build windows

package discovery

import (
	"fmt"
	"net"
	"syscall"
)

// enableBroadcast sets SO_BROADCAST on pc's underlying socket. See the unix
// variant for why this is needed; the only difference here is that
// syscall.SetsockoptInt takes a syscall.Handle, not a plain int, on Windows.
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
		sockErr = syscall.SetsockoptInt(syscall.Handle(fd), syscall.SOL_SOCKET, syscall.SO_BROADCAST, 1)
	}); err != nil {
		return err
	}
	return sockErr
}
