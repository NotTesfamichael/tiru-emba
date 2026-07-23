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
)

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

	OrgID   int64        `json:"org_id,omitempty"`   // MsgOrgCreated/MsgOrgInvite/MsgOrgJoined
	OrgName string       `json:"org_name,omitempty"` // MsgOrgCreate (request) / MsgOrgCreated/MsgOrgJoined (response)
	Code    string       `json:"code,omitempty"`     // MsgOrgInviteCode (response) / MsgOrgJoin (request)
	Orgs    []OrgSummary `json:"orgs,omitempty"`     // MsgOrgListResult
}
