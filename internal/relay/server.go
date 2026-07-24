package relay

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"time"
)

const (
	readTimeout  = 5 * time.Minute // generous: a real client sends a request roughly on user action, not on a tight loop
	writeTimeout = 5 * time.Second

	// outboxSize is how many envelopes a connection's outbox can buffer
	// before Hub.Send starts dropping messages to it rather than blocking
	// the sender's goroutine.
	outboxSize = 16
)

// Server accepts relay connections. Before authenticating, a connection can
// only send MsgAuthRegister/MsgAuthLogin/MsgAuthResume/MsgAuthRecoverStart/
// MsgAuthRecoverFinish; once authenticated, it's registered with the Hub
// (so other connections can reach it) and can send MsgRelay and the org_*/
// account_*/unlockable*/avatar_*/todo_* messages. Presence and relay are
// scoped to shared organization membership (Phase 3) rather than a flat
// global roster.
type Server struct {
	ln     net.Listener
	auth   *Auth
	orgs   *Orgs
	points *Points
	todos  *Todos
	hub    *Hub
}

// NewServer binds addr. If tlsConfig is non-nil, the listener is TLS;
// otherwise it's plain TCP -- the caller (cmd/tiru-server) is responsible
// for warning loudly about running without TLS, since passwords and
// session tokens cross this connection.
func NewServer(addr string, auth *Auth, orgs *Orgs, points *Points, todos *Todos, tlsConfig *tls.Config) (*Server, error) {
	var ln net.Listener
	var err error
	if tlsConfig != nil {
		ln, err = tls.Listen("tcp", addr, tlsConfig)
	} else {
		ln, err = net.Listen("tcp", addr)
	}
	if err != nil {
		return nil, fmt.Errorf("relay: listen %s: %w", addr, err)
	}
	return &Server{ln: ln, auth: auth, orgs: orgs, points: points, todos: todos, hub: NewHub()}, nil
}

// Addr returns the listener's actual bound address (useful when addr was
// given as ":0" for an ephemeral port, e.g. in tests).
func (s *Server) Addr() net.Addr {
	return s.ln.Addr()
}

// Run accepts connections until ctx is canceled. Each connection is handled
// in its own goroutine and closed when that goroutine returns.
func (s *Server) Run(ctx context.Context) error {
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
		go s.handleConn(ctx, conn)
	}
}

// handleConn owns conn's whole lifecycle: a dedicated writer goroutine
// drains outbox onto the wire for as long as the connection lives (so
// every write -- this connection's own synchronous responses, and
// asynchronous relay/presence pushes arriving from OTHER connections'
// goroutines via the Hub -- goes through one place, never racing on conn
// directly). outbox is deliberately never closed: Hub.Send can briefly
// still hold a reference to it after Leave (a benign, bounded race), and
// sending on a closed channel would panic, whereas sending into an
// abandoned buffered channel nobody drains anymore is harmless.
func (s *Server) handleConn(ctx context.Context, conn net.Conn) {
	defer conn.Close()

	outbox := make(chan Envelope, outboxSize)
	stop := make(chan struct{})
	go writeLoop(conn, outbox, stop)
	defer close(stop)

	user := s.readLoop(ctx, conn, outbox)
	if user == nil {
		return
	}

	s.hub.Leave(user.Handle, outbox)
	mates, err := s.orgs.MateHandles(ctx, *user)
	if err != nil {
		return
	}
	for _, mate := range mates {
		s.hub.Send(mate, Envelope{Type: MsgPresenceLeft, Handle: user.Handle})
	}
}

func writeLoop(conn net.Conn, outbox <-chan Envelope, stop <-chan struct{}) {
	for {
		select {
		case env := <-outbox:
			b, err := json.Marshal(env)
			if err != nil {
				continue
			}
			b = append(b, '\n')
			_ = conn.SetWriteDeadline(time.Now().Add(writeTimeout))
			if _, err := conn.Write(b); err != nil {
				return
			}
		case <-stop:
			return
		}
	}
}

// readLoop reads envelopes until the connection ends, returning whichever
// User it authenticated as (nil if it never did).
func (s *Server) readLoop(ctx context.Context, conn net.Conn, outbox chan<- Envelope) *User {
	reader := bufio.NewReader(conn)
	var user *User

	for {
		_ = conn.SetReadDeadline(time.Now().Add(readTimeout))
		line, err := reader.ReadString('\n')
		if err != nil {
			return user
		}

		var env Envelope
		if err := json.Unmarshal([]byte(line), &env); err != nil {
			continue // ignore malformed traffic rather than dropping the connection
		}

		if user == nil {
			user = s.handlePreAuth(ctx, env, outbox)
			continue
		}
		s.handleAuthed(ctx, env, user, outbox)
	}
}

// handlePreAuth handles every message type allowed before authentication,
// returning the now-authenticated User on success (nil on failure, or on
// any other message type, or on a recovery step that isn't done yet).
func (s *Server) handlePreAuth(ctx context.Context, env Envelope, outbox chan<- Envelope) *User {
	switch env.Type {
	case MsgAuthRegister:
		if err := s.auth.Register(ctx, env.Handle, env.Password, env.AvatarASCII, env.SecurityQuestion, env.SecurityAnswer); err != nil {
			outbox <- Envelope{Type: MsgAuthError, Reason: err.Error()}
			return nil
		}
		return s.loginAndJoin(ctx, env.Handle, env.Password, outbox)

	case MsgAuthLogin:
		return s.loginAndJoin(ctx, env.Handle, env.Password, outbox)

	case MsgAuthResume:
		user, token, expiresAt, err := s.auth.ResumeSession(ctx, env.Token)
		if err != nil {
			outbox <- Envelope{Type: MsgAuthError, Reason: err.Error()}
			return nil
		}
		return s.completeJoin(ctx, user, token, expiresAt, outbox)

	case MsgAuthRecoverStart:
		question, err := s.auth.RecoverStart(ctx, env.Handle)
		if err != nil {
			outbox <- Envelope{Type: MsgAuthError, Reason: err.Error()}
			return nil
		}
		outbox <- Envelope{Type: MsgAuthRecoverQuestion, SecurityQuestion: question}
		// Still not authenticated -- the connection stays in the pre-auth
		// loop for the MsgAuthRecoverFinish that should follow.
		return nil

	case MsgAuthRecoverFinish:
		user, token, expiresAt, err := s.auth.RecoverFinish(ctx, env.Handle, env.SecurityAnswer, env.Password)
		if err != nil {
			outbox <- Envelope{Type: MsgAuthError, Reason: err.Error()}
			return nil
		}
		return s.completeJoin(ctx, user, token, expiresAt, outbox)

	case MsgCheckHandle:
		_, err := s.auth.Lookup(ctx, env.Handle)
		outbox <- Envelope{Type: MsgHandleCheckResult, Available: errors.Is(err, ErrNotFound)}
		// Still not authenticated -- stays in the pre-auth loop, same as
		// MsgAuthRecoverStart.
		return nil

	default:
		outbox <- Envelope{Type: MsgError, Reason: "not authenticated"}
		return nil
	}
}

// loginAndJoin authenticates handle/password and finishes joining, same as
// completeJoin.
func (s *Server) loginAndJoin(ctx context.Context, handle, password string, outbox chan<- Envelope) *User {
	user, token, expiresAt, err := s.auth.Login(ctx, handle, password)
	if err != nil {
		outbox <- Envelope{Type: MsgAuthError, Reason: err.Error()}
		return nil
	}
	return s.completeJoin(ctx, user, token, expiresAt, outbox)
}

// completeJoin registers user with the Hub and pushes the auth success
// response followed by presence for whichever of the user's org-mates
// (across every org they belong to) are currently online -- not a global
// roster, since Phase 3 scopes visibility to shared organization
// membership. Shared by every path that ends in a successful
// authentication: fresh login, register, resumed session, and a completed
// password recovery.
func (s *Server) completeJoin(ctx context.Context, user User, token string, expiresAt time.Time, outbox chan<- Envelope) *User {
	if err := s.hub.Join(user.Handle, outbox); err != nil {
		outbox <- Envelope{Type: MsgError, Reason: err.Error()}
		return nil
	}

	outbox <- Envelope{Type: MsgAuthToken, Token: token, ExpiresAt: expiresAt, IsAdmin: user.IsAdmin}

	if mates, err := s.orgs.MateHandles(ctx, user); err == nil {
		for _, mate := range mates {
			if !s.hub.Online(mate) {
				continue
			}
			outbox <- Envelope{Type: MsgPresenceJoined, Handle: mate}
			s.hub.Send(mate, Envelope{Type: MsgPresenceJoined, Handle: user.Handle})
		}
	}

	return &user
}

// handleAuthed handles every message type available once a connection is
// authenticated: relay (org-gated) and organization management.
func (s *Server) handleAuthed(ctx context.Context, env Envelope, user *User, outbox chan<- Envelope) {
	switch env.Type {
	case MsgRelay:
		s.handleRelay(ctx, env, user, outbox)

	case MsgOrgCreate:
		org, err := s.orgs.Create(ctx, env.OrgName, *user)
		if err != nil {
			outbox <- Envelope{Type: MsgError, Reason: err.Error()}
			return
		}
		outbox <- Envelope{Type: MsgOrgCreated, OrgID: org.ID, OrgName: org.Name}

	case MsgOrgList:
		orgs, err := s.orgs.List(ctx, *user)
		if err != nil {
			outbox <- Envelope{Type: MsgError, Reason: err.Error()}
			return
		}
		summaries := make([]OrgSummary, len(orgs))
		for i, o := range orgs {
			summaries[i] = OrgSummary{ID: o.ID, Name: o.Name}
		}
		outbox <- Envelope{Type: MsgOrgListResult, Orgs: summaries}

	case MsgOrgInvite:
		code, expiresAt, err := s.orgs.Invite(ctx, env.OrgID, *user)
		if err != nil {
			outbox <- Envelope{Type: MsgError, Reason: err.Error()}
			return
		}
		outbox <- Envelope{Type: MsgOrgInviteCode, Code: code, ExpiresAt: expiresAt}

	case MsgOrgJoin:
		org, err := s.orgs.Join(ctx, env.Code, *user)
		if err != nil {
			outbox <- Envelope{Type: MsgError, Reason: err.Error()}
			return
		}
		outbox <- Envelope{Type: MsgOrgJoined, OrgID: org.ID, OrgName: org.Name}
		s.notifyOrgOfNewMember(ctx, org.ID, user.Handle, outbox)

	case MsgAccountBio:
		s.handleAccountBio(ctx, user, outbox)

	case MsgUnlockablesList:
		s.handleUnlockablesList(ctx, user, outbox)

	case MsgUnlockableRedeem:
		if err := s.points.Redeem(ctx, *user, env.UnlockableID); err != nil {
			outbox <- Envelope{Type: MsgError, Reason: err.Error()}
			return
		}
		outbox <- Envelope{Type: MsgUnlockableRedeemed, UnlockableID: env.UnlockableID}

	case MsgAvatarSet:
		if err := s.points.SetActive(ctx, *user, env.UnlockableID); err != nil {
			outbox <- Envelope{Type: MsgError, Reason: err.Error()}
			return
		}
		outbox <- Envelope{Type: MsgAvatarSetOK}

	case MsgTodoList:
		todos, err := s.todos.List(ctx, env.OrgID, *user)
		if err != nil {
			outbox <- Envelope{Type: MsgError, Reason: err.Error()}
			return
		}
		outbox <- Envelope{Type: MsgTodoListResult, Todos: todos}

	case MsgTodoAdd:
		todo, err := s.todos.Add(ctx, env.OrgID, *user, env.Text)
		if err != nil {
			outbox <- Envelope{Type: MsgError, Reason: err.Error()}
			return
		}
		outbox <- Envelope{Type: MsgTodoAdded, Todo: &todo}

	case MsgTodoComplete:
		todo, err := s.todos.Complete(ctx, env.OrgID, env.TodoID, *user)
		if err != nil {
			outbox <- Envelope{Type: MsgError, Reason: err.Error()}
			return
		}
		outbox <- Envelope{Type: MsgTodoCompleted, TodoID: todo.ID}

	default:
		outbox <- Envelope{Type: MsgError, Reason: fmt.Sprintf("unknown message type: %q", env.Type)}
	}
}

// handleAccountBio answers MsgAccountBio with the caller's own stats. A
// failure fetching the profile/org list is reported as MsgError rather
// than silently answering with zero-valued fields, since a genuinely
// missing profile (shouldn't happen post-registration, but Store keeps it
// fallible) is worth surfacing rather than masking as "0 points".
func (s *Server) handleAccountBio(ctx context.Context, user *User, outbox chan<- Envelope) {
	profile, err := s.points.Profile(ctx, *user)
	if err != nil {
		outbox <- Envelope{Type: MsgError, Reason: err.Error()}
		return
	}
	orgs, err := s.orgs.List(ctx, *user)
	if err != nil {
		outbox <- Envelope{Type: MsgError, Reason: err.Error()}
		return
	}
	orgNames := make([]string, len(orgs))
	for i, o := range orgs {
		orgNames[i] = o.Name
	}
	outbox <- Envelope{
		Type:        MsgAccountBioResult,
		Handle:      user.Handle,
		AvatarASCII: profile.AvatarASCII,
		Points:      profile.Points,
		OrgNames:    orgNames,
		CreatedAt:   user.CreatedAt,
	}
}

func (s *Server) handleUnlockablesList(ctx context.Context, user *User, outbox chan<- Envelope) {
	all, unlocked, err := s.points.ListUnlockables(ctx, *user)
	if err != nil {
		outbox <- Envelope{Type: MsgError, Reason: err.Error()}
		return
	}
	profile, err := s.points.Profile(ctx, *user)
	if err != nil {
		outbox <- Envelope{Type: MsgError, Reason: err.Error()}
		return
	}
	infos := make([]UnlockableInfo, len(all))
	for i, u := range all {
		infos[i] = UnlockableInfo{
			ID:       u.ID,
			Name:     u.Name,
			Kind:     u.Kind,
			AsciiArt: u.AsciiArt,
			Cost:     u.Cost,
			Owned:    unlocked[u.ID],
			Active:   profile.ActiveUnlockableID != nil && *profile.ActiveUnlockableID == u.ID,
		}
	}
	outbox <- Envelope{Type: MsgUnlockablesListResult, Unlockables: infos}
}

func (s *Server) handleRelay(ctx context.Context, env Envelope, user *User, outbox chan<- Envelope) {
	target := NormalizeHandle(env.To)

	targetUser, err := s.auth.Lookup(ctx, target)
	if err != nil {
		outbox <- Envelope{Type: MsgError, Reason: fmt.Sprintf("%s is not a registered user", target)}
		return
	}
	shares, err := s.orgs.SharesOrgWith(ctx, *user, targetUser)
	if err != nil {
		outbox <- Envelope{Type: MsgError, Reason: "internal error checking organization membership"}
		return
	}
	if !shares {
		outbox <- Envelope{Type: MsgError, Reason: fmt.Sprintf("you don't share an organization with %s", target)}
		return
	}

	out := Envelope{Type: MsgRelay, Handle: user.Handle, Body: env.Body}
	if !s.hub.Send(target, out) {
		outbox <- Envelope{Type: MsgError, Reason: fmt.Sprintf("%s is not online", target)}
		return
	}
	// Best-effort: a failed point award shouldn't fail (or even be
	// reported as failing) an otherwise-successful delivery.
	_ = s.points.AwardMessage(ctx, user.ID)
}

// notifyOrgOfNewMember tells every already-online member of orgID (other
// than the new member) that newHandle just joined, and seeds the new
// member's own view with each of those online org-mates -- the same
// bidirectional presence exchange loginAndJoin does at connect time, but
// scoped to just this one org (membership in the user's OTHER orgs hasn't
// changed) rather than recomputing every org they belong to.
func (s *Server) notifyOrgOfNewMember(ctx context.Context, orgID int64, newHandle string, outbox chan<- Envelope) {
	members, err := s.orgs.MemberHandles(ctx, orgID)
	if err != nil {
		return
	}
	for _, member := range members {
		if member == newHandle || !s.hub.Online(member) {
			continue
		}
		outbox <- Envelope{Type: MsgPresenceJoined, Handle: member}
		s.hub.Send(member, Envelope{Type: MsgPresenceJoined, Handle: newHandle})
	}
}
