package artifact

import (
	"encoding/base64"
	"fmt"
)

// ReferenceID encodes a complete content reference as an opaque URL path ID.
func ReferenceID(reference Reference) string {
	return base64.RawURLEncoding.EncodeToString([]byte(ReferenceURI(reference)))
}

// ParseReferenceID validates and decodes an ID created by ReferenceID.
func ParseReferenceID(value string) (Reference, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return Reference{}, fmt.Errorf("artifact reference ID: %w", err)
	}
	return ParseReferenceURI(string(decoded))
}
