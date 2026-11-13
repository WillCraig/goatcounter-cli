package main

import (
	"os"

	"github.com/WillCraig/goatcounter-cli/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], os.Environ(), os.Stdout, os.Stderr))
}
