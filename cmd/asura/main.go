package main

import (
	"os"
)

func main() {
	code := RunCLI(os.Args[1:], os.Stdout, os.Stderr)
	os.Exit(code)
}
