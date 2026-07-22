// Package network implements direct one-to-one message delivery over TCP,
// once a peer's address is known via discovery. Each message is delivered as
// a short-lived connection: dial, write one JSON line, close -- there's no
// persistent per-peer session to manage.
package network

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"time"
)

const dialTimeout = 3 * time.Second

// Message is the wire format for one direct message.
type Message struct {
	From string `json:"from"`
	Body string `json:"body"`
}

// Received is a Message annotated with when it arrived and who sent it (the
// remote address, independent of the claimed From handle).
type Received struct {
	Message
	RemoteAddr string
	At         time.Time
}

// Server accepts incoming direct-message connections. NewServer binds
// synchronously so a failure (e.g. the port already in use) is reported
// immediately rather than surfacing later from a goroutine; Run does the
// blocking accept loop and is meant to be called in a goroutine.
type Server struct {
	ln net.Listener
}

func NewServer(port int) (*Server, error) {
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return nil, fmt.Errorf("network: listen tcp :%d: %w", port, err)
	}
	return &Server{ln: ln}, nil
}

// Run accepts connections until ctx is canceled, emitting a Received on out
// for each successfully decoded message.
func (s *Server) Run(ctx context.Context, out chan<- Received) error {
	defer s.ln.Close()

	go func() {
		<-ctx.Done()
		s.ln.Close()
	}()

	for {
		conn, err := s.ln.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return nil
			default:
				continue
			}
		}
		go s.handle(ctx, conn, out)
	}
}

func (s *Server) handle(ctx context.Context, conn net.Conn, out chan<- Received) {
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))

	scanner := bufio.NewScanner(conn)
	if !scanner.Scan() {
		return
	}

	var msg Message
	if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
		return // ignore malformed traffic
	}

	remote := conn.RemoteAddr().String()
	if host, _, err := net.SplitHostPort(remote); err == nil {
		remote = host
	}

	received := Received{Message: msg, RemoteAddr: remote, At: time.Now()}
	select {
	case out <- received:
	case <-ctx.Done():
	}
}

// Send dials addr (host:port) and delivers msg as a single JSON line, then
// closes the connection.
func Send(addr string, msg Message) error {
	conn, err := net.DialTimeout("tcp", addr, dialTimeout)
	if err != nil {
		return fmt.Errorf("network: dial %s: %w", addr, err)
	}
	defer conn.Close()

	payload, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("network: marshal message: %w", err)
	}
	payload = append(payload, '\n')

	_ = conn.SetWriteDeadline(time.Now().Add(dialTimeout))
	if _, err := conn.Write(payload); err != nil {
		return fmt.Errorf("network: write to %s: %w", addr, err)
	}
	return nil
}
