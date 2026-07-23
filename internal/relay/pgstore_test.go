package relay

import (
	"context"
	"os"
	"testing"
	"time"
)

// testDBURL points at a local dev database by default; override with
// TIRU_EMBA_TEST_DB_URL if you're pointed at a different instance.
func testDBURL() string {
	if url := os.Getenv("TIRU_EMBA_TEST_DB_URL"); url != "" {
		return url
	}
	return "postgres://localhost:5432/tiru_emba_dev?sslmode=disable"
}

// connectTestStore skips the test (rather than failing it) when no
// Postgres is reachable, so `go test ./...` doesn't hard-fail on a machine
// that hasn't set one up -- these are integration tests for the real
// driver/SQL, layered on top of auth_test.go's fake-store unit tests, which
// cover the actual business logic without needing a database at all.
func connectTestStore(t *testing.T) *PGStore {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	store, err := NewPGStore(ctx, testDBURL())
	if err != nil {
		t.Skipf("no reachable Postgres at %s, skipping integration test: %v", testDBURL(), err)
	}
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if _, err := store.pool.Exec(ctx, `TRUNCATE users, sessions, orgs, org_members, org_invites CASCADE`); err != nil {
		t.Fatalf("truncate test tables: %v", err)
	}
	t.Cleanup(store.Close)
	return store
}

func TestPGStoreCreateAndLookupUser(t *testing.T) {
	store := connectTestStore(t)
	ctx := context.Background()

	created, err := store.CreateUser(ctx, "@alex", "hashed-password")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if created.ID == 0 {
		t.Error("expected a non-zero generated ID")
	}

	fetched, err := store.UserByHandle(ctx, "@alex")
	if err != nil {
		t.Fatalf("UserByHandle: %v", err)
	}
	if fetched != created {
		t.Errorf("fetched = %+v, want %+v", fetched, created)
	}
}

func TestPGStoreDuplicateHandleRejected(t *testing.T) {
	store := connectTestStore(t)
	ctx := context.Background()

	if _, err := store.CreateUser(ctx, "@alex", "hash1"); err != nil {
		t.Fatalf("first CreateUser: %v", err)
	}
	if _, err := store.CreateUser(ctx, "@alex", "hash2"); err != ErrHandleTaken {
		t.Errorf("second CreateUser err = %v, want ErrHandleTaken", err)
	}
}

func TestPGStoreUnknownHandleReturnsNotFound(t *testing.T) {
	store := connectTestStore(t)
	if _, err := store.UserByHandle(context.Background(), "@nobody"); err != ErrNotFound {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestPGStoreSessionRoundTrip(t *testing.T) {
	store := connectTestStore(t)
	ctx := context.Background()

	user, err := store.CreateUser(ctx, "@alex", "hashed-password")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	expiresAt := time.Now().Add(time.Hour).Round(time.Microsecond) // Postgres timestamptz precision
	if err := store.CreateSession(ctx, user.ID, "test-token", expiresAt); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	gotUser, gotExpiresAt, err := store.UserBySessionToken(ctx, "test-token")
	if err != nil {
		t.Fatalf("UserBySessionToken: %v", err)
	}
	if gotUser != user {
		t.Errorf("gotUser = %+v, want %+v", gotUser, user)
	}
	if !gotExpiresAt.Equal(expiresAt) {
		t.Errorf("gotExpiresAt = %v, want %v", gotExpiresAt, expiresAt)
	}

	if err := store.DeleteSession(ctx, "test-token"); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	if _, _, err := store.UserBySessionToken(ctx, "test-token"); err != ErrNotFound {
		t.Errorf("after DeleteSession, err = %v, want ErrNotFound", err)
	}
}

func TestPGStoreSessionForUnknownTokenReturnsNotFound(t *testing.T) {
	store := connectTestStore(t)
	if _, _, err := store.UserBySessionToken(context.Background(), "no-such-token"); err != ErrNotFound {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestPGStoreDeletingUnknownSessionIsNotAnError(t *testing.T) {
	store := connectTestStore(t)
	if err := store.DeleteSession(context.Background(), "no-such-token"); err != nil {
		t.Errorf("DeleteSession on an unknown token should be a no-op, got %v", err)
	}
}

func TestPGStoreCreateOrgMakesOwnerAnAdmin(t *testing.T) {
	store := connectTestStore(t)
	ctx := context.Background()
	owner, err := store.CreateUser(ctx, "@owner", "hash")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	org, err := store.CreateOrg(ctx, "Acme", owner.ID)
	if err != nil {
		t.Fatalf("CreateOrg: %v", err)
	}
	if org.ID == 0 || org.Name != "Acme" || org.OwnerUserID != owner.ID {
		t.Fatalf("CreateOrg = %+v, want a real ID, Name Acme, OwnerUserID %d", org, owner.ID)
	}

	isAdmin, err := store.IsOrgAdmin(ctx, org.ID, owner.ID)
	if err != nil {
		t.Fatalf("IsOrgAdmin: %v", err)
	}
	if !isAdmin {
		t.Error("expected the org's creator to be an admin")
	}

	orgs, err := store.OrgsForUser(ctx, owner.ID)
	if err != nil {
		t.Fatalf("OrgsForUser: %v", err)
	}
	if len(orgs) != 1 || orgs[0].ID != org.ID {
		t.Errorf("OrgsForUser = %+v, want exactly [%+v]", orgs, org)
	}
}

func TestPGStoreOrgMateAndSharesOrgQueries(t *testing.T) {
	store := connectTestStore(t)
	ctx := context.Background()
	alice, _ := store.CreateUser(ctx, "@alice", "hash")
	bob, _ := store.CreateUser(ctx, "@bob", "hash")
	stranger, _ := store.CreateUser(ctx, "@stranger", "hash")

	org, err := store.CreateOrg(ctx, "Acme", alice.ID)
	if err != nil {
		t.Fatalf("CreateOrg: %v", err)
	}
	code := "test-invite-code"
	if err := store.CreateOrgInvite(ctx, org.ID, alice.ID, code, time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("CreateOrgInvite: %v", err)
	}
	if _, err := store.RedeemOrgInvite(ctx, code, bob.ID); err != nil {
		t.Fatalf("RedeemOrgInvite: %v", err)
	}

	shares, err := store.SharesOrg(ctx, alice.ID, bob.ID)
	if err != nil {
		t.Fatalf("SharesOrg: %v", err)
	}
	if !shares {
		t.Error("expected alice and bob to share an org after bob redeemed the invite")
	}

	shares, err = store.SharesOrg(ctx, alice.ID, stranger.ID)
	if err != nil {
		t.Fatalf("SharesOrg: %v", err)
	}
	if shares {
		t.Error("expected alice and stranger not to share an org")
	}

	mates, err := store.OrgMateHandles(ctx, alice.ID)
	if err != nil {
		t.Fatalf("OrgMateHandles: %v", err)
	}
	if len(mates) != 1 || mates[0] != "@bob" {
		t.Errorf("OrgMateHandles(alice) = %v, want [@bob]", mates)
	}

	members, err := store.OrgMemberHandles(ctx, org.ID)
	if err != nil {
		t.Fatalf("OrgMemberHandles: %v", err)
	}
	if len(members) != 2 {
		t.Errorf("OrgMemberHandles = %v, want 2 members", members)
	}
}

func TestPGStoreRedeemOrgInviteRejectsExpired(t *testing.T) {
	store := connectTestStore(t)
	ctx := context.Background()
	owner, _ := store.CreateUser(ctx, "@owner2", "hash")
	joiner, _ := store.CreateUser(ctx, "@joiner", "hash")
	org, _ := store.CreateOrg(ctx, "Acme2", owner.ID)

	code := "expired-code"
	if err := store.CreateOrgInvite(ctx, org.ID, owner.ID, code, time.Now().Add(-time.Minute)); err != nil {
		t.Fatalf("CreateOrgInvite: %v", err)
	}
	if _, err := store.RedeemOrgInvite(ctx, code, joiner.ID); err != ErrInviteInvalid {
		t.Errorf("err = %v, want ErrInviteInvalid", err)
	}
}

func TestPGStoreRedeemOrgInviteRejectsSecondUse(t *testing.T) {
	store := connectTestStore(t)
	ctx := context.Background()
	owner, _ := store.CreateUser(ctx, "@owner3", "hash")
	first, _ := store.CreateUser(ctx, "@first-joiner", "hash")
	second, _ := store.CreateUser(ctx, "@second-joiner", "hash")
	org, _ := store.CreateOrg(ctx, "Acme3", owner.ID)

	code := "single-use-code"
	if err := store.CreateOrgInvite(ctx, org.ID, owner.ID, code, time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("CreateOrgInvite: %v", err)
	}
	if _, err := store.RedeemOrgInvite(ctx, code, first.ID); err != nil {
		t.Fatalf("first RedeemOrgInvite: %v", err)
	}
	if _, err := store.RedeemOrgInvite(ctx, code, second.ID); err != ErrInviteInvalid {
		t.Errorf("second RedeemOrgInvite err = %v, want ErrInviteInvalid (single-use)", err)
	}
}

func TestPGStoreRedeemOrgInviteRejectsAlreadyMember(t *testing.T) {
	store := connectTestStore(t)
	ctx := context.Background()
	owner, _ := store.CreateUser(ctx, "@owner4", "hash")
	org, _ := store.CreateOrg(ctx, "Acme4", owner.ID)

	code := "self-redeem-code"
	if err := store.CreateOrgInvite(ctx, org.ID, owner.ID, code, time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("CreateOrgInvite: %v", err)
	}
	if _, err := store.RedeemOrgInvite(ctx, code, owner.ID); err != ErrAlreadyOrgMember {
		t.Errorf("err = %v, want ErrAlreadyOrgMember", err)
	}
}

func TestPGStoreIsOrgAdminFalseForNonMember(t *testing.T) {
	store := connectTestStore(t)
	ctx := context.Background()
	owner, _ := store.CreateUser(ctx, "@owner5", "hash")
	outsider, _ := store.CreateUser(ctx, "@outsider", "hash")
	org, _ := store.CreateOrg(ctx, "Acme5", owner.ID)

	isAdmin, err := store.IsOrgAdmin(ctx, org.ID, outsider.ID)
	if err != nil {
		t.Fatalf("IsOrgAdmin: %v", err)
	}
	if isAdmin {
		t.Error("expected a non-member to never be reported as an admin")
	}
}
