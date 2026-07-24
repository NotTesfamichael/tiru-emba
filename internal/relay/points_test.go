package relay

import (
	"context"
	"testing"
)

func TestRedeemUnlockableSpendsPointsAndRecordsUnlock(t *testing.T) {
	store := newFakeStore()
	points := NewPoints(store)
	ctx := context.Background()

	user := User{ID: 1, Handle: "@alex"}
	store.profiles[1] = UserProfile{UserID: 1, Points: 20}
	store.unlockables[1] = Unlockable{ID: 1, Name: "Shades", Kind: "avatar", Cost: 5}

	if err := points.Redeem(ctx, user, 1); err != nil {
		t.Fatalf("Redeem: %v", err)
	}
	profile, _ := store.ProfileByUserID(ctx, 1)
	if profile.Points != 15 {
		t.Errorf("Points = %d, want 15", profile.Points)
	}

	_, unlocked, err := points.ListUnlockables(ctx, user)
	if err != nil {
		t.Fatalf("ListUnlockables: %v", err)
	}
	if !unlocked[1] {
		t.Error("expected unlockable 1 to be marked owned")
	}
}

func TestRedeemUnlockableInsufficientPoints(t *testing.T) {
	store := newFakeStore()
	points := NewPoints(store)
	ctx := context.Background()

	user := User{ID: 1, Handle: "@alex"}
	store.profiles[1] = UserProfile{UserID: 1, Points: 2}
	store.unlockables[1] = Unlockable{ID: 1, Name: "Shades", Kind: "avatar", Cost: 5}

	if err := points.Redeem(ctx, user, 1); err != ErrInsufficientPoints {
		t.Errorf("err = %v, want ErrInsufficientPoints", err)
	}
}

func TestRedeemUnlockableAlreadyUnlocked(t *testing.T) {
	store := newFakeStore()
	points := NewPoints(store)
	ctx := context.Background()

	user := User{ID: 1, Handle: "@alex"}
	store.profiles[1] = UserProfile{UserID: 1, Points: 100}
	store.unlockables[1] = Unlockable{ID: 1, Name: "Shades", Kind: "avatar", Cost: 5}

	if err := points.Redeem(ctx, user, 1); err != nil {
		t.Fatalf("first Redeem: %v", err)
	}
	if err := points.Redeem(ctx, user, 1); err != ErrAlreadyUnlocked {
		t.Errorf("second Redeem err = %v, want ErrAlreadyUnlocked", err)
	}
}

func TestSetActiveRequiresOwnership(t *testing.T) {
	store := newFakeStore()
	points := NewPoints(store)
	ctx := context.Background()

	user := User{ID: 1, Handle: "@alex"}
	store.unlockables[1] = Unlockable{ID: 1, Name: "Shades", Kind: "avatar", Cost: 5}

	if err := points.SetActive(ctx, user, 1); err != ErrNotFound {
		t.Errorf("err = %v, want ErrNotFound (not yet unlocked)", err)
	}

	store.profiles[1] = UserProfile{UserID: 1, Points: 100}
	if err := points.Redeem(ctx, user, 1); err != nil {
		t.Fatalf("Redeem: %v", err)
	}
	if err := points.SetActive(ctx, user, 1); err != nil {
		t.Fatalf("SetActive after redeem: %v", err)
	}
	profile, _ := store.ProfileByUserID(ctx, 1)
	if profile.ActiveUnlockableID == nil || *profile.ActiveUnlockableID != 1 {
		t.Errorf("ActiveUnlockableID = %v, want pointer to 1", profile.ActiveUnlockableID)
	}
}

func TestAwardMessageAndTodoComplete(t *testing.T) {
	store := newFakeStore()
	points := NewPoints(store)
	ctx := context.Background()
	store.profiles[1] = UserProfile{UserID: 1}

	if err := points.AwardMessage(ctx, 1); err != nil {
		t.Fatalf("AwardMessage: %v", err)
	}
	if err := points.AwardTodoComplete(ctx, 1); err != nil {
		t.Fatalf("AwardTodoComplete: %v", err)
	}
	profile, _ := store.ProfileByUserID(ctx, 1)
	if profile.Points != messageAward+todoCompleteAward {
		t.Errorf("Points = %d, want %d", profile.Points, messageAward+todoCompleteAward)
	}
}
