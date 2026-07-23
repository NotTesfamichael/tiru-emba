package relay

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"
)

const (
	dialTimeout    = 5 * time.Second
	requestTimeout = 10 * time.Second
)

// Client is the client side of a connection to a relay Server: register or
// log in, then a live connection that can send further requests (org
// management, relay) and receives presence/relay pushes asynchronously via
// Events -- a client may receive a push at any time, interleaved with the
// response to whatever it just asked for, since a push can arrive from
// another connection's goroutine on the server while this one is mid-request.
//
// Requests (Register/Login/CreateOrg/...) are synchronous and one-at-a-time
// by design -- this client doesn't pipeline multiple concurrent requests.
// SendRelay is the one exception: a successful delivery produces no server
// response at all (by protocol design, so the common case of sending a
// message doesn't cost a round trip), so its failures surface later via
// Events rather than a returned error.
type Client struct {
	conn   net.Conn
	reader *bufio.Reader

	writeMu sync.Mutex

	mu      sync.Mutex
	waiting bool
	replyCh chan Envelope

	events chan Envelope
	done   chan struct{}
}

// Dial connects to addr (host:port) and starts the background read loop.
// The returned Client is ready for Register or Login; nothing else is
// meaningful before one of those succeeds.
func Dial(addr string) (*Client, error) {
	conn, err := net.DialTimeout("tcp", addr, dialTimeout)
	if err != nil {
		return nil, fmt.Errorf("relay: dial %s: %w", addr, err)
	}
	c := &Client{
		conn:   conn,
		reader: bufio.NewReader(conn),
		events: make(chan Envelope, 16),
		done:   make(chan struct{}),
	}
	go c.readLoop()
	return c, nil
}

// Events returns the channel of asynchronous pushes: presence_joined,
// presence_left, incoming relay messages, and any error that arrives with
// no request currently pending (e.g. a delayed relay-send failure). Closed
// when the connection ends.
func (c *Client) Events() <-chan Envelope {
	return c.events
}

// Close ends the connection.
func (c *Client) Close() error {
	return c.conn.Close()
}

func (c *Client) readLoop() {
	defer close(c.done)
	defer close(c.events)

	for {
		line, err := c.reader.ReadString('\n')
		if err != nil {
			return
		}

		var env Envelope
		if err := json.Unmarshal([]byte(line), &env); err != nil {
			continue // ignore malformed traffic rather than dropping the connection
		}

		switch env.Type {
		case MsgPresenceJoined, MsgPresenceLeft, MsgRelay:
			// Always an unsolicited push -- the server never sends these
			// as a direct response to anything this client requested.
			c.events <- env
			continue
		}

		c.mu.Lock()
		waiting := c.waiting
		replyCh := c.replyCh
		c.mu.Unlock()

		if waiting {
			replyCh <- env
		} else {
			// No request is pending, so this can only be a stray push --
			// e.g. an error about a fire-and-forget SendRelay that failed.
			c.events <- env
		}
	}
}

func (c *Client) write(env Envelope) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	b, err := json.Marshal(env)
	if err != nil {
		return fmt.Errorf("relay: marshal: %w", err)
	}
	b = append(b, '\n')
	_ = c.conn.SetWriteDeadline(time.Now().Add(requestTimeout))
	if _, err := c.conn.Write(b); err != nil {
		return fmt.Errorf("relay: write: %w", err)
	}
	return nil
}

// request sends env and waits for the single reply the server sends back
// for it, per this Client's one-at-a-time request model.
func (c *Client) request(env Envelope) (Envelope, error) {
	c.mu.Lock()
	if c.waiting {
		c.mu.Unlock()
		return Envelope{}, fmt.Errorf("relay: a request is already in flight on this connection")
	}
	c.waiting = true
	replyCh := make(chan Envelope, 1)
	c.replyCh = replyCh
	c.mu.Unlock()

	defer func() {
		c.mu.Lock()
		c.waiting = false
		c.mu.Unlock()
	}()

	if err := c.write(env); err != nil {
		return Envelope{}, err
	}

	select {
	case reply := <-replyCh:
		return reply, nil
	case <-c.done:
		return Envelope{}, fmt.Errorf("relay: connection closed while waiting for a response")
	case <-time.After(requestTimeout):
		return Envelope{}, fmt.Errorf("relay: timed out waiting for a response")
	}
}

// Register creates a new account and, on success, is auto-logged-in --
// same as Login, just also creating the account first.
func (c *Client) Register(handle, password string) (token string, expiresAt time.Time, err error) {
	reply, err := c.request(Envelope{Type: MsgAuthRegister, Handle: handle, Password: password})
	if err != nil {
		return "", time.Time{}, err
	}
	if reply.Type == MsgAuthError {
		return "", time.Time{}, errors.New(reply.Reason)
	}
	return reply.Token, reply.ExpiresAt, nil
}

// Login authenticates an existing account.
func (c *Client) Login(handle, password string) (token string, expiresAt time.Time, err error) {
	reply, err := c.request(Envelope{Type: MsgAuthLogin, Handle: handle, Password: password})
	if err != nil {
		return "", time.Time{}, err
	}
	if reply.Type == MsgAuthError {
		return "", time.Time{}, errors.New(reply.Reason)
	}
	return reply.Token, reply.ExpiresAt, nil
}

// CreateOrg creates a new org with the caller as its first member/admin.
func (c *Client) CreateOrg(name string) (OrgSummary, error) {
	reply, err := c.request(Envelope{Type: MsgOrgCreate, OrgName: name})
	if err != nil {
		return OrgSummary{}, err
	}
	if reply.Type == MsgError {
		return OrgSummary{}, errors.New(reply.Reason)
	}
	return OrgSummary{ID: reply.OrgID, Name: reply.OrgName}, nil
}

// ListOrgs returns every org the caller belongs to.
func (c *Client) ListOrgs() ([]OrgSummary, error) {
	reply, err := c.request(Envelope{Type: MsgOrgList})
	if err != nil {
		return nil, err
	}
	if reply.Type == MsgError {
		return nil, errors.New(reply.Reason)
	}
	return reply.Orgs, nil
}

// InviteToOrg generates a redeemable invite code for orgID. The caller must
// be an admin of that org.
func (c *Client) InviteToOrg(orgID int64) (code string, expiresAt time.Time, err error) {
	reply, err := c.request(Envelope{Type: MsgOrgInvite, OrgID: orgID})
	if err != nil {
		return "", time.Time{}, err
	}
	if reply.Type == MsgError {
		return "", time.Time{}, errors.New(reply.Reason)
	}
	return reply.Code, reply.ExpiresAt, nil
}

// JoinOrg redeems an invite code.
func (c *Client) JoinOrg(code string) (OrgSummary, error) {
	reply, err := c.request(Envelope{Type: MsgOrgJoin, Code: code})
	if err != nil {
		return OrgSummary{}, err
	}
	if reply.Type == MsgError {
		return OrgSummary{}, errors.New(reply.Reason)
	}
	return OrgSummary{ID: reply.OrgID, Name: reply.OrgName}, nil
}

// SendRelay delivers body to the user at "to" (both org-mates of the
// caller). Fire-and-forget: see the Client doc comment for why a
// successful send returns nil without waiting for any server
// confirmation, and failures surface later via Events instead.
func (c *Client) SendRelay(to, body string) error {
	return c.write(Envelope{Type: MsgRelay, To: to, Body: body})
}
