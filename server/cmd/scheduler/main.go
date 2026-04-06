package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/eavalenzuela/Moebius/server/config"
	"github.com/eavalenzuela/Moebius/server/logging"
	"github.com/eavalenzuela/Moebius/server/notify"
	"github.com/eavalenzuela/Moebius/server/scheduler"
	"github.com/eavalenzuela/Moebius/server/store"
	"github.com/eavalenzuela/Moebius/shared/version"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "version" {
		fmt.Println("moebius-scheduler", version.FullVersion())
		return
	}

	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load(config.ProcessScheduler)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	log := logging.New(cfg.LogFormat, cfg.LogLevel, "scheduler")
	log.Info("moebius-scheduler starting", "version", version.FullVersion())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Connect to PostgreSQL
	st, err := store.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("connect database: %w", err)
	}
	defer st.Close()
	log.Info("database connected")

	// Build notifier
	var smtpCfg *notify.SMTPConfig
	if cfg.SMTPHost != "" {
		smtpCfg = &notify.SMTPConfig{
			Host:     cfg.SMTPHost,
			Port:     cfg.SMTPPort,
			Username: cfg.SMTPUsername,
			Password: cfg.SMTPPassword,
			From:     cfg.SMTPFrom,
		}
	}
	notifier := notify.New(smtpCfg, log)

	// Create and run scheduler
	sched := scheduler.New(st.Pool(), st, notifier, log, scheduler.Config{
		TickSeconds:                cfg.SchedulerTickSeconds,
		ReaperDispatchedTimeoutSec: cfg.ReaperDispatchedTimeoutSec,
		ReaperInflightTimeoutSec:   cfg.ReaperInflightTimeoutSec,
	})

	// Handle signals
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	errCh := make(chan error, 1)
	go func() {
		errCh <- sched.Run(ctx)
	}()

	select {
	case sig := <-sigCh:
		log.Info("received signal, shutting down", "signal", sig.String())
		cancel()
		return <-errCh
	case err := <-errCh:
		return err
	}
}
