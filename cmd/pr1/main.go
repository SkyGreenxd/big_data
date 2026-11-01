package main

import (
	"os"

	"github.com/SkyGreenxd/bigdata/internal/pr1"
)

func main() {
	os.Exit(pr1.Run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
