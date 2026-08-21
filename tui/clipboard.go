package tui

import (
	"os/exec"
	"runtime"
	"strings"

	"github.com/rohanthewiz/serr"
)

// Reading the system clipboard, for the summarize-the-clipboard door in
// summarize.go.
//
// WHY SHELLING OUT RATHER THAN A CLIPBOARD LIBRARY: every Go clipboard package
// worth using is either cgo (which would end the pure-Go build bytdb bought us
// — see the build note in the skill doc) or a per-platform X11/Wayland
// dependency for a feature that is one keystroke on one screen. The utilities
// below are the same ones gn-clip.sh already relies on, so a machine where the
// script works is a machine where this works.
//
// The TUI reads the clipboard LOCALLY even in HTTP mode, and that is correct:
// the clipboard belongs to the person at the keyboard, not to whichever machine
// happens to hold the notes.

// clipboardReader is one candidate command: the binary and the arguments that
// make it print the clipboard to stdout.
type clipboardReader struct {
	name string
	args []string
}

// clipboardReaders lists the candidates for this platform, in preference order.
// A var rather than a const so a test can point it at a stub.
var clipboardReaders = defaultClipboardReaders()

func defaultClipboardReaders() []clipboardReader {
	switch runtime.GOOS {
	case "darwin":
		return []clipboardReader{{name: "pbpaste"}}
	case "windows":
		// -Raw keeps the newlines; without it PowerShell hands back an array of
		// lines that stringifies with spaces where the line breaks were.
		return []clipboardReader{{name: "powershell", args: []string{"-NoProfile", "-Command", "Get-Clipboard -Raw"}}}
	default:
		// Wayland first: on a Wayland session xclip may exist and answer from an
		// XWayland clipboard that mirrors nothing the user copied.
		return []clipboardReader{
			{name: "wl-paste", args: []string{"--no-newline"}},
			{name: "xclip", args: []string{"-selection", "clipboard", "-o"}},
			{name: "xsel", args: []string{"--clipboard", "--output"}},
		}
	}
}

// readClipboard returns the clipboard's text.
//
// Runs on a command goroutine, never the event loop: pbpaste on a large paste
// is fast but not instant, and a keystroke must not wait on a process.
func readClipboard() (string, error) {
	for _, r := range clipboardReaders {
		if _, err := exec.LookPath(r.name); err != nil {
			continue
		}
		out, err := exec.Command(r.name, r.args...).Output()
		if err != nil {
			return "", serr.Wrap(err, "could not read the clipboard", "tool", r.name)
		}
		return string(out), nil
	}
	return "", serr.New("no clipboard tool found on PATH", "looked_for", clipboardToolNames())
}

func clipboardToolNames() string {
	names := make([]string, 0, len(clipboardReaders))
	for _, r := range clipboardReaders {
		names = append(names, r.name)
	}
	return strings.Join(names, ", ")
}
