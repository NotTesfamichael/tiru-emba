package ui

import (
	"hash/fnv"
	"strings"
)

// avatars is a small set of fun, compact ASCII faces. Kept short (well
// under the sidebar's usable width) so one always fits alongside a handle
// on a single line.
var avatars = []string{
	"(•‿•)",
	"(¬‿¬)",
	"(◕‿◕)",
	"ʕ•ᴥ•ʔ",
	"(⌐■_■)",
	"(≧◡≦)",
	"ಠ_ಠ",
	"(-_-)",
	"(^_^)",
	"(o_O)",
	"(>_<)",
	"(✿◠‿◠)",
}

// avatarForHandle deterministically maps a handle to one of avatars, the
// same way colorForHandle maps it to a color: stable across restarts, and
// consistent for everyone looking at the same handle. Salted differently
// from colorForHandle so a face and a color don't always land on the same
// index together for every handle.
func avatarForHandle(handle string) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(strings.ToLower(handle) + "|avatar"))
	return avatars[h.Sum32()%uint32(len(avatars))]
}
