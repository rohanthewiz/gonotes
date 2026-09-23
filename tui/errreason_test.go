package tui

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rohanthewiz/serr"
)

// errReason is checked against real failures from the real HTTP store, not
// hand-built errors, so a change in how the store wraps them shows up here.
func TestErrReasonNamesTransportFailures(t *testing.T) {
	// A port nothing listens on: grab one, then close it.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()

	refused := NewHTTPStore("http://" + addr).(*httpStore)
	_, err = refused.ListNotes("u")
	if err == nil {
		t.Fatal("a request to a closed port succeeded")
	}
	got := errReason(serr.Wrap(err, "failed to load notes"))
	if want := addr + " refused the connection"; !strings.HasPrefix(got, want) {
		t.Errorf("refused: got %q, want it to start %q", got, want)
	}

	// A server that answers too slowly for the client's timeout.
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
	}))
	defer slow.Close()
	st := NewHTTPStore(slow.URL).(*httpStore)
	st.hc.Timeout = 50 * time.Millisecond
	_, err = st.ListNotes("u")
	if got := errReason(err); !strings.Contains(got, "timed out") {
		t.Errorf("timeout: got %q, want it to say timed out", got)
	}
}

func TestErrReasonNamesAPIFailures(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{&apiError{status: 500, message: "database error"}, "server error 500: database error"},
		{&apiError{status: 502}, "server error 502"},
		{&apiError{status: 401, message: "authentication required"}, "not signed in (401)"},
		{&apiError{status: 404, message: "note not found"}, "note not found"},
		{serr.New("a local store failure"), "a local store failure"},
	}
	for _, c := range cases {
		if got := errReason(serr.Wrap(c.err, "context")); !strings.HasPrefix(got, c.want) {
			t.Errorf("errReason(%v) = %q, want prefix %q", c.err, got, c.want)
		}
	}
}
