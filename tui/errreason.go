package tui

import (
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"syscall"
)

// errReason turns a store error into the short reason the status bar shows.
//
// The status bar is one line, truncated to the terminal width, and it is
// written as "<what failed>: <why>". Go's own transport errors put the why at
// the far end of a long line, e.g.
//
//	failed to save note: Put "http://studio.local:8444/api/v1/notes/42": dial tcp 192.168.1.5:8444: connect: connection refused
//
// and a pane 80 columns wide cuts it off before "connection refused". So the
// failures a remote session actually meets mid-session are named in a few
// words, most useful part first:
//
//	transport  refused / timed out / unknown host / dropped → "studio.local:8444 refused the connection — is the server still running?"
//	API        5xx → "server error 500: <message>", 401 → "not signed in (401)…"
//
// Anything else (a local store's error, a 404/409 with the server's own
// sentence) is already short and specific, and is shown as it was.
//
// errors.As / errors.Is see through serr's wrapping, which is how every
// store method returns these.
func errReason(err error) string {
	if err == nil {
		return ""
	}

	var ae *apiError
	if asAPIError(err, &ae) {
		switch {
		case ae.status == http.StatusUnauthorized:
			// request() already retried once with fresh credentials, so a 401
			// that reaches here means signing in again failed too.
			return "not signed in (401) — restart the TUI to sign in again"
		case ae.status >= 500:
			reason := "server error " + strconv.Itoa(ae.status)
			if ae.message != "" {
				reason += ": " + ae.message
			}
			return reason
		}
		return ae.Error()
	}

	var ue *url.Error
	if errors.As(err, &ue) {
		host := ue.URL
		if u, perr := url.Parse(ue.URL); perr == nil && u.Host != "" {
			host = u.Host
		}
		var dnsErr *net.DNSError
		switch {
		case ue.Timeout():
			return "no answer from " + host + " (timed out)"
		case errors.Is(err, syscall.ECONNREFUSED):
			return host + " refused the connection — is the server still running?"
		case errors.As(err, &dnsErr):
			return "can't find " + host + " (unknown host)"
		case errors.Is(err, syscall.ECONNRESET), errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
			return host + " dropped the connection"
		}
		// Unclassified transport failure: drop the "Get <url>:" prefix, which
		// repeats the address, and keep the cause.
		return "can't reach " + host + ": " + ue.Err.Error()
	}

	return err.Error()
}
