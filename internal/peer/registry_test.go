package peer

import "testing"

func TestLookupCaseInsensitive(t *testing.T) {
	r := NewRegistry()
	r.Upsert(Info{ID: "1", Handle: "@kal", Addr: "192.168.0.5", TCPPort: 7777})

	cases := []string{"@kal", "@Kal", "@KAL", "@kAl"}
	for _, h := range cases {
		if _, ok := r.Lookup(h); !ok {
			t.Errorf("Lookup(%q) = not found, want found", h)
		}
	}

	if _, ok := r.Lookup("@notreal"); ok {
		t.Error("Lookup(@notreal) = found, want not found")
	}
}
