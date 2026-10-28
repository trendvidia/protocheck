package protocheck

// Reference Catalog implementation (RFC-001 §7 / #043): localized messages
// are produced at format time from a violation's code + params through the
// catalog registered for a locale; a miss falls back to the violation's
// FallbackMessage. Concrete catalogs backed by files or translation
// services live in user code — MapCatalog covers the common in-memory
// case.

import (
	"fmt"
	"strings"
)

// MapCatalog is an in-memory Catalog: entries keyed by violation code,
// values are message templates. `{name}` placeholders interpolate the
// violation's Params — a placeholder with no matching param is left
// verbatim, so missing translations surface visibly rather than silently
// vanishing.
type MapCatalog struct {
	entries map[string]string
}

// NewMapCatalog builds a MapCatalog from code → template entries.
//
//	protocheck.NewMapCatalog(map[string]string{
//	    "string.min_len":     "must be at least {min_len} characters",
//	    "users.email.format": "not a valid address (pattern {pattern})",
//	})
func NewMapCatalog(entries map[string]string) *MapCatalog {
	copied := make(map[string]string, len(entries))
	for code, tmpl := range entries {
		copied[code] = tmpl
	}
	return &MapCatalog{entries: copied}
}

// Render implements Catalog.
func (c *MapCatalog) Render(code string, params map[string]any) (string, bool) {
	tmpl, ok := c.entries[code]
	if !ok {
		return "", false
	}
	return interpolate(tmpl, params), true
}

// interpolate substitutes {name} placeholders with the fmt %v rendering of
// the matching param. Unmatched placeholders and stray braces pass through
// unchanged.
func interpolate(tmpl string, params map[string]any) string {
	if len(params) == 0 || !strings.ContainsRune(tmpl, '{') {
		return tmpl
	}
	var b strings.Builder
	b.Grow(len(tmpl))
	for {
		open := strings.IndexByte(tmpl, '{')
		if open < 0 {
			b.WriteString(tmpl)
			return b.String()
		}
		closing := strings.IndexByte(tmpl[open:], '}')
		if closing < 0 {
			b.WriteString(tmpl)
			return b.String()
		}
		name := tmpl[open+1 : open+closing]
		if val, ok := params[name]; ok {
			b.WriteString(tmpl[:open])
			fmt.Fprintf(&b, "%v", val)
		} else {
			b.WriteString(tmpl[:open+closing+1])
		}
		tmpl = tmpl[open+closing+1:]
	}
}
