package main

import (
	"bytes"
	"testing"
)

const helperRejection = "linux-deep-clean-helper: requests are not accepted in this build\n"

func TestRunRejectsEveryRequest(t *testing.T) {
	var stderr bytes.Buffer

	if got := run(&stderr); got != 1 {
		t.Fatalf("run() exit code = %d, want 1", got)
	}
	if got := stderr.String(); got != helperRejection {
		t.Fatalf("run() stderr = %q, want %q", got, helperRejection)
	}
}
