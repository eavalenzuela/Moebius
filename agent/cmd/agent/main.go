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
	"syscall"

	agentconfig "github.com/moebius-oss/moebius/agent/config"
	"github.com/moebius-oss/moebius/agent/enrollment"
	"github.com/moebius-oss/moebius/agent/executor"
	"github.com/moebius-oss/moebius/agent/platform"
	linuxplatform "github.com/moebius-oss/moebius/agent/platform/linux"
	windowsplatform "github.com/moebius-oss/moebius/agent/platform/windows"
	"github.com/moebius-oss/moebius/agent/poller"
	"github.com/moebius-oss/moebius/agent/renewal"
	"github.com/moebius-oss/moebius/agent/tlsutil"
	"github.com/moebius-oss/moebius/shared/version"
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
		fmt.Println("TODO: show agent status")
	case "cdm":
		fmt.Println("TODO: CDM management")
	case "install":
		fmt.Println("TODO: agent install")
	case "uninstall":
		fmt.Println("TODO: agent uninstall")
	case "verify":
		fmt.Println("TODO: signature verification")
	case "logs":
		fmt.Println("TODO: show agent logs")
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", os.Args[1])
		usage()
		os.Exit(1)
	}
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

	// Start poller with executor
	exec := executor.New(cfg.Server.URL, client, log)
	p := poller.New(poller.Config{
		ServerURL:    cfg.Server.URL,
		AgentID:      agentID,
		PollInterval: cfg.Server.PollIntervalSeconds,
		Client:       client,
		Log:          log,
		JobHandler:   exec.HandleJob,
	})

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

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
