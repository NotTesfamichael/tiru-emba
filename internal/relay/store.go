// Package relay implements the server side of tiru-emba's cross-network
// mode: account registration/login, session tokens, and (in later phases)
// organizations and message relaying between authenticated users on
// different networks. It's a separate system from internal/network, which
// remains the LAN-only peer-to-peer path.
package relay

import (
	"context"
	"time"
)

// User is one registered account.
type User struct {
	ID           int64
	Handle       string
	PasswordHash string
	CreatedAt    time.Time
}

// Org is one organization/workspace: an isolated group with its own
// membership, scoping presence and relaying (Phase 3) instead of the flat
// global roster Phase 2 used.
type Org struct {
	ID          int64
	Name        string
	OwnerUserID int64
	CreatedAt   time.Time
}

// Store is the persistence boundary Auth depends on, so auth logic (hashing,
// token generation, expiry rules) can be unit tested against a fake instead
// of a real database. PGStore is the real, PostgreSQL-backed implementation.
type Store interface {
	// CreateUser creates a new account. Returns ErrHandleTaken if handle is
	// already registered.
	CreateUser(ctx context.Context, handle, passwordHash string) (User, error)

	// UserByHandle looks up an account by handle. Returns ErrNotFound if
	// there isn't one.
	UserByHandle(ctx context.Context, handle string) (User, error)

	// CreateSession records a new session token for userID, valid until
	// expiresAt.
	CreateSession(ctx context.Context, userID int64, token string, expiresAt time.Time) error

	// UserBySessionToken resolves a session token to the account it
	// belongs to, plus that session's expiry. Returns ErrNotFound if the
	// token doesn't exist (expiry itself is checked by the caller, Auth,
	// not here, so Store stays a plain data layer).
	UserBySessionToken(ctx context.Context, token string) (user User, expiresAt time.Time, err error)

	// DeleteSession revokes a session token (e.g. on logout). Deleting a
	// token that doesn't exist is not an error.
	DeleteSession(ctx context.Context, token string) error

	// CreateOrg creates a new org and adds ownerUserID as its first member
	// with the 'admin' role, atomically.
	CreateOrg(ctx context.Context, name string, ownerUserID int64) (Org, error)

	// OrgsForUser lists every org userID belongs to.
	OrgsForUser(ctx context.Context, userID int64) ([]Org, error)

	// OrgMemberHandles lists the handles of every member of one org.
	OrgMemberHandles(ctx context.Context, orgID int64) ([]string, error)

	// OrgMateHandles lists the handles of every user who shares at least
	// one org with userID (excluding userID itself), across all orgs they
	// belong to combined -- used to scope presence broadcasts.
	OrgMateHandles(ctx context.Context, userID int64) ([]string, error)

	// SharesOrg reports whether userID1 and userID2 belong to at least one
	// common org -- used to gate relay delivery.
	SharesOrg(ctx context.Context, userID1, userID2 int64) (bool, error)

	// IsOrgAdmin reports whether userID is an admin of orgID.
	IsOrgAdmin(ctx context.Context, orgID, userID int64) (bool, error)

	// CreateOrgInvite records a new single-use invite code for orgID.
	CreateOrgInvite(ctx context.Context, orgID, createdBy int64, code string, expiresAt time.Time) error

	// RedeemOrgInvite validates code (exists, not expired, not already
	// fully used) and adds userID to that org as a member, atomically.
	// Returns ErrInviteInvalid for a bad/expired/exhausted code, and
	// ErrAlreadyOrgMember if userID already belongs to that org.
	RedeemOrgInvite(ctx context.Context, code string, userID int64) (Org, error)
}
