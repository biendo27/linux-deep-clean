//go:build vmtest && !vmguardunit

package vmtest

import (
	"fmt"
	"os"
	"testing"
)

const vmTestBodyMarker = "vmtest guarded test body reached"

var vmTestGuardVerified bool

// TestMain is compiled into every normal vmtest run. The vmguardunit tag is
// reserved for dependency-injection tests; normal VM test bodies must exclude
// it so that no test body can bypass this process-wide gate.
func TestMain(m *testing.M) {
	if err := requireDisposableGuest(); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "refusing to run vmtest outside a verified disposable guest: %v\n", err)
		os.Exit(1)
	}

	vmTestGuardVerified = true
	os.Exit(m.Run())
}

func TestVMTestRequiresBothGuards(t *testing.T) {
	if !vmTestGuardVerified {
		t.Fatal("vmtest guard did not run before the test body")
	}
	t.Log(vmTestBodyMarker)
}
