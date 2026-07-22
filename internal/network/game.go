package network

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"time"
)

// GameEventKind distinguishes what happened on an active GameSession.
type GameEventKind int

const (
	GameEventMove GameEventKind = iota
	GameEventResign
	GameEventDisconnected
)

// GameEvent is one thing that happened on the other end of a GameSession.
type GameEvent struct {
	Kind     GameEventKind
	Position int // meaningful only for GameEventMove
}

// GameInvite is surfaced to the UI for an incoming game challenge. Exactly
// one of Accept/Deny must be called -- the challenger is blocked waiting for
// a response (up to gameInviteTimeout).
type GameInvite struct {
	GameID     string
	GameType   string
	From       string
	RemoteAddr string

	respond chan gameResponse
}

type gameResponse struct {
	accept bool
	result chan gameAcceptResult
}

type gameAcceptResult struct {
	session *GameSession
	err     error
}

// Accept accepts the invite and returns the live GameSession to play on.
// Safe to call at most once.
func (o GameInvite) Accept() (*GameSession, error) {
	result := make(chan gameAcceptResult, 1)
	o.respond <- gameResponse{accept: true, result: result}
	r := <-result
	return r.session, r.err
}

// Deny declines the invite. Safe to call at most once.
func (o GameInvite) Deny() {
	o.respond <- gameResponse{accept: false, result: make(chan gameAcceptResult, 1)}
}

// GameSession is a live, held-open connection for the duration of one game:
// unlike a text message or file transfer, it doesn't close after one
// round trip -- both sides read and write moves on it until the game ends.
type GameSession struct {
	conn   net.Conn
	Peer   string
	GameID string
	events chan GameEvent
}

// NewGameSession wraps an already-open, already-handshaked connection and
// starts reading further envelopes (moves, resignation) from it in the
// background. reader must be the same *bufio.Reader used to read the
// connection's earlier header line(s), so no already-buffered bytes are
// lost.
func NewGameSession(ctx context.Context, conn net.Conn, reader *bufio.Reader, peer, gameID string) *GameSession {
	s := &GameSession{conn: conn, Peer: peer, GameID: gameID, events: make(chan GameEvent, 8)}
	go s.readLoop(ctx, reader)
	return s
}

func (s *GameSession) readLoop(ctx context.Context, reader *bufio.Reader) {
	defer close(s.events)
	_ = s.conn.SetReadDeadline(time.Time{}) // a real game can sit idle between moves; no deadline

	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			select {
			case s.events <- GameEvent{Kind: GameEventDisconnected}:
			case <-ctx.Done():
			}
			return
		}

		var env envelope
		if err := json.Unmarshal([]byte(line), &env); err != nil {
			continue // ignore malformed traffic
		}

		var ev GameEvent
		switch env.Type {
		case typeGameMove:
			ev = GameEvent{Kind: GameEventMove, Position: env.Position}
		case typeGameResign:
			ev = GameEvent{Kind: GameEventResign}
		default:
			continue
		}

		select {
		case s.events <- ev:
		case <-ctx.Done():
			return
		}
	}
}

// Events returns the channel of incoming events. Closed once the connection
// ends (after a final GameEventDisconnected, if that's how it ended).
func (s *GameSession) Events() <-chan GameEvent {
	return s.events
}

// SendMove tells the opponent a mark was placed at pos (0-8).
func (s *GameSession) SendMove(pos int) error {
	return writeEnvelope(s.conn, envelope{Type: typeGameMove, Position: pos})
}

// Resign tells the opponent you're quitting the game early.
func (s *GameSession) Resign() error {
	return writeEnvelope(s.conn, envelope{Type: typeGameResign})
}

// Close ends the session. Call once the game is over (win/draw/resign/
// disconnect) or if abandoning it.
func (s *GameSession) Close() error {
	return s.conn.Close()
}

// SendGameInvite dials addr, challenges whoever's listening to gameType, and
// blocks waiting for their accept/deny (up to gameInviteTimeout). A nil
// session with a non-empty reason means they declined; a nil session with
// no reason and no error means something else closed the connection before
// responding.
func SendGameInvite(ctx context.Context, addr, from, gameType string) (session *GameSession, reason string, err error) {
	conn, err := net.DialTimeout("tcp", addr, dialTimeout)
	if err != nil {
		return nil, "", fmt.Errorf("network: dial %s: %w", addr, err)
	}

	gameID, err := randomID()
	if err != nil {
		conn.Close()
		return nil, "", fmt.Errorf("network: generate game id: %w", err)
	}

	if err := writeEnvelope(conn, envelope{Type: typeGameInvite, From: from, GameType: gameType, GameID: gameID}); err != nil {
		conn.Close()
		return nil, "", err
	}

	_ = conn.SetReadDeadline(time.Now().Add(gameInviteTimeout))
	reader := bufio.NewReader(conn)
	line, err := reader.ReadString('\n')
	if err != nil {
		conn.Close()
		return nil, "", fmt.Errorf("network: waiting for response: %w", err)
	}

	var resp envelope
	if err := json.Unmarshal([]byte(line), &resp); err != nil {
		conn.Close()
		return nil, "", fmt.Errorf("network: decode response: %w", err)
	}
	if resp.Type != typeGameAccept {
		conn.Close()
		return nil, resp.Reason, nil
	}

	return NewGameSession(ctx, conn, reader, from, gameID), "", nil
}

func (s *Server) handleGameInvite(ctx context.Context, conn net.Conn, reader *bufio.Reader, env envelope, remote string, inviteC chan<- GameInvite) {
	respond := func(accept bool, reason string) error {
		out := envelope{Type: typeGameDeny, Reason: reason}
		if accept {
			out.Type = typeGameAccept
		}
		return writeEnvelope(conn, out)
	}

	respondCh := make(chan gameResponse, 1)
	invite := GameInvite{
		GameID:     env.GameID,
		GameType:   env.GameType,
		From:       env.From,
		RemoteAddr: remote,
		respond:    respondCh,
	}

	select {
	case inviteC <- invite:
	case <-ctx.Done():
		conn.Close()
		return
	}

	var resp gameResponse
	select {
	case resp = <-respondCh:
	case <-time.After(gameInviteTimeout):
		_ = respond(false, "timed out waiting for a response")
		conn.Close()
		return
	case <-ctx.Done():
		conn.Close()
		return
	}

	if !resp.accept {
		_ = respond(false, "declined")
		conn.Close()
		resp.result <- gameAcceptResult{nil, nil}
		return
	}

	if err := respond(true, ""); err != nil {
		conn.Close()
		resp.result <- gameAcceptResult{nil, err}
		return
	}

	// Ownership of conn transfers to the GameSession from here -- it is
	// deliberately NOT closed in this function on the success path.
	session := NewGameSession(ctx, conn, reader, env.From, env.GameID)
	resp.result <- gameAcceptResult{session, nil}
}
