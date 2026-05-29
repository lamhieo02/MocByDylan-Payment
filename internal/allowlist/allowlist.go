// Package allowlist parses and checks the INVENTORY_ALLOWED_EMAILS allow-list.
// Emails are normalised to lowercase and trimmed before matching.
package allowlist

import (
	"log"
	"os"
	"strings"
)

// set is the in-memory representation of the allow-list.
var set map[string]struct{}

func init() {
	set = parse(os.Getenv("INVENTORY_ALLOWED_EMAILS"))
	if len(set) == 0 {
		log.Println("[allowlist] WARNING: INVENTORY_ALLOWED_EMAILS is empty — all inventory requests will be rejected")
	}
}

// parse splits a comma-separated list of emails into a normalised set.
func parse(raw string) map[string]struct{} {
	m := make(map[string]struct{})
	for _, entry := range strings.Split(raw, ",") {
		email := normalize(entry)
		if email != "" {
			m[email] = struct{}{}
		}
	}
	return m
}

// normalize trims whitespace and converts an email to lowercase.
func normalize(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// Allowed reports whether the given email is on the allow-list.
// The email is normalised before the lookup.
func Allowed(email string) bool {
	if len(set) == 0 {
		return false
	}
	_, ok := set[normalize(email)]
	return ok
}

// Reload re-reads INVENTORY_ALLOWED_EMAILS from the environment.
// Useful in tests that change the env var after package init.
func Reload() {
	set = parse(os.Getenv("INVENTORY_ALLOWED_EMAILS"))
}
