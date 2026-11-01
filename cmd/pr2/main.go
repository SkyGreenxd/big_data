package main

import (
	"errors"
	"flag"
	"fmt"
	"os"

	"bigdata/internal/pr2"
)

func main() {
	if err := pr2.Run(os.Args[1:], os.Stdin, os.Stdout); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
