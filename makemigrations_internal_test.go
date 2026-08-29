package fabrin

import (
	"testing"
	"time"
)

func TestNextVersionAdvancesPastARecordedVersionFromTheCurrentSecond(t *testing.T) {
	current := time.Now().UTC().Format("20060102150405")
	got, err := nextVersion(map[string][]manifestEntry{
		"shop": {{Version: current}},
	})
	if err != nil {
		t.Fatalf("nextVersion: %v", err)
	}
	if got <= current {
		t.Fatalf("nextVersion = %q, want a version after %q", got, current)
	}
}
