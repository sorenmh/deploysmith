package service

import (
	"fmt"
	"regexp"
)

// namePattern restricts app and environment names. Both become path
// segments in the gitops repository and storage keys, so they must not
// contain separators or be "." or "..".
var namePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,62}$`)

func validateName(kind, name string) error {
	if name == "" {
		return invalid("invalid_request", fmt.Sprintf("%s name is required", kind))
	}
	if !namePattern.MatchString(name) {
		return invalid("invalid_request", fmt.Sprintf("%s name must start with a letter or digit and contain only letters, digits, '.', '_' or '-' (at most 63 characters)", kind))
	}
	return nil
}
