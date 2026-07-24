package onboarding

import (
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/NotTesfamichael/tiru-emba/internal/relay"
)

// newTestModel returns a Model wired to a fake dialer that "succeeds"
// without ever touching the network, already past stepConnecting -- so
// tests can exercise the Welcome/wizard state machine in isolation.
func newTestModel(t *testing.T) Model {
	t.Helper()
	m := New("fake:1234", "@me", "")
	m.dial = func(addr string) (*relay.Client, error) { return nil, nil }

	msg := dialCmd(m.dial, m.serverAddr)()
	updated, _ := m.Update(msg)
	if updated.step != stepWelcome {
		t.Fatalf("setup: step = %v, want stepWelcome", updated.step)
	}
	return updated
}

func key(s string) tea.KeyMsg {
	switch s {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	}
}

func typeString(t *testing.T, m Model, s string) Model {
	t.Helper()
	for _, r := range s {
		var cmd tea.Cmd
		m, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		_ = cmd
	}
	return m
}

// advancePastUsername submits the current (register wizard) username
// field and simulates a successful live availability check coming back --
// field 0 is checked against the server asynchronously (see
// checkHandleResultMsg), so a plain Enter alone no longer advances past it
// synchronously the way every other field still does.
func advancePastUsername(t *testing.T, m Model) Model {
	t.Helper()
	handle := m.input.Value()
	m, _ = m.Update(key("enter"))
	if !m.checkingHandle {
		t.Fatalf("expected checkingHandle=true right after submitting the username field")
	}
	m, _ = m.Update(checkHandleResultMsg{handle: handle, available: true})
	return m
}

// chooseSecurityQuestion selects choice i (an index into
// securityQuestionChoices) on the security-question field and submits it.
func chooseSecurityQuestion(m Model, i int) Model {
	m.choiceCursor = i
	m, _ = m.Update(key("enter"))
	return m
}

func TestDialFailureShowsError(t *testing.T) {
	m := New("fake:1234", "@me", "")
	m.dial = func(addr string) (*relay.Client, error) { return nil, os.ErrNotExist }
	msg := dialCmd(m.dial, m.serverAddr)()
	updated, _ := m.Update(msg)
	if updated.step != stepError {
		t.Fatalf("step = %v, want stepError", updated.step)
	}
	if updated.err == "" {
		t.Error("expected a non-empty error message")
	}
}

func TestWelcomeCursorMovement(t *testing.T) {
	m := newTestModel(t)
	if m.welcomeCursor != 0 {
		t.Fatalf("initial welcomeCursor = %d, want 0", m.welcomeCursor)
	}
	m, _ = m.Update(key("down"))
	if m.welcomeCursor != 1 {
		t.Errorf("after down: welcomeCursor = %d, want 1", m.welcomeCursor)
	}
	m, _ = m.Update(key("down"))
	if m.welcomeCursor != 2 {
		t.Errorf("after second down: welcomeCursor = %d, want 2", m.welcomeCursor)
	}
	// Shouldn't go past the last option.
	m, _ = m.Update(key("down"))
	if m.welcomeCursor != 2 {
		t.Errorf("after third down: welcomeCursor = %d, want to stay at 2", m.welcomeCursor)
	}
	m, _ = m.Update(key("up"))
	if m.welcomeCursor != 1 {
		t.Errorf("after up: welcomeCursor = %d, want 1", m.welcomeCursor)
	}
}

func TestSelectingLoginEntersWizard(t *testing.T) {
	m := newTestModel(t)
	m, _ = m.Update(key("enter")) // welcomeCursor 0 == "Log in"
	if m.step != stepWizard || m.flow != flowLogin {
		t.Fatalf("step=%v flow=%v, want stepWizard/flowLogin", m.step, m.flow)
	}
	if len(m.fields) != len(loginFields) {
		t.Errorf("fields = %d, want %d", len(m.fields), len(loginFields))
	}
	// The handle field should be pre-filled with the run's own handle.
	if m.input.Value() != "@me" {
		t.Errorf("prefilled handle input = %q, want %q", m.input.Value(), "@me")
	}
}

func TestEscFromWizardReturnsToWelcome(t *testing.T) {
	m := newTestModel(t)
	m, _ = m.Update(key("enter"))
	if m.step != stepWizard {
		t.Fatalf("expected stepWizard")
	}
	m, _ = m.Update(key("esc"))
	if m.step != stepWelcome {
		t.Errorf("step = %v, want stepWelcome after esc", m.step)
	}
}

func TestRegisterRequiresMatchingPasswords(t *testing.T) {
	m := newTestModel(t)
	m.welcomeCursor = 1 // Register
	m, _ = m.Update(key("enter"))

	// Field 0: username (pre-filled, just accept it).
	m = advancePastUsername(t, m)
	// Field 1: password.
	m = typeString(t, m, "correcthorse")
	m, _ = m.Update(key("enter"))
	// Field 2: confirm password -- deliberately different.
	m = typeString(t, m, "different")
	m, _ = m.Update(key("enter"))

	if m.fieldIdx != 2 {
		t.Errorf("fieldIdx = %d, want to stay at 2 after a password mismatch", m.fieldIdx)
	}
	if m.err == "" {
		t.Error("expected a non-empty error for mismatched passwords")
	}
}

func TestRegisterRejectsUnreadableAvatarPath(t *testing.T) {
	m := newTestModel(t)
	m.welcomeCursor = 1
	m, _ = m.Update(key("enter"))

	m = advancePastUsername(t, m)        // username
	m = typeString(t, m, "correcthorse") // password
	m, _ = m.Update(key("enter"))
	m = typeString(t, m, "correcthorse") // confirm (matches)
	m, _ = m.Update(key("enter"))
	m = typeString(t, m, "/no/such/path.png") // avatar path
	m, _ = m.Update(key("enter"))

	if m.fieldIdx != 3 {
		t.Errorf("fieldIdx = %d, want to stay at 3 for an unreadable avatar path", m.fieldIdx)
	}
	if m.err == "" {
		t.Error("expected a non-empty error for an unreadable avatar path")
	}
}

func TestRegisterAcceptsValidAvatarPathThenSkipsSecurityQuestion(t *testing.T) {
	dir := t.TempDir()
	imgPath := filepath.Join(dir, "me.png")
	writeTestPNG(t, imgPath)

	m := newTestModel(t)
	m.welcomeCursor = 1
	m, _ = m.Update(key("enter"))

	m = advancePastUsername(t, m)        // username
	m = typeString(t, m, "correcthorse") // password
	m, _ = m.Update(key("enter"))
	m = typeString(t, m, "correcthorse") // confirm
	m, _ = m.Update(key("enter"))
	m = typeString(t, m, imgPath) // avatar path
	m, _ = m.Update(key("enter"))
	if m.fieldIdx != 4 {
		t.Fatalf("fieldIdx = %d, want 4 after a valid avatar path, err=%q", m.fieldIdx, m.err)
	}
	if len(m.fields[4].choices) == 0 {
		t.Fatalf("expected the security-question field to be a choice list")
	}

	// Choosing "Skip" should store empty question+answer and jump
	// straight past the (now-irrelevant) answer field to submission.
	skipIdx := len(securityQuestionChoices) - 1
	if securityQuestionChoices[skipIdx] != skipSecurityQuestion {
		t.Fatalf("test assumption broken: last choice isn't the skip sentinel")
	}
	m = chooseSecurityQuestion(m, skipIdx)
	if m.fieldIdx != len(m.fields) {
		t.Fatalf("fieldIdx = %d, want %d (submission) right after skipping", m.fieldIdx, len(m.fields))
	}
	if m.answers[4] != "" || m.answers[5] != "" {
		t.Errorf("answers[4:6] = %q, %q, want empty question and answer after skip", m.answers[4], m.answers[5])
	}

	// Regression: View() and a further keypress must not panic on an
	// out-of-bounds fieldIdx while the submit Cmd is still in flight --
	// Bubble Tea renders View() again with this exact Model immediately
	// after Update returns, before any async result arrives.
	_ = m.View()
	m, _ = m.Update(key("enter"))
	_ = m.View()
}

func TestRegisterWithRealSecurityQuestionAndAnswer(t *testing.T) {
	m := newTestModel(t)
	m.welcomeCursor = 1
	m, _ = m.Update(key("enter"))

	m = advancePastUsername(t, m)
	m = typeString(t, m, "correcthorse")
	m, _ = m.Update(key("enter"))
	m = typeString(t, m, "correcthorse")
	m, _ = m.Update(key("enter"))
	m, _ = m.Update(key("enter")) // avatar path: blank, optional

	m = chooseSecurityQuestion(m, 0) // the first real question, not skip
	if m.fieldIdx != 5 {
		t.Fatalf("fieldIdx = %d, want 5 after picking a real question", m.fieldIdx)
	}
	if m.answers[4] != securityQuestions[0] {
		t.Errorf("answers[4] = %q, want %q", m.answers[4], securityQuestions[0])
	}

	// The answer field is required now that a real question was chosen.
	m, _ = m.Update(key("enter"))
	if m.err == "" {
		t.Error("expected an error for a blank security answer")
	}
	m = typeString(t, m, "blue")
	m, cmd := m.Update(key("enter"))
	if cmd == nil {
		t.Error("expected submitWizard's Cmd to fire")
	}
	if m.answers[5] != "blue" {
		t.Errorf("answers[5] = %q, want %q", m.answers[5], "blue")
	}
}

// TestRegisterAcceptsShellEscapedAvatarPath is a regression test for a real
// bug reported live: dragging a file with a space in its name into a
// terminal that backslash-escapes special characters (e.g. Ghostty, rather
// than quoting the whole path) produced a literal "\ " in the typed input,
// which didn't match the real file on disk and was rejected as "no such
// file or directory" even though the file existed.
func TestRegisterAcceptsShellEscapedAvatarPath(t *testing.T) {
	dir := t.TempDir()
	name := "photo_2026-07-24 16.11.01.jpeg"
	realPath := filepath.Join(dir, name)
	writeTestPNG(t, realPath)
	escapedPath := filepath.Join(dir, `photo_2026-07-24\ 16.11.01.jpeg`)

	m := newTestModel(t)
	m.welcomeCursor = 1
	m, _ = m.Update(key("enter"))

	m = advancePastUsername(t, m)        // username
	m = typeString(t, m, "correcthorse") // password
	m, _ = m.Update(key("enter"))
	m = typeString(t, m, "correcthorse") // confirm
	m, _ = m.Update(key("enter"))
	m = typeString(t, m, escapedPath) // avatar path, as a terminal would actually type it
	m, _ = m.Update(key("enter"))

	if m.fieldIdx != 4 {
		t.Fatalf("fieldIdx = %d, want 4 -- the shell-escaped path should have resolved to the real file, err=%q", m.fieldIdx, m.err)
	}
	if m.answers[3] != realPath {
		t.Errorf("stored avatar path = %q, want the unescaped %q", m.answers[3], realPath)
	}
}

// TestSubmittingLastFieldNeverPanicsOnView drives every wizard flow to its
// final field and asserts View() survives the transient
// fieldIdx==len(fields) window right after submission -- this is a
// regression test for a real crash caught via live/manual testing, not
// just Update()'s return values (which alone don't exercise the render
// path where the panic actually happened).
func TestSubmittingLastFieldNeverPanicsOnView(t *testing.T) {
	tests := []struct {
		name    string
		cursor  int
		answers []string
	}{
		{"login", 0, []string{"@x", "password123"}},
		{"forgot-password-handle", 2, []string{"@x"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newTestModel(t)
			m.welcomeCursor = tt.cursor
			m, _ = m.Update(key("enter"))
			for _, ans := range tt.answers {
				m = typeString(t, m, ans)
				m, _ = m.Update(key("enter"))
			}
			assertSubmittingNeverPanics(t, m)
		})
	}

	t.Run("register", func(t *testing.T) {
		m := newTestModel(t)
		m.welcomeCursor = 1
		m, _ = m.Update(key("enter"))
		m = advancePastUsername(t, m)
		m = typeString(t, m, "password123")
		m, _ = m.Update(key("enter"))
		m = typeString(t, m, "password123")
		m, _ = m.Update(key("enter"))
		m, _ = m.Update(key("enter"))                                 // avatar path: blank, optional
		m = chooseSecurityQuestion(m, len(securityQuestionChoices)-1) // skip -- jumps straight to submission
		assertSubmittingNeverPanics(t, m)
	})
}

// assertSubmittingNeverPanics checks that m (expected to already be at the
// transient fieldIdx==len(fields) submission boundary) survives a View()
// call and a further keypress without panicking -- see the doc comment on
// TestSubmittingLastFieldNeverPanicsOnView.
func assertSubmittingNeverPanics(t *testing.T, m Model) {
	t.Helper()
	if m.fieldIdx != len(m.fields) {
		t.Fatalf("setup: fieldIdx = %d, want %d (the submission boundary), err=%q", m.fieldIdx, len(m.fields), m.err)
	}
	view := func() (s string, panicked bool) {
		defer func() {
			if recover() != nil {
				panicked = true
			}
		}()
		return m.View(), false
	}
	if s, panicked := view(); panicked {
		t.Fatal("View() panicked right after submitting the final field")
	} else if s == "" {
		t.Error("expected a non-empty View() while submitting")
	}
	// A further keypress arriving before the async result comes back must
	// not panic either.
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("a keypress during in-flight submission panicked: %v", r)
			}
		}()
		m, _ = m.Update(key("enter"))
	}()
}

// TestFailedLoginReturnsToWelcomeNotStuck is a regression test for a real
// dead-end caught via live testing: a failed login/register left fieldIdx
// stuck at len(fields) forever, showing a bare "submitting..." screen
// with no way to retry (input in that state is deliberately ignored, see
// the fieldIdx>=len(fields) guard in handleWizardKey).
func TestFailedLoginReturnsToWelcomeNotStuck(t *testing.T) {
	m := newTestModel(t)
	m, _ = m.Update(key("enter")) // enter the Login wizard
	if m.step != stepWizard {
		t.Fatalf("expected stepWizard")
	}
	// Simulate the async loginCmd coming back with an error, without
	// actually invoking it (that would dial a real *relay.Client, which
	// is nil in this test).
	m, _ = m.Update(authResultMsg{err: errors.New("relay: invalid handle or password")})

	if m.step != stepWelcome {
		t.Fatalf("step = %v, want stepWelcome after a failed login", m.step)
	}
	if m.err == "" {
		t.Error("expected the error to be shown on the Welcome screen")
	}
	if view := m.View(); view == "" {
		t.Error("expected a non-empty View()")
	}
	// The user must be able to try again immediately.
	m, _ = m.Update(key("enter"))
	if m.step != stepWizard {
		t.Errorf("step = %v, want stepWizard -- should be able to retry immediately", m.step)
	}
}

func TestRegisterRejectsShortPassword(t *testing.T) {
	m := newTestModel(t)
	m.welcomeCursor = 1
	m, _ = m.Update(key("enter"))
	m = advancePastUsername(t, m)

	m = typeString(t, m, "short")
	m, _ = m.Update(key("enter"))
	if m.fieldIdx != 1 {
		t.Errorf("fieldIdx = %d, want to stay at 1 for a too-short password", m.fieldIdx)
	}
	if m.err == "" {
		t.Error("expected a non-empty error for a too-short password")
	}
	if !strings.Contains(m.fields[1].hint, "6") {
		t.Errorf("password field hint = %q, want it to mention the minimum (6)", m.fields[1].hint)
	}

	// The input still holds "short" (unchanged on error) -- lengthen it
	// past the minimum and it should proceed normally.
	m = typeString(t, m, "er123")
	m, _ = m.Update(key("enter"))
	if m.fieldIdx != 2 {
		t.Errorf("fieldIdx = %d, want 2 after a valid-length password, err=%q", m.fieldIdx, m.err)
	}
}

func TestRegisterRejectsHandleAlreadyTaken(t *testing.T) {
	m := newTestModel(t)
	m.welcomeCursor = 1
	m, _ = m.Update(key("enter"))

	handle := m.input.Value()
	m, cmd := m.Update(key("enter"))
	if cmd == nil {
		t.Fatal("expected checkHandleCmd to be returned")
	}
	if !m.checkingHandle {
		t.Fatal("expected checkingHandle=true")
	}
	m, _ = m.Update(checkHandleResultMsg{handle: handle, available: false})
	if m.checkingHandle {
		t.Error("expected checkingHandle=false once the result arrives")
	}
	if m.fieldIdx != 0 {
		t.Errorf("fieldIdx = %d, want to stay at 0 for an already-taken handle", m.fieldIdx)
	}
	if m.err == "" {
		t.Error("expected a non-empty error for an already-taken handle")
	}
}

func TestBackNavigationRestoresPreviousAnswerForEditing(t *testing.T) {
	m := newTestModel(t)
	m.welcomeCursor = 1
	m, _ = m.Update(key("enter"))
	m = advancePastUsername(t, m)
	m = typeString(t, m, "correcthorse")
	m, _ = m.Update(key("enter")) // now on field 2 (confirm)

	m, _ = m.Update(key("esc"))
	if m.fieldIdx != 1 {
		t.Fatalf("fieldIdx = %d, want 1 after esc from field 2", m.fieldIdx)
	}
	if m.input.Value() != "correcthorse" {
		t.Errorf("input = %q, want the previously-entered password restored for editing", m.input.Value())
	}

	// Editing and re-submitting should work normally.
	m, _ = m.Update(key("enter"))
	if m.fieldIdx != 2 {
		t.Errorf("fieldIdx = %d, want 2 after re-submitting the restored password", m.fieldIdx)
	}
}

func TestBackNavigationFromFirstFieldReturnsToWelcome(t *testing.T) {
	m := newTestModel(t)
	m, _ = m.Update(key("enter")) // Log in wizard, field 0
	if m.step != stepWizard || m.fieldIdx != 0 {
		t.Fatalf("setup: step=%v fieldIdx=%d", m.step, m.fieldIdx)
	}
	m, _ = m.Update(key("esc"))
	if m.step != stepWelcome {
		t.Errorf("step = %v, want stepWelcome after esc from the first field", m.step)
	}
}

func TestBackNavigationThroughSecurityQuestionChoiceRestoresSelection(t *testing.T) {
	m := newTestModel(t)
	m.welcomeCursor = 1
	m, _ = m.Update(key("enter"))
	m = advancePastUsername(t, m)
	m = typeString(t, m, "correcthorse")
	m, _ = m.Update(key("enter"))
	m = typeString(t, m, "correcthorse")
	m, _ = m.Update(key("enter"))
	m, _ = m.Update(key("enter")) // avatar path blank

	m = chooseSecurityQuestion(m, 2) // pick the third question
	if m.fieldIdx != 5 {
		t.Fatalf("fieldIdx = %d, want 5", m.fieldIdx)
	}
	m = typeString(t, m, "some answer")

	// Back up into the question field -- the choice cursor should reflect
	// what was previously picked, not reset to the first item.
	m, _ = m.Update(key("esc"))
	if m.fieldIdx != 4 {
		t.Fatalf("fieldIdx = %d, want 4 after esc", m.fieldIdx)
	}
	if m.choiceCursor != 2 {
		t.Errorf("choiceCursor = %d, want 2 (the previously-selected question)", m.choiceCursor)
	}
}

func writeTestPNG(t *testing.T, path string) {
	t.Helper()
	img := image.NewGray(image.Rect(0, 0, 4, 4))
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			img.SetGray(x, y, color.Gray{Y: uint8(x * 60)})
		}
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create test image: %v", err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatalf("encode test image: %v", err)
	}
}
