// Package network implements direct one-to-one delivery over TCP, once a
// peer's address is known via discovery. Three shapes of connection share
// one wire protocol (a one-line JSON envelope, then whatever the envelope
// type calls for):
//
//   - text messages: one-shot -- dial, write one envelope, close.
//   - file transfers: bounded stateful -- dial, offer, wait for the human on
//     the other end to accept or deny, stream bytes only if accepted, close.
//   - games: unbounded stateful -- dial, invite, wait for accept/deny, then
//     hold the connection open for the lifetime of the game, moves flowing
//     both ways until someone wins, draws, resigns, or disconnects.
package network

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"time"
)

const (
	dialTimeout       = 3 * time.Second
	fileAcceptTimeout = 2 * time.Minute
	fileXferTimeout   = 5 * time.Minute
	gameInviteTimeout = 60 * time.Second
)

type envelopeType string

const (
	typeText       envelopeType = "text"
	typeFileOffer  envelopeType = "file_offer"
	typeFileAccept envelopeType = "file_accept"
	typeFileDeny   envelopeType = "file_deny"
	typeGameInvite envelopeType = "game_invite"
	typeGameAccept envelopeType = "game_accept"
	typeGameDeny   envelopeType = "game_deny"
	typeGameMove   envelopeType = "game_move"
	typeGameResign envelopeType = "game_resign"
)

// envelope is the one-line JSON header every connection starts with (and,
// for games, every subsequent move is its own envelope on the same
// connection).
type envelope struct {
	Type       envelopeType `json:"type"`
	From       string       `json:"from"`
	Body       string       `json:"body,omitempty"`
	FileName   string       `json:"file_name,omitempty"`
	FileSize   int64        `json:"file_size,omitempty"`
	TransferID string       `json:"transfer_id,omitempty"`
	Reason     string       `json:"reason,omitempty"`
	GameType   string       `json:"game_type,omitempty"`
	GameID     string       `json:"game_id,omitempty"`
	Position   int          `json:"position,omitempty"`
}

func writeEnvelope(conn net.Conn, env envelope) error {
	b, err := json.Marshal(env)
	if err != nil {
		return fmt.Errorf("network: marshal envelope: %w", err)
	}
	b = append(b, '\n')
	_ = conn.SetWriteDeadline(time.Now().Add(dialTimeout))
	if _, err := conn.Write(b); err != nil {
		return fmt.Errorf("network: write to %s: %w", conn.RemoteAddr(), err)
	}
	return nil
}

// Message is the plain-text payload for Send/Received.
type Message struct {
	From string
	Body string
}

// Received is a Message annotated with when it arrived and who sent it (the
// remote address, independent of the claimed From handle).
type Received struct {
	Message
	RemoteAddr string
	At         time.Time
}

// Server accepts incoming connections: text messages, file-transfer offers,
// and game invites alike. NewServer binds synchronously so a failure (e.g.
// the port already in use) is reported immediately rather than surfacing
// later from a goroutine; Run does the blocking accept loop and is meant to
// be called in a goroutine.
type Server struct {
	ln      net.Listener
	fileDir string
}

// NewServer binds port and prepares to save any accepted file transfer into
// fileDir (created on demand if it doesn't already exist).
func NewServer(port int, fileDir string) (*Server, error) {
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return nil, fmt.Errorf("network: listen tcp :%d: %w", port, err)
	}
	return &Server{ln: ln, fileDir: fileDir}, nil
}

// Run accepts connections until ctx is canceled. Text messages are emitted
// on msgC; incoming file offers are emitted on offerC (the UI must call
// FileOffer.Respond exactly once per offer) with the eventual outcome of an
// accepted transfer emitted on resultC; incoming game invites are emitted on
// inviteC (the UI must call GameInvite.Accept or .Deny exactly once).
func (s *Server) Run(ctx context.Context, msgC chan<- Received, offerC chan<- FileOffer, resultC chan<- FileResult, inviteC chan<- GameInvite) error {
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
		go s.handle(ctx, conn, msgC, offerC, resultC, inviteC)
	}
}

// handle reads the one-line header and dispatches by type. For typeText and
// typeFileOffer, the connection is always done after this function returns,
// so it's closed here. typeGameInvite is different: on acceptance,
// ownership of conn transfers to a GameSession that outlives this call, so
// handleGameInvite is responsible for its own connection lifecycle instead.
func (s *Server) handle(ctx context.Context, conn net.Conn, msgC chan<- Received, offerC chan<- FileOffer, resultC chan<- FileResult, inviteC chan<- GameInvite) {
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))

	reader := bufio.NewReader(conn)
	line, err := reader.ReadString('\n')
	if err != nil {
		conn.Close()
		return
	}

	var env envelope
	if err := json.Unmarshal([]byte(line), &env); err != nil {
		conn.Close()
		return // ignore malformed traffic
	}
	if env.Type == "" {
		env.Type = typeText // interop with a client build from before file transfer existed
	}

	remote := conn.RemoteAddr().String()
	if host, _, err := net.SplitHostPort(remote); err == nil {
		remote = host
	}

	switch env.Type {
	case typeText:
		defer conn.Close()
		received := Received{Message: Message{From: env.From, Body: env.Body}, RemoteAddr: remote, At: time.Now()}
		select {
		case msgC <- received:
		case <-ctx.Done():
		}

	case typeFileOffer:
		defer conn.Close()
		s.handleFileOffer(ctx, conn, reader, env, remote, offerC, resultC)

	case typeGameInvite:
		s.handleGameInvite(ctx, conn, reader, env, remote, inviteC)

	default:
		conn.Close()
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
	return writeEnvelope(conn, envelope{Type: typeText, From: msg.From, Body: msg.Body})
}
