package auth

import (
	"fmt"
	"net"
	"net/http"
	"strings"
)

// clientCertHeader is the header a reverse proxy uses to forward the
// PEM-encoded client certificate after terminating mTLS.
const clientCertHeader = "X-Client-Cert"

// ProxyCertSanitizer strips the X-Client-Cert header from requests that
// do not originate from a trusted proxy. This prevents untrusted clients
// from injecting a forged client certificate header.
type ProxyCertSanitizer struct {
	trusted []*net.IPNet
}

// NewProxyCertSanitizer creates a sanitizer from a comma-separated list
// of trusted CIDR ranges (e.g. "127.0.0.0/8,10.0.0.0/8").
func NewProxyCertSanitizer(cidrs string) (*ProxyCertSanitizer, error) {
	s := &ProxyCertSanitizer{}
	for _, raw := range strings.Split(cidrs, ",") {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		_, network, err := net.ParseCIDR(raw)
		if err != nil {
			return nil, fmt.Errorf("parse trusted proxy CIDR %q: %w", raw, err)
		}
		s.trusted = append(s.trusted, network)
	}
	return s, nil
}

// Handler returns middleware that strips X-Client-Cert from untrusted sources.
func (s *ProxyCertSanitizer) Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(clientCertHeader) != "" && !s.isTrusted(r.RemoteAddr) {
			r.Header.Del(clientCertHeader)
		}
		next.ServeHTTP(w, r)
	})
}

// isTrusted checks whether the remote address falls within a trusted CIDR.
func (s *ProxyCertSanitizer) isTrusted(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		// RemoteAddr may be an IP without port in some edge cases.
		host = remoteAddr
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	for _, network := range s.trusted {
		if network.Contains(ip) {
			return true
		}
	}
	return false
}
