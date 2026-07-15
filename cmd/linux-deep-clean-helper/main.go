package main

import (
	"fmt"
	"io"
	"os"
)

func main() {
	os.Exit(run(os.Stderr))
}

func run(stderr io.Writer) int {
	_, _ = fmt.Fprint(stderr, "linux-deep-clean-helper: requests are not accepted in this build\n")
	return 1
}
