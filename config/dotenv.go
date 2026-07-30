package config

import (
	"bufio"
	"fmt"
	"io"
	"strings"
)

// parseDotenv reads KEY=VALUE lines.
//
// A deliberately small subset of what dotenv implementations support — just what
// real files in the wild contain — because every additional rule is a rule someone
// must guess at:
//
//   - blank lines and # comments, at any indentation
//   - an optional `export ` prefix, so a file can be sourced by a shell too
//   - single or double quotes around the value, stripped
//   - a trailing # comment after an UNQUOTED value
//
// Not supported, on purpose: variable interpolation (${OTHER}), multi-line values,
// and escape sequences. Interpolation in particular makes a settings file a
// program, and then the question "what is this value" needs an evaluator.
//
// A line with no '=' is an error rather than a skip. Skipping it means a setting
// silently does not apply — the failure mode this whole package exists to avoid.
func parseDotenv(r io.Reader) (map[string]string, error) {
	out := map[string]string{}
	sc := bufio.NewScanner(r)

	for lineNo := 1; sc.Scan(); lineNo++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		line = strings.TrimPrefix(line, "export ")

		key, val, found := strings.Cut(line, "=")
		if !found {
			return nil, fmt.Errorf("line %d: %q has no '='", lineNo, sc.Text())
		}

		key = strings.TrimSpace(key)
		if key == "" {
			return nil, fmt.Errorf("line %d: empty key", lineNo)
		}

		val = strings.TrimSpace(val)

		if unquoted, ok := stripQuotes(val); ok {
			// Quotes are the author saying "this is the value, verbatim", so a # inside
			// them is data and must survive.
			val = unquoted
		} else if i := strings.Index(val, "#"); i >= 0 {
			val = strings.TrimSpace(val[:i])
		}

		out[key] = val
	}

	if err := sc.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// stripQuotes removes a matching pair of surrounding quotes, reporting whether it
// found one.
func stripQuotes(s string) (string, bool) {
	if len(s) < 2 {
		return s, false
	}
	first, last := s[0], s[len(s)-1]
	if first != last {
		return s, false
	}
	if first != '"' && first != '\'' {
		return s, false
	}
	return s[1 : len(s)-1], true
}
