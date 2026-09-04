package security

import (
	"context"
	"testing"
)

func TestValidateOutboundURLBlocksPrivateAndUnsafeTargets(t *testing.T) {
	for _, raw := range []string{
		"file:///etc/passwd",
		"http://127.0.0.1:8080/",
		"http://169.254.169.254/latest/meta-data/",
		"http://10.0.0.1/internal",
	} {
		if err := ValidateOutboundURL(context.Background(), raw, false); err == nil {
			t.Errorf("ValidateOutboundURL(%q) = nil, want rejection", raw)
		}
	}
	if err := ValidateOutboundURL(context.Background(), "https://93.184.216.34/path", false); err != nil {
		t.Fatalf("public URL rejected: %v", err)
	}
	if err := ValidateOutboundURL(context.Background(), "http://127.0.0.1:8080/", true); err != nil {
		t.Fatalf("private URL should be allowed when explicitly configured: %v", err)
	}
	for _, raw := range []string{"", "http://", "ftp://example.com", "http://user:pass@example.com"} {
		if err := ValidateOutboundURL(context.Background(), raw, false); err == nil {
			t.Errorf("ValidateOutboundURL(%q) = nil, want invalid URL rejection", raw)
		}
	}
	if err := ValidateOutboundURL(context.Background(), "https://does-not-exist.invalid/", false); err == nil {
		t.Fatal("unresolvable hostname was accepted")
	}
}
