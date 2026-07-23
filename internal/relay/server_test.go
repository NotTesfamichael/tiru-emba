package relay

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"testing"
	"time"
)

// startTestServer starts a real Server on an ephemeral port, backed by the
// real Postgres test store (skipping if unreachable, same as pgstore_test.go).
func startTestServer(t *testing.T) net.Addr {
	t.Helper()
	store := connectTestStore(t)
	auth := NewAuth(store)
	orgs := NewOrgs(store)

	srv, err := NewServer("127.0.0.1:0", auth, orgs, nil)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go srv.Run(ctx)

	return srv.Addr()
}

// testConn is a persistent connection to a test server, for tests that
// exchange more than one message (auth, then relay/presence).
type testConn struct {
	t    *testing.T
	conn net.Conn
	r    *bufio.Reader
}

func dial(t *testing.T, addr net.Addr) *testConn {
	t.Helper()
	conn, err := net.DialTimeout("tcp", addr.String(), 2*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	return &testConn{t: t, conn: conn, r: bufio.NewReader(conn)}
}

func (c *testConn) send(env Envelope) {
	c.t.Helper()
	b, err := json.Marshal(env)
	if err != nil {
		c.t.Fatalf("marshal: %v", err)
	}
	b = append(b, '\n')
	if _, err := c.conn.Write(b); err != nil {
		c.t.Fatalf("write: %v", err)
	}
}

func (c *testConn) recv() Envelope {
	c.t.Helper()
	_ = c.conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	line, err := c.r.ReadString('\n')
	if err != nil {
		c.t.Fatalf("read: %v", err)
	}
	var env Envelope
	if err := json.Unmarshal([]byte(line), &env); err != nil {
		c.t.Fatalf("unmarshal %q: %v", line, err)
	}
	return env
}

// recvUntil reads envelopes until one matches want, skipping any others (for
// tests where the exact arrival order of a burst of presence pushes isn't
// what's being tested), failing after too many unmatched reads.
func (c *testConn) recvUntil(want MsgType) Envelope {
	c.t.Helper()
	for i := 0; i < 10; i++ {
		env := c.recv()
		if env.Type == want {
			return env
		}
	}
	c.t.Fatalf("never received a %q message after 10 reads", want)
	return Envelope{}
}

// registerAndAuth registers+auto-logs-in handle and consumes its own
// MsgAuthToken response, returning the connection ready for whatever the
// test does next.
func registerAndAuth(t *testing.T, addr net.Addr, handle, password string) *testConn {
	t.Helper()
	c := dial(t, addr)
	c.send(Envelope{Type: MsgAuthRegister, Handle: handle, Password: password})
	resp := c.recv()
	if resp.Type != MsgAuthToken {
		t.Fatalf("register resp = %+v, want MsgAuthToken", resp)
	}
	return c
}

// dialAndRoundTrip is a one-shot helper for tests that only care about a
// single request/response. The connection is closed immediately after
// (not left open until the test ends) so a second call for the same
// handle -- e.g. register, then a separate login -- doesn't get rejected
// by the one-connection-per-handle rule as if it were still online.
func dialAndRoundTrip(t *testing.T, addr net.Addr, req Envelope) Envelope {
	t.Helper()
	c := dial(t, addr)
	c.send(req)
	resp := c.recv()
	c.conn.Close()
	return resp
}

func TestServerRegisterOverTheWireAutoLogsIn(t *testing.T) {
	addr := startTestServer(t)

	resp := dialAndRoundTrip(t, addr, Envelope{Type: MsgAuthRegister, Handle: "@alex", Password: "correct horse"})
	if resp.Type != MsgAuthToken {
		t.Fatalf("resp = %+v, want MsgAuthToken", resp)
	}
	if resp.Token == "" {
		t.Error("expected a non-empty token")
	}
	if !resp.ExpiresAt.After(time.Now()) {
		t.Errorf("expected ExpiresAt in the future, got %v", resp.ExpiresAt)
	}
}

func TestServerRegisterThenLoginOverTheWire(t *testing.T) {
	addr := startTestServer(t)

	dialAndRoundTrip(t, addr, Envelope{Type: MsgAuthRegister, Handle: "@bob", Password: "correct horse"})

	resp := dialAndRoundTrip(t, addr, Envelope{Type: MsgAuthLogin, Handle: "@bob", Password: "correct horse"})
	if resp.Type != MsgAuthToken || resp.Token == "" {
		t.Fatalf("resp = %+v, want a token", resp)
	}
}

func TestServerLoginWrongPasswordOverTheWire(t *testing.T) {
	addr := startTestServer(t)
	dialAndRoundTrip(t, addr, Envelope{Type: MsgAuthRegister, Handle: "@kal", Password: "correct horse"})

	resp := dialAndRoundTrip(t, addr, Envelope{Type: MsgAuthLogin, Handle: "@kal", Password: "wrong password"})
	if resp.Type != MsgAuthError {
		t.Fatalf("resp = %+v, want MsgAuthError", resp)
	}
}

func TestServerDuplicateRegisterOverTheWire(t *testing.T) {
	addr := startTestServer(t)
	dialAndRoundTrip(t, addr, Envelope{Type: MsgAuthRegister, Handle: "@dup", Password: "correct horse"})

	resp := dialAndRoundTrip(t, addr, Envelope{Type: MsgAuthRegister, Handle: "@dup", Password: "another password"})
	if resp.Type != MsgAuthError {
		t.Fatalf("resp = %+v, want MsgAuthError", resp)
	}
}

func TestServerRejectsAnythingBeforeAuth(t *testing.T) {
	addr := startTestServer(t)
	resp := dialAndRoundTrip(t, addr, Envelope{Type: "not_a_real_type"})
	if resp.Type != MsgError || resp.Reason != "not authenticated" {
		t.Fatalf("resp = %+v, want MsgError %q", resp, "not authenticated")
	}
}

func TestServerUnknownMessageTypeOnceAuthed(t *testing.T) {
	addr := startTestServer(t)
	c := registerAndAuth(t, addr, "@authed", "correct horse")

	c.send(Envelope{Type: "not_a_real_type"})
	if resp := c.recv(); resp.Type != MsgError {
		t.Fatalf("resp = %+v, want MsgError", resp)
	}
}

func TestServerMalformedJSONIsIgnoredNotFatal(t *testing.T) {
	addr := startTestServer(t)
	c := dial(t, addr)

	if _, err := c.conn.Write([]byte("{not valid json\n")); err != nil {
		t.Fatalf("write garbage: %v", err)
	}

	// The connection should stay open (garbage is ignored, not fatal) --
	// prove it by then sending a real, valid request on the same conn.
	c.send(Envelope{Type: MsgAuthRegister, Handle: "@garbage-survivor", Password: "correct horse"})
	if resp := c.recv(); resp.Type != MsgAuthToken {
		t.Fatalf("resp = %+v, want MsgAuthToken (connection should have survived the garbage line)", resp)
	}
}

// createSharedOrg makes host create an org, invite guest, and have guest
// redeem that invite -- via the real wire protocol, exactly the way a real
// client pair would -- establishing the shared org membership presence and
// relay now require. Returns the new org's ID.
func createSharedOrg(t *testing.T, host, guest *testConn) int64 {
	t.Helper()
	host.send(Envelope{Type: MsgOrgCreate, OrgName: "Test Org"})
	created := host.recvUntil(MsgOrgCreated)
	if created.OrgName != "Test Org" {
		t.Fatalf("org_created OrgName = %q, want %q", created.OrgName, "Test Org")
	}

	host.send(Envelope{Type: MsgOrgInvite, OrgID: created.OrgID})
	invited := host.recvUntil(MsgOrgInviteCode)
	if invited.Code == "" {
		t.Fatal("expected a non-empty invite code")
	}

	guest.send(Envelope{Type: MsgOrgJoin, Code: invited.Code})
	joined := guest.recvUntil(MsgOrgJoined)
	if joined.OrgID != created.OrgID {
		t.Fatalf("org_joined OrgID = %d, want %d", joined.OrgID, created.OrgID)
	}

	return created.OrgID
}

func TestPresenceBroadcastsOrgJoinToOthersAlreadyOnline(t *testing.T) {
	addr := startTestServer(t)
	first := registerAndAuth(t, addr, "@first", "correct horse")
	second := registerAndAuth(t, addr, "@second", "correct horse")

	// @second joining the org first created is exactly the moment @first
	// (online, already a member) should be told about them.
	createSharedOrg(t, first, second)

	joined := first.recvUntil(MsgPresenceJoined)
	if joined.Handle != "@second" {
		t.Errorf("Handle = %q, want %q (the newly-joined org-mate)", joined.Handle, "@second")
	}
}

func TestPresenceSeedsExistingRosterOnReconnect(t *testing.T) {
	addr := startTestServer(t)
	first := registerAndAuth(t, addr, "@first", "correct horse")
	second := registerAndAuth(t, addr, "@second", "correct horse")
	createSharedOrg(t, first, second)
	first.recvUntil(MsgPresenceJoined) // drain @second's org-join notification

	// @second disconnects and reconnects; on the fresh connection, presence
	// seeding (loginAndJoin) should tell them @first -- an org-mate -- is
	// online, without needing to redo org setup.
	second.conn.Close()
	first.recvUntil(MsgPresenceLeft) // drain the resulting leave event

	reconnected := dial(t, addr)
	reconnected.send(Envelope{Type: MsgAuthLogin, Handle: "@second", Password: "correct horse"})
	reconnected.recvUntil(MsgAuthToken)

	seeded := reconnected.recvUntil(MsgPresenceJoined)
	if seeded.Handle != "@first" {
		t.Errorf("Handle = %q, want %q (the already-online org-mate)", seeded.Handle, "@first")
	}
}

func TestPresenceBroadcastsLeaveOnDisconnect(t *testing.T) {
	addr := startTestServer(t)
	first := registerAndAuth(t, addr, "@first", "correct horse")
	second := registerAndAuth(t, addr, "@second", "correct horse")
	createSharedOrg(t, first, second)
	first.recvUntil(MsgPresenceJoined) // drain @second's org-join notification

	second.conn.Close()

	left := first.recvUntil(MsgPresenceLeft)
	if left.Handle != "@second" {
		t.Errorf("Handle = %q, want %q", left.Handle, "@second")
	}
}

func TestJoinRejectsSecondConnectionForSameHandle(t *testing.T) {
	addr := startTestServer(t)
	registerAndAuth(t, addr, "@onlyone", "correct horse")

	c := dial(t, addr)
	c.send(Envelope{Type: MsgAuthLogin, Handle: "@onlyone", Password: "correct horse"})
	resp := c.recv()
	if resp.Type != MsgError {
		t.Fatalf("resp = %+v, want MsgError (already connected elsewhere)", resp)
	}
}

func TestRelayDeliversMessageBetweenTwoAuthedUsers(t *testing.T) {
	addr := startTestServer(t)
	alice := registerAndAuth(t, addr, "@alice", "correct horse")
	bob := registerAndAuth(t, addr, "@bob", "correct horse")
	createSharedOrg(t, alice, bob)
	alice.recvUntil(MsgPresenceJoined) // drain @bob's org-join notification

	alice.send(Envelope{Type: MsgRelay, To: "@bob", Body: "hello bob"})

	delivered := bob.recvUntil(MsgRelay)
	if delivered.Handle != "@alice" {
		t.Errorf("Handle (sender) = %q, want %q", delivered.Handle, "@alice")
	}
	if delivered.Body != "hello bob" {
		t.Errorf("Body = %q, want %q", delivered.Body, "hello bob")
	}
}

func TestRelayToOfflineHandleReportsError(t *testing.T) {
	addr := startTestServer(t)
	alice := registerAndAuth(t, addr, "@alice-alone", "correct horse")
	offline := registerAndAuth(t, addr, "@offline-guy", "correct horse")
	createSharedOrg(t, alice, offline)
	alice.recvUntil(MsgPresenceJoined)
	offline.conn.Close()
	alice.recvUntil(MsgPresenceLeft)

	alice.send(Envelope{Type: MsgRelay, To: "@offline-guy", Body: "hello?"})
	resp := alice.recv()
	if resp.Type != MsgError {
		t.Fatalf("resp = %+v, want MsgError", resp)
	}
}

func TestRelayBlockedWithoutSharedOrg(t *testing.T) {
	addr := startTestServer(t)
	alice := registerAndAuth(t, addr, "@lonely-alice", "correct horse")
	registerAndAuth(t, addr, "@stranger-bob", "correct horse")
	// Deliberately no createSharedOrg -- these two share no org membership.

	alice.send(Envelope{Type: MsgRelay, To: "@stranger-bob", Body: "hello?"})
	resp := alice.recv()
	if resp.Type != MsgError {
		t.Fatalf("resp = %+v, want MsgError (no shared org)", resp)
	}
}

func TestRelaySenderCannotSpoofFromHandle(t *testing.T) {
	addr := startTestServer(t)
	alice := registerAndAuth(t, addr, "@realalice", "correct horse")
	bob := registerAndAuth(t, addr, "@bob2", "correct horse")
	createSharedOrg(t, alice, bob)
	alice.recvUntil(MsgPresenceJoined)

	// alice's connection claims to be from someone else via Handle -- the
	// server must ignore that and stamp its own authenticated identity.
	alice.send(Envelope{Type: MsgRelay, To: "@bob2", Body: "spoofed?", Handle: "@someone-else"})

	delivered := bob.recvUntil(MsgRelay)
	if delivered.Handle != "@realalice" {
		t.Errorf("Handle = %q, want %q (the server's own authenticated identity, not the client-claimed one)", delivered.Handle, "@realalice")
	}
}

func TestOrgCreateAndList(t *testing.T) {
	addr := startTestServer(t)
	c := registerAndAuth(t, addr, "@founder", "correct horse")

	c.send(Envelope{Type: MsgOrgCreate, OrgName: "Acme"})
	created := c.recvUntil(MsgOrgCreated)
	if created.OrgName != "Acme" || created.OrgID == 0 {
		t.Fatalf("org_created = %+v, want a non-zero OrgID and OrgName %q", created, "Acme")
	}

	c.send(Envelope{Type: MsgOrgList})
	list := c.recvUntil(MsgOrgListResult)
	if len(list.Orgs) != 1 || list.Orgs[0].ID != created.OrgID || list.Orgs[0].Name != "Acme" {
		t.Errorf("org_list_result = %+v, want exactly [{%d Acme}]", list.Orgs, created.OrgID)
	}
}

func TestOrgCreateRejectsEmptyName(t *testing.T) {
	addr := startTestServer(t)
	c := registerAndAuth(t, addr, "@founder2", "correct horse")

	c.send(Envelope{Type: MsgOrgCreate, OrgName: "   "})
	resp := c.recv()
	if resp.Type != MsgError {
		t.Fatalf("resp = %+v, want MsgError for a blank org name", resp)
	}
}

func TestOrgJoinFullFlow(t *testing.T) {
	addr := startTestServer(t)
	host := registerAndAuth(t, addr, "@host-user", "correct horse")
	guest := registerAndAuth(t, addr, "@guest-user", "correct horse")

	orgID := createSharedOrg(t, host, guest)

	guest.send(Envelope{Type: MsgOrgList})
	list := guest.recvUntil(MsgOrgListResult)
	if len(list.Orgs) != 1 || list.Orgs[0].ID != orgID {
		t.Errorf("guest's org_list_result = %+v, want exactly one org with ID %d", list.Orgs, orgID)
	}
}

func TestOrgInviteRequiresAdmin(t *testing.T) {
	addr := startTestServer(t)
	host := registerAndAuth(t, addr, "@admin-user", "correct horse")
	member := registerAndAuth(t, addr, "@plain-member", "correct horse")
	orgID := createSharedOrg(t, host, member)
	host.recvUntil(MsgPresenceJoined)   // drain @member's join notification on host's connection
	member.recvUntil(MsgPresenceJoined) // and @host's, queued right behind member's own org_joined

	// @plain-member is a member, not an admin -- they shouldn't be able to
	// generate an invite for the org host created.
	member.send(Envelope{Type: MsgOrgInvite, OrgID: orgID})
	member.recvUntil(MsgError) // fails the test itself if no MsgError arrives
}

func TestOrgJoinRejectsInvalidCode(t *testing.T) {
	addr := startTestServer(t)
	c := registerAndAuth(t, addr, "@hopeful", "correct horse")

	c.send(Envelope{Type: MsgOrgJoin, Code: "this-code-does-not-exist"})
	resp := c.recv()
	if resp.Type != MsgError {
		t.Fatalf("resp = %+v, want MsgError (invalid code)", resp)
	}
}

func TestOrgJoinRejectsAlreadyMember(t *testing.T) {
	addr := startTestServer(t)
	host := registerAndAuth(t, addr, "@host-user2", "correct horse")
	guest := registerAndAuth(t, addr, "@guest-user2", "correct horse")
	orgID := createSharedOrg(t, host, guest)
	host.recvUntil(MsgPresenceJoined)

	// Host generates a second invite to the same org, and tries to redeem
	// it themselves -- they're already a member (in fact the admin).
	host.send(Envelope{Type: MsgOrgInvite, OrgID: orgID})
	invited := host.recvUntil(MsgOrgInviteCode)

	host.send(Envelope{Type: MsgOrgJoin, Code: invited.Code})
	resp := host.recv()
	if resp.Type != MsgError {
		t.Fatalf("resp = %+v, want MsgError (already a member)", resp)
	}
}
