// Package notify fires an OS desktop notification (with sound, where the
// platform supports it) for incoming messages. Best-effort only: a machine
// with no notification daemon (e.g. a headless SSH session) shouldn't crash
// the chat over it, so failures are silently ignored.
package notify

import "github.com/gen2brain/beeep"

// Alert shows a desktop notification with the default system alert sound.
func Alert(title, body string) {
	_ = beeep.Alert(title, body, "")
}
