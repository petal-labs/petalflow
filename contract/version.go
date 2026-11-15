// Package contract declares PetalFlow's externally visible contract versions.
package contract

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/petal-labs/petalflow/schemafmt"
)

const (
	// APIVersionHeader identifies the HTTP response header carrying APIVersion.
	APIVersionHeader = "PetalFlow-API-Version"

	// APIVersion is the current HTTP API contract version.
	APIVersion = "1.0.0"

	// EventSchemaVersion is the current runtime event schema version.
	EventSchemaVersion = "1.0.0"

	// AdapterContractVersion is the current provider adapter contract version.
	AdapterContractVersion = "1.0.0"
)

// ValidateVersion requires a valid semantic version with the same major as
// the supported contract. Minor and patch changes remain compatible.
func ValidateVersion(version, supported string) error {
	majorText, _, ok := strings.Cut(supported, ".")
	if !ok {
		return fmt.Errorf("invalid supported contract version %q", supported)
	}
	major, err := strconv.Atoi(majorText)
	if err != nil {
		return fmt.Errorf("invalid supported contract version %q: %w", supported, err)
	}
	return schemafmt.ValidateSchemaVersion(version, major)
}
