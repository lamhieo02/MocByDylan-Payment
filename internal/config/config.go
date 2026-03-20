// Package config loads environment variables from a .env-style file.
// Importing this package (even as a blank import) automatically loads
// "base.env" from the project root before any handler runs.
//
// Existing environment variables are never overwritten, so values set
// directly in the environment (e.g. Railway env vars) always take precedence.
package config

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"
)

// Load reads the file at path and sets any env var that is not already
// present in the environment. Lines starting with '#' and blank lines
// are ignored. Inline '#' comments and surrounding quotes are stripped.
// Returns nil when the file is absent (os.ErrNotExist).
func Load(path string) error {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("config: open %s: %w", path, err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())

		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		if idx := strings.Index(line, " #"); idx >= 0 {
			line = strings.TrimSpace(line[:idx])
		}

		idx := strings.IndexByte(line, '=')
		if idx < 0 {
			continue
		}

		key := strings.TrimSpace(line[:idx])
		val := strings.TrimSpace(line[idx+1:])

		if len(val) >= 2 {
			if (val[0] == '"' && val[len(val)-1] == '"') ||
				(val[0] == '\'' && val[len(val)-1] == '\'') {
				val = val[1 : len(val)-1]
			}
		}

		if key == "" {
			continue
		}

		if os.Getenv(key) == "" {
			if err := os.Setenv(key, val); err != nil {
				return fmt.Errorf("config: setenv %s (line %d): %w", key, lineNum, err)
			}
		}
	}
	return scanner.Err()
}

var candidatePaths = []string{
	"base.env",
	"../base.env",
	"../../base.env",
}

func init() {
	for _, p := range candidatePaths {
		if err := Load(p); err != nil {
			fmt.Fprintf(os.Stderr, "[config] warning: %v\n", err)
		}
	}
}
