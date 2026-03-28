package main

import (
	"fmt"
	"os"

	"github.com/moebius-oss/moebius/shared/version"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "run":
		fmt.Println("TODO: start agent daemon")
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

func usage() {
	fmt.Fprintln(os.Stderr, `Usage: agent <command>

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
