// Package util provides small helpers shared across the Go agent harness.
package util

import (
	"strings"
	"unicode"
)

// SnakeCase converts a CamelCase or PascalCase identifier to snake_case.
//
// It matches the behaviour of the Python Skill._auto_name helper: a trailing
// "Skill" suffix is stripped before conversion, and an underscore is inserted
// only before an uppercase letter that follows a lowercase letter or digit.
// Consecutive capitals therefore collapse (for example, "HTTPClient" becomes
// "httpclient", not "http_client").
func SnakeCase(name string) string {
	name = strings.TrimSuffix(name, "Skill")
	if name == "" {
		return ""
	}

	var b strings.Builder
	b.Grow(len(name) + 4)
	runes := []rune(name)
	for i, r := range runes {
		if i > 0 && unicode.IsUpper(r) {
			prev := runes[i-1]
			if unicode.IsLower(prev) || unicode.IsDigit(prev) {
				b.WriteByte('_')
			}
		}
		b.WriteRune(unicode.ToLower(r))
	}
	return b.String()
}
