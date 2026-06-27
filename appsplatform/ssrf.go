package appsplatform

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
)

// AllowPrivateWebhooksEnv, when truthy ("1"/"true"/"yes"), disables the
// destination guard that blocks outbound webhook deliveries to private,
// loopback, link-local, and metadata addresses. It is OFF by default; only
// self-hosters who legitimately POST to internal targets should set it.
const AllowPrivateWebhooksEnv = "VULOS_APPS_ALLOW_PRIVATE_WEBHOOKS"

// resolveIPs resolves a host to its IP addresses. It is a package var so tests
// can stub DNS resolution without network access.
var resolveIPs = net.LookupIP

// allowPrivateWebhooks reports whether the private-destination guard is disabled
// via the environment.
func allowPrivateWebhooks() bool {
	v := strings.TrimSpace(os.Getenv(AllowPrivateWebhooksEnv))
	return v == "1" || strings.EqualFold(v, "true") || strings.EqualFold(v, "yes")
}

// ValidateWebhookURL guards outbound webhook destinations against SSRF. The
// scheme must be http or https, and (unless AllowPrivateWebhooksEnv is set) the
// host must not resolve to a private/loopback/link-local/metadata range
// (RFC1918, 127.0.0.0/8, ::1, 169.254.0.0/16 incl. 169.254.169.254, fc00::/7,
// fe80::/10, 0.0.0.0/::, multicast).
//
// An empty URL is allowed (it means "no webhook configured"). When a host is a
// hostname it is resolved and EVERY resolved address must be permitted, so a
// name that maps to even one blocked address is rejected.
func ValidateWebhookURL(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("appsplatform: invalid webhook_url: %w", err)
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
	default:
		return fmt.Errorf("appsplatform: webhook_url scheme %q not allowed (use http or https)", u.Scheme)
	}
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("appsplatform: webhook_url has no host")
	}
	if allowPrivateWebhooks() {
		return nil
	}

	var ips []net.IP
	if ip := net.ParseIP(host); ip != nil {
		ips = []net.IP{ip}
	} else {
		resolved, err := resolveIPs(host)
		if err != nil {
			return fmt.Errorf("appsplatform: webhook_url host %q does not resolve: %w", host, err)
		}
		if len(resolved) == 0 {
			return fmt.Errorf("appsplatform: webhook_url host %q resolved to no addresses", host)
		}
		ips = resolved
	}
	for _, ip := range ips {
		if isBlockedIP(ip) {
			return fmt.Errorf("appsplatform: webhook_url host %q resolves to disallowed address %s", host, ip)
		}
	}
	return nil
}

// isBlockedIP reports whether ip falls in a range that must never be a webhook
// destination (loopback, RFC1918/ULA private, link-local incl. the cloud
// metadata 169.254.169.254, unspecified, and multicast).
func isBlockedIP(ip net.IP) bool {
	if ip4 := ip.To4(); ip4 != nil {
		ip = ip4
	}
	return ip.IsUnspecified() || // 0.0.0.0, ::
		ip.IsLoopback() || // 127.0.0.0/8, ::1
		ip.IsPrivate() || // RFC1918, fc00::/7
		ip.IsLinkLocalUnicast() || // 169.254.0.0/16 (incl .169.254), fe80::/10
		ip.IsLinkLocalMulticast() ||
		ip.IsMulticast()
}
