package main

import (
	"bytes"
	"slices"
	"strings"
	"time"

	"github.com/rohanthewiz/serr"
	"gopkg.in/yaml.v3"
)

// mdFrontmatter is the YAML frontmatter block written to and read from
// Markdown note files. The field set is Obsidian-friendly: tags and
// categories are plain string lists, timestamps are RFC3339 strings.
// Categories use "Name" or "Name/Subcategory" notation, one entry per
// selected subcategory (category names containing "/" are not supported).
type mdFrontmatter struct {
	GUID        string     `yaml:"guid,omitempty"`
	Title       string     `yaml:"title,omitempty"`
	Description string     `yaml:"description,omitempty"`
	Tags        stringList `yaml:"tags,omitempty"`
	Categories  stringList `yaml:"categories,omitempty"`
	Private     bool       `yaml:"private,omitempty"`
	Flagged     bool       `yaml:"flagged,omitempty"`
	Created     string     `yaml:"created,omitempty"`
	Updated     string     `yaml:"updated,omitempty"`
}

// stringList is a []string that also accepts a single comma-separated
// scalar when unmarshaling, since hand-edited or Obsidian-legacy
// frontmatter often writes "tags: a, b" instead of a YAML sequence.
type stringList []string

func (s *stringList) UnmarshalYAML(value *yaml.Node) error {
	switch value.Kind {
	case yaml.ScalarNode:
		*s = splitAndTrim(value.Value, ",")
		return nil
	case yaml.SequenceNode:
		var out []string
		for _, item := range value.Content {
			if v := strings.TrimSpace(item.Value); v != "" {
				out = append(out, v)
			}
		}
		*s = out
		return nil
	default:
		return serr.New("expected a string or list of strings")
	}
}

// renderNoteMd assembles a note file: frontmatter block, blank line, body.
// The body gets a trailing newline if it lacks one (parseNoteMd trims it
// back off, keeping the roundtrip stable).
func renderNoteMd(fm mdFrontmatter, body string) ([]byte, error) {
	yb, err := yaml.Marshal(&fm)
	if err != nil {
		return nil, serr.Wrap(err, "failed to marshal frontmatter")
	}

	var buf bytes.Buffer
	buf.WriteString("---\n")
	buf.Write(yb)
	buf.WriteString("---\n")
	if body != "" {
		buf.WriteString("\n")
		buf.WriteString(body)
		if !strings.HasSuffix(body, "\n") {
			buf.WriteString("\n")
		}
	}
	return buf.Bytes(), nil
}

// parseNoteMd splits a Markdown file into frontmatter and body.
// Files without a leading "---" block (or with an unterminated one)
// are treated as all-body with empty frontmatter, so plain Markdown
// files import cleanly. One trailing newline is trimmed from the body
// to mirror the one renderNoteMd appends.
func parseNoteMd(raw []byte) (fm mdFrontmatter, body string, err error) {
	content := strings.ReplaceAll(string(raw), "\r\n", "\n")

	if !strings.HasPrefix(content, "---\n") {
		return fm, strings.TrimSuffix(content, "\n"), nil
	}

	rest := content[len("---\n"):]
	var yamlPart string
	switch {
	case strings.HasPrefix(rest, "---\n"): // empty frontmatter block
		body = rest[len("---\n"):]
	case strings.HasSuffix(rest, "\n---"): // frontmatter only, no body
		yamlPart = rest[:len(rest)-len("---")]
	default:
		idx := strings.Index(rest, "\n---\n")
		if idx < 0 {
			// No closing delimiter — not frontmatter, treat whole file as body
			return fm, strings.TrimSuffix(content, "\n"), nil
		}
		yamlPart = rest[:idx+1]
		body = rest[idx+len("\n---\n"):]
	}

	if yamlPart != "" {
		if err = yaml.Unmarshal([]byte(yamlPart), &fm); err != nil {
			return fm, "", serr.Wrap(err, "invalid YAML frontmatter")
		}
	}

	body = strings.TrimPrefix(body, "\n")
	body = strings.TrimSuffix(body, "\n")
	return fm, body, nil
}

// sanitizeFileName makes a note title safe for use as a file or folder
// name across platforms. Reserved characters become "-", surrounding
// spaces/dots are trimmed, and the result is capped at 100 runes.
func sanitizeFileName(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r < 0x20 || strings.ContainsRune(`/\:*?"<>|`, r) {
			b.WriteRune('-')
		} else {
			b.WriteRune(r)
		}
	}
	out := strings.Trim(b.String(), " .")
	if runes := []rune(out); len(runes) > 100 {
		out = strings.Trim(string(runes[:100]), " .")
	}
	if out == "" {
		out = "untitled"
	}
	return out
}

// parseCategorySpecs groups "Name" / "Name/Sub" entries by category name,
// preserving first-seen order and deduplicating subcategories.
func parseCategorySpecs(specs []string) (names []string, subsByName map[string][]string) {
	subsByName = map[string][]string{}
	for _, spec := range specs {
		parts := strings.Split(spec, "/")
		name := strings.TrimSpace(parts[0])
		if name == "" {
			continue
		}
		if _, seen := subsByName[name]; !seen {
			names = append(names, name)
			subsByName[name] = nil
		}
		for _, sub := range parts[1:] {
			sub = strings.TrimSpace(sub)
			if sub != "" && !slices.Contains(subsByName[name], sub) {
				subsByName[name] = append(subsByName[name], sub)
			}
		}
	}
	return names, subsByName
}

// normalizeTags canonicalizes a comma-separated tag string for storage
// and comparison: split, trim, drop empties, rejoin.
func normalizeTags(s string) string {
	return strings.Join(splitAndTrim(s, ","), ",")
}

// parseMdTime parses a frontmatter timestamp, accepting RFC3339 plus the
// laxer date formats people type by hand. Returns the zero time if the
// value is empty or unparsable.
func parseMdTime(s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}
	}
	layouts := []string{
		time.RFC3339,
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05",
		"2006-01-02",
	}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

func splitAndTrim(s, sep string) []string {
	var out []string
	for part := range strings.SplitSeq(s, sep) {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}
