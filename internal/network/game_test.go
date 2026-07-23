package network

import (
	"context"
	"testing"
	"time"
)

func TestGameInviteAcceptedThenMovesExchangeBothWays(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	addr, _, _, _, inviteC := startTestServerFull(t, t.TempDir())

	var challengerSession *GameSession
	sendDone := make(chan struct{})
	go func() {
		defer close(sendDone)
		session, reason, err := SendGameInvite(ctx, addr, "@alex", "tictactoe")
		if err != nil {
			t.Errorf("SendGameInvite: %v", err)
			return
		}
		if session == nil {
			t.Errorf("expected an accepted session, got reason=%q", reason)
			return
		}
		challengerSession = session
	}()

	var invite GameInvite
	select {
	case invite = <-inviteC:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for invite")
	}
	if invite.From != "@alex" || invite.GameType != "tictactoe" {
		t.Errorf("unexpected invite: %+v", invite)
	}

	inviteeSession, err := invite.Accept()
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	defer inviteeSession.Close()

	<-sendDone
	if challengerSession == nil {
		t.Fatal("challenger never got a session")
	}
	defer challengerSession.Close()

	// Challenger (X) moves first: center square.
	if err := challengerSession.SendMove(4); err != nil {
		t.Fatalf("challenger SendMove: %v", err)
	}
	select {
	case ev := <-inviteeSession.Events():
		if ev.Kind != GameEventMove || ev.Position != 4 {
			t.Errorf("invitee got %+v, want move at 4", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for invitee to see the challenger's move")
	}

	// Invitee (O) replies: corner square.
	if err := inviteeSession.SendMove(0); err != nil {
		t.Fatalf("invitee SendMove: %v", err)
	}
	select {
	case ev := <-challengerSession.Events():
		if ev.Kind != GameEventMove || ev.Position != 0 {
			t.Errorf("challenger got %+v, want move at 0", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for challenger to see the invitee's move")
	}
}

func TestSendDataRoundTrip(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	addr, _, _, _, inviteC := startTestServerFull(t, t.TempDir())

	var challengerSession *GameSession
	sendDone := make(chan struct{})
	go func() {
		defer close(sendDone)
		session, _, err := SendGameInvite(ctx, addr, "@host", "ludo")
		if err != nil {
			t.Errorf("SendGameInvite: %v", err)
			return
		}
		challengerSession = session
	}()

	invite := <-inviteC
	inviteeSession, err := invite.Accept()
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	defer inviteeSession.Close()
	<-sendDone
	defer challengerSession.Close()

	payload := `{"kind":"roll"}`
	if err := challengerSession.SendData(payload); err != nil {
		t.Fatalf("SendData: %v", err)
	}
	select {
	case ev := <-inviteeSession.Events():
		if ev.Kind != GameEventMove || ev.Data != payload {
			t.Errorf("got %+v, want a move event with Data=%q", ev, payload)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the data payload")
	}
}

func TestGameInviteDenied(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	addr, _, _, _, inviteC := startTestServerFull(t, t.TempDir())

	go func() {
		invite := <-inviteC
		invite.Deny()
	}()

	session, reason, err := SendGameInvite(ctx, addr, "@alex", "tictactoe")
	if err != nil {
		t.Fatalf("SendGameInvite: %v", err)
	}
	if session != nil {
		t.Fatal("expected a nil session for a denied invite")
		session.Close()
	}
	if reason != "declined" {
		t.Errorf("reason = %q, want %q", reason, "declined")
	}
}

func TestGameResignNotifiesOpponent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	addr, _, _, _, inviteC := startTestServerFull(t, t.TempDir())

	var challengerSession *GameSession
	sendDone := make(chan struct{})
	go func() {
		defer close(sendDone)
		session, _, err := SendGameInvite(ctx, addr, "@alex", "tictactoe")
		if err != nil {
			t.Errorf("SendGameInvite: %v", err)
			return
		}
		challengerSession = session
	}()

	invite := <-inviteC
	inviteeSession, err := invite.Accept()
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	defer inviteeSession.Close()
	<-sendDone
	defer challengerSession.Close()

	if err := challengerSession.Resign(); err != nil {
		t.Fatalf("Resign: %v", err)
	}

	select {
	case ev := <-inviteeSession.Events():
		if ev.Kind != GameEventResign {
			t.Errorf("got %+v, want a resign event", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the resign event")
	}
}

func TestGameDisconnectSurfacesAsEvent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	addr, _, _, _, inviteC := startTestServerFull(t, t.TempDir())

	var challengerSession *GameSession
	sendDone := make(chan struct{})
	go func() {
		defer close(sendDone)
		session, _, err := SendGameInvite(ctx, addr, "@alex", "tictactoe")
		if err != nil {
			t.Errorf("SendGameInvite: %v", err)
			return
		}
		challengerSession = session
	}()

	invite := <-inviteC
	inviteeSession, err := invite.Accept()
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	<-sendDone

	// The challenger just closes the connection outright, as if the app
	// crashed or the network dropped, instead of resigning cleanly.
	challengerSession.Close()

	select {
	case ev := <-inviteeSession.Events():
		if ev.Kind != GameEventDisconnected {
			t.Errorf("got %+v, want a disconnected event", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the disconnect event")
	}
}
