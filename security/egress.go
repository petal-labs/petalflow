package security

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
)

var ErrOutboundURLBlocked = errors.New("outbound URL blocked by egress policy")

// ValidateOutboundURL rejects non-HTTP schemes and destinations in private,
// loopback, link-local, multicast, and cloud-metadata address ranges unless
// explicitly permitted. DNS results are checked before a request is sent.
func ValidateOutboundURL(ctx context.Context, rawURL string, allowPrivate bool) error {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || u.Scheme != "http" && u.Scheme != "https" || u.User != nil || u.Hostname() == "" {
		return fmt.Errorf("%w: invalid URL", ErrOutboundURLBlocked)
	}
	if allowPrivate {
		return nil
	}
	host := u.Hostname()
	if ip := net.ParseIP(host); ip != nil {
		if isBlockedIP(ip) {
			return fmt.Errorf("%w: private destination", ErrOutboundURLBlocked)
		}
		return nil
	}
	ips, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
	if err != nil {
		return fmt.Errorf("%w: destination resolution failed", ErrOutboundURLBlocked)
	}
	for _, ip := range ips {
		if isBlockedIP(ip) {
			return fmt.Errorf("%w: private destination", ErrOutboundURLBlocked)
		}
	}
	return nil
}

func isBlockedIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast() || ip.Equal(net.ParseIP("169.254.169.254"))
}
