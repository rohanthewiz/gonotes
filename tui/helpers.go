package tui

import (
	"database/sql"
	"strings"
)

// nullStr safely extracts a string from sql.NullString, returning "" if null.
func nullStr(ns sql.NullString) string {
	if ns.Valid {
		return ns.String
	}
	return ""
}

// strPtr returns a pointer to s, or nil if s is empty.
// Useful for building NoteInput/CategoryInput where nil means "no change".
func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// truncate shortens s to maxLen characters, appending "…" if truncated.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	if maxLen <= 1 {
		return "…"
	}
	return s[:maxLen-1] + "…"
}

// parseTags splits a comma-separated tag string into trimmed, non-empty tags.
func parseTags(tagStr string) []string {
	if tagStr == "" {
		return nil
	}
	raw := strings.Split(tagStr, ",")
	tags := make([]string, 0, len(raw))
	for _, t := range raw {
		t = strings.TrimSpace(t)
		if t != "" {
			tags = append(tags, t)
		}
	}
	return tags
}

// joinTags is the inverse of parseTags — joins a slice into "tag1, tag2, ...".
func joinTags(tags []string) string {
	return strings.Join(tags, ", ")
}

// max returns the larger of a and b.
func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// min returns the smaller of a and b.
func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
