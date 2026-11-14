package artifact

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

const (
	ReferenceScheme = "artifact"
	MetaDigest      = "artifact_digest"
	MetaSize        = "artifact_size"
	MetaContentType = "artifact_content_type"
	MetaEncoding    = "artifact_encoding"
	MetaID          = "artifact_id"
)

// ReferenceURI returns a self-describing, non-secret URI for stored content.
func ReferenceURI(reference Reference) string {
	query := url.Values{}
	query.Set("size", strconv.FormatInt(reference.Size, 10))
	query.Set("content_type", reference.ContentType)
	return (&url.URL{
		Scheme:   ReferenceScheme,
		Host:     "sha256",
		Path:     "/" + reference.Digest,
		RawQuery: query.Encode(),
	}).String()
}

// ParseReferenceURI validates and decodes a URI created by ReferenceURI.
func ParseReferenceURI(value string) (Reference, error) {
	parsed, err := url.Parse(value)
	if err != nil {
		return Reference{}, fmt.Errorf("artifact reference URI: %w", err)
	}
	if parsed.Scheme != ReferenceScheme || parsed.Host != "sha256" || parsed.User != nil || parsed.Fragment != "" {
		return Reference{}, fmt.Errorf("artifact reference URI: invalid scheme or authority")
	}
	if len(parsed.Query()) != 2 || parsed.Query().Get("size") == "" || parsed.Query().Get("content_type") == "" {
		return Reference{}, fmt.Errorf("artifact reference URI: size and content_type are required")
	}
	size, err := strconv.ParseInt(parsed.Query().Get("size"), 10, 64)
	if err != nil {
		return Reference{}, fmt.Errorf("artifact reference URI: invalid size: %w", err)
	}
	reference := Reference{
		Digest:      strings.TrimPrefix(parsed.EscapedPath(), "/"),
		Size:        size,
		ContentType: parsed.Query().Get("content_type"),
	}
	if err := reference.Validate(); err != nil {
		return Reference{}, err
	}
	return reference, nil
}
