package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moebius-oss/moebius/server/api"
	"github.com/moebius-oss/moebius/server/audit"
	"github.com/moebius-oss/moebius/server/auth"
	"github.com/moebius-oss/moebius/server/config"
	"github.com/moebius-oss/moebius/server/health"
	"github.com/moebius-oss/moebius/server/logging"
	"github.com/moebius-oss/moebius/server/migrate"
	"github.com/moebius-oss/moebius/server/pki"
	"github.com/moebius-oss/moebius/server/rbac"
	"github.com/moebius-oss/moebius/server/store"
	"github.com/moebius-oss/moebius/shared/models"
	"github.com/moebius-oss/moebius/shared/version"
)

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "version":
			fmt.Println("moebius-api", version.FullVersion())
			return
		case "migrate":
			runMigrate()
			return
		case "generate-ca":
			runGenerateCA()
			return
		case "create-admin":
			runCreateAdmin()
			return
		}
	}

	if err := runServer(); err != nil {
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		os.Exit(1)
	}
}

func runServer() error {
	cfg, err := config.Load(config.ProcessAPI)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	log := logging.New(cfg.LogFormat, cfg.LogLevel, "api")

	// Connect to PostgreSQL
	ctx := context.Background()
	st, err := store.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("connect database: %w", err)
	}
	defer st.Close()
	log.Info("database connected")

	// Load CA
	ca, err := pki.LoadCA(cfg.CACertPath, cfg.CAKeyPath)
	if err != nil {
		return fmt.Errorf("load CA: %w", err)
	}
	log.Info("CA loaded")

	// Services
	auditLog := audit.New(st.Pool(), log)
	enrollSvc := auth.NewEnrollmentService(st.Pool())
	healthHandler := health.New(map[string]health.Checker{"database": st})

	// Build router
	router := api.NewRouter(api.RouterConfig{
		Pool:       st.Pool(),
		Store:      st,
		CA:         ca,
		Audit:      auditLog,
		Log:        log,
		Health:     healthHandler,
		Enrollment: enrollSvc,
	})

	// Start HTTP server
	addr := fmt.Sprintf(":%d", cfg.HTTPPort)
	srv := &http.Server{
		Addr:              addr,
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
	}

	// Graceful shutdown
	errCh := make(chan error, 1)
	go func() {
		log.Info("API server listening", slog.String("addr", addr), slog.String("version", version.Version))
		errCh <- srv.ListenAndServe()
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-sigCh:
		log.Info("shutting down", slog.String("signal", sig.String()))
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	case err := <-errCh:
		if err != nil && err != http.ErrServerClosed {
			return err
		}
		return nil
	}
}

func runMigrate() {
	if err := doMigrate(); err != nil {
		fmt.Fprintf(os.Stderr, "migrate: %v\n", err)
		os.Exit(1)
	}
}

func doMigrate() error {
	cfg, err := config.Load(config.ProcessAPI)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	log := logging.New(cfg.LogFormat, cfg.LogLevel, "migrate")
	ctx := context.Background()

	poolCfg, err := pgxpool.ParseConfig(cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("parse database URL: %w", err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return fmt.Errorf("connect database: %w", err)
	}
	defer pool.Close()

	return migrate.Run(ctx, pool, log)
}

func runCreateAdmin() {
	if err := doCreateAdmin(); err != nil {
		fmt.Fprintf(os.Stderr, "create-admin: %v\n", err)
		os.Exit(1)
	}
}

func doCreateAdmin() error {
	cfg, err := config.Load(config.ProcessAPI)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	log := logging.New(cfg.LogFormat, cfg.LogLevel, "create-admin")
	ctx := context.Background()

	poolCfg, err := pgxpool.ParseConfig(cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("parse database URL: %w", err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return fmt.Errorf("connect database: %w", err)
	}
	defer pool.Close()

	tenantName := "Default"
	if len(os.Args) > 2 {
		tenantName = os.Args[2]
	}

	// Create tenant
	tenantID := models.NewTenantID()
	_, err = pool.Exec(ctx,
		`INSERT INTO tenants (id, name, slug) VALUES ($1, $2, $3)
		 ON CONFLICT (slug) DO UPDATE SET name = EXCLUDED.name`,
		tenantID, tenantName, "default",
	)
	if err != nil {
		return fmt.Errorf("create tenant: %w", err)
	}
	// Re-read the ID in case ON CONFLICT matched an existing row
	err = pool.QueryRow(ctx, `SELECT id FROM tenants WHERE slug = $1`, "default").Scan(&tenantID)
	if err != nil {
		return fmt.Errorf("get tenant id: %w", err)
	}

	// Create Super Admin system role
	permsJSON, _ := json.Marshal(rbac.SuperAdminPermissions)
	roleID := models.NewRoleID()
	_, err = pool.Exec(ctx,
		`INSERT INTO roles (id, name, permissions, is_custom)
		 VALUES ($1, $2, $3, FALSE)
		 ON CONFLICT DO NOTHING`,
		roleID, "Super Admin", permsJSON,
	)
	if err != nil {
		return fmt.Errorf("create admin role: %w", err)
	}
	// Re-read in case it already existed
	err = pool.QueryRow(ctx,
		`SELECT id FROM roles WHERE name = $1 AND is_custom = FALSE`, "Super Admin",
	).Scan(&roleID)
	if err != nil {
		return fmt.Errorf("get admin role id: %w", err)
	}

	// Create admin user
	userID := models.NewUserID()
	_, err = pool.Exec(ctx,
		`INSERT INTO users (id, tenant_id, email, role_id)
		 VALUES ($1, $2, $3, $4)
		 ON CONFLICT DO NOTHING`,
		userID, tenantID, "admin@localhost", roleID,
	)
	if err != nil {
		return fmt.Errorf("create admin user: %w", err)
	}
	// Re-read in case it already existed
	err = pool.QueryRow(ctx,
		`SELECT id FROM users WHERE tenant_id = $1 AND email = $2`,
		tenantID, "admin@localhost",
	).Scan(&userID)
	if err != nil {
		return fmt.Errorf("get admin user id: %w", err)
	}

	// Generate API key
	rawKey := generateBootstrapKey()
	hash := sha256.Sum256([]byte(rawKey))
	keyHash := hex.EncodeToString(hash[:])

	_, err = pool.Exec(ctx,
		`INSERT INTO api_keys (id, tenant_id, user_id, name, key_hash, role_id, is_admin)
		 VALUES ($1, $2, $3, $4, $5, $6, TRUE)`,
		models.NewAPIKeyID(), tenantID, userID, "bootstrap-admin", keyHash, roleID,
	)
	if err != nil {
		return fmt.Errorf("create API key: %w", err)
	}

	log.Info("admin bootstrap complete",
		slog.String("tenant_id", tenantID),
		slog.String("tenant_name", tenantName),
		slog.String("user_id", userID),
	)

	fmt.Println("\n=== Bootstrap Admin Created ===")
	fmt.Printf("Tenant:  %s (%s)\n", tenantName, tenantID)
	fmt.Printf("User:    admin@localhost (%s)\n", userID)
	fmt.Printf("API Key: %s\n", rawKey)
	fmt.Println("\nSave this API key — it cannot be recovered.")
	return nil
}

func generateBootstrapKey() string {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		fmt.Fprintf(os.Stderr, "generate key: %v\n", err)
		os.Exit(1)
	}
	return "sk_" + hex.EncodeToString(b)
}

func runGenerateCA() {
	outDir := "keys"
	if len(os.Args) > 2 {
		outDir = os.Args[2]
	}

	if err := os.MkdirAll(outDir, 0o700); err != nil { //nolint:gosec // CLI arg from operator
		fmt.Fprintf(os.Stderr, "Error creating output directory: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("Generating Root CA...")
	rootCertPEM, rootKeyPEM, err := pki.GenerateCA("Moebius Root CA", true, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error generating root CA: %v\n", err)
		os.Exit(1)
	}
	rootCA, err := pki.ParseCA(rootCertPEM, rootKeyPEM)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing root CA: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("Generating Intermediate CA...")
	intCertPEM, intKeyPEM, err := pki.GenerateCA("Moebius Intermediate CA", false, rootCA)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error generating intermediate CA: %v\n", err)
		os.Exit(1)
	}

	files := map[string][]byte{
		"root-ca.crt":         rootCertPEM,
		"root-ca.key":         rootKeyPEM,
		"intermediate-ca.crt": intCertPEM,
		"intermediate-ca.key": intKeyPEM,
	}
	for name, data := range files {
		path := filepath.Join(outDir, name)
		perm := os.FileMode(0o644)
		if filepath.Ext(name) == ".key" {
			perm = 0o600
		}
		if err := os.WriteFile(path, data, perm); err != nil { //nolint:gosec // CLI arg from operator
			fmt.Fprintf(os.Stderr, "Error writing %s: %v\n", path, err)
			os.Exit(1)
		}
		fmt.Printf("  wrote %s\n", path)
	}

	fmt.Println("Done. Set CA_CERT_PATH and CA_KEY_PATH to the intermediate CA files.")
}
