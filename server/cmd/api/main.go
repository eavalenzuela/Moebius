package main

import (
	"fmt"
	"os"

	"github.com/moebius-oss/moebius/shared/version"
)

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "version":
			fmt.Println("moebius-api", version.FullVersion())
			return
		case "migrate":
			fmt.Println("TODO: run database migrations")
			return
		case "generate-ca":
			fmt.Println("TODO: generate internal CA")
			return
		case "create-admin":
			fmt.Println("TODO: create initial admin user")
			return
		}
	}

	fmt.Println("moebius-api", version.FullVersion())
	fmt.Println("TODO: start API server")
}
