package recovery

import (
	"fmt"
	"regexp"
)

var (
	incidentIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{2,63}$`)
	snapshotIDPattern = regexp.MustCompile(`^[0-9a-f]{8,64}$`)
)

// ValidateIncidentID keeps an operator-supplied incident safe for the remote,
// line-oriented recovery protocol and for target-side evidence filenames.
func ValidateIncidentID(value string) error {
	if !incidentIDPattern.MatchString(value) {
		return fmt.Errorf("incident identifier must be 3-64 lowercase letters, digits, dots, underscores, or hyphens")
	}
	return nil
}

// ValidateSnapshotID accepts Restic's unambiguous hexadecimal IDs or the
// explicit latest selector. Snapshot selection is verified again on target.
func ValidateSnapshotID(value string) error {
	if value == "latest" || snapshotIDPattern.MatchString(value) {
		return nil
	}
	return fmt.Errorf("snapshot must be latest or 8-64 lowercase hexadecimal characters")
}
