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

	// IsAdmin is a system-wide (not per-org) role: only admins may create
	// new organizations (see Orgs.Create) -- everyone else can only join
	// one via an invite code. Set via PromoteToAdmin, e.g. from
	// cmd/tiru-server's --admin-handle flag; there's no self-service way
	// to become one.
	IsAdmin bool
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

// UserProfile holds the account-flavor data added alongside the core User
// record: registration's ASCII-art avatar, the gamification points
// balance, which unlockable (if any) is currently equipped, and the
// security question used to recover a forgotten password.
type UserProfile struct {
	UserID             int64
	AvatarASCII        string
	SecurityQuestion   string
	SecurityAnswerHash string
	Points             int
	ActiveUnlockableID *int64
}

// Unlockable is one catalog entry a user can redeem with points: a custom
// ASCII avatar or border.
type Unlockable struct {
	ID       int64
	Name     string
	Kind     string // "avatar" or "border"
	AsciiArt string
	Cost     int
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

	// PromoteToAdmin marks handle as a system admin. A no-op (not an
	// error) if handle isn't registered yet -- the operator is expected to
	// re-run whatever sets this (e.g. restart with --admin-handle) after
	// the account exists.
	PromoteToAdmin(ctx context.Context, handle string) error

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

	// CreateUserProfile creates the profile row for a newly-registered
	// user, right after CreateUser. avatarASCII is what registration's
	// local image-to-ASCII conversion produced (may be empty).
	// securityAnswerHash is the bcrypt hash of the answer to
	// securityQuestion; both may be empty, meaning the account opted out
	// of password recovery.
	CreateUserProfile(ctx context.Context, userID int64, avatarASCII, securityQuestion, securityAnswerHash string) error

	// ProfileByUserID looks up a user's profile. Returns ErrNotFound if
	// there isn't one.
	ProfileByUserID(ctx context.Context, userID int64) (UserProfile, error)

	// AddPoints adds delta (negative to spend) to userID's points balance.
	AddPoints(ctx context.Context, userID int64, delta int) error

	// UpdatePassword replaces userID's password hash -- used by
	// Auth.RecoverFinish after a successful security-answer check.
	UpdatePassword(ctx context.Context, userID int64, newPasswordHash string) error

	// ListUnlockables returns the full unlockables catalog.
	ListUnlockables(ctx context.Context) ([]Unlockable, error)

	// UnlockedIDs returns the set of unlockable IDs userID has already
	// redeemed.
	UnlockedIDs(ctx context.Context, userID int64) (map[int64]bool, error)

	// RedeemUnlockable atomically checks userID has enough points for
	// unlockableID, deducts the cost, and records the unlock. Returns
	// ErrNotFound if unlockableID doesn't exist, ErrAlreadyUnlocked if
	// already redeemed, or ErrInsufficientPoints if too few points.
	RedeemUnlockable(ctx context.Context, userID, unlockableID int64) error

	// SetActiveUnlockable sets which already-redeemed unlockable is
	// userID's currently-equipped avatar/border. A nil unlockableID
	// clears it back to the default registration avatar.
	SetActiveUnlockable(ctx context.Context, userID int64, unlockableID *int64) error

	// IsOrgMember reports whether userID belongs to orgID, in any role --
	// unlike IsOrgAdmin, membership alone is enough for shared-todo access.
	IsOrgMember(ctx context.Context, orgID, userID int64) (bool, error)

	// CreateTodo creates a new shared todo in orgID.
	CreateTodo(ctx context.Context, orgID, createdByUserID int64, createdByHandle, text string) (TodoInfo, error)

	// ListTodos returns every todo (done and not) in orgID, oldest first.
	ListTodos(ctx context.Context, orgID int64) ([]TodoInfo, error)

	// CompleteTodo marks todoID (which must belong to orgID) done.
	// Returns ErrNotFound if there's no such todo in that org.
	CompleteTodo(ctx context.Context, orgID, todoID int64) (TodoInfo, error)
}
