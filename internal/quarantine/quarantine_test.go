//go:build linux

package quarantine

import (
	"errors"
	"sync"
	"testing"

	"github.com/biendo27/linux-deep-clean/internal/domain"
	"github.com/biendo27/linux-deep-clean/internal/linuxfs"
	"github.com/biendo27/linux-deep-clean/internal/mounts"
)

func TestOpenPerMountQuarantineRejectsNilLayout(t *testing.T) {
	store, err := OpenPerMountQuarantine(nil)
	if !errors.Is(err, linuxfs.ErrUnsupported) {
		t.Fatalf("OpenPerMountQuarantine(nil) error = %v, want linuxfs.ErrUnsupported", err)
	}
	if store != nil {
		t.Fatal("OpenPerMountQuarantine(nil) returned a store")
	}
}

func TestOpenPerMountQuarantineWithAcceptsOnlyQualifiedPrivateQuarantine(t *testing.T) {
	rootID := quarantineTestRootID(t)
	directory := &testPrivateDirectory{rootID: rootID, kind: mounts.LayoutPrivateQuarantine}
	calls := 0
	store, err := openPerMountQuarantineWith(mounts.LayoutPrivateQuarantine, func() (privateDirectory, error) {
		calls++
		return directory, nil
	})
	if err != nil {
		t.Fatalf("openPerMountQuarantineWith() error = %v", err)
	}
	if calls != 1 {
		t.Fatalf("private directory opener calls = %d, want 1", calls)
	}
	if store.RootID() != rootID {
		t.Fatalf("store.RootID() = %q, want %q", store.RootID(), rootID)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Store.Close() error = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("second Store.Close() error = %v", err)
	}
	if directory.closeCalls != 1 {
		t.Fatalf("private directory close calls = %d, want 1", directory.closeCalls)
	}
}

func TestOpenPerMountQuarantineWithFailsClosedAndClosesRejectedAuthority(t *testing.T) {
	rootID := quarantineTestRootID(t)
	for _, test := range []struct {
		name      string
		kind      mounts.LayoutKind
		opener    func(*testPrivateDirectory) func() (privateDirectory, error)
		want      error
		wantCalls int
		wantClose int
	}{
		{
			name: "wrong source kind never opens directory",
			kind: mounts.LayoutPrivateStaging,
			opener: func(directory *testPrivateDirectory) func() (privateDirectory, error) {
				return func() (privateDirectory, error) { return directory, nil }
			},
			want:      linuxfs.ErrUnsupported,
			wantCalls: 0,
			wantClose: 0,
		},
		{
			name: "missing private directory",
			kind: mounts.LayoutPrivateQuarantine,
			opener: func(*testPrivateDirectory) func() (privateDirectory, error) {
				return func() (privateDirectory, error) { return nil, nil }
			},
			want:      linuxfs.ErrUnsupported,
			wantCalls: 1,
			wantClose: 0,
		},
		{
			name: "linuxfs drift closes partial directory",
			kind: mounts.LayoutPrivateQuarantine,
			opener: func(directory *testPrivateDirectory) func() (privateDirectory, error) {
				return func() (privateDirectory, error) { return directory, linuxfs.ErrDrifted }
			},
			want:      linuxfs.ErrDrifted,
			wantCalls: 1,
			wantClose: 1,
		},
		{
			name: "private directory kind drift",
			kind: mounts.LayoutPrivateQuarantine,
			opener: func(directory *testPrivateDirectory) func() (privateDirectory, error) {
				directory.kind = mounts.LayoutPrivateStaging
				return func() (privateDirectory, error) { return directory, nil }
			},
			want:      linuxfs.ErrDrifted,
			wantCalls: 1,
			wantClose: 1,
		},
		{
			name: "invalid root identity",
			kind: mounts.LayoutPrivateQuarantine,
			opener: func(directory *testPrivateDirectory) func() (privateDirectory, error) {
				directory.rootID = ""
				return func() (privateDirectory, error) { return directory, nil }
			},
			want:      linuxfs.ErrUnsupported,
			wantCalls: 1,
			wantClose: 1,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			directory := &testPrivateDirectory{rootID: rootID, kind: mounts.LayoutPrivateQuarantine}
			calls := 0
			opener := test.opener(directory)
			store, err := openPerMountQuarantineWith(test.kind, func() (privateDirectory, error) {
				calls++
				return opener()
			})
			if !errors.Is(err, test.want) {
				t.Fatalf("openPerMountQuarantineWith() error = %v, want %v", err, test.want)
			}
			if store != nil {
				t.Fatal("openPerMountQuarantineWith() returned a store after rejected authority")
			}
			if calls != test.wantCalls {
				t.Fatalf("private directory opener calls = %d, want %d", calls, test.wantCalls)
			}
			if directory.closeCalls != test.wantClose {
				t.Fatalf("private directory close calls = %d, want %d", directory.closeCalls, test.wantClose)
			}
		})
	}
}

func TestOpenPerMountQuarantineWithRejectsMissingOpener(t *testing.T) {
	store, err := openPerMountQuarantineWith(mounts.LayoutPrivateQuarantine, nil)
	if !errors.Is(err, linuxfs.ErrUnsupported) {
		t.Fatalf("openPerMountQuarantineWith(nil opener) error = %v, want linuxfs.ErrUnsupported", err)
	}
	if store != nil {
		t.Fatal("openPerMountQuarantineWith(nil opener) returned a store")
	}
}

func TestStoreCloseIsConcurrentAndIdempotent(t *testing.T) {
	directory := &testPrivateDirectory{
		rootID: quarantineTestRootID(t),
		kind:   mounts.LayoutPrivateQuarantine,
	}
	store, err := openPerMountQuarantineWith(mounts.LayoutPrivateQuarantine, func() (privateDirectory, error) {
		return directory, nil
	})
	if err != nil {
		t.Fatalf("openPerMountQuarantineWith() error = %v", err)
	}

	const callers = 32
	var group sync.WaitGroup
	closeErrors := make(chan error, callers)
	for range callers {
		group.Add(1)
		go func() {
			defer group.Done()
			closeErrors <- store.Close()
		}()
	}
	group.Wait()
	close(closeErrors)
	for err := range closeErrors {
		if err != nil {
			t.Fatalf("Store.Close() error = %v", err)
		}
	}
	if directory.closeCalls != 1 {
		t.Fatalf("private directory close calls = %d, want 1", directory.closeCalls)
	}
}

func TestStoreNilValuePreservesMetadataOnlyBoundary(t *testing.T) {
	var store *Store
	if store.RootID() != "" {
		t.Fatalf("nil Store.RootID() = %q, want empty", store.RootID())
	}
	if err := store.Close(); err != nil {
		t.Fatalf("nil Store.Close() error = %v", err)
	}
}

type testPrivateDirectory struct {
	rootID     domain.TrustedRootID
	kind       mounts.LayoutKind
	closeCalls int
}

func (directory *testPrivateDirectory) RootID() domain.TrustedRootID {
	if directory == nil {
		return ""
	}
	return directory.rootID
}

func (directory *testPrivateDirectory) Kind() mounts.LayoutKind {
	if directory == nil {
		return ""
	}
	return directory.kind
}

func (directory *testPrivateDirectory) Close() error {
	if directory != nil {
		directory.closeCalls++
	}
	return nil
}

func quarantineTestRootID(t *testing.T) domain.TrustedRootID {
	t.Helper()
	rootID, err := domain.NewTrustedRootID("quarantine-test-root")
	if err != nil {
		t.Fatalf("NewTrustedRootID() error = %v", err)
	}
	return rootID
}
