package relay

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

// inviteTTL is how long a generated invite code stays redeemable.
const inviteTTL = 7 * 24 * time.Hour

// Orgs implements organization management on top of a Store, kept separate
// from Auth (a different concern) but following the same pattern: business
// rules here, persistence in Store, so this is unit-testable against a fake.
type Orgs struct {
	store Store
}

func NewOrgs(store Store) *Orgs {
	return &Orgs{store: store}
}

// Create makes a new org with owner as its first member and admin.
func (o *Orgs) Create(ctx context.Context, name string, owner User) (Org, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Org{}, fmt.Errorf("relay: org name is required")
	}
	return o.store.CreateOrg(ctx, name, owner.ID)
}

// List returns every org user belongs to.
func (o *Orgs) List(ctx context.Context, user User) ([]Org, error) {
	return o.store.OrgsForUser(ctx, user.ID)
}

// Invite generates a new single-use, time-limited invite code for orgID.
// Only an admin of that org may do this.
func (o *Orgs) Invite(ctx context.Context, orgID int64, creator User) (code string, expiresAt time.Time, err error) {
	isAdmin, err := o.store.IsOrgAdmin(ctx, orgID, creator.ID)
	if err != nil {
		return "", time.Time{}, err
	}
	if !isAdmin {
		return "", time.Time{}, ErrNotOrgAdmin
	}

	code, err = randomInviteCode()
	if err != nil {
		return "", time.Time{}, err
	}
	expiresAt = time.Now().Add(inviteTTL)
	if err := o.store.CreateOrgInvite(ctx, orgID, creator.ID, code, expiresAt); err != nil {
		return "", time.Time{}, err
	}
	return code, expiresAt, nil
}

// Join redeems an invite code, adding user to the org it belongs to.
func (o *Orgs) Join(ctx context.Context, code string, user User) (Org, error) {
	return o.store.RedeemOrgInvite(ctx, code, user.ID)
}

// MateHandles returns every handle who shares at least one org with user,
// across every org they belong to combined -- used to scope presence.
func (o *Orgs) MateHandles(ctx context.Context, user User) ([]string, error) {
	return o.store.OrgMateHandles(ctx, user.ID)
}

// SharesOrgWith reports whether a and b belong to at least one common org
// -- used to gate relay delivery.
func (o *Orgs) SharesOrgWith(ctx context.Context, a, b User) (bool, error) {
	return o.store.SharesOrg(ctx, a.ID, b.ID)
}

// MemberHandles returns every handle belonging to orgID.
func (o *Orgs) MemberHandles(ctx context.Context, orgID int64) ([]string, error) {
	return o.store.OrgMemberHandles(ctx, orgID)
}

func randomInviteCode() (string, error) {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("relay: generate invite code: %w", err)
	}
	return hex.EncodeToString(b), nil
}
