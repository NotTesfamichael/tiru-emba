package relay

import (
	"context"
	"fmt"
	"strings"
)

// Todos implements org-scoped shared task management on top of a Store,
// the same thin-business-layer split Auth/Orgs/Points already use.
// Personal (non-shared) todos never reach the server at all -- they're
// handled entirely client-side (internal/store/todo.go) since they're
// never meant to be visible to anyone else.
type Todos struct {
	store  Store
	points *Points
}

func NewTodos(store Store, points *Points) *Todos {
	return &Todos{store: store, points: points}
}

// Add creates a new shared todo in orgID. Returns ErrNotOrgMember if user
// doesn't belong to that org.
func (t *Todos) Add(ctx context.Context, orgID int64, user User, text string) (TodoInfo, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return TodoInfo{}, fmt.Errorf("relay: todo text is required")
	}
	if err := t.requireMember(ctx, orgID, user); err != nil {
		return TodoInfo{}, err
	}
	return t.store.CreateTodo(ctx, orgID, user.ID, user.Handle, text)
}

// List returns every todo in orgID. Returns ErrNotOrgMember if user
// doesn't belong to that org.
func (t *Todos) List(ctx context.Context, orgID int64, user User) ([]TodoInfo, error) {
	if err := t.requireMember(ctx, orgID, user); err != nil {
		return nil, err
	}
	return t.store.ListTodos(ctx, orgID)
}

// Complete marks todoID done and awards the completer points. Returns
// ErrNotOrgMember if user doesn't belong to orgID, or ErrNotFound if
// todoID doesn't exist in that org.
func (t *Todos) Complete(ctx context.Context, orgID, todoID int64, user User) (TodoInfo, error) {
	if err := t.requireMember(ctx, orgID, user); err != nil {
		return TodoInfo{}, err
	}
	todo, err := t.store.CompleteTodo(ctx, orgID, todoID)
	if err != nil {
		return TodoInfo{}, err
	}
	// Best-effort: a failed point award (e.g. a transient DB hiccup)
	// shouldn't undo an already-real todo completion.
	_ = t.points.AwardTodoComplete(ctx, user.ID)
	return todo, nil
}

func (t *Todos) requireMember(ctx context.Context, orgID int64, user User) error {
	member, err := t.store.IsOrgMember(ctx, orgID, user.ID)
	if err != nil {
		return err
	}
	if !member {
		return ErrNotOrgMember
	}
	return nil
}
