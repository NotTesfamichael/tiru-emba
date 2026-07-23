package relay

import (
	"context"
	"testing"
	"time"
)

// fakeStore is an in-memory Store double, so Auth's own logic (hashing,
// token generation, expiry rules, credential-error unification) can be
// tested without a real database.
type fakeStore struct {
	usersByHandle map[string]User
	usersByID     map[int64]User
	sessions      map[string]fakeSession
	nextID        int64

	orgs       map[int64]Org
	orgMembers map[int64]map[int64]string // orgID -> userID -> role
	invites    map[string]fakeInvite
	nextOrgID  int64
}

type fakeSession struct {
	userID    int64
	expiresAt time.Time
}

type fakeInvite struct {
	orgID     int64
	expiresAt time.Time
	maxUses   int
	usedCount int
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		usersByHandle: make(map[string]User),
		usersByID:     make(map[int64]User),
		sessions:      make(map[string]fakeSession),
		orgs:          make(map[int64]Org),
		orgMembers:    make(map[int64]map[int64]string),
		invites:       make(map[string]fakeInvite),
	}
}

func (f *fakeStore) CreateUser(ctx context.Context, handle, passwordHash string) (User, error) {
	if _, exists := f.usersByHandle[handle]; exists {
		return User{}, ErrHandleTaken
	}
	f.nextID++
	u := User{ID: f.nextID, Handle: handle, PasswordHash: passwordHash, CreatedAt: time.Now()}
	f.usersByHandle[handle] = u
	f.usersByID[u.ID] = u
	return u, nil
}

func (f *fakeStore) UserByHandle(ctx context.Context, handle string) (User, error) {
	u, ok := f.usersByHandle[handle]
	if !ok {
		return User{}, ErrNotFound
	}
	return u, nil
}

func (f *fakeStore) CreateSession(ctx context.Context, userID int64, token string, expiresAt time.Time) error {
	f.sessions[token] = fakeSession{userID: userID, expiresAt: expiresAt}
	return nil
}

func (f *fakeStore) UserBySessionToken(ctx context.Context, token string) (User, time.Time, error) {
	sess, ok := f.sessions[token]
	if !ok {
		return User{}, time.Time{}, ErrNotFound
	}
	u, ok := f.usersByID[sess.userID]
	if !ok {
		return User{}, time.Time{}, ErrNotFound
	}
	return u, sess.expiresAt, nil
}

func (f *fakeStore) DeleteSession(ctx context.Context, token string) error {
	delete(f.sessions, token)
	return nil
}

func (f *fakeStore) CreateOrg(ctx context.Context, name string, ownerUserID int64) (Org, error) {
	f.nextOrgID++
	org := Org{ID: f.nextOrgID, Name: name, OwnerUserID: ownerUserID, CreatedAt: time.Now()}
	f.orgs[org.ID] = org
	f.orgMembers[org.ID] = map[int64]string{ownerUserID: "admin"}
	return org, nil
}

func (f *fakeStore) OrgsForUser(ctx context.Context, userID int64) ([]Org, error) {
	var result []Org
	for orgID, members := range f.orgMembers {
		if _, ok := members[userID]; ok {
			result = append(result, f.orgs[orgID])
		}
	}
	return result, nil
}

func (f *fakeStore) OrgMemberHandles(ctx context.Context, orgID int64) ([]string, error) {
	var handles []string
	for userID := range f.orgMembers[orgID] {
		handles = append(handles, f.usersByID[userID].Handle)
	}
	return handles, nil
}

func (f *fakeStore) OrgMateHandles(ctx context.Context, userID int64) ([]string, error) {
	seen := make(map[int64]bool)
	for _, members := range f.orgMembers {
		if _, in := members[userID]; !in {
			continue
		}
		for other := range members {
			if other != userID {
				seen[other] = true
			}
		}
	}
	var handles []string
	for uid := range seen {
		handles = append(handles, f.usersByID[uid].Handle)
	}
	return handles, nil
}

func (f *fakeStore) SharesOrg(ctx context.Context, userID1, userID2 int64) (bool, error) {
	for _, members := range f.orgMembers {
		_, ok1 := members[userID1]
		_, ok2 := members[userID2]
		if ok1 && ok2 {
			return true, nil
		}
	}
	return false, nil
}

func (f *fakeStore) IsOrgAdmin(ctx context.Context, orgID, userID int64) (bool, error) {
	role, ok := f.orgMembers[orgID][userID]
	return ok && role == "admin", nil
}

func (f *fakeStore) CreateOrgInvite(ctx context.Context, orgID, createdBy int64, code string, expiresAt time.Time) error {
	f.invites[code] = fakeInvite{orgID: orgID, expiresAt: expiresAt, maxUses: 1}
	return nil
}

func (f *fakeStore) RedeemOrgInvite(ctx context.Context, code string, userID int64) (Org, error) {
	inv, ok := f.invites[code]
	if !ok || time.Now().After(inv.expiresAt) || inv.usedCount >= inv.maxUses {
		return Org{}, ErrInviteInvalid
	}
	if _, already := f.orgMembers[inv.orgID][userID]; already {
		return Org{}, ErrAlreadyOrgMember
	}
	f.orgMembers[inv.orgID][userID] = "member"
	inv.usedCount++
	f.invites[code] = inv
	return f.orgs[inv.orgID], nil
}

func TestRegisterAndLoginRoundTrip(t *testing.T) {
	auth := NewAuth(newFakeStore())
	ctx := context.Background()

	if err := auth.Register(ctx, "alex", "correct horse"); err != nil {
		t.Fatalf("Register: %v", err)
	}

	loggedInUser, token, expiresAt, err := auth.Login(ctx, "@alex", "correct horse")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if loggedInUser.Handle != "@alex" {
		t.Errorf("Login returned Handle = %q, want %q", loggedInUser.Handle, "@alex")
	}
	if token == "" {
		t.Fatal("expected a non-empty session token")
	}
	if !expiresAt.After(time.Now()) {
		t.Fatalf("expected expiresAt in the future, got %v", expiresAt)
	}

	user, err := auth.Authenticate(ctx, token)
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if user.Handle != "@alex" {
		t.Errorf("Handle = %q, want %q", user.Handle, "@alex")
	}
}

func TestRegisterNormalizesHandleWithoutAt(t *testing.T) {
	store := newFakeStore()
	auth := NewAuth(store)
	ctx := context.Background()

	if err := auth.Register(ctx, "alex", "correct horse"); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if _, ok := store.usersByHandle["@alex"]; !ok {
		t.Error("expected the stored handle to be normalized to \"@alex\"")
	}
}

func TestRegisterDuplicateHandleFails(t *testing.T) {
	auth := NewAuth(newFakeStore())
	ctx := context.Background()

	if err := auth.Register(ctx, "@alex", "correct horse"); err != nil {
		t.Fatalf("first Register: %v", err)
	}
	err := auth.Register(ctx, "@alex", "a different password")
	if err != ErrHandleTaken {
		t.Errorf("second Register err = %v, want ErrHandleTaken", err)
	}
	// Case-insensitivity isn't attempted here on purpose -- handles are
	// stored/compared exactly as normalized (leading "@", otherwise as
	// typed), matching how internal/peer.Registry.Lookup already treats
	// handle comparison as its own separate (case-insensitive) concern at
	// the LAN layer; the relay layer doesn't need to duplicate that here.
}

func TestRegisterRejectsShortPassword(t *testing.T) {
	auth := NewAuth(newFakeStore())
	if err := auth.Register(context.Background(), "@alex", "short"); err == nil {
		t.Error("expected a short password to be rejected")
	}
}

func TestLoginWrongPasswordFails(t *testing.T) {
	auth := NewAuth(newFakeStore())
	ctx := context.Background()
	_ = auth.Register(ctx, "@alex", "correct horse")

	_, _, _, err := auth.Login(ctx, "@alex", "wrong password")
	if err != ErrInvalidCredentials {
		t.Errorf("err = %v, want ErrInvalidCredentials", err)
	}
}

func TestLoginUnknownHandleFailsSameAsWrongPassword(t *testing.T) {
	auth := NewAuth(newFakeStore())
	_, _, _, err := auth.Login(context.Background(), "@nobody", "whatever")
	if err != ErrInvalidCredentials {
		t.Errorf("err = %v, want ErrInvalidCredentials (not a distinct \"no such user\" error, to avoid handle enumeration)", err)
	}
}

func TestAuthenticateRejectsExpiredSession(t *testing.T) {
	store := newFakeStore()
	auth := NewAuth(store)
	ctx := context.Background()
	_ = auth.Register(ctx, "@alex", "correct horse")

	_, token, _, err := auth.Login(ctx, "@alex", "correct horse")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	// Backdate the session's expiry directly in the fake store.
	sess := store.sessions[token]
	sess.expiresAt = time.Now().Add(-time.Minute)
	store.sessions[token] = sess

	if _, err := auth.Authenticate(ctx, token); err != ErrSessionExpired {
		t.Errorf("err = %v, want ErrSessionExpired", err)
	}
}

func TestAuthenticateRejectsUnknownToken(t *testing.T) {
	auth := NewAuth(newFakeStore())
	if _, err := auth.Authenticate(context.Background(), "not-a-real-token"); err != ErrNotFound {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestLogoutRevokesSession(t *testing.T) {
	auth := NewAuth(newFakeStore())
	ctx := context.Background()
	_ = auth.Register(ctx, "@alex", "correct horse")
	_, token, _, _ := auth.Login(ctx, "@alex", "correct horse")

	if err := auth.Logout(ctx, token); err != nil {
		t.Fatalf("Logout: %v", err)
	}
	if _, err := auth.Authenticate(ctx, token); err != ErrNotFound {
		t.Errorf("err = %v, want ErrNotFound after logout", err)
	}
}

func TestPasswordHashIsNeverStoredInPlaintext(t *testing.T) {
	store := newFakeStore()
	auth := NewAuth(store)
	if err := auth.Register(context.Background(), "@alex", "correct horse"); err != nil {
		t.Fatalf("Register: %v", err)
	}
	u := store.usersByHandle["@alex"]
	if u.PasswordHash == "correct horse" {
		t.Fatal("password was stored in plaintext")
	}
	if len(u.PasswordHash) == 0 {
		t.Fatal("expected a non-empty password hash")
	}
}
