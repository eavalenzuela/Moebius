package main

import (
	"fmt"
	"os"

	"github.com/eavalenzuela/Moebius/shared/version"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "version" {
		fmt.Println("moebius-worker", version.FullVersion())
		return
	}

	fmt.Println("moebius-worker", version.FullVersion())
	fmt.Println("TODO: start worker")
}
