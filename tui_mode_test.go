package main

import (
	"os"
	"path/filepath"
	"testing"

	"gonotes/tui"
)

// These tests cover the local-vs-HTTP decision, which is the one place where
// getting an answer wrong points the TUI at somebody else's notes with writes
// enabled. It used to be a single boolean — "did anything answer on 8444?" — and
// that boolean cannot distinguish the server guarding this data directory from
// an unrelated one on the same port, so an explicit -d was silently overridden
// by whatever happened to be running.
//
// decideStore is a pure function of the probe result precisely so this can be
// stated without a server, a terminal, or a data directory.

const (
	mine   = "/Users/me/.gonotes/data"
	theirs = "/tmp/scratch/data"
)

// withServerURL sets or clears GONOTES_URL for one test, since decideStore asks
// tui.ServerURLIsExplicit whether the URL was chosen or defaulted.
func withServerURL(t *testing.T, url string) {
	t.Helper()
	t.Setenv("GONOTES_URL", url)
}

func TestDecideHTTPWithTheDefaultURL(t *testing.T) {
	cases := []struct {
		name        string
		up          bool
		serverDir   string
		wantHTTP    bool
		wantsNotice bool
		wantsBadge  bool
	}{
		{
			name:       "no server: the ordinary local launch, unlabelled",
			up:         false,
			wantHTTP:   false,
			wantsBadge: false,
		},
		{
			name:       "a server holding these very files: defer to it, badged with the server",
			up:         true,
			serverDir:  mine,
			wantHTTP:   true,
			wantsBadge: true,
		},
		{
			name:        "a server holding a different directory: pass it over, and say so",
			up:          true,
			serverDir:   theirs,
			wantHTTP:    false,
			wantsNotice: true,
			wantsBadge:  true,
		},
		{
			name:        "a server too old to identify itself: keep the old behavior, and say why",
			up:          true,
			serverDir:   "",
			wantHTTP:    true,
			wantsNotice: true,
			wantsBadge:  true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			withServerURL(t, "") // unset: the URL is the built-in guess

			gotHTTP, mode := decideStore(tc.up, tui.ServerInfo{DataDir: tc.serverDir},
				tui.DefaultServerURL, defaultDirForTest(t), mine)

			if gotHTTP != tc.wantHTTP {
				t.Errorf("useHTTP = %v, want %v", gotHTTP, tc.wantHTTP)
			}
			if got := mode.Notice != ""; got != tc.wantsNotice {
				t.Errorf("notice = %q; wanted a notice: %v", mode.Notice, tc.wantsNotice)
			}
			// The badge is the half that survives a cats pane, where the capture
			// hint claims the status line a moment after launch — so every
			// unusual outcome has to be legible from the badge alone.
			if got := mode.Badge != ""; got != tc.wantsBadge {
				t.Errorf("badge = %q; wanted a badge: %v", mode.Badge, tc.wantsBadge)
			}
		})
	}
}

// TestDecideHTTPHonorsAnExplicitURL is the other half of the rule, and the
// reason the identity check is not applied everywhere: a GONOTES_URL pointing at
// another machine reports a data directory in a filesystem this process cannot
// see, so comparing it with a local path is meaningless and would break the
// remote setups the HTTP store exists for.
func TestDecideHTTPHonorsAnExplicitURL(t *testing.T) {
	withServerURL(t, "http://notes.example.com:8444")

	got, mode := decideStore(true, tui.ServerInfo{DataDir: "/srv/gonotes/data"},
		"http://notes.example.com:8444", defaultDirForTest(t), mine)

	if !got {
		t.Error("a server named in GONOTES_URL was second-guessed on its data directory")
	}
	if mode.Notice != "" {
		t.Errorf("an expected server produced an unexpected notice: %q", mode.Notice)
	}
	if mode.Badge != "notes.example.com:8444" {
		t.Errorf("badge = %q, want the server it is talking to", mode.Badge)
	}
}

// TestDecideHTTPReportsAnAbsentNamedServer: falling back to local is right, but
// doing it in silence lets someone edit local notes for an hour believing they
// are writing to the server they named.
func TestDecideHTTPReportsAnAbsentNamedServer(t *testing.T) {
	withServerURL(t, "http://127.0.0.1:59997")

	got, mode := decideStore(false, tui.ServerInfo{}, "http://127.0.0.1:59997",
		defaultDirForTest(t), mine)

	if got {
		t.Fatal("decided on HTTP mode with nothing answering")
	}
	if mode.Notice == "" {
		t.Error("a named server that is not there went unmentioned")
	}
	if mode.Badge == "" {
		t.Error("local-instead-of-the-named-server left no lasting sign")
	}
}

// TestDecideHTTPTreatsUnknownDirsAsUnknown pins the one comparison that must
// never be made: two servers that both keep quiet about their directory are not
// thereby serving the same notes as us. An empty DataDir is "unknown", and the
// unknown branch is the one that keeps the old behavior AND explains itself —
// not the silent match.
func TestDecideHTTPTreatsUnknownDirsAsUnknown(t *testing.T) {
	withServerURL(t, "")

	_, mode := decideStore(true, tui.ServerInfo{DataDir: ""}, tui.DefaultServerURL,
		defaultDirForTest(t), "")
	if mode.Notice == "" {
		t.Error("an unidentified server matched an unknown local directory silently")
	}
}

// defaultDirForTest is ~/.gonotes, the directory that earns no badge on an
// ordinary launch.
func defaultDirForTest(t *testing.T) string {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory in this environment")
	}
	return filepath.Join(home, ".gonotes")
}

// TestLocalBadgeLabelsOnlyTheUnusual: a badge that is always on screen is a
// badge nobody reads, so the plain launch gets none — and every other case does.
func TestLocalBadgeLabelsOnlyTheUnusual(t *testing.T) {
	def := defaultDirForTest(t)
	other := filepath.Join(t.TempDir(), "notes")

	if got := localBadge(def, false); got != "" {
		t.Errorf("an ordinary launch was badged with %q", got)
	}
	if got := localBadge(def, true); got == "" {
		t.Error("a launch that passed over a server left no lasting sign")
	}
	if got := localBadge(other, false); got == "" {
		t.Errorf("a second data directory (%s) went unbadged", other)
	}
}

// TestBadgeForURLDropsTheScheme keeps the label to the part that identifies the
// server, since it is sharing a title bar with the app name.
func TestBadgeForURLDropsTheScheme(t *testing.T) {
	cases := map[string]string{
		"http://localhost:8444":     "localhost:8444",
		"https://notes.example.com": "notes.example.com",
		"http://192.168.1.10:8444":  "192.168.1.10:8444",
	}
	for in, want := range cases {
		if got := badgeForURL(in); got != want {
			t.Errorf("badgeForURL(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestSameDirResolvesSpellings: macOS makes this concrete — /tmp is a symlink to
// /private/tmp, so the same directory arrives spelled two ways and a plain
// string compare would call it two directories.
func TestSameDirResolvesSpellings(t *testing.T) {
	dir := t.TempDir()

	if !sameDir(dir, dir) {
		t.Fatal("a directory did not match itself")
	}
	if !sameDir(dir, filepath.Join(dir, ".")) {
		t.Error("a trailing . made a directory unequal to itself")
	}
	if sameDir(dir, filepath.Join(dir, "elsewhere")) {
		t.Error("two different directories compared equal")
	}
}
