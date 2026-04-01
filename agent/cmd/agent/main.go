package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"syscall"
	"time"

	"github.com/eavalenzuela/Moebius/agent/cdm"
	agentconfig "github.com/eavalenzuela/Moebius/agent/config"
	"github.com/eavalenzuela/Moebius/agent/enrollment"
	"github.com/eavalenzuela/Moebius/agent/executor"
	"github.com/eavalenzuela/Moebius/agent/installer"
	"github.com/eavalenzuela/Moebius/agent/inventory"
	"github.com/eavalenzuela/Moebius/agent/ipc"
	"github.com/eavalenzuela/Moebius/agent/localaudit"
	"github.com/eavalenzuela/Moebius/agent/localauth"
	"github.com/eavalenzuela/Moebius/agent/localcli"
	"github.com/eavalenzuela/Moebius/agent/localui"
	"github.com/eavalenzuela/Moebius/agent/logshipper"
	"github.com/eavalenzuela/Moebius/agent/platform"
	linuxplatform "github.com/eavalenzuela/Moebius/agent/platform/linux"
	windowsplatform "github.com/eavalenzuela/Moebius/agent/platform/windows"
	"github.com/eavalenzuela/Moebius/agent/poller"
	"github.com/eavalenzuela/Moebius/agent/renewal"
	"github.com/eavalenzuela/Moebius/agent/tlsutil"
	"github.com/eavalenzuela/Moebius/agent/update"
	"github.com/eavalenzuela/Moebius/shared/version"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "run":
		if err := runDaemon(); err != nil {
			fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
			os.Exit(1)
		}
	case "version":
		fmt.Println("moebius-agent", version.FullVersion())
	case "status":
		runCLI(func(cli *localcli.CLI) error {
			return cli.RunStatus()
		})
	case "cdm":
		runCDMCommand()
	case "logs":
		runCLI(func(cli *localcli.CLI) error {
			tail := 50
			args := os.Args[2:]
			for i := 0; i < len(args)-1; i++ {
				if args[i] == "--tail" {
					if n, err := strconv.Atoi(args[i+1]); err == nil {
						tail = n
					}
				}
			}
			return cli.RunLogs(tail)
		})
	case "install":
		runInstall()
	case "uninstall":
		purge := false
		for _, arg := range os.Args[2:] {
			if arg == "--purge" {
				purge = true
			}
		}
		plat := detectPlatform()
		if err := installer.Uninstall(plat, purge); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
	case "verify":
		fmt.Println("TODO: signature verification")
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", os.Args[1])
		usage()
		os.Exit(1)
	}
}

// runCDMCommand handles "agent cdm <subcommand>".
func runCDMCommand() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, `Usage: moebius-agent cdm <subcommand>

Subcommands:
  status    Show CDM state
  enable    Enable CDM
  disable   Disable CDM
  grant     Grant CDM session (--duration <duration>)
  revoke    Revoke CDM session`)
		os.Exit(1)
	}

	sub := os.Args[2]
	switch sub {
	case "status":
		runCLI(func(cli *localcli.CLI) error {
			return cli.RunCDMStatus()
		})
	case "enable":
		runCLI(func(cli *localcli.CLI) error {
			return cli.RunCDMEnable(cliUsername())
		})
	case "disable":
		runCLI(func(cli *localcli.CLI) error {
			return cli.RunCDMDisable(cliUsername())
		})
	case "grant":
		duration := "10m"
		args := os.Args[3:]
		for i := 0; i < len(args)-1; i++ {
			if args[i] == "--duration" {
				duration = args[i+1]
			}
		}
		runCLI(func(cli *localcli.CLI) error {
			return cli.RunCDMGrant(cliUsername(), duration)
		})
	case "revoke":
		runCLI(func(cli *localcli.CLI) error {
			return cli.RunCDMRevoke(cliUsername())
		})
	default:
		fmt.Fprintf(os.Stderr, "unknown cdm subcommand: %s\n", sub)
		os.Exit(1)
	}
}

// runCLI connects to the agent daemon, authenticates, and runs fn.
func runCLI(fn func(*localcli.CLI) error) {
	plat := detectPlatform()
	cli := localcli.New(plat.SocketPath())
	defer cli.Close()

	// Authenticate with the daemon.
	username := os.Getenv("MOEBIUS_USERNAME")
	password := os.Getenv("MOEBIUS_PASSWORD")
	if err := cli.Login(username, password); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	if err := fn(cli); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

// cliUsername returns the OS username from env or current user.
func cliUsername() string {
	if u := os.Getenv("MOEBIUS_USERNAME"); u != "" {
		return u
	}
	if u := os.Getenv("USER"); u != "" {
		return u
	}
	return "unknown"
}

func runDaemon() error {
	plat := detectPlatform()
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	// Load config
	cfg, err := agentconfig.Load(plat.ConfigPath())
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	log.Info("config loaded", slog.String("server_url", cfg.Server.URL))

	// Check if we need to enroll
	agentID, enrolled := tryReadAgentID(plat.AgentIDPath())
	if !enrolled {
		log.Info("agent not enrolled, starting enrollment")
		// For enrollment, use server CA if available, otherwise skip verification
		enrollClient := &http.Client{}
		caPool, caErr := tlsutil.LoadCAPool(plat.CACertPath())
		if caErr == nil {
			enrollClient.Transport = &http.Transport{
				TLSClientConfig: &tls.Config{
					RootCAs:    caPool,
					MinVersion: tls.VersionTLS12,
				},
			}
		}

		result, err := enrollment.Enroll(cfg.Server.URL, plat.EnrollmentTokenPath(), enrollClient, log)
		if err != nil {
			return fmt.Errorf("enrollment: %w", err)
		}

		if err := enrollment.SaveCredentials(result,
			plat.ClientCertPath(), plat.ClientKeyPath(),
			plat.CACertPath(), plat.AgentIDPath(),
			plat.EnrollmentTokenPath(),
		); err != nil {
			return fmt.Errorf("save credentials: %w", err)
		}

		agentID = result.AgentID
		if result.PollIntervalSeconds > 0 {
			cfg.Server.PollIntervalSeconds = result.PollIntervalSeconds
		}
		log.Info("enrollment successful", slog.String("agent_id", agentID))
	}

	// Load mTLS credentials
	certProvider, err := tlsutil.NewCertProvider(plat.ClientCertPath(), plat.ClientKeyPath())
	if err != nil {
		return fmt.Errorf("load client cert: %w", err)
	}

	caPool, err := tlsutil.LoadCAPool(plat.CACertPath())
	if err != nil {
		return fmt.Errorf("load CA: %w", err)
	}

	tlsCfg := tlsutil.NewTLSConfig(certProvider, caPool)
	client := &http.Client{
		Transport: &http.Transport{TLSClientConfig: tlsCfg},
	}

	// Check certificate renewal before starting poller
	if needs, expiresAt, err := renewal.NeedsRenewal(plat.ClientCertPath()); err == nil && needs {
		log.Info("certificate needs renewal", slog.Time("expires_at", expiresAt))
		if result, err := renewal.Renew(cfg.Server.URL, client, log); err != nil {
			log.Error("certificate renewal failed", slog.String("error", err.Error()))
		} else {
			if err := renewal.SaveRenewal(result, plat.ClientCertPath(), plat.ClientKeyPath(), plat.CACertPath()); err != nil {
				log.Error("save renewed cert failed", slog.String("error", err.Error()))
			} else {
				// Hot-swap the cert
				if err := certProvider.Swap(plat.ClientCertPath(), plat.ClientKeyPath()); err != nil {
					log.Error("cert hot-swap failed", slog.String("error", err.Error()))
				} else {
					log.Info("certificate renewed successfully")
				}
			}
		}
	}

	// Initialize log shipper — tee agent logs to server
	shipper := logshipper.New(cfg.Server.URL, agentID, client)
	innerHandler := slog.NewTextHandler(os.Stderr, nil)
	log = slog.New(logshipper.NewHandler(shipper, innerHandler, slog.LevelInfo))

	// Initialize CDM
	cdmAudit := cdm.NewAuditLog(plat.CDMAuditLogPath())
	cdmMgr, err := cdm.New(plat.CDMStatePath(), cdmAudit)
	if err != nil {
		return fmt.Errorf("initialize CDM: %w", err)
	}
	log.Info("CDM initialized", slog.Bool("enabled", cdmMgr.Enabled()))

	// Check for pending update from a previous restart
	if result := update.CheckPostRestart(plat.PendingUpdatePath(), log); result != nil {
		if result.Success {
			log.Info("post-restart update verification passed",
				slog.String("job_id", result.Pending.JobID))
			// Job completion will be reported on next check-in
			_ = update.RemovePending(plat.PendingUpdatePath())
		} else if result.NeedRoll {
			log.Error("post-restart verification failed, rolling back",
				slog.String("error", result.Error))
			if err := update.Rollback(plat.BinaryPath(), plat.BinaryPreviousPath(), plat.PendingUpdatePath(), log); err != nil {
				log.Error("automatic rollback failed", slog.String("error", err.Error()))
			}
			// Continue running — the poller will report the rollback status
		}
	}

	// --- Local audit log ---
	audit := localaudit.New(plat.LocalAuditLogPath())

	// --- IPC server for local CLI ---
	auth := localauth.NewPlatformAuthenticator()
	sessions := localauth.NewSessionManager()
	router := ipc.NewRouter()

	// Register auth methods (login does not require a token).
	localauth.RegisterIPC(router, auth, sessions, audit)

	// Register CLI methods (require a valid session token).
	requireAuth := localcli.RequireAuthMiddleware(sessions)
	localcli.RegisterIPC(router, &localcli.DaemonState{
		AgentID:    agentID,
		Config:     cfg,
		CDMManager: cdmMgr,
		AuditLog:   audit,
		LogFile:    cfg.Logging.File,
	}, requireAuth)

	ipcServer := ipc.NewServer(plat.SocketPath(), router, log)

	// Start poller with executor and inventory
	inv := inventory.New(log)
	exec := executor.New(cfg.Server.URL, client, inv, cdmMgr, plat.DropDir(), log)
	exec.SetPlatform(plat)
	exec.SetPollInterval(cfg.Server.PollIntervalSeconds)
	exec.SetRestartFunc(func() error {
		return restartService(plat.ServiceName())
	})
	p := poller.New(poller.Config{
		ServerURL:     cfg.Server.URL,
		AgentID:       agentID,
		PollInterval:  cfg.Server.PollIntervalSeconds,
		Client:        client,
		Log:           log,
		JobHandler:    exec.HandleJob,
		DeltaProvider: inv.ComputeDelta,
	})

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// Start log shipper goroutine
	go shipper.Run(ctx)

	// Start IPC server
	go func() {
		if err := ipcServer.Serve(ctx); err != nil {
			log.Error("IPC server error", slog.String("error", err.Error()))
		}
	}()

	// Start local web UI (if enabled)
	if cfg.LocalUI.Enabled {
		uiServer := localui.NewServer(
			localui.ServerConfig{
				Port:    cfg.LocalUI.Port,
				DataDir: plat.DataDir(),
				LogDir:  plat.LogDir(),
			},
			auth,
			sessions,
			cdmMgr,
			audit,
			log,
			agentID,
			cfg.Server.URL,
		)
		go func() {
			if err := uiServer.Serve(ctx); err != nil {
				log.Error("local web UI error", slog.String("error", err.Error()))
			}
		}()
	}

	// Periodic session cleanup
	go func() {
		ticker := time.NewTicker(1 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				sessions.Cleanup()
			}
		}
	}()

	// Feed CDM state to poller for check-in reporting
	go func() {
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				snap := cdmMgr.Snapshot()
				p.SetCDMState(snap.Enabled, snap.SessionActive, snap.SessionExpiresAt)
			}
		}
	}()

	log.Info("agent started",
		slog.String("agent_id", agentID),
		slog.String("version", version.Version),
		slog.Int("poll_interval", cfg.Server.PollIntervalSeconds),
	)

	return p.Run(ctx)
}

func detectPlatform() platform.Platform {
	switch runtime.GOOS {
	case "windows":
		return &windowsplatform.Platform{}
	default:
		return &linuxplatform.Platform{}
	}
}

func tryReadAgentID(path string) (string, bool) {
	id, err := poller.ReadAgentID(path)
	if err != nil {
		return "", false
	}
	return id, true
}

func runInstall() {
	var enrollmentToken, serverURL, caCertPath string
	var cdmEnabled bool

	args := os.Args[2:]
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--enrollment-token":
			if i+1 < len(args) {
				enrollmentToken = args[i+1]
				i++
			}
		case "--server-url":
			if i+1 < len(args) {
				serverURL = args[i+1]
				i++
			}
		case "--ca-cert":
			if i+1 < len(args) {
				caCertPath = args[i+1]
				i++
			}
		case "--cdm-enabled":
			cdmEnabled = true
		default:
			fmt.Fprintf(os.Stderr, "unknown install option: %s\n", args[i])
			fmt.Fprintln(os.Stderr, `Usage: moebius-agent install [options]

Options:
  --enrollment-token <token>   Enrollment token (required for new install)
  --server-url <url>           Server URL (required for new install)
  --ca-cert <path>             Path to CA certificate file
  --cdm-enabled                Enable CDM at install time`)
			os.Exit(1)
		}
	}

	plat := detectPlatform()
	if err := installer.Install(plat, enrollmentToken, serverURL, caCertPath, cdmEnabled); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `Usage: moebius-agent <command>

Commands:
  run          Start agent daemon (called by service manager)
  version      Show agent version
  status       Show agent status and config
  cdm          CDM management (status, enable, disable, grant, revoke)
  install      Install agent on this device
  uninstall    Uninstall agent from this device
  verify       Verify file signature
  logs         View agent logs`)
}
