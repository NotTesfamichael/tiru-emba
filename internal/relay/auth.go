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

const minPasswordLength = 8

// Auth implements account registration/login on top of a Store, kept
// separate from the storage layer so hashing, token generation, and expiry
// rules can be unit tested against a fake Store instead of a real database.
type Auth struct {
	store Store
}

func NewAuth(store Store) *Auth {
	return &Auth{store: store}
}

// Register creates a new account. Returns ErrHandleTaken if handle is
// already registered.
func (a *Auth) Register(ctx context.Context, handle, password string) error {
	handle = NormalizeHandle(handle)
	if err := validateHandle(handle); err != nil {
		return err
	}
	if len(password) < minPasswordLength {
		return fmt.Errorf("relay: password must be at least %d characters", minPasswordLength)
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("relay: hash password: %w", err)
	}
	_, err = a.store.CreateUser(ctx, handle, string(hash))
	return err
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

	token, err = randomToken()
	if err != nil {
		return User{}, "", time.Time{}, err
	}
	expiresAt = time.Now().Add(sessionTTL)
	if err := a.store.CreateSession(ctx, user.ID, token, expiresAt); err != nil {
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
