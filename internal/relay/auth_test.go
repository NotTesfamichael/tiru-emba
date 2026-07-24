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

	profiles     map[int64]UserProfile
	unlockables  map[int64]Unlockable
	nextUnlockID int64
	unlocks      map[int64]map[int64]bool // userID -> unlockableID -> owned

	todos      map[int64]TodoInfo
	todoOrgs   map[int64]int64 // todoID -> orgID
	nextTodoID int64
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
		profiles:      make(map[int64]UserProfile),
		unlockables:   make(map[int64]Unlockable),
		unlocks:       make(map[int64]map[int64]bool),
		todos:         make(map[int64]TodoInfo),
		todoOrgs:      make(map[int64]int64),
	}
}

func (f *fakeStore) CreateUserProfile(ctx context.Context, userID int64, avatarASCII, securityQuestion, securityAnswerHash string) error {
	f.profiles[userID] = UserProfile{
		UserID: userID, AvatarASCII: avatarASCII,
		SecurityQuestion: securityQuestion, SecurityAnswerHash: securityAnswerHash,
	}
	return nil
}

func (f *fakeStore) ProfileByUserID(ctx context.Context, userID int64) (UserProfile, error) {
	p, ok := f.profiles[userID]
	if !ok {
		return UserProfile{}, ErrNotFound
	}
	return p, nil
}

func (f *fakeStore) AddPoints(ctx context.Context, userID int64, delta int) error {
	p := f.profiles[userID]
	p.Points += delta
	f.profiles[userID] = p
	return nil
}

func (f *fakeStore) UpdatePassword(ctx context.Context, userID int64, newPasswordHash string) error {
	u := f.usersByID[userID]
	u.PasswordHash = newPasswordHash
	f.usersByID[u.ID] = u
	f.usersByHandle[u.Handle] = u
	return nil
}

func (f *fakeStore) ListUnlockables(ctx context.Context) ([]Unlockable, error) {
	var result []Unlockable
	for _, u := range f.unlockables {
		result = append(result, u)
	}
	return result, nil
}

func (f *fakeStore) UnlockedIDs(ctx context.Context, userID int64) (map[int64]bool, error) {
	result := make(map[int64]bool)
	for id, owned := range f.unlocks[userID] {
		if owned {
			result[id] = true
		}
	}
	return result, nil
}

func (f *fakeStore) RedeemUnlockable(ctx context.Context, userID, unlockableID int64) error {
	u, ok := f.unlockables[unlockableID]
	if !ok {
		return ErrNotFound
	}
	if f.unlocks[userID][unlockableID] {
		return ErrAlreadyUnlocked
	}
	p := f.profiles[userID]
	if p.Points < u.Cost {
		return ErrInsufficientPoints
	}
	p.Points -= u.Cost
	f.profiles[userID] = p
	if f.unlocks[userID] == nil {
		f.unlocks[userID] = make(map[int64]bool)
	}
	f.unlocks[userID][unlockableID] = true
	return nil
}

func (f *fakeStore) SetActiveUnlockable(ctx context.Context, userID int64, unlockableID *int64) error {
	p := f.profiles[userID]
	p.ActiveUnlockableID = unlockableID
	f.profiles[userID] = p
	return nil
}

func (f *fakeStore) IsOrgMember(ctx context.Context, orgID, userID int64) (bool, error) {
	_, ok := f.orgMembers[orgID][userID]
	return ok, nil
}

func (f *fakeStore) CreateTodo(ctx context.Context, orgID, createdByUserID int64, createdByHandle, text string) (TodoInfo, error) {
	f.nextTodoID++
	t := TodoInfo{ID: f.nextTodoID, Text: text, CreatedBy: createdByHandle, CreatedAt: time.Now()}
	f.todos[t.ID] = t
	f.todoOrgs[t.ID] = orgID
	return t, nil
}

func (f *fakeStore) ListTodos(ctx context.Context, orgID int64) ([]TodoInfo, error) {
	var result []TodoInfo
	for id, t := range f.todos {
		if f.todoOrgs[id] == orgID {
			result = append(result, t)
		}
	}
	return result, nil
}

func (f *fakeStore) CompleteTodo(ctx context.Context, orgID, todoID int64) (TodoInfo, error) {
	t, ok := f.todos[todoID]
	if !ok || f.todoOrgs[todoID] != orgID {
		return TodoInfo{}, ErrNotFound
	}
	t.Done = true
	f.todos[todoID] = t
	return t, nil
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

func (f *fakeStore) PromoteToAdmin(ctx context.Context, handle string) error {
	u, ok := f.usersByHandle[handle]
	if !ok {
		return nil
	}
	u.IsAdmin = true
	f.usersByHandle[handle] = u
	f.usersByID[u.ID] = u
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

	if err := auth.Register(ctx, "alex", "correct horse", "", "", ""); err != nil {
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

	if err := auth.Register(ctx, "alex", "correct horse", "", "", ""); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if _, ok := store.usersByHandle["@alex"]; !ok {
		t.Error("expected the stored handle to be normalized to \"@alex\"")
	}
}

func TestRegisterDuplicateHandleFails(t *testing.T) {
	auth := NewAuth(newFakeStore())
	ctx := context.Background()

	if err := auth.Register(ctx, "@alex", "correct horse", "", "", ""); err != nil {
		t.Fatalf("first Register: %v", err)
	}
	err := auth.Register(ctx, "@alex", "a different password", "", "", "")
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
	if err := auth.Register(context.Background(), "@alex", "short", "", "", ""); err == nil {
		t.Error("expected a short password to be rejected")
	}
}

func TestLoginWrongPasswordFails(t *testing.T) {
	auth := NewAuth(newFakeStore())
	ctx := context.Background()
	_ = auth.Register(ctx, "@alex", "correct horse", "", "", "")

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
	_ = auth.Register(ctx, "@alex", "correct horse", "", "", "")

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
	_ = auth.Register(ctx, "@alex", "correct horse", "", "", "")
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
	if err := auth.Register(context.Background(), "@alex", "correct horse", "", "", ""); err != nil {
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

func TestRegisterStoresProfile(t *testing.T) {
	store := newFakeStore()
	auth := NewAuth(store)
	ctx := context.Background()
	if err := auth.Register(ctx, "@alex", "correct horse", "(^_^)", "favorite color?", "Blue"); err != nil {
		t.Fatalf("Register: %v", err)
	}
	u := store.usersByHandle["@alex"]
	profile, err := store.ProfileByUserID(ctx, u.ID)
	if err != nil {
		t.Fatalf("ProfileByUserID: %v", err)
	}
	if profile.AvatarASCII != "(^_^)" {
		t.Errorf("AvatarASCII = %q, want %q", profile.AvatarASCII, "(^_^)")
	}
	if profile.SecurityQuestion != "favorite color?" {
		t.Errorf("SecurityQuestion = %q, want %q", profile.SecurityQuestion, "favorite color?")
	}
	if profile.SecurityAnswerHash == "Blue" || profile.SecurityAnswerHash == "" {
		t.Errorf("SecurityAnswerHash looks unhashed or empty: %q", profile.SecurityAnswerHash)
	}
}

func TestResumeSessionRotatesTokenAndRevokesOld(t *testing.T) {
	auth := NewAuth(newFakeStore())
	ctx := context.Background()
	_ = auth.Register(ctx, "@alex", "correct horse", "", "", "")
	_, oldToken, _, _ := auth.Login(ctx, "@alex", "correct horse")

	user, newToken, expiresAt, err := auth.ResumeSession(ctx, oldToken)
	if err != nil {
		t.Fatalf("ResumeSession: %v", err)
	}
	if user.Handle != "@alex" {
		t.Errorf("Handle = %q, want %q", user.Handle, "@alex")
	}
	if newToken == "" || newToken == oldToken {
		t.Errorf("expected a fresh, different token, got %q (old was %q)", newToken, oldToken)
	}
	if !expiresAt.After(time.Now()) {
		t.Errorf("expected expiresAt in the future, got %v", expiresAt)
	}
	if _, err := auth.Authenticate(ctx, oldToken); err != ErrNotFound {
		t.Errorf("old token should be revoked after resume, err = %v, want ErrNotFound", err)
	}
	if _, err := auth.Authenticate(ctx, newToken); err != nil {
		t.Errorf("new token should authenticate, got err = %v", err)
	}
}

func TestResumeSessionRejectsExpiredToken(t *testing.T) {
	store := newFakeStore()
	auth := NewAuth(store)
	ctx := context.Background()
	_ = auth.Register(ctx, "@alex", "correct horse", "", "", "")
	_, token, _, _ := auth.Login(ctx, "@alex", "correct horse")
	sess := store.sessions[token]
	sess.expiresAt = time.Now().Add(-time.Minute)
	store.sessions[token] = sess

	if _, _, _, err := auth.ResumeSession(ctx, token); err != ErrSessionExpired {
		t.Errorf("err = %v, want ErrSessionExpired", err)
	}
}

func TestRecoverStartReturnsQuestion(t *testing.T) {
	auth := NewAuth(newFakeStore())
	ctx := context.Background()
	_ = auth.Register(ctx, "@alex", "correct horse", "", "favorite color?", "blue")

	question, err := auth.RecoverStart(ctx, "@alex")
	if err != nil {
		t.Fatalf("RecoverStart: %v", err)
	}
	if question != "favorite color?" {
		t.Errorf("question = %q, want %q", question, "favorite color?")
	}
}

func TestRecoverStartRefusesAccountWithoutQuestion(t *testing.T) {
	auth := NewAuth(newFakeStore())
	ctx := context.Background()
	_ = auth.Register(ctx, "@alex", "correct horse", "", "", "")

	if _, err := auth.RecoverStart(ctx, "@alex"); err != ErrNoSecurityQuestion {
		t.Errorf("err = %v, want ErrNoSecurityQuestion", err)
	}
}

func TestRecoverStartUnknownHandle(t *testing.T) {
	auth := NewAuth(newFakeStore())
	if _, err := auth.RecoverStart(context.Background(), "@nobody"); err != ErrNotFound {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestRecoverFinishWrongAnswerFails(t *testing.T) {
	auth := NewAuth(newFakeStore())
	ctx := context.Background()
	_ = auth.Register(ctx, "@alex", "correct horse", "", "favorite color?", "blue")

	if _, _, _, err := auth.RecoverFinish(ctx, "@alex", "red", "new password123"); err != ErrWrongSecurityAnswer {
		t.Errorf("err = %v, want ErrWrongSecurityAnswer", err)
	}
}

func TestRecoverFinishSucceedsCaseInsensitivelyAndLogsIn(t *testing.T) {
	auth := NewAuth(newFakeStore())
	ctx := context.Background()
	_ = auth.Register(ctx, "@alex", "correct horse", "", "favorite color?", "Blue")

	user, token, expiresAt, err := auth.RecoverFinish(ctx, "@alex", "  blue  ", "new password123")
	if err != nil {
		t.Fatalf("RecoverFinish: %v", err)
	}
	if user.Handle != "@alex" {
		t.Errorf("Handle = %q, want %q", user.Handle, "@alex")
	}
	if token == "" || !expiresAt.After(time.Now()) {
		t.Errorf("expected a fresh valid session, got token=%q expiresAt=%v", token, expiresAt)
	}

	// The new password should now work, and the old one shouldn't.
	if _, _, _, err := auth.Login(ctx, "@alex", "new password123"); err != nil {
		t.Errorf("Login with new password: %v", err)
	}
	if _, _, _, err := auth.Login(ctx, "@alex", "correct horse"); err != ErrInvalidCredentials {
		t.Errorf("Login with old password should now fail, err = %v", err)
	}
}
