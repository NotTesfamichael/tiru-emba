package relay

import "context"

// messageAward and todoCompleteAward are how many points a successfully
// relayed message and a completed shared todo earn their user,
// respectively -- the only two server-side point-earning hooks.
const (
	messageAward      = 1
	todoCompleteAward = 5
)

// Points implements the gamification layer on top of a Store: awarding
// points for activity, and letting a user browse/redeem/equip the
// unlockable ASCII avatars/borders those points can buy. Kept separate
// from Auth (account/session concerns) and Orgs (workspace concerns),
// the same "thin business layer over Store" split those two already use.
type Points struct {
	store Store
}

func NewPoints(store Store) *Points {
	return &Points{store: store}
}

// AwardMessage credits userID for a successfully relayed message.
func (p *Points) AwardMessage(ctx context.Context, userID int64) error {
	return p.store.AddPoints(ctx, userID, messageAward)
}

// AwardTodoComplete credits userID for completing a shared todo.
func (p *Points) AwardTodoComplete(ctx context.Context, userID int64) error {
	return p.store.AddPoints(ctx, userID, todoCompleteAward)
}

// Profile returns user's profile (avatar, points, active unlockable).
func (p *Points) Profile(ctx context.Context, user User) (UserProfile, error) {
	return p.store.ProfileByUserID(ctx, user.ID)
}

// ListUnlockables returns the full catalog plus which entries user has
// already redeemed -- left as two separate values (rather than a single
// "Owned bool" field baked into a summary struct here) so the server, not
// this business layer, decides how to shape the wire response, the same
// division of labor Orgs.List/Orgs.MemberHandles already leave to
// server.go's handleAuthed.
func (p *Points) ListUnlockables(ctx context.Context, user User) ([]Unlockable, map[int64]bool, error) {
	all, err := p.store.ListUnlockables(ctx)
	if err != nil {
		return nil, nil, err
	}
	unlocked, err := p.store.UnlockedIDs(ctx, user.ID)
	if err != nil {
		return nil, nil, err
	}
	return all, unlocked, nil
}

// Redeem spends user's points to unlock unlockableID. See
// Store.RedeemUnlockable for the possible sentinel errors.
func (p *Points) Redeem(ctx context.Context, user User, unlockableID int64) error {
	return p.store.RedeemUnlockable(ctx, user.ID, unlockableID)
}

// SetActive equips unlockableID as user's active avatar/border. Returns
// ErrNotFound if user hasn't redeemed it.
func (p *Points) SetActive(ctx context.Context, user User, unlockableID int64) error {
	unlocked, err := p.store.UnlockedIDs(ctx, user.ID)
	if err != nil {
		return err
	}
	if !unlocked[unlockableID] {
		return ErrNotFound
	}
	return p.store.SetActiveUnlockable(ctx, user.ID, &unlockableID)
}
