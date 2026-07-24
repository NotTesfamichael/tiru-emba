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

	// ErrNoSecurityQuestion is returned by Auth.RecoverStart/RecoverFinish
	// when the account never set one up at registration -- refused
	// outright rather than exposing/matching an empty question, which
	// would otherwise let anyone recover such an account with a blank
	// answer.
	ErrNoSecurityQuestion = errors.New("relay: no recovery question set for this account")

	// ErrWrongSecurityAnswer is returned by Auth.RecoverFinish when the
	// submitted answer doesn't match the one hashed at registration.
	ErrWrongSecurityAnswer = errors.New("relay: security answer is incorrect")

	// ErrInsufficientPoints is returned by RedeemUnlockable when the user
	// doesn't have enough points to cover the unlockable's cost.
	ErrInsufficientPoints = errors.New("relay: not enough points")

	// ErrAlreadyUnlocked is returned by RedeemUnlockable when the user has
	// already redeemed that unlockable.
	ErrAlreadyUnlocked = errors.New("relay: already unlocked")

	// ErrNotOrgMember is returned by Todos.Add/List/Complete when the
	// caller doesn't belong to the org they're trying to act on.
	ErrNotOrgMember = errors.New("relay: not a member of that organization")

	// ErrNotSystemAdmin is returned by Orgs.Create when the caller isn't a
	// system admin (User.IsAdmin) -- a coarser, separate concept from
	// per-org admin (ErrNotOrgAdmin): only system admins may create a new
	// org at all, everyone else can only join one via an invite code.
	ErrNotSystemAdmin = errors.New("relay: only an admin can create an organization")
)
