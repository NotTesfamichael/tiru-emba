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

	"golang.org/x/net/ipv4"
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
	ID      string `json:"id"`       // random per-process instance ID, used to ignore our own packets
	Handle  string `json:"handle"`   // e.g. "@alex"
	TCPPort int    `json:"tcp_port"` // port this peer listens on for direct messages (Phase 2)
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

func groupAddr() *net.UDPAddr {
	return &net.UDPAddr{IP: net.ParseIP(GroupAddr), Port: Port}
}

// candidateInterfaces returns every up, multicast-capable, non-loopback
// interface that has an IPv4 address. A machine on a real network commonly
// has a dozen+ interfaces beyond the Wi-Fi adapter (VPN tunnels, Docker
// bridges, Apple's awdl/llw/anpi interfaces, ...), any of which can satisfy a
// naive "pick the first plausible one" check. Rather than guess which one is
// "the" LAN adapter, every candidate is joined/sent on -- the real one just
// needs to be somewhere in the set.
func candidateInterfaces() []net.Interface {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var out []net.Interface
	for _, ifi := range ifaces {
		if ifi.Flags&net.FlagUp == 0 || ifi.Flags&net.FlagMulticast == 0 || ifi.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := ifi.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ipNet, ok := a.(*net.IPNet)
			if ok && ipNet.IP.To4() != nil {
				out = append(out, ifi)
				break
			}
		}
	}
	return out
}

// Broadcaster periodically announces a Heartbeat on the multicast group.
// NewBroadcaster does the socket setup synchronously so a bind failure is
// reported before the caller does anything else (e.g. hands off to a TUI
// that would otherwise swallow it); Run does the actual periodic sending and
// blocks until ctx is canceled, so it's meant to be called in a goroutine.
type Broadcaster struct {
	conn    net.PacketConn
	pconn   *ipv4.PacketConn
	payload []byte
}

func NewBroadcaster(hb Heartbeat) (*Broadcaster, error) {
	conn, err := net.ListenPacket("udp4", ":0")
	if err != nil {
		return nil, fmt.Errorf("discovery: open send socket: %w", err)
	}

	payload, err := json.Marshal(hb)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("discovery: marshal heartbeat: %w", err)
	}

	return &Broadcaster{conn: conn, pconn: ipv4.NewPacketConn(conn), payload: payload}, nil
}

func (b *Broadcaster) Run(ctx context.Context) error {
	defer b.conn.Close()

	send := func() {
		for _, ifi := range candidateInterfaces() {
			ifi := ifi
			_ = b.pconn.SetMulticastInterface(&ifi)
			_, _ = b.pconn.WriteTo(b.payload, nil, groupAddr())
		}
	}

	ticker := time.NewTicker(HeartbeatInterval)
	defer ticker.Stop()

	send() // announce immediately so peers don't wait a full interval to see us

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			send()
		}
	}
}

// Listener joins the multicast group (on every candidate interface) and
// reports PeerSeen sightings. Mirrors Broadcaster's split: NewListener binds
// and joins synchronously so a conflict (e.g. another process already on
// Port) fails loudly right away, and Run does the actual blocking receive
// loop, meant to be called in a goroutine.
type Listener struct {
	conn  net.PacketConn
	pconn *ipv4.PacketConn
}

func NewListener() (*Listener, error) {
	conn, err := net.ListenPacket("udp4", fmt.Sprintf(":%d", Port))
	if err != nil {
		return nil, fmt.Errorf("discovery: listen udp :%d: %w", Port, err)
	}
	pconn := ipv4.NewPacketConn(conn)

	joined := 0
	for _, ifi := range candidateInterfaces() {
		ifi := ifi
		if err := pconn.JoinGroup(&ifi, groupAddr()); err == nil {
			joined++
		}
	}
	if joined == 0 {
		conn.Close()
		return nil, fmt.Errorf("discovery: failed to join multicast group %s on any interface", GroupAddr)
	}

	return &Listener{conn: conn, pconn: pconn}, nil
}

// Run emits a PeerSeen on out for every heartbeat received from a different
// instance (selfID filters out our own announcements). Blocks until ctx is
// canceled.
func (l *Listener) Run(ctx context.Context, selfID string, out chan<- PeerSeen) error {
	defer l.conn.Close()

	// Unblock ReadFrom when ctx is canceled.
	go func() {
		<-ctx.Done()
		l.conn.Close()
	}()

	buf := make([]byte, maxDatagramSize)
	for {
		n, _, src, err := l.pconn.ReadFrom(buf)
		if err != nil {
			select {
			case <-ctx.Done():
				return nil
			default:
				continue
			}
		}

		udpSrc, ok := src.(*net.UDPAddr)
		if !ok {
			continue
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
			Addr:    udpSrc.IP,
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
