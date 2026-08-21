package summarize

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// The half of this package that can actually be wrong is everything AROUND the
// model call: what is refused before it, and what is accepted after it. The
// runner seam lets all of that run without `claude` installed, which is also
// what keeps these tests deterministic and free.

func withRunner(t *testing.T, fn func(ctx context.Context, model, text string) (string, error)) {
	t.Helper()
	prev := runner
	runner = fn
	t.Cleanup(func() { runner = prev })
}

// available fakes a machine with the CLI installed, so the tests exercise the
// parse path rather than stopping at the availability gate.
func stubAvailable(t *testing.T) {
	t.Helper()
	prev := lookPath
	lookPath = func(string) (string, error) { return "/usr/local/bin/claude", nil }
	t.Cleanup(func() { lookPath = prev })
}

func TestSummarizeParsesPlainJSON(t *testing.T) {
	stubAvailable(t)
	withRunner(t, func(_ context.Context, model, text string) (string, error) {
		if model != DefaultModel {
			t.Errorf("model = %q, want the default %q", model, DefaultModel)
		}
		if text != "the source text" {
			t.Errorf("text = %q, want it passed through untouched", text)
		}
		return `{"title":"A Title","description":"","body":"- one\n- two"}`, nil
	})

	res, err := Summarize(context.Background(), "the source text", "")
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if res.Title != "A Title" || res.Description != "" || res.Body != "- one\n- two" {
		t.Fatalf("got %+v", res)
	}
}

// A fenced reply is the common case, not an exotic one: models fence JSON even
// when the prompt says not to.
func TestSummarizeStripsCodeFences(t *testing.T) {
	stubAvailable(t)
	withRunner(t, func(context.Context, string, string) (string, error) {
		return "```json\n{\"title\":\"T\",\"body\":\"B\"}\n```\n", nil
	})
	res, err := Summarize(context.Background(), "x", "")
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if res.Body != "B" {
		t.Fatalf("body = %q", res.Body)
	}
}

// A body whose own content contains a fence must survive: it lives inside a
// JSON string, on one line, so no line of the reply begins with a fence.
func TestSummarizeKeepsFencesInsideTheBody(t *testing.T) {
	stubAvailable(t)
	withRunner(t, func(context.Context, string, string) (string, error) {
		return "{\"title\":\"T\",\"body\":\"before\\n```go\\ncode\\n```\\nafter\"}", nil
	})
	res, err := Summarize(context.Background(), "x", "")
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if !strings.Contains(res.Body, "```go") {
		t.Fatalf("the body lost its code fence: %q", res.Body)
	}
}

// Failing loudly is the design: a mangled note is worse than no note, because
// the source is gone by the time anyone notices.
func TestSummarizeRejectsUnparseableReply(t *testing.T) {
	stubAvailable(t)
	withRunner(t, func(context.Context, string, string) (string, error) {
		return "Sure! Here is a summary of your text.", nil
	})
	if _, err := Summarize(context.Background(), "x", ""); err == nil {
		t.Fatal("expected an error for a prose reply")
	}
}

func TestSummarizeRejectsEmptyBody(t *testing.T) {
	stubAvailable(t)
	withRunner(t, func(context.Context, string, string) (string, error) {
		return `{"title":"T","body":"   \n"}`, nil
	})
	if _, err := Summarize(context.Background(), "x", ""); err == nil {
		t.Fatal("expected an error for an empty body")
	}
}

func TestSummarizeCapsTheTitle(t *testing.T) {
	stubAvailable(t)
	long := strings.Repeat("x", TitleMax+50)
	withRunner(t, func(context.Context, string, string) (string, error) {
		return `{"title":"` + long + `","body":"B"}`, nil
	})
	res, err := Summarize(context.Background(), "x", "")
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if n := len([]rune(res.Title)); n != TitleMax {
		t.Fatalf("title length = %d, want %d", n, TitleMax)
	}
	if !strings.HasSuffix(res.Title, "...") {
		t.Fatalf("a truncated title should say so: %q", res.Title)
	}
}

// The guards below all fire BEFORE the model call — the point of each one is
// that nothing is spent on a request that cannot produce a useful note.
func TestSummarizeRefusesBeforeSpendingAnything(t *testing.T) {
	stubAvailable(t)
	called := false
	withRunner(t, func(context.Context, string, string) (string, error) {
		called = true
		return "", nil
	})

	for _, tc := range []struct {
		name string
		text string
	}{
		{"empty", ""},
		{"whitespace only", "  \n\t "},
		{"over the size cap", strings.Repeat("a", MaxInput+1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Summarize(context.Background(), tc.text, ""); err == nil {
				t.Fatal("expected a refusal")
			}
			if called {
				t.Fatal("the CLI was invoked for a request that should have been refused")
			}
		})
	}
}

func TestSummarizeReportsAMissingCLI(t *testing.T) {
	prev := lookPath
	lookPath = func(string) (string, error) { return "", errors.New("not found") }
	t.Cleanup(func() { lookPath = prev })

	_, err := Summarize(context.Background(), "x", "")
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable — a UI keys its whole explanation off this", err)
	}
}

func TestSummarizePassesTheRequestedModel(t *testing.T) {
	stubAvailable(t)
	var got string
	withRunner(t, func(_ context.Context, model, _ string) (string, error) {
		got = model
		return `{"title":"T","body":"B"}`, nil
	})
	if _, err := Summarize(context.Background(), "x", "sonnet"); err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if got != "sonnet" {
		t.Fatalf("model = %q, want sonnet", got)
	}
}
