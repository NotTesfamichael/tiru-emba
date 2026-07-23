package relay

import "errors"

var (
	// ErrNotFound is returned when a lookup (by handle or session token)
	// finds nothing.
	ErrNotFound = errors.New("relay: not found")

	// ErrHandleTaken is returned by CreateUser when the handle already has
	// an account.
	ErrHandleTaken = errors.New("relay: handle already registered")

	// ErrInvalidCredentials is returned by Auth.Login for either a
	// nonexistent handle or a wrong password -- deliberately not
	// distinguished, so a login attempt can't be used to enumerate which
	// handles are registered.
	ErrInvalidCredentials = errors.New("relay: invalid handle or password")

	// ErrSessionExpired is returned by Auth.Authenticate for a session
	// token that exists but has passed its expiry.
	ErrSessionExpired = errors.New("relay: session expired")

	// ErrInviteInvalid is returned by RedeemOrgInvite for a code that
	// doesn't exist, has expired, or has already been used up.
	ErrInviteInvalid = errors.New("relay: invite code is invalid or expired")

	// ErrAlreadyOrgMember is returned by RedeemOrgInvite when the redeeming
	// user already belongs to that org.
	ErrAlreadyOrgMember = errors.New("relay: already a member of that org")

	// ErrNotOrgAdmin is returned by Orgs.Invite when the caller isn't an
	// admin of the org they're trying to generate an invite for.
	ErrNotOrgAdmin = errors.New("relay: only an org admin can do that")
)
