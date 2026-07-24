package relay

import (
	"context"
	"testing"
)

func TestTodoAddListComplete(t *testing.T) {
	store := newFakeStore()
	points := NewPoints(store)
	todos := NewTodos(store, points)
	ctx := context.Background()

	user := User{ID: 1, Handle: "@alex"}
	store.orgMembers[10] = map[int64]string{1: "member"}
	store.profiles[1] = UserProfile{UserID: 1}

	added, err := todos.Add(ctx, 10, user, "buy milk")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if added.Text != "buy milk" || added.Done {
		t.Errorf("added = %+v, want Text=%q Done=false", added, "buy milk")
	}

	list, err := todos.List(ctx, 10, user)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 || list[0].ID != added.ID {
		t.Errorf("List = %+v, want exactly the one added todo", list)
	}

	completed, err := todos.Complete(ctx, 10, added.ID, user)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if !completed.Done {
		t.Error("expected Done=true after Complete")
	}
	profile, _ := store.ProfileByUserID(ctx, 1)
	if profile.Points != todoCompleteAward {
		t.Errorf("Points = %d, want %d (awarded for completion)", profile.Points, todoCompleteAward)
	}
}

func TestTodoAddRequiresOrgMembership(t *testing.T) {
	store := newFakeStore()
	points := NewPoints(store)
	todos := NewTodos(store, points)
	ctx := context.Background()

	user := User{ID: 1, Handle: "@alex"}
	// Deliberately no org_members entry for orgID 10 / user 1.
	if _, err := todos.Add(ctx, 10, user, "buy milk"); err != ErrNotOrgMember {
		t.Errorf("err = %v, want ErrNotOrgMember", err)
	}
}

func TestTodoAddRejectsEmptyText(t *testing.T) {
	store := newFakeStore()
	points := NewPoints(store)
	todos := NewTodos(store, points)
	ctx := context.Background()

	user := User{ID: 1, Handle: "@alex"}
	store.orgMembers[10] = map[int64]string{1: "member"}

	if _, err := todos.Add(ctx, 10, user, "   "); err == nil {
		t.Error("expected an error for blank todo text")
	}
}

func TestTodoCompleteWrongOrgFails(t *testing.T) {
	store := newFakeStore()
	points := NewPoints(store)
	todos := NewTodos(store, points)
	ctx := context.Background()

	user := User{ID: 1, Handle: "@alex"}
	store.orgMembers[10] = map[int64]string{1: "member"}
	store.orgMembers[20] = map[int64]string{1: "member"}
	store.profiles[1] = UserProfile{UserID: 1}

	added, err := todos.Add(ctx, 10, user, "buy milk")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if _, err := todos.Complete(ctx, 20, added.ID, user); err != ErrNotFound {
		t.Errorf("err = %v, want ErrNotFound (todo belongs to a different org)", err)
	}
}
