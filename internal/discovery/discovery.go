// Package discovery implements zero-configuration peer discovery over the
// local Wi-Fi network using IPv4 multicast heartbeats. No central server is
// involved: every client periodically announces itself and listens for the
// same announcements from everyone else on the LAN.
package discovery

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"time"
)

const (
	// GroupAddr is an administratively-scoped (site-local) multicast group,
	// chosen from the 239.0.0.0/8 private-use range so it won't collide with
	// well-known multicast traffic on the network.
	GroupAddr = "239.255.42.99"
	Port      = 9999

	// HeartbeatInterval controls how often we announce ourselves.
	HeartbeatInterval = 2 * time.Second
	// PeerTTL is how long a peer is considered online after its last
	// heartbeat before it's pruned from the registry.
	PeerTTL = 6 * time.Second

	maxDatagramSize = 1024
)

// Heartbeat is the wire format broadcast (multicast) by every running client.
type Heartbeat struct {
	ID      string `json:"id"`      // random per-process instance ID, used to ignore our own packets
	Handle  string `json:"handle"`  // e.g. "@alex"
	TCPPort int     `json:"tcp_port"` // port this peer listens on for direct messages (Phase 2)
}

// PeerSeen is emitted on the output channel every time a heartbeat from
// another instance is received.
type PeerSeen struct {
	ID      string
	Handle  string
	Addr    net.IP
	TCPPort int
	SeenAt  time.Time
}

func groupUDPAddr() *net.UDPAddr {
	return &net.UDPAddr{IP: net.ParseIP(GroupAddr), Port: Port}
}

// multicastInterface picks the first "real" up, multicast-capable, non-loopback
// interface (typically the Wi-Fi adapter). Falling back to nil lets the OS
// choose, which works but is less predictable on multi-homed machines.
func multicastInterface() *net.Interface {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	for _, ifi := range ifaces {
		if ifi.Flags&net.FlagUp == 0 {
			continue
		}
		if ifi.Flags&net.FlagMulticast == 0 {
			continue
		}
		if ifi.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := ifi.Addrs()
		if err != nil || len(addrs) == 0 {
			continue
		}
		return &ifi
	}
	return nil
}

// Broadcast periodically announces hb on the multicast group until ctx is
// canceled. It's meant to be run in its own goroutine.
func Broadcast(ctx context.Context, hb Heartbeat) error {
	conn, err := net.DialUDP("udp4", nil, groupUDPAddr())
	if err != nil {
		return fmt.Errorf("discovery: dial multicast group: %w", err)
	}
	defer conn.Close()

	payload, err := json.Marshal(hb)
	if err != nil {
		return fmt.Errorf("discovery: marshal heartbeat: %w", err)
	}

	ticker := time.NewTicker(HeartbeatInterval)
	defer ticker.Stop()

	// Send one immediately so peers don't wait a full interval to see us.
	if _, err := conn.Write(payload); err != nil {
		return fmt.Errorf("discovery: send heartbeat: %w", err)
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if _, err := conn.Write(payload); err != nil {
				return fmt.Errorf("discovery: send heartbeat: %w", err)
			}
		}
	}
}

// Listen joins the multicast group and emits a PeerSeen on out for every
// heartbeat received from a different instance (selfID is used to filter out
// our own announcements). It's meant to be run in its own goroutine and
// blocks until ctx is canceled.
func Listen(ctx context.Context, selfID string, out chan<- PeerSeen) error {
	conn, err := net.ListenMulticastUDP("udp4", multicastInterface(), groupUDPAddr())
	if err != nil {
		return fmt.Errorf("discovery: listen multicast group: %w", err)
	}
	defer conn.Close()

	// Unblock ReadFromUDP when ctx is canceled.
	go func() {
		<-ctx.Done()
		conn.Close()
	}()

	buf := make([]byte, maxDatagramSize)
	for {
		n, src, err := conn.ReadFromUDP(buf)
		if err != nil {
			select {
			case <-ctx.Done():
				return nil
			default:
				continue
			}
		}

		var hb Heartbeat
		if err := json.Unmarshal(buf[:n], &hb); err != nil {
			continue // ignore malformed / foreign traffic on the group
		}
		if hb.ID == selfID {
			continue // ignore our own announcement
		}

		peer := PeerSeen{
			ID:      hb.ID,
			Handle:  hb.Handle,
			Addr:    src.IP,
			TCPPort: hb.TCPPort,
			SeenAt:  time.Now(),
		}

		select {
		case out <- peer:
		case <-ctx.Done():
			return nil
		}
	}
}
