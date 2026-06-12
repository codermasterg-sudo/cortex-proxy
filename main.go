package main

import (
	"fmt"
	"os"

	"github.com/cortex-io/cortex-proxy/cmd"
)

var version = "dev"

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "Usage: cortex-proxy <install|start> [flags]")
		os.Exit(1)
	}
	switch os.Args[1] {
	case "install":
		cmd.RunInstall(os.Args[2:])
	case "start":
		cmd.RunStart(os.Args[2:])
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", os.Args[1])
		os.Exit(1)
	}
}
