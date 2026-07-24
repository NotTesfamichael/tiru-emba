package relay

import "time"

// MsgType identifies what an Envelope carries. This protocol starts small
// (just auth, for Phase 1) and grows in later phases to carry presence and
// relayed chat/game envelopes, the same way internal/network's envelope
// grew from just text messages to files and games.
type MsgType string

const (
	// MsgAuthRegister creates a new account: Handle + Password set,
	// answered with MsgAuthToken (auto-logged-in on success) or
	// MsgAuthError.
	MsgAuthRegister MsgType = "auth_register"

	// MsgAuthLogin authenticates an existing account: Handle + Password
	// set, answered with MsgAuthToken or MsgAuthError.
	MsgAuthLogin MsgType = "auth_login"

	// MsgAuthToken is the success response to MsgAuthRegister/MsgAuthLogin:
	// Token + ExpiresAt set.
	MsgAuthToken MsgType = "auth_token"

	// MsgAuthError is the failure response to MsgAuthRegister/MsgAuthLogin
	// specifically: Reason set. MsgError (below) covers every other kind
	// of failure once a connection is past that point.
	MsgAuthError MsgType = "auth_error"

	// MsgError is a generic failure response for anything past the auth
	// step: not authenticated yet, an unknown message type, a relay target
	// that isn't online. Reason set.
	MsgError MsgType = "error"

	// MsgRelay carries a message between two authenticated connections.
	// Client -> server: To + Body set (deliver Body to the user at To).
	// Server -> the recipient: Handle + Body set (Handle is who it's from
	// -- the server sets this itself from the sender's authenticated
	// identity; a client can't spoof who a relayed message is from).
	MsgRelay MsgType = "relay"

	// MsgPresenceJoined is pushed to every other online client when Handle
	// comes online, and once per already-online user to a client right
	// after it authenticates (so it learns the current roster without a
	// separate "full list" message type).
	MsgPresenceJoined MsgType = "presence_joined"

	// MsgPresenceLeft is pushed to every other online client when Handle
	// disconnects.
	MsgPresenceLeft MsgType = "presence_left"

	// MsgOrgCreate creates a new org: OrgName set, caller becomes its
	// first member (as admin). Answered with MsgOrgCreated or MsgError.
	MsgOrgCreate MsgType = "org_create"

	// MsgOrgCreated is the success response to MsgOrgCreate: OrgID +
	// OrgName set.
	MsgOrgCreated MsgType = "org_created"

	// MsgOrgList requests every org the caller belongs to (no fields).
	// Answered with MsgOrgListResult.
	MsgOrgList MsgType = "org_list"

	// MsgOrgListResult is the response to MsgOrgList: Orgs set.
	MsgOrgListResult MsgType = "org_list_result"

	// MsgOrgInvite generates a redeemable invite code for an org: OrgID
	// set. Only an admin of that org may do this. Answered with
	// MsgOrgInviteCode or MsgError.
	MsgOrgInvite MsgType = "org_invite"

	// MsgOrgInviteCode is the success response to MsgOrgInvite: Code +
	// ExpiresAt set.
	MsgOrgInviteCode MsgType = "org_invite_code"

	// MsgOrgJoin redeems an invite code: Code set. Answered with
	// MsgOrgJoined or MsgError.
	MsgOrgJoin MsgType = "org_join"

	// MsgOrgJoined is the success response to MsgOrgJoin: OrgID + OrgName
	// set.
	MsgOrgJoined MsgType = "org_joined"

	// MsgAuthResume resumes a previously-issued session token instead of
	// logging in with a handle/password again -- Token set. Answered with
	// MsgAuthToken (a freshly-rotated token) or MsgAuthError.
	MsgAuthResume MsgType = "auth_resume"

	// MsgAuthRecoverStart begins the "forgot password" flow: Handle set.
	// Answered with MsgAuthRecoverQuestion or MsgAuthError (e.g. no
	// security question was ever set for that account).
	MsgAuthRecoverStart MsgType = "auth_recover_start"

	// MsgAuthRecoverQuestion is the response to MsgAuthRecoverStart:
	// SecurityQuestion set.
	MsgAuthRecoverQuestion MsgType = "auth_recover_question"

	// MsgAuthRecoverFinish completes password recovery: Handle +
	// SecurityAnswer + Password (the new password) set. Answered with
	// MsgAuthToken (auto-logged-in on success, same as MsgAuthRegister) or
	// MsgAuthError.
	MsgAuthRecoverFinish MsgType = "auth_recover_finish"

	// MsgCheckHandle asks whether a handle is already registered, before
	// the rest of the registration form is even filled in: Handle set.
	// Answered with MsgHandleCheckResult. Pre-auth, like MsgAuthRegister --
	// not a new information leak beyond what MsgAuthRegister already
	// reveals at the end of the form (ErrHandleTaken), just surfaced
	// earlier in the flow.
	MsgCheckHandle MsgType = "check_handle"

	// MsgHandleCheckResult is the response to MsgCheckHandle: Available
	// set.
	MsgHandleCheckResult MsgType = "handle_check_result"

	// MsgAccountBio requests the caller's own account stats (no fields).
	// Answered with MsgAccountBioResult.
	MsgAccountBio MsgType = "account_bio"

	// MsgAccountBioResult is the response to MsgAccountBio: Handle,
	// Points, AvatarASCII, OrgNames, and CreatedAt set.
	MsgAccountBioResult MsgType = "account_bio_result"

	// MsgUnlockablesList requests the full unlockables catalog plus which
	// entries the caller has redeemed/equipped (no fields). Answered with
	// MsgUnlockablesListResult.
	MsgUnlockablesList MsgType = "unlockables_list"

	// MsgUnlockablesListResult is the response to MsgUnlockablesList:
	// Unlockables set.
	MsgUnlockablesListResult MsgType = "unlockables_list_result"

	// MsgUnlockableRedeem spends points to unlock a catalog entry:
	// UnlockableID set. Answered with MsgUnlockableRedeemed or MsgError
	// (insufficient points, already unlocked, or no such entry).
	MsgUnlockableRedeem MsgType = "unlockable_redeem"

	// MsgUnlockableRedeemed is the success response to
	// MsgUnlockableRedeem: UnlockableID set.
	MsgUnlockableRedeemed MsgType = "unlockable_redeemed"

	// MsgAvatarSet equips an already-redeemed unlockable as the caller's
	// active avatar/border: UnlockableID set. Answered with
	// MsgAvatarSetOK or MsgError (e.g. not yet unlocked).
	MsgAvatarSet MsgType = "avatar_set"

	// MsgAvatarSetOK is the success response to MsgAvatarSet (no fields).
	MsgAvatarSetOK MsgType = "avatar_set_ok"

	// MsgTodoList requests every shared todo for an org: OrgID set.
	// Answered with MsgTodoListResult.
	MsgTodoList MsgType = "todo_list"

	// MsgTodoListResult is the response to MsgTodoList: Todos set.
	MsgTodoListResult MsgType = "todo_list_result"

	// MsgTodoAdd creates a new shared todo: OrgID + Text set. Answered
	// with MsgTodoAdded or MsgError.
	MsgTodoAdd MsgType = "todo_add"

	// MsgTodoAdded is the success response to MsgTodoAdd: Todo set.
	MsgTodoAdded MsgType = "todo_added"

	// MsgTodoComplete marks a shared todo done, awarding the completer
	// points: OrgID + TodoID set. Answered with MsgTodoCompleted or
	// MsgError.
	MsgTodoComplete MsgType = "todo_complete"

	// MsgTodoCompleted is the success response to MsgTodoComplete: TodoID
	// set.
	MsgTodoCompleted MsgType = "todo_completed"
)

// UnlockableInfo is one entry in a MsgUnlockablesListResult response.
type UnlockableInfo struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	AsciiArt string `json:"ascii_art"`
	Cost     int    `json:"cost"`
	Owned    bool   `json:"owned"`
	Active   bool   `json:"active"`
}

// TodoInfo is one entry in a MsgTodoListResult response, or the payload
// of MsgTodoAdded.
type TodoInfo struct {
	ID        int64     `json:"id"`
	Text      string    `json:"text"`
	Done      bool      `json:"done"`
	CreatedBy string    `json:"created_by"`
	CreatedAt time.Time `json:"created_at"`
}

// OrgSummary is one entry in a MsgOrgListResult response.
type OrgSummary struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// Envelope is the one-line JSON message exchanged on a relay connection,
// mirroring internal/network's envelope in shape (one flat struct, fields
// populated per Type) but as its own type: the relay protocol authenticates
// and (later) scopes to organizations, which the LAN protocol has no
// concept of, so keeping them separate types avoids a single struct
// straining to serve both.
type Envelope struct {
	Type      MsgType   `json:"type"`
	Handle    string    `json:"handle,omitempty"` // auth subject; sender's handle on a delivered MsgRelay; subject of a presence event
	Password  string    `json:"password,omitempty"`
	Token     string    `json:"token,omitempty"`
	ExpiresAt time.Time `json:"expires_at,omitempty"`
	Reason    string    `json:"reason,omitempty"`
	To        string    `json:"to,omitempty"`   // MsgRelay, client -> server only: who to deliver Body to
	Body      string    `json:"body,omitempty"` // MsgRelay payload, opaque to the server

	// IsAdmin is set on every MsgAuthToken response (fresh login/register,
	// a resumed session, or completed recovery) so the client knows
	// whether to offer creating a new org -- only a system admin may
	// (see MsgOrgCreate).
	IsAdmin bool `json:"is_admin,omitempty"`

	// Available is MsgHandleCheckResult's field.
	Available bool `json:"available,omitempty"`

	OrgID   int64        `json:"org_id,omitempty"`   // MsgOrgCreated/MsgOrgInvite/MsgOrgJoined; also MsgTodo*'s org scope
	OrgName string       `json:"org_name,omitempty"` // MsgOrgCreate (request) / MsgOrgCreated/MsgOrgJoined (response)
	Code    string       `json:"code,omitempty"`     // MsgOrgInviteCode (response) / MsgOrgJoin (request)
	Orgs    []OrgSummary `json:"orgs,omitempty"`     // MsgOrgListResult

	// AvatarASCII, SecurityQuestion, SecurityAnswer are all MsgAuthRegister
	// (request) fields; SecurityQuestion doubles as MsgAuthRecoverQuestion's
	// (response) field, and Handle+SecurityAnswer+Password are
	// MsgAuthRecoverFinish's (request) fields.
	AvatarASCII      string `json:"avatar_ascii,omitempty"`
	SecurityQuestion string `json:"security_question,omitempty"`
	SecurityAnswer   string `json:"security_answer,omitempty"`

	// Points, OrgNames, CreatedAt are MsgAccountBioResult fields (Handle
	// and AvatarASCII, above, double as this response's handle/avatar).
	Points    int       `json:"points,omitempty"`
	OrgNames  []string  `json:"org_names,omitempty"`
	CreatedAt time.Time `json:"created_at,omitempty"`

	// Unlockables is MsgUnlockablesListResult's field. UnlockableID is
	// MsgUnlockableRedeem/MsgUnlockableRedeemed/MsgAvatarSet's field (0
	// for MsgAvatarSet means "clear back to the default avatar").
	Unlockables  []UnlockableInfo `json:"unlockables,omitempty"`
	UnlockableID int64            `json:"unlockable_id,omitempty"`

	// Text is MsgTodoAdd's field. TodoID is MsgTodoComplete/MsgTodoCompleted's
	// field. Todo/Todos are MsgTodoAdded/MsgTodoListResult's fields.
	Text   string     `json:"text,omitempty"`
	TodoID int64      `json:"todo_id,omitempty"`
	Todo   *TodoInfo  `json:"todo,omitempty"`
	Todos  []TodoInfo `json:"todos,omitempty"`
}
