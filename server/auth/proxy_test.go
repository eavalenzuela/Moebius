package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNewProxyCertSanitizer_ValidCIDRs(t *testing.T) {
	s, err := NewProxyCertSanitizer("127.0.0.0/8, 10.0.0.0/8, ::1/128")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(s.trusted) != 3 {
		t.Fatalf("expected 3 trusted networks, got %d", len(s.trusted))
	}
}

func TestNewProxyCertSanitizer_InvalidCIDR(t *testing.T) {
	_, err := NewProxyCertSanitizer("not-a-cidr")
	if err == nil {
		t.Fatal("expected error for invalid CIDR")
	}
}

func TestNewProxyCertSanitizer_EmptyAndWhitespace(t *testing.T) {
	s, err := NewProxyCertSanitizer("  , ,127.0.0.0/8, ")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(s.trusted) != 1 {
		t.Fatalf("expected 1 trusted network, got %d", len(s.trusted))
	}
}

func TestHandler_UntrustedIPStripsHeader(t *testing.T) {
	s, _ := NewProxyCertSanitizer("10.0.0.0/8")

	var gotHeader string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Get(clientCertHeader)
	})

	req := httptest.NewRequest("GET", "/", http.NoBody)
	req.RemoteAddr = "203.0.113.5:12345" // untrusted IP
	req.Header.Set(clientCertHeader, "FAKE-CERT-PEM")

	rr := httptest.NewRecorder()
	s.Handler(inner).ServeHTTP(rr, req)

	if gotHeader != "" {
		t.Errorf("expected header stripped for untrusted IP, got %q", gotHeader)
	}
}

func TestHandler_TrustedIPPreservesHeader(t *testing.T) {
	s, _ := NewProxyCertSanitizer("10.0.0.0/8")

	var gotHeader string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Get(clientCertHeader)
	})

	req := httptest.NewRequest("GET", "/", http.NoBody)
	req.RemoteAddr = "10.1.2.3:54321" // trusted IP
	req.Header.Set(clientCertHeader, "REAL-CERT-PEM")

	rr := httptest.NewRecorder()
	s.Handler(inner).ServeHTTP(rr, req)

	if gotHeader != "REAL-CERT-PEM" {
		t.Errorf("expected header preserved for trusted IP, got %q", gotHeader)
	}
}

func TestHandler_NoHeaderPassesThrough(t *testing.T) {
	s, _ := NewProxyCertSanitizer("10.0.0.0/8")

	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	})

	req := httptest.NewRequest("GET", "/", http.NoBody)
	req.RemoteAddr = "203.0.113.5:12345"
	// no X-Client-Cert header

	rr := httptest.NewRecorder()
	s.Handler(inner).ServeHTTP(rr, req)

	if !called {
		t.Fatal("inner handler not called")
	}
}

func TestHandler_IPv6TrustedIP(t *testing.T) {
	s, _ := NewProxyCertSanitizer("::1/128")

	var gotHeader string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Get(clientCertHeader)
	})

	req := httptest.NewRequest("GET", "/", http.NoBody)
	req.RemoteAddr = "[::1]:8080"
	req.Header.Set(clientCertHeader, "IPV6-CERT")

	rr := httptest.NewRecorder()
	s.Handler(inner).ServeHTTP(rr, req)

	if gotHeader != "IPV6-CERT" {
		t.Errorf("expected header preserved for IPv6 loopback, got %q", gotHeader)
	}
}

func TestHandler_IPv6UntrustedIP(t *testing.T) {
	s, _ := NewProxyCertSanitizer("::1/128")

	var gotHeader string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Get(clientCertHeader)
	})

	req := httptest.NewRequest("GET", "/", http.NoBody)
	req.RemoteAddr = "[2001:db8::1]:8080"
	req.Header.Set(clientCertHeader, "FAKE-CERT")

	rr := httptest.NewRecorder()
	s.Handler(inner).ServeHTTP(rr, req)

	if gotHeader != "" {
		t.Errorf("expected header stripped for untrusted IPv6, got %q", gotHeader)
	}
}

func TestIsTrusted_RemoteAddrWithoutPort(t *testing.T) {
	s, _ := NewProxyCertSanitizer("127.0.0.0/8")
	// Edge case: RemoteAddr without port
	if !s.isTrusted("127.0.0.1") {
		t.Error("expected 127.0.0.1 (no port) to be trusted")
	}
}

func TestIsTrusted_UnparseableAddr(t *testing.T) {
	s, _ := NewProxyCertSanitizer("127.0.0.0/8")
	if s.isTrusted("not-an-ip") {
		t.Error("expected unparseable address to be untrusted")
	}
}
