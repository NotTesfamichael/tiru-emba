package store

import (
	"testing"
)

func TestTodoAddAndCompleteRoundTrip(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	ts, items, err := OpenTodos("@alex")
	if err != nil {
		t.Fatalf("OpenTodos: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("expected no existing items, got %d", len(items))
	}

	items, created, err := ts.Add(items, "buy milk")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if created.Text != "buy milk" || created.Done {
		t.Errorf("created = %+v, want Text=%q Done=false", created, "buy milk")
	}

	items, ok, err := ts.Complete(items, created.ID)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if !ok {
		t.Fatal("Complete returned ok=false for an existing id")
	}
	if !items[0].Done {
		t.Error("expected item to be marked done")
	}
}

func TestTodoCompleteUnknownIDIsNoop(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ts, items, _ := OpenTodos("@alex")
	_, ok, err := ts.Complete(items, 999)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if ok {
		t.Error("expected ok=false for an unknown id")
	}
}

func TestTodoPersistsAcrossReopen(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	ts, items, _ := OpenTodos("@alex")
	items, _, err := ts.Add(items, "buy milk")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if _, _, err := ts.Add(items, "walk dog"); err != nil {
		t.Fatalf("Add: %v", err)
	}

	_, reloaded, err := OpenTodos("@alex")
	if err != nil {
		t.Fatalf("re-open OpenTodos: %v", err)
	}
	if len(reloaded) != 2 {
		t.Fatalf("reloaded = %d items, want 2", len(reloaded))
	}
	if reloaded[0].Text != "buy milk" || reloaded[1].Text != "walk dog" {
		t.Errorf("reloaded = %+v", reloaded)
	}
}

func TestTodoDifferentHandlesAreIsolated(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	tsAlex, itemsAlex, _ := OpenTodos("@alex")
	if _, _, err := tsAlex.Add(itemsAlex, "alex's todo"); err != nil {
		t.Fatalf("Add: %v", err)
	}

	_, itemsBob, err := OpenTodos("@bob")
	if err != nil {
		t.Fatalf("OpenTodos @bob: %v", err)
	}
	if len(itemsBob) != 0 {
		t.Errorf("expected @bob's todos to be empty, got %+v", itemsBob)
	}
}
