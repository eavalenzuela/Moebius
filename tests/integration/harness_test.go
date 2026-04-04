//go:build integration

package integration

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/eavalenzuela/Moebius/server/api"
	"github.com/eavalenzuela/Moebius/server/audit"
	"github.com/eavalenzuela/Moebius/server/auth"
	"github.com/eavalenzuela/Moebius/server/health"
	"github.com/eavalenzuela/Moebius/server/migrate"
	"github.com/eavalenzuela/Moebius/server/pki"
	"github.com/eavalenzuela/Moebius/server/rbac"
	"github.com/eavalenzuela/Moebius/server/storage"
	"github.com/eavalenzuela/Moebius/server/store"
	"github.com/eavalenzuela/Moebius/shared/models"
	"github.com/jackc/pgx/v5/pgxpool"
)

// testHarness holds shared state for integration tests.
type testHarness struct {
	t      *testing.T
	pool   *pgxpool.Pool
	store  *store.Store
	ca     *pki.CA
	server *httptest.Server
	apiURL string
	log    *slog.Logger

	// Bootstrap data
	tenantID string
	adminKey string // raw API key (sk_...)
	userID   string
	roleID   string
}

// newHarness creates a fully-wired test environment:
// connects to test database, runs migrations, generates CA, starts API server.
func newHarness(t *testing.T) *testHarness {
	t.Helper()

	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	// Connect to test database (from env vars set by CI service containers)
	dbURL := os.Getenv("MOEBIUS_TEST_DATABASE_URL")
	if dbURL == "" {
		dbURL = fmt.Sprintf(
			"postgres://%s:%s@%s:%s/%s?sslmode=%s",
			envOr("MOEBIUS_DB_USER", "moebius"),
			envOr("MOEBIUS_DB_PASSWORD", "moebius"),
			envOr("MOEBIUS_DB_HOST", "localhost"),
			envOr("MOEBIUS_DB_PORT", "5432"),
			envOr("MOEBIUS_DB_NAME", "moebius_test"),
			envOr("MOEBIUS_DB_SSLMODE", "disable"),
		)
	}

	st, err := store.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("connect to database: %v", err)
	}
	t.Cleanup(st.Close)
	pool := st.Pool()

	// Clean and recreate schema for test isolation
	cleanDatabase(t, pool)

	// Run migrations
	if err := migrate.Run(ctx, pool, log); err != nil {
		t.Fatalf("run migrations: %v", err)
	}

	// Generate test CA
	certPEM, keyPEM, err := pki.GenerateCA("Test Intermediate CA", false, nil)
	if err != nil {
		t.Fatalf("generate CA: %v", err)
	}
	ca, err := pki.ParseCA(certPEM, keyPEM)
	if err != nil {
		t.Fatalf("parse CA: %v", err)
	}

	// Bootstrap tenant, role, user, and admin API key
	tenantID := models.NewTenantID()
	roleID := models.NewRoleID()
	userID := models.NewUserID()
	now := time.Now().UTC()

	_, err = pool.Exec(ctx,
		`INSERT INTO tenants (id, name, slug, created_at) VALUES ($1, $2, $3, $4)`,
		tenantID, "Test Tenant", "test", now)
	if err != nil {
		t.Fatalf("create tenant: %v", err)
	}

	permJSON, _ := json.Marshal(rbac.SuperAdminPermissions)
	_, err = pool.Exec(ctx,
		`INSERT INTO roles (id, tenant_id, name, permissions, is_custom) VALUES ($1, $2, $3, $4, $5)`,
		roleID, tenantID, "Super Admin", permJSON, false)
	if err != nil {
		t.Fatalf("create role: %v", err)
	}

	_, err = pool.Exec(ctx,
		`INSERT INTO users (id, tenant_id, email, role_id, created_at) VALUES ($1, $2, $3, $4, $5)`,
		userID, tenantID, "admin@test.local", roleID, now)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	// Generate admin API key
	rawKey := "sk_" + hex.EncodeToString(mustRandBytes(t, 24))
	keyHash := hashKey(rawKey)
	keyID := models.NewAPIKeyID()
	_, err = pool.Exec(ctx,
		`INSERT INTO api_keys (id, tenant_id, user_id, name, key_hash, role_id, is_admin, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		keyID, tenantID, userID, "admin-key", keyHash, roleID, true, now)
	if err != nil {
		t.Fatalf("create api key: %v", err)
	}

	// Storage backend
	storageDir := t.TempDir()
	storageBe, err := storage.NewLocalBackend(storageDir)
	if err != nil {
		t.Fatalf("create storage backend: %v", err)
	}

	// Enrollment service
	enrollment := auth.NewEnrollmentService(pool)

	// Build router and start test server
	healthH := health.New(map[string]health.Checker{"database": st})
	auditLog := audit.New(pool, log)

	router := api.NewRouter(api.RouterConfig{
		Pool:       pool,
		Store:      st,
		CA:         ca,
		Audit:      auditLog,
		Log:        log,
		Health:     healthH,
		Enrollment: enrollment,
		Storage:    storageBe,
	})

	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	return &testHarness{
		t:        t,
		pool:     pool,
		store:    st,
		ca:       ca,
		server:   srv,
		apiURL:   srv.URL,
		log:      log,
		tenantID: tenantID,
		adminKey: rawKey,
		userID:   userID,
		roleID:   roleID,
	}
}

// cleanDatabase drops all tables for test isolation.
func cleanDatabase(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()

	// Drop all tables in public schema
	_, err := pool.Exec(ctx, `
		DO $$ DECLARE
			r RECORD;
		BEGIN
			FOR r IN (SELECT tablename FROM pg_tables WHERE schemaname = 'public') LOOP
				EXECUTE 'DROP TABLE IF EXISTS public.' || quote_ident(r.tablename) || ' CASCADE';
			END LOOP;
		END $$;
	`)
	if err != nil {
		t.Fatalf("clean database: %v", err)
	}
}

// ─── API request helpers ───────────────────────────────

// apiRequest makes an authenticated API request with the admin key.
func (h *testHarness) apiRequest(method, path string, body any) *http.Response {
	return h.apiRequestWithKey(h.adminKey, method, path, body)
}

// apiRequestWithKey makes an API request with a specific key.
func (h *testHarness) apiRequestWithKey(key, method, path string, body any) *http.Response {
	h.t.Helper()
	var bodyReader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			h.t.Fatalf("marshal request body: %v", err)
		}
		bodyReader = bytes.NewReader(b)
	}

	req, err := http.NewRequest(method, h.apiURL+path, bodyReader)
	if err != nil {
		h.t.Fatalf("create request: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", "Bearer "+key)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		h.t.Fatalf("send request: %v", err)
	}
	return resp
}

// agentCheckin simulates an agent check-in by injecting mTLS identity into context.
// Since httptest doesn't do mTLS, we insert agent identity directly via a wrapper.
func (h *testHarness) agentCheckin(agentID string, body any) *http.Response {
	h.t.Helper()
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", h.apiURL+"/v1/agents/checkin", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")

	// For agent endpoints, we need mTLS context. Since we're using httptest without TLS,
	// we'll make agent requests through a small wrapper that injects the context.
	// Instead, let's call directly with a TLS connection using our test CA.
	h.t.Fatalf("use agentCheckinDirect for mTLS-authenticated requests")
	return nil
}

// createAgentCert generates a client certificate for the given agentID signed by the test CA.
func (h *testHarness) createAgentCert(agentID string) (certPEM, keyPEM []byte, serialHex string) {
	h.t.Helper()

	// Generate agent key
	agentKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		h.t.Fatalf("generate agent key: %v", err)
	}

	// Create CSR
	csrTemplate := &x509.CertificateRequest{
		Subject: pkix.Name{CommonName: agentID},
	}
	csrDER, err := x509.CreateCertificateRequest(rand.Reader, csrTemplate, agentKey)
	if err != nil {
		h.t.Fatalf("create CSR: %v", err)
	}
	csrPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDER})

	// Sign with CA
	certPEMSigned, serialHexVal, _, err := h.ca.SignCSR(csrPEM, agentID, 24*time.Hour)
	if err != nil {
		h.t.Fatalf("sign agent CSR: %v", err)
	}

	// Encode agent private key
	keyDER, _ := x509.MarshalECPrivateKey(agentKey)
	agentKeyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	return certPEMSigned, agentKeyPEM, serialHexVal
}

// startMTLSServer restarts the test server with mTLS enabled and returns the new URL.
// We need this for agent endpoints that check r.TLS.PeerCertificates.
func (h *testHarness) startMTLSServer() string {
	h.t.Helper()

	// Close the plain HTTP server
	h.server.Close()

	// Create a new TLS server with client cert verification
	router := h.rebuildRouter()
	srv := httptest.NewUnstartedServer(router)

	// Build CA cert pool
	caPool := x509.NewCertPool()
	caPool.AddCert(h.ca.Cert)

	srv.TLS = &tls.Config{
		ClientAuth: tls.RequireAnyClientCert,
		ClientCAs:  caPool,
		MinVersion: tls.VersionTLS12,
	}
	srv.StartTLS()
	h.t.Cleanup(srv.Close)
	h.server = srv
	h.apiURL = srv.URL
	return srv.URL
}

// mtlsClient creates an HTTP client authenticated with the given agent certificate.
func (h *testHarness) mtlsClient(certPEM, keyPEM []byte) *http.Client {
	h.t.Helper()
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		h.t.Fatalf("parse agent cert/key: %v", err)
	}

	caPool := x509.NewCertPool()
	caPool.AddCert(h.ca.Cert)

	return &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				Certificates:       []tls.Certificate{cert},
				RootCAs:            caPool,
				InsecureSkipVerify: true, //nolint:gosec // test only
			},
		},
	}
}

// agentRequest makes an mTLS-authenticated request for an agent.
func (h *testHarness) agentRequest(client *http.Client, method, path string, body any) *http.Response {
	h.t.Helper()
	var bodyReader io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		bodyReader = bytes.NewReader(b)
	}

	req, err := http.NewRequest(method, h.apiURL+path, bodyReader)
	if err != nil {
		h.t.Fatalf("create request: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := client.Do(req)
	if err != nil {
		h.t.Fatalf("send request: %v", err)
	}
	return resp
}

// rebuildRouter creates a fresh router (used when restarting server with TLS).
func (h *testHarness) rebuildRouter() http.Handler {
	healthH := health.New(map[string]health.Checker{"database": h.store})
	auditLog := audit.New(h.pool, h.log)
	enrollment := auth.NewEnrollmentService(h.pool)
	storageDir := h.t.TempDir()
	storageBe, _ := storage.NewLocalBackend(storageDir)

	return api.NewRouter(api.RouterConfig{
		Pool:       h.pool,
		Store:      h.store,
		CA:         h.ca,
		Audit:      auditLog,
		Log:        h.log,
		Health:     healthH,
		Enrollment: enrollment,
		Storage:    storageBe,
	})
}

// createEnrollmentToken creates an enrollment token and returns the raw token.
func (h *testHarness) createEnrollmentToken() string {
	h.t.Helper()
	enrollment := auth.NewEnrollmentService(h.pool)
	result, err := enrollment.CreateToken(context.Background(), h.tenantID, h.userID, nil, 24*time.Hour)
	if err != nil {
		h.t.Fatalf("create enrollment token: %v", err)
	}
	return result.Raw
}

// enrollAgent performs the full enrollment flow and returns agentID, cert, key.
func (h *testHarness) enrollAgent(hostname string) (agentID string, certPEM, keyPEM []byte) {
	h.t.Helper()

	token := h.createEnrollmentToken()

	// Generate agent key + CSR
	agentKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		h.t.Fatalf("generate agent key: %v", err)
	}
	csrTemplate := &x509.CertificateRequest{
		Subject: pkix.Name{CommonName: hostname},
	}
	csrDER, err := x509.CreateCertificateRequest(rand.Reader, csrTemplate, agentKey)
	if err != nil {
		h.t.Fatalf("create CSR: %v", err)
	}
	csrPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDER})

	// POST /v1/agents/enroll (unauthenticated)
	body := map[string]string{
		"enrollment_token": token,
		"csr":              string(csrPEM),
		"hostname":         hostname,
		"os":               "linux",
		"os_version":       "Ubuntu 24.04",
		"arch":             "amd64",
		"agent_version":    "1.0.0",
	}
	b, _ := json.Marshal(body)
	resp, err := http.Post(h.apiURL+"/v1/agents/enroll", "application/json", bytes.NewReader(b))
	if err != nil {
		h.t.Fatalf("enroll request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		h.t.Fatalf("enroll failed: status=%d body=%s", resp.StatusCode, string(respBody))
	}

	var enrollResp struct {
		AgentID     string `json:"agent_id"`
		Certificate string `json:"certificate"`
		CAChain     string `json:"ca_chain"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&enrollResp); err != nil {
		h.t.Fatalf("decode enroll response: %v", err)
	}

	keyDER, _ := x509.MarshalECPrivateKey(agentKey)
	agentKeyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	return enrollResp.AgentID, []byte(enrollResp.Certificate), agentKeyPEM
}

// createAPIKeyWithPerms creates an API key for a role with specific permissions.
func (h *testHarness) createAPIKeyWithPerms(name string, perms []string, isAdmin bool) string {
	h.t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()

	roleID := models.NewRoleID()
	permJSON, _ := json.Marshal(perms)
	_, err := h.pool.Exec(ctx,
		`INSERT INTO roles (id, tenant_id, name, permissions, is_custom) VALUES ($1, $2, $3, $4, $5)`,
		roleID, h.tenantID, name+"-role", permJSON, true)
	if err != nil {
		h.t.Fatalf("create role: %v", err)
	}

	rawKey := "sk_" + hex.EncodeToString(mustRandBytes(h.t, 24))
	keyHash := hashKey(rawKey)
	keyID := models.NewAPIKeyID()
	_, err = h.pool.Exec(ctx,
		`INSERT INTO api_keys (id, tenant_id, user_id, name, key_hash, role_id, is_admin, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		keyID, h.tenantID, h.userID, name, keyHash, roleID, isAdmin, now)
	if err != nil {
		h.t.Fatalf("create api key: %v", err)
	}

	return rawKey
}

// readJSON decodes a response body into v and closes it.
func readJSON(t *testing.T, resp *http.Response, v any) {
	t.Helper()
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		t.Fatalf("decode response: %v", err)
	}
}

// assertStatus checks the response status code.
func assertStatus(t *testing.T, resp *http.Response, expected int) {
	t.Helper()
	if resp.StatusCode != expected {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected status %d, got %d: %s", expected, resp.StatusCode, string(body))
	}
}

// ─── Utilities ─────────────────────────────────────────

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func mustRandBytes(t *testing.T, n int) []byte {
	t.Helper()
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("rand: %v", err)
	}
	return b
}

func hashKey(raw string) string {
	h := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(h[:])
}

// randomSerial generates a random certificate serial number.
func randomSerial() (*big.Int, error) {
	max := new(big.Int).Lsh(big.NewInt(1), 128)
	return rand.Int(rand.Reader, max)
}
