package main

import "testing"

func TestMatchTrack(t *testing.T) {
	const id = "4cOdK2wGLETKBW3PvgPWqT"
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"bare id", id, id},
		{"uri", "spotify:track:" + id, id},
		{"web url", "https://open.spotify.com/track/" + id, id},
		{"web url with query", "https://open.spotify.com/track/" + id + "?si=abc123", id},
		{"intl locale segment", "https://open.spotify.com/intl-de/track/" + id, id},
		{"play host", "play.spotify.com/track/" + id, id},
		{"trailing space", "  " + id + "  ", id},
		{"album uri rejected", "spotify:album:" + id, ""},
		{"album url rejected", "https://open.spotify.com/album/" + id, ""},
		{"playlist rejected", "https://open.spotify.com/playlist/" + id, ""},
		{"wrong length", "4cOdK2wGLETKBW3PvgPWq", ""},
		{"non-spotify host", "https://example.com/track/" + id, ""},
		{"plain text", "hello world", ""},
		{"empty", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := matchTrack(c.in); got != c.want {
				t.Fatalf("matchTrack(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestLRCTimestamp(t *testing.T) {
	cases := []struct {
		ms   int64
		want string
	}{
		{0, "[00:00.00]"},
		{12340, "[00:12.34]"},
		{65000, "[01:05.00]"},
		{3661230, "[61:01.23]"},
	}
	for _, c := range cases {
		if got := lrcTimestamp(c.ms); got != c.want {
			t.Fatalf("lrcTimestamp(%d) = %q, want %q", c.ms, got, c.want)
		}
	}
}

func TestDeriveSecret(t *testing.T) {
	// index i XORs with (i%33)+9: 44^9=37, 55^10=61 -> decimal-concat "3761".
	if got := string(deriveSecret([]int{44, 55})); got != "3761" {
		t.Fatalf("deriveSecret = %q, want %q", got, "3761")
	}
}
