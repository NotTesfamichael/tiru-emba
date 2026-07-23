package relay

import (
	"testing"
	"time"
)

func TestClientRegisterAndLogin(t *testing.T) {
	addr := startTestServer(t)

	c, err := Dial(addr.String())
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer c.Close()

	token, expiresAt, err := c.Register("@clientalex", "correct horse battery")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if token == "" {
		t.Error("expected a non-empty token")
	}
	if !expiresAt.After(time.Now()) {
		t.Errorf("expected ExpiresAt in the future, got %v", expiresAt)
	}
}

func TestClientLoginWrongPassword(t *testing.T) {
	addr := startTestServer(t)

	c, err := Dial(addr.String())
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer c.Close()
	if _, _, err := c.Register("@clientbob", "correct horse battery"); err != nil {
		t.Fatalf("Register: %v", err)
	}
	c.Close()

	c2, err := Dial(addr.String())
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer c2.Close()
	if _, _, err := c2.Login("@clientbob", "wrong password"); err == nil {
		t.Error("expected Login with the wrong password to fail")
	}
}

func TestClientOrgCreateInviteJoin(t *testing.T) {
	addr := startTestServer(t)

	host, err := Dial(addr.String())
	if err != nil {
		t.Fatalf("Dial host: %v", err)
	}
	defer host.Close()
	if _, _, err := host.Register("@clienthost", "correct horse battery"); err != nil {
		t.Fatalf("Register host: %v", err)
	}

	guest, err := Dial(addr.String())
	if err != nil {
		t.Fatalf("Dial guest: %v", err)
	}
	defer guest.Close()
	if _, _, err := guest.Register("@clientguest", "correct horse battery"); err != nil {
		t.Fatalf("Register guest: %v", err)
	}

	org, err := host.CreateOrg("Client Org")
	if err != nil {
		t.Fatalf("CreateOrg: %v", err)
	}
	if org.Name != "Client Org" || org.ID == 0 {
		t.Fatalf("CreateOrg = %+v, want a real ID and Name %q", org, "Client Org")
	}

	code, _, err := host.InviteToOrg(org.ID)
	if err != nil {
		t.Fatalf("InviteToOrg: %v", err)
	}

	joined, err := guest.JoinOrg(code)
	if err != nil {
		t.Fatalf("JoinOrg: %v", err)
	}
	if joined.ID != org.ID {
		t.Errorf("JoinOrg = %+v, want OrgID %d", joined, org.ID)
	}

	// host should get an unsolicited presence_joined push about guest.
	select {
	case ev := <-host.Events():
		if ev.Type != MsgPresenceJoined || ev.Handle != "@clientguest" {
			t.Errorf("host event = %+v, want presence_joined for @clientguest", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for host's presence_joined push")
	}

	orgs, err := guest.ListOrgs()
	if err != nil {
		t.Fatalf("ListOrgs: %v", err)
	}
	if len(orgs) != 1 || orgs[0].ID != org.ID {
		t.Errorf("guest ListOrgs = %+v, want exactly one org with ID %d", orgs, org.ID)
	}
}

func TestClientSendRelayDeliversAsEvent(t *testing.T) {
	addr := startTestServer(t)

	alice, err := Dial(addr.String())
	if err != nil {
		t.Fatalf("Dial alice: %v", err)
	}
	defer alice.Close()
	if _, _, err := alice.Register("@clientalice2", "correct horse battery"); err != nil {
		t.Fatalf("Register alice: %v", err)
	}

	bob, err := Dial(addr.String())
	if err != nil {
		t.Fatalf("Dial bob: %v", err)
	}
	defer bob.Close()
	if _, _, err := bob.Register("@clientbob2", "correct horse battery"); err != nil {
		t.Fatalf("Register bob: %v", err)
	}

	org, err := alice.CreateOrg("Relay Org")
	if err != nil {
		t.Fatalf("CreateOrg: %v", err)
	}
	code, _, err := alice.InviteToOrg(org.ID)
	if err != nil {
		t.Fatalf("InviteToOrg: %v", err)
	}
	if _, err := bob.JoinOrg(code); err != nil {
		t.Fatalf("JoinOrg: %v", err)
	}
	<-alice.Events() // drain alice's presence_joined push about bob
	<-bob.Events()   // and bob's own, queued right behind his JoinOrg response

	if err := alice.SendRelay("@clientbob2", "hello over the relay"); err != nil {
		t.Fatalf("SendRelay: %v", err)
	}

	select {
	case ev := <-bob.Events():
		if ev.Type != MsgRelay || ev.Handle != "@clientalice2" || ev.Body != "hello over the relay" {
			t.Errorf("bob event = %+v, want a relay from @clientalice2 with the right body", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for bob to receive the relayed message")
	}
}

func TestClientSendRelayWithoutSharedOrgReportsErrorAsEvent(t *testing.T) {
	addr := startTestServer(t)

	alice, err := Dial(addr.String())
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer alice.Close()
	if _, _, err := alice.Register("@lonelyclient", "correct horse battery"); err != nil {
		t.Fatalf("Register: %v", err)
	}

	stranger, err := Dial(addr.String())
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer stranger.Close()
	if _, _, err := stranger.Register("@strangerclient", "correct horse battery"); err != nil {
		t.Fatalf("Register: %v", err)
	}

	if err := alice.SendRelay("@strangerclient", "hello?"); err != nil {
		t.Fatalf("SendRelay (local write): %v", err)
	}

	select {
	case ev := <-alice.Events():
		if ev.Type != MsgError {
			t.Errorf("event = %+v, want MsgError (no shared org)", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the async relay-failure event")
	}
}
