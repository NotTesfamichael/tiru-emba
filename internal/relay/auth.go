package relay

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// sessionTTL is how long a session token stays valid after login, mirroring
// the "remembered handle" convenience the LAN client already has via
// internal/config, just server-enforced instead of purely local.
const sessionTTL = 30 * 24 * time.Hour

const minPasswordLength = 6

// Auth implements account registration/login on top of a Store, kept
// separate from the storage layer so hashing, token generation, and expiry
// rules can be unit tested against a fake Store instead of a real database.
type Auth struct {
	store Store
}

func NewAuth(store Store) *Auth {
	return &Auth{store: store}
}

// Register creates a new account, plus its profile (ASCII avatar and
// optional security question for later recovery). Returns ErrHandleTaken
// if handle is already registered. avatarASCII, securityQuestion, and
// securityAnswer may all be empty -- an account can skip either or both;
// skipping the security question just means RecoverStart/RecoverFinish
// will refuse recovery for it later (ErrNoSecurityQuestion), not that
// registration itself fails.
func (a *Auth) Register(ctx context.Context, handle, password, avatarASCII, securityQuestion, securityAnswer string) error {
	handle = NormalizeHandle(handle)
	if err := validateHandle(handle); err != nil {
		return err
	}
	if len(password) < minPasswordLength {
		return fmt.Errorf("relay: password must be at least %d characters", minPasswordLength)
	}
	securityQuestion = strings.TrimSpace(securityQuestion)

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("relay: hash password: %w", err)
	}

	// Only pay bcrypt's (deliberately expensive) cost for the security
	// answer when there's an actual question to go with it -- an empty
	// question already makes RecoverStart/RecoverFinish refuse recovery
	// outright (ErrNoSecurityQuestion) before ever touching this hash, so
	// hashing a blank answer in that case would just be wasted work on
	// every registration that skips this optional step.
	var answerHash string
	if securityQuestion != "" {
		h, err := bcrypt.GenerateFromPassword([]byte(normalizeAnswer(securityAnswer)), bcrypt.DefaultCost)
		if err != nil {
			return fmt.Errorf("relay: hash security answer: %w", err)
		}
		answerHash = string(h)
	}

	user, err := a.store.CreateUser(ctx, handle, string(hash))
	if err != nil {
		return err
	}
	return a.store.CreateUserProfile(ctx, user.ID, avatarASCII, securityQuestion, answerHash)
}

// normalizeAnswer makes a security-answer comparison forgiving of case and
// surrounding whitespace, the same way a human answering "Blue" vs "blue "
// would expect to still match.
func normalizeAnswer(answer string) string {
	return strings.ToLower(strings.TrimSpace(answer))
}

// Login verifies handle/password and issues a new session token valid for
// sessionTTL, along with the account it authenticated (callers past Phase 1
// need at least the handle to register presence). Deliberately returns the
// same ErrInvalidCredentials whether the handle doesn't exist or the
// password is wrong, so a login attempt can't be used to enumerate
// registered handles.
func (a *Auth) Login(ctx context.Context, handle, password string) (user User, token string, expiresAt time.Time, err error) {
	handle = NormalizeHandle(handle)
	user, err = a.store.UserByHandle(ctx, handle)
	if errors.Is(err, ErrNotFound) {
		return User{}, "", time.Time{}, ErrInvalidCredentials
	}
	if err != nil {
		return User{}, "", time.Time{}, err
	}
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); err != nil {
		return User{}, "", time.Time{}, ErrInvalidCredentials
	}

	token, expiresAt, err = a.issueSession(ctx, user.ID)
	if err != nil {
		return User{}, "", time.Time{}, err
	}
	return user, token, expiresAt, nil
}

// Authenticate resolves a session token to the account it belongs to,
// rejecting one that's expired (ErrSessionExpired) as well as one that
// doesn't exist (ErrNotFound, from the Store).
func (a *Auth) Authenticate(ctx context.Context, token string) (User, error) {
	user, expiresAt, err := a.store.UserBySessionToken(ctx, token)
	if err != nil {
		return User{}, err
	}
	if time.Now().After(expiresAt) {
		return User{}, ErrSessionExpired
	}
	return user, nil
}

// ResumeSession validates a previously-issued token (e.g. one a client
// persisted locally between launches) and, on success, rotates it for a
// fresh one with a full new sessionTTL -- this is what lets a client "stay
// logged in automatically" indefinitely across restarts without ever
// re-prompting for a password, as long as it keeps resuming before the
// current token expires. The old token is revoked so it can't also still
// be used after the rotation.
func (a *Auth) ResumeSession(ctx context.Context, token string) (User, string, time.Time, error) {
	user, err := a.Authenticate(ctx, token)
	if err != nil {
		return User{}, "", time.Time{}, err
	}
	newToken, expiresAt, err := a.issueSession(ctx, user.ID)
	if err != nil {
		return User{}, "", time.Time{}, err
	}
	_ = a.store.DeleteSession(ctx, token) // best-effort; a resumed-but-not-yet-revoked old token is a benign, bounded race
	return user, newToken, expiresAt, nil
}

// RecoverStart returns the security question registered for handle, the
// first step of the "forgot password" flow. Returns ErrNotFound if handle
// isn't registered, or ErrNoSecurityQuestion if it is but never set one up
// (refused outright rather than exposing/matching a blank question, which
// would otherwise let anyone recover such an account with a blank answer).
func (a *Auth) RecoverStart(ctx context.Context, handle string) (string, error) {
	user, err := a.store.UserByHandle(ctx, NormalizeHandle(handle))
	if err != nil {
		return "", err
	}
	profile, err := a.store.ProfileByUserID(ctx, user.ID)
	if err != nil {
		return "", err
	}
	if profile.SecurityQuestion == "" {
		return "", ErrNoSecurityQuestion
	}
	return profile.SecurityQuestion, nil
}

// RecoverFinish checks answer against the account's stored security
// answer and, on success, sets newPassword and immediately issues a fresh
// session (the same "auto-logged-in on success" convenience Register
// gets), so a successful recovery doesn't also require a separate login
// step. Returns ErrNoSecurityQuestion/ErrNotFound the same as
// RecoverStart, or ErrWrongSecurityAnswer for a mismatched answer.
func (a *Auth) RecoverFinish(ctx context.Context, handle, answer, newPassword string) (User, string, time.Time, error) {
	if len(newPassword) < minPasswordLength {
		return User{}, "", time.Time{}, fmt.Errorf("relay: password must be at least %d characters", minPasswordLength)
	}
	user, err := a.store.UserByHandle(ctx, NormalizeHandle(handle))
	if err != nil {
		return User{}, "", time.Time{}, err
	}
	profile, err := a.store.ProfileByUserID(ctx, user.ID)
	if err != nil {
		return User{}, "", time.Time{}, err
	}
	if profile.SecurityQuestion == "" {
		return User{}, "", time.Time{}, ErrNoSecurityQuestion
	}
	if err := bcrypt.CompareHashAndPassword([]byte(profile.SecurityAnswerHash), []byte(normalizeAnswer(answer))); err != nil {
		return User{}, "", time.Time{}, ErrWrongSecurityAnswer
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return User{}, "", time.Time{}, fmt.Errorf("relay: hash password: %w", err)
	}
	if err := a.store.UpdatePassword(ctx, user.ID, string(hash)); err != nil {
		return User{}, "", time.Time{}, err
	}

	token, expiresAt, err := a.issueSession(ctx, user.ID)
	if err != nil {
		return User{}, "", time.Time{}, err
	}
	return user, token, expiresAt, nil
}

func (a *Auth) issueSession(ctx context.Context, userID int64) (token string, expiresAt time.Time, err error) {
	token, err = randomToken()
	if err != nil {
		return "", time.Time{}, err
	}
	expiresAt = time.Now().Add(sessionTTL)
	if err := a.store.CreateSession(ctx, userID, token, expiresAt); err != nil {
		return "", time.Time{}, err
	}
	return token, expiresAt, nil
}

// Lookup resolves handle to its account with no password check -- for
// internal server-side use only (e.g. resolving a relay target to check
// shared org membership), never exposed as a client-facing request, since
// it would otherwise let anyone probe which handles are registered.
func (a *Auth) Lookup(ctx context.Context, handle string) (User, error) {
	return a.store.UserByHandle(ctx, NormalizeHandle(handle))
}

// Logout revokes a session token.
func (a *Auth) Logout(ctx context.Context, token string) error {
	return a.store.DeleteSession(ctx, token)
}

// PromoteToAdmin marks handle as a system admin (see User.IsAdmin) --
// there's no self-service way to become one; this is meant to be called
// from server startup (e.g. cmd/tiru-server's --admin-handle flag), not
// exposed as a client-facing request.
func (a *Auth) PromoteToAdmin(ctx context.Context, handle string) error {
	return a.store.PromoteToAdmin(ctx, NormalizeHandle(handle))
}

func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("relay: generate session token: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// NormalizeHandle mirrors main.go's own handle normalization (trim, ensure
// a leading "@") so a handle registered via the relay and one used for LAN
// chat compare equal.
func NormalizeHandle(handle string) string {
	handle = strings.TrimSpace(handle)
	if handle == "" {
		return handle
	}
	if !strings.HasPrefix(handle, "@") {
		handle = "@" + handle
	}
	return handle
}

func validateHandle(handle string) error {
	// handle always has its leading "@" by the time this runs (via
	// NormalizeHandle), so the character budget is len(handle)-1.
	if n := len(handle) - 1; n < 1 || n > 32 {
		return fmt.Errorf("relay: handle must be 1-32 characters (excluding @)")
	}
	return nil
}
