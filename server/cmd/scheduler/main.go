package main

import (
	"fmt"
	"os"

	"github.com/moebius-oss/moebius/shared/version"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "version" {
		fmt.Println("moebius-scheduler", version.FullVersion())
		return
	}

	fmt.Println("moebius-scheduler", version.FullVersion())
	fmt.Println("TODO: start scheduler")
}
