// Package onboarding implements tiru-emba's relay-mode sign-in flow: a
// Welcome screen choosing Log in / Register / Forgot password, a
// single-field-at-a-time wizard for whichever is chosen, and (on success)
// handing an authenticated *relay.Client back to the hosting router
// (internal/ui.App) via AuthenticatedMsg. LAN-only mode (no --server)
// never enters this screen at all.
package onboarding

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/NotTesfamichael/tiru-emba/internal/asciiart"
	"github.com/NotTesfamichael/tiru-emba/internal/relay"
	"github.com/NotTesfamichael/tiru-emba/internal/shellpath"
)

// avatarCols is how wide (in terminal columns) a registration profile
// picture is rendered as ASCII art. Deliberately small -- this is a
// persistent little avatar shown inline in bios/sidebars/unlockables
// lists, not a full photo preview (that's internal/ui's much larger
// imagePreviewCols, for photos sent in chat) -- a wide render here badly
// mangled the /account bio layout when tested live.
const avatarCols = 16

// minPasswordLength mirrors internal/relay/auth.go's own minimum -- kept
// as a separate constant (rather than importing it) since it's unexported
// there; checked here too so a too-short password is caught immediately
// instead of only after a round trip to the server.
const minPasswordLength = 6

type step int

const (
	stepConnecting step = iota
	stepResuming
	stepWelcome
	stepWizard
	stepError
)

type flow int

const (
	flowNone flow = iota
	flowLogin
	flowRegister
	flowRecoverHandle
	flowRecoverAnswer
)

// AuthenticatedMsg is emitted once an authenticated *relay.Client is ready
// -- via fresh login, registration, a resumed session, or a completed
// password recovery. The hosting router is expected to move on to a
// mandatory org-select screen next, never straight to chat, even when
// Resumed is true.
type AuthenticatedMsg struct {
	Client    *relay.Client
	Handle    string
	Token     string
	ExpiresAt time.Time
	IsAdmin   bool
	Resumed   bool
}

// field is one step of a single-field-at-a-time wizard: simpler to get
// right than a multi-field form with tab-navigation, and a natural fit for
// a narrow terminal. A field with choices is rendered/handled as a
// selectable menu instead of free text -- its "value" is whichever choice
// string is selected.
type field struct {
	prompt    string
	mask      bool
	optional  bool
	hint      string // shown under the prompt, e.g. "minimum 6 characters"
	minLength int    // 0 means no minimum
	choices   []string
}

// skipSecurityQuestion is the sentinel choice meaning "opt out of setting
// one up" -- selecting it stores an empty question/answer (see
// Auth.Register: both may be blank) and skips the answer field entirely,
// rather than asking for an answer to a question that was just skipped.
const skipSecurityQuestion = "Skip (no password recovery)"

// securityQuestions is a fixed catalog every user picks from, rather than
// free-typing their own each time -- capped at 10 real questions (plus the
// skip option) per product decision.
var securityQuestions = []string{
	"What was the name of your first pet?",
	"What is your mother's maiden name?",
	"What was the name of your elementary school?",
	"What city were you born in?",
	"What was your childhood nickname?",
	"What is the name of your best childhood friend?",
	"What was the make of your first car?",
	"What is your favorite book?",
	"What was the name of your first employer?",
	"What street did you grow up on?",
}

var securityQuestionChoices = append(append([]string{}, securityQuestions...), skipSecurityQuestion)

var loginFields = []field{
	{prompt: "Handle"},
	{prompt: "Password", mask: true},
}

var registerFields = []field{
	{prompt: "Choose a username"},
	{prompt: "Choose a password", mask: true, minLength: minPasswordLength, hint: fmt.Sprintf("minimum %d characters", minPasswordLength)},
	{prompt: "Confirm password", mask: true},
	{prompt: "Profile picture file path (optional, blank to skip)", optional: true},
	{prompt: "Security question, for password recovery", choices: securityQuestionChoices},
	{prompt: "Security answer"},
}

var recoverHandleFields = []field{
	{prompt: "Handle to recover"},
}

// Model is the onboarding screen. Value-receiver Update/View, following the
// same self-contained-screen shape internal/games/tictactoe uses.
type Model struct {
	serverAddr string
	dial       func(addr string) (*relay.Client, error)
	handle     string // this run's own (LAN) handle -- pre-fills the login/register handle field
	savedToken string // a persisted session token to try resuming, "" for a fresh login

	client *relay.Client

	step step
	flow flow
	err  string

	welcomeCursor int

	fields         []field
	fieldIdx       int
	answers        []string
	input          textinput.Model
	choiceCursor   int  // cursor within fields[fieldIdx].choices, for a menu-type field
	checkingHandle bool // true while an async handle-availability check is in flight

	recoverHandle   string
	recoverQuestion string

	width, height int
}

// New constructs the onboarding screen. handle is this run's own LAN
// handle (already resolved from --handle/config the same way LAN-only mode
// resolves one); savedToken is a persisted session token to try resuming
// automatically, or "" for a fresh login/register.
func New(serverAddr, handle, savedToken string) Model {
	ti := textinput.New()
	ti.CharLimit = 300
	return Model{
		serverAddr: serverAddr,
		dial:       relay.Dial,
		handle:     handle,
		savedToken: savedToken,
		step:       stepConnecting,
		input:      ti,
	}
}

func (m Model) Init() tea.Cmd {
	return dialCmd(m.dial, m.serverAddr)
}

// Close releases the dialed connection, if any -- meant for a hosting
// router to call if the program quits before onboarding ever finishes
// (e.g. ctrl+c at the Welcome screen), so the socket isn't leaked.
func (m Model) Close() {
	if m.client != nil {
		_ = m.client.Close()
	}
}

type dialResultMsg struct {
	client *relay.Client
	err    error
}

func dialCmd(dial func(string) (*relay.Client, error), addr string) tea.Cmd {
	return func() tea.Msg {
		client, err := dial(addr)
		return dialResultMsg{client: client, err: err}
	}
}

type authResultMsg struct {
	handle    string
	token     string
	expiresAt time.Time
	isAdmin   bool
	resumed   bool
	err       error
}

func resumeCmd(client *relay.Client, token, handle string) tea.Cmd {
	return func() tea.Msg {
		newToken, expiresAt, isAdmin, err := client.ResumeSession(token)
		return authResultMsg{handle: handle, token: newToken, expiresAt: expiresAt, isAdmin: isAdmin, resumed: true, err: err}
	}
}

func loginCmd(client *relay.Client, handle, password string) tea.Cmd {
	return func() tea.Msg {
		token, expiresAt, isAdmin, err := client.Login(handle, password)
		return authResultMsg{handle: handle, token: token, expiresAt: expiresAt, isAdmin: isAdmin, err: err}
	}
}

func registerCmd(client *relay.Client, handle, password, avatarASCII, question, answer string) tea.Cmd {
	return func() tea.Msg {
		token, expiresAt, isAdmin, err := client.Register(handle, password, avatarASCII, question, answer)
		return authResultMsg{handle: handle, token: token, expiresAt: expiresAt, isAdmin: isAdmin, err: err}
	}
}

type recoverStartResultMsg struct {
	question string
	err      error
}

func recoverStartCmd(client *relay.Client, handle string) tea.Cmd {
	return func() tea.Msg {
		question, err := client.RecoverStart(handle)
		return recoverStartResultMsg{question: question, err: err}
	}
}

func recoverFinishCmd(client *relay.Client, handle, answer, newPassword string) tea.Cmd {
	return func() tea.Msg {
		token, expiresAt, isAdmin, err := client.RecoverFinish(handle, answer, newPassword)
		return authResultMsg{handle: handle, token: token, expiresAt: expiresAt, isAdmin: isAdmin, err: err}
	}
}

// checkHandleResultMsg carries back not just the availability result but
// the handle it was checked for, so the eventual commitAnswer call uses
// exactly what was validated even though the input itself stays untouched
// (blocked, not cleared) while the check is in flight.
type checkHandleResultMsg struct {
	handle    string
	available bool
	err       error
}

func checkHandleCmd(client *relay.Client, handle string) tea.Cmd {
	return func() tea.Msg {
		available, err := client.CheckHandle(handle)
		return checkHandleResultMsg{handle: handle, available: available, err: err}
	}
}

func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case dialResultMsg:
		if msg.err != nil {
			m.step = stepError
			m.err = fmt.Sprintf("could not connect to %s: %v", m.serverAddr, msg.err)
			return m, nil
		}
		m.client = msg.client
		if m.savedToken != "" {
			m.step = stepResuming
			return m, resumeCmd(m.client, m.savedToken, m.handle)
		}
		m.step = stepWelcome
		return m, nil

	case authResultMsg:
		if msg.err != nil {
			// A failure here always lands back on Welcome with the error
			// shown -- for stepResuming, a stale/expired/invalid saved
			// token; for stepWizard (login/register/recover-finish), the
			// alternative is leaving fieldIdx stuck at len(fields) with
			// nowhere to go (a real dead end caught via live testing:
			// input was already ignored in that state by design, once
			// the crash-guard above was added, so a failed login used to
			// permanently strand the user on a bare "submitting..."
			// screen with no way to retry).
			m.step = stepWelcome
			m.savedToken = ""
			m.err = msg.err.Error()
			return m, nil
		}
		client := m.client
		return m, func() tea.Msg {
			return AuthenticatedMsg{
				Client: client, Handle: msg.handle, Token: msg.token,
				ExpiresAt: msg.expiresAt, IsAdmin: msg.isAdmin, Resumed: msg.resumed,
			}
		}

	case recoverStartResultMsg:
		if msg.err != nil {
			m.err = msg.err.Error()
			m.step = stepWelcome
			return m, nil
		}
		m.recoverQuestion = msg.question
		m.startRecoverAnswer()
		return m, nil

	case checkHandleResultMsg:
		m.checkingHandle = false
		if msg.err != nil {
			m.err = msg.err.Error()
			return m, nil
		}
		if !msg.available {
			m.err = "that handle is already registered"
			return m, nil
		}
		return m.commitAnswer(msg.handle)

	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m Model) handleKey(msg tea.KeyMsg) (Model, tea.Cmd) {
	if msg.String() == "ctrl+c" {
		return m, tea.Quit
	}

	switch m.step {
	case stepWelcome:
		return m.handleWelcomeKey(msg)
	case stepWizard:
		return m.handleWizardKey(msg)
	case stepError:
		if msg.String() == "enter" {
			m.err = ""
			m.step = stepConnecting
			return m, dialCmd(m.dial, m.serverAddr)
		}
	}
	return m, nil
}

// welcomeOptions is the Welcome screen's menu, in display order --
// index into this slice is what m.welcomeCursor tracks.
var welcomeOptions = []string{"Log in", "Register", "Forgot password"}

func (m Model) handleWelcomeKey(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		if m.welcomeCursor > 0 {
			m.welcomeCursor--
		}
	case "down", "j":
		if m.welcomeCursor < len(welcomeOptions)-1 {
			m.welcomeCursor++
		}
	case "enter":
		m.err = ""
		switch m.welcomeCursor {
		case 0:
			m.startWizard(flowLogin, loginFields)
		case 1:
			m.startWizard(flowRegister, registerFields)
		case 2:
			m.startWizard(flowRecoverHandle, recoverHandleFields)
		}
	}
	return m, nil
}

func (m *Model) startWizard(f flow, fields []field) {
	m.flow = f
	m.fields = fields
	m.fieldIdx = 0
	m.answers = m.answers[:0]
	m.step = stepWizard
	m.err = ""
	m.resetInput()
}

// startRecoverAnswer begins the second stage of password recovery, once
// RecoverStart has returned the account's security question.
func (m *Model) startRecoverAnswer() {
	m.flow = flowRecoverAnswer
	m.fields = []field{
		{prompt: fmt.Sprintf("Answer: %s", m.recoverQuestion)},
		{prompt: "New password", mask: true, minLength: minPasswordLength, hint: fmt.Sprintf("minimum %d characters", minPasswordLength)},
	}
	m.fieldIdx = 0
	m.answers = m.answers[:0]
	m.step = stepWizard
	m.resetInput()
}

// resetInput moves to the current field with a blank starting value
// (moving forward) -- see resetInputWithValue for moving backward, where
// the field's previous answer is restored instead.
func (m *Model) resetInput() {
	m.resetInputWithValue("")
}

// resetInputWithValue prepares the current field (m.fields[m.fieldIdx])
// for input, prefilled with prefill -- "" when arriving at a field for the
// first time (moving forward), or the previously-entered value when
// backing up into it (see retreatWizard), so re-editing doesn't mean
// retyping from scratch.
func (m *Model) resetInputWithValue(prefill string) {
	cur := m.fields[m.fieldIdx]
	if len(cur.choices) > 0 {
		m.choiceCursor = indexOf(cur.choices, prefill)
		if m.choiceCursor < 0 {
			m.choiceCursor = 0
		}
		return
	}

	m.input.SetValue(prefill)
	// The handle field is pre-filled with this run's own LAN handle on
	// first arrival (prefill == ""), so a normal run can just press enter
	// -- kept editable for the rarer case of deliberately signing into a
	// different relay account. A restored previous answer (prefill != "")
	// always wins, even if it happens to equal m.handle.
	if prefill == "" && (m.flow == flowLogin || m.flow == flowRegister) && m.fieldIdx == 0 {
		m.input.SetValue(m.handle)
	}
	if cur.mask {
		m.input.EchoMode = textinput.EchoPassword
		m.input.EchoCharacter = '•'
	} else {
		m.input.EchoMode = textinput.EchoNormal
	}
	m.input.Focus()
	m.input.CursorEnd()
}

func indexOf(choices []string, value string) int {
	for i, c := range choices {
		if c == value {
			return i
		}
	}
	return -1
}

func (m Model) handleWizardKey(msg tea.KeyMsg) (Model, tea.Cmd) {
	// Once the last field is submitted, fieldIdx == len(fields) while the
	// async request (login/register/recover) is still in flight -- ignore
	// further input until its result arrives (authResultMsg/
	// recoverStartResultMsg), the same "swallow input mid-request" idea
	// stepConnecting/stepResuming already use, just scoped to this step.
	// checkingHandle is the same idea for the shorter availability check.
	if m.fieldIdx >= len(m.fields) || m.checkingHandle {
		return m, nil
	}

	cur := m.fields[m.fieldIdx]
	if len(cur.choices) > 0 {
		switch msg.String() {
		case "esc":
			return m.retreatWizard()
		case "up", "k":
			if m.choiceCursor > 0 {
				m.choiceCursor--
			}
		case "down", "j":
			if m.choiceCursor < len(cur.choices)-1 {
				m.choiceCursor++
			}
		case "enter":
			return m.advanceWizard()
		}
		return m, nil
	}

	switch msg.String() {
	case "esc":
		return m.retreatWizard()
	case "enter":
		return m.advanceWizard()
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

// retreatWizard goes back one field, restoring its previously-entered
// answer for editing -- or, from the first field, abandons the wizard back
// to Welcome (there's nothing earlier to go back to within it).
func (m Model) retreatWizard() (Model, tea.Cmd) {
	m.err = ""
	if m.fieldIdx == 0 {
		m.step = stepWelcome
		return m, nil
	}
	m.fieldIdx--
	prevAnswer := m.answers[len(m.answers)-1]
	m.answers = m.answers[:len(m.answers)-1]
	m.resetInputWithValue(prevAnswer)
	return m, nil
}

func (m Model) advanceWizard() (Model, tea.Cmd) {
	cur := m.fields[m.fieldIdx]
	if len(cur.choices) > 0 {
		return m.advanceChoiceField()
	}

	value := strings.TrimSpace(m.input.Value())
	if value == "" && !cur.optional {
		m.err = fmt.Sprintf("%s is required", cur.prompt)
		return m, nil
	}
	if value != "" && cur.minLength > 0 && len(value) < cur.minLength {
		m.err = fmt.Sprintf("must be at least %d characters", cur.minLength)
		return m, nil
	}

	// Fail-fast, field-specific validation before moving on -- so a typo
	// in the profile picture path or a password mismatch is caught here,
	// not only after a round trip to the server.
	switch {
	case m.flow == flowRegister && m.fieldIdx == 0:
		// Checked live against the server rather than only at final
		// submission, so a taken handle doesn't mean redoing the whole
		// form -- see checkHandleResultMsg.
		m.err = ""
		m.checkingHandle = true
		return m, checkHandleCmd(m.client, value)

	case m.flow == flowRegister && m.fieldIdx == 2 && value != m.answers[1]:
		m.err = "passwords don't match"
		return m, nil

	case m.flow == flowRegister && m.fieldIdx == 3 && value != "":
		// shellpath.Resolve undoes a dragged-in file's terminal-specific
		// escaping (e.g. Ghostty backslash-escapes spaces instead of
		// quoting the whole path) -- without this, a real file with a
		// space in its name was rejected as "no such file or directory"
		// even though it existed, caught via live testing.
		resolved, ok := shellpath.Resolve(value)
		if !ok {
			m.err = fmt.Sprintf("can't read %s: no such file", resolved)
			return m, nil
		}
		if _, err := asciiart.FromFile(resolved, avatarCols); err != nil {
			m.err = fmt.Sprintf("%s doesn't look like a supported image: %v", resolved, err)
			return m, nil
		}
		value = resolved
	}

	return m.commitAnswer(value)
}

// advanceChoiceField commits whichever choice is currently highlighted.
// Choosing skipSecurityQuestion stores an empty question *and* answer and
// skips the answer field entirely -- there's no point asking for an
// answer to a question that was just declined.
func (m Model) advanceChoiceField() (Model, tea.Cmd) {
	cur := m.fields[m.fieldIdx]
	selected := cur.choices[m.choiceCursor]
	m.err = ""
	if selected == skipSecurityQuestion {
		m.answers = append(m.answers, "", "")
		m.fieldIdx += 2
		if m.fieldIdx >= len(m.fields) {
			return m.submitWizard()
		}
		m.resetInput()
		return m, nil
	}
	return m.commitAnswer(selected)
}

// commitAnswer records value for the current field and moves to the next
// one, or submits the wizard if that was the last field.
func (m Model) commitAnswer(value string) (Model, tea.Cmd) {
	m.err = ""
	m.answers = append(m.answers, value)
	m.fieldIdx++
	if m.fieldIdx < len(m.fields) {
		m.resetInput()
		return m, nil
	}
	return m.submitWizard()
}

func (m Model) submitWizard() (Model, tea.Cmd) {
	switch m.flow {
	case flowLogin:
		return m, loginCmd(m.client, m.answers[0], m.answers[1])

	case flowRegister:
		handle, password := m.answers[0], m.answers[1]
		avatarPath := m.answers[3]
		var avatarASCII string
		if avatarPath != "" {
			// Already validated (path exists, decodes) in advanceWizard.
			avatarASCII, _ = asciiart.FromFile(avatarPath, avatarCols)
		}
		question, answer := m.answers[4], m.answers[5]
		return m, registerCmd(m.client, handle, password, avatarASCII, question, answer)

	case flowRecoverHandle:
		m.recoverHandle = m.answers[0]
		return m, recoverStartCmd(m.client, m.recoverHandle)

	case flowRecoverAnswer:
		return m, recoverFinishCmd(m.client, m.recoverHandle, m.answers[0], m.answers[1])
	}
	return m, nil
}
