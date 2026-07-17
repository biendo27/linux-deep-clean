//go:build linux

package linuxfs

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/biendo27/linux-deep-clean/internal/mounts"
	"golang.org/x/sys/unix"
)

func TestPrivateLedgerCoverageIdentityParsingAndErrorClasses(t *testing.T) {
	record := testPrivateLedgerRecordID(t, "a", "b", 3)
	if record.ActionBindingDigest() != strings.Repeat("a", privateLedgerDigestHexBytes) {
		t.Fatalf("ActionBindingDigest() = %q", record.ActionBindingDigest())
	}
	if record.Token() != "ldc-"+strings.Repeat("b", privateLedgerTokenHexBytes) {
		t.Fatalf("Token() = %q", record.Token())
	}
	if record.Ordinal() != 3 {
		t.Fatalf("Ordinal() = %d, want 3", record.Ordinal())
	}
	if parsed, err := privateLedgerRecordIDFromBasename(record.basename()); err != nil || parsed != record {
		t.Fatalf("privateLedgerRecordIDFromBasename() = %#v, %v; want %#v, nil", parsed, err, record)
	}
	for _, basename := range []string{
		"ordinary.cbor",
		"ldc-ledger-v1-not-a-record.cbor",
		"ldc-ledger-v1-" + strings.Repeat("a", privateLedgerDigestHexBytes) + "-ldc-" + strings.Repeat("b", privateLedgerTokenHexBytes) + "-x3.cbor",
		"ldc-ledger-v1-" + strings.Repeat("a", privateLedgerDigestHexBytes) + "-ldc-" + strings.Repeat("b", privateLedgerTokenHexBytes) + "-16.cbor",
	} {
		if _, err := privateLedgerRecordIDFromBasename(basename); err == nil {
			t.Fatalf("privateLedgerRecordIDFromBasename(%q) error = nil", basename)
		}
	}

	for _, test := range []struct {
		name string
		err  error
		want error
	}{
		{name: "hostile", err: unix.ELOOP, want: ErrDrifted},
		{name: "unsupported", err: unix.EOPNOTSUPP, want: ErrUnsupported},
		{name: "interrupted", err: unix.EINTR, want: ErrInterrupted},
		{name: "other", err: unix.EPERM, want: ErrDrifted},
	} {
		t.Run("lock open "+test.name, func(t *testing.T) {
			if err := classifyPrivateLedgerLockOpen(test.err); !errors.Is(err, test.want) {
				t.Fatalf("classifyPrivateLedgerLockOpen(%v) = %v, want %v", test.err, err, test.want)
			}
		})
	}
	for _, test := range []struct {
		name string
		err  error
		want error
	}{
		{name: "unsupported", err: unix.EBADF, want: ErrUnsupported},
		{name: "interrupted", err: unix.EIO, want: ErrInterrupted},
	} {
		t.Run("sync "+test.name, func(t *testing.T) {
			if err := classifyPrivateLedgerSync("coverage", test.err); !errors.Is(err, test.want) {
				t.Fatalf("classifyPrivateLedgerSync(%v) = %v, want %v", test.err, err, test.want)
			}
		})
	}
}

func TestPrivateLedgerCoverageSessionBoundaryFailures(t *testing.T) {
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := WithPrivateLedgerWriteSession(cancelled, nil, nil); !errors.Is(err, ErrInterrupted) {
		t.Fatalf("cancelled write session error = %v, want ErrInterrupted", err)
	}
	if err := WithPrivateLedgerReadSession(context.Background(), nil, func(*PrivateLedgerSession) error { return nil }); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("nil directory read session error = %v, want ErrUnsupported", err)
	}

	rootFD, _, directory, rootID := stageTestDirectories(t)
	defer unix.Close(rootFD)
	defer directory.Close()
	lease := testPrivateDirectoryLease(t, directory, rootID, mounts.LayoutPrivateState)
	defer lease.Close()
	record := testPrivateLedgerRecordID(t, "c", "d", 0)

	var retained *PrivateLedgerSession
	if err := WithPrivateLedgerWriteSession(context.Background(), lease, func(session *PrivateLedgerSession) error {
		retained = session
		if err := session.withActive(context.Background(), nil); !errors.Is(err, ErrUnsupported) {
			t.Fatalf("withActive(nil) error = %v, want ErrUnsupported", err)
		}
		if _, err := session.List(context.Background(), 0); !errors.Is(err, ErrUnsupported) {
			t.Fatalf("List(0) error = %v, want ErrUnsupported", err)
		}
		if _, err := session.Read(context.Background(), record, 0); !errors.Is(err, ErrUnsupported) {
			t.Fatalf("Read(limit 0) error = %v, want ErrUnsupported", err)
		}
		return session.Publish(context.Background(), record, []byte("record"))
	}); err != nil {
		t.Fatalf("write session error = %v", err)
	}
	if _, err := retained.Read(context.Background(), record, 32); !errors.Is(err, ErrDrifted) {
		t.Fatalf("closed session Read() error = %v, want ErrDrifted", err)
	}

	if err := WithPrivateLedgerReadSession(context.Background(), lease, func(session *PrivateLedgerSession) error {
		if err := session.Publish(context.Background(), record, []byte("different")); !errors.Is(err, ErrUnsupported) {
			t.Fatalf("read-only Publish() error = %v, want ErrUnsupported", err)
		}
		if _, err := session.Read(context.Background(), record, 1); !errors.Is(err, ErrUnsupported) {
			t.Fatalf("bounded Read() error = %v, want ErrUnsupported", err)
		}
		return nil
	}); err != nil {
		t.Fatalf("read session error = %v", err)
	}
}

func TestPrivateLedgerCoverageLockInitializationHelpers(t *testing.T) {
	rootFD, _, directory, rootID := stageTestDirectories(t)
	defer unix.Close(rootFD)
	defer directory.Close()
	lease := testPrivateDirectoryLease(t, directory, rootID, mounts.LayoutPrivateState)
	defer lease.Close()

	if _, _, _, _, err := openPrivateLedgerLock(-1, false); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("openPrivateLedgerLock(-1) error = %v, want ErrUnsupported", err)
	}
	if err := directory.withFD(func(directoryFD int) error {
		if err := preflightPrivateLedgerInitialization(context.Background(), directoryFD); err != nil {
			return err
		}
		lockFD, _, created, initialized, err := openPrivateLedgerLock(directoryFD, true)
		if err != nil {
			return err
		}
		if !created || initialized {
			_ = unix.Close(lockFD)
			t.Fatalf("new lock created=%t initialized=%t, want true false", created, initialized)
		}
		if err := snapshotPrivateLedgerNewLock(lockFD); err != nil {
			_ = unix.Close(lockFD)
			return err
		}
		if err := initializePrivateLedgerLock(lockFD, directoryFD); err != nil {
			_ = unix.Close(lockFD)
			return err
		}
		snapshot, err := snapshotPrivateLedgerLock(lockFD)
		if closeErr := unix.Close(lockFD); err == nil {
			err = closeErr
		}
		if err != nil {
			return err
		}
		if err := verifyPrivateLedgerLock(context.Background(), directoryFD, snapshot); err != nil {
			return err
		}
		lockFD, snapshot, created, initialized, err = openPrivateLedgerLock(directoryFD, true)
		if err != nil {
			return err
		}
		defer unix.Close(lockFD)
		if created || !initialized {
			t.Fatalf("existing lock created=%t initialized=%t, want false true", created, initialized)
		}
		return verifyPrivateLedgerLock(context.Background(), directoryFD, snapshot)
	}); err != nil {
		t.Fatalf("lock helper coverage error = %v", err)
	}
}

func TestPrivateLedgerCoverageFailClosedHelperBranches(t *testing.T) {
	record := testPrivateLedgerRecordID(t, "e", "f", 0)
	if err := (&PrivateLedgerSession{}).Publish(context.Background(), record, make([]byte, maximumDurableFileBytes+1)); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("oversized Publish() error = %v, want ErrUnsupported", err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := (&PrivateLedgerSession{}).withActive(cancelled, func() error { return nil }); !errors.Is(err, ErrInterrupted) {
		t.Fatalf("cancelled withActive() error = %v, want ErrInterrupted", err)
	}
	if err := (&PrivateLedgerSession{}).recheckLocked(context.Background()); !errors.Is(err, ErrDrifted) {
		t.Fatalf("unavailable recheckLocked() error = %v, want ErrDrifted", err)
	}
	if err := acquirePrivateLedgerLock(context.Background(), -1, unix.LOCK_EX); !errors.Is(err, ErrDrifted) {
		t.Fatalf("acquirePrivateLedgerLock(-1) error = %v, want ErrDrifted", err)
	}

	rootFD, _, directory, rootID := stageTestDirectories(t)
	defer unix.Close(rootFD)
	defer directory.Close()
	lease := testPrivateDirectoryLease(t, directory, rootID, mounts.LayoutPrivateState)
	defer lease.Close()
	if err := directory.withFD(func(directoryFD int) error {
		if _, _, _, _, err := openPrivateLedgerLock(directoryFD, false); !errors.Is(err, unix.ENOENT) {
			return errors.New("read-only open without lock did not preserve ENOENT")
		}
		if _, err := privateLedgerRecordIDsAt(cancelled, directoryFD, 1); !errors.Is(err, ErrInterrupted) {
			return errors.New("cancelled inventory did not fail interrupted")
		}
		if _, err := privateLedgerRecordIDsAt(context.Background(), directoryFD, 0); !errors.Is(err, ErrUnsupported) {
			return errors.New("zero inventory limit did not fail unsupported")
		}
		if _, err := snapshotPrivateLedgerEntry(-1); err == nil {
			return errors.New("invalid descriptor unexpectedly produced a ledger entry snapshot")
		}
		if _, err := snapshotPrivateLedgerEntry(directoryFD); !errors.Is(err, ErrDrifted) {
			return errors.New("directory descriptor was accepted as a regular ledger entry")
		}

		fd, err := unix.Openat(directoryFD, "coverage-ledger-entry", unix.O_RDWR|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC|unix.O_NOFOLLOW, durableFileMode)
		if err != nil {
			return err
		}
		defer unix.Close(fd)
		if _, err := snapshotPrivateLedgerLock(fd); !errors.Is(err, ErrDrifted) {
			return errors.New("zero-byte lock unexpectedly counted as initialized")
		}
		if err := unix.Fchmod(fd, 0o644); err != nil {
			return err
		}
		if _, err := snapshotPrivateLedgerEntry(fd); !errors.Is(err, ErrDrifted) {
			return errors.New("wrong-mode ledger entry was accepted")
		}
		if err := unix.Fchmod(fd, durableFileMode); err != nil {
			return err
		}
		if err := unix.Ftruncate(fd, maximumDurableFileBytes+1); err != nil {
			return err
		}
		if _, err := snapshotPrivateLedgerEntry(fd); !errors.Is(err, ErrUnsupported) {
			return errors.New("oversized ledger entry was accepted")
		}
		if err := unix.Ftruncate(fd, 0); err != nil {
			return err
		}
		if err := initializePrivateLedgerLock(fd, -1); !errors.Is(err, ErrUnsupported) {
			return errors.New("lock initialization with invalid directory descriptor did not fail unsupported")
		}
		return nil
	}); err != nil {
		t.Fatalf("fail-closed helper coverage error = %v", err)
	}
}

func TestPrivateLedgerCoverageBoundsLedgerDirectoryEnumeration(t *testing.T) {
	rootFD, _, directory, rootID := stageTestDirectories(t)
	defer unix.Close(rootFD)
	defer directory.Close()
	lease := testPrivateDirectoryLease(t, directory, rootID, mounts.LayoutPrivateState)
	defer lease.Close()

	if err := WithPrivateLedgerWriteSession(context.Background(), lease, func(*PrivateLedgerSession) error { return nil }); err != nil {
		t.Fatalf("initialize ledger lock: %v", err)
	}
	if err := directory.withFD(func(directoryFD int) error {
		for index := 0; index < maximumPrivateLedgerDirEntries; index++ {
			name := "unrelated-" + strconv.Itoa(index)
			fd, err := unix.Openat(directoryFD, name, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC|unix.O_NOFOLLOW, durableFileMode)
			if err != nil {
				return err
			}
			if err := unix.Close(fd); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("populate oversized ledger directory: %v", err)
	}
	called := false
	err := WithPrivateLedgerReadSession(context.Background(), lease, func(*PrivateLedgerSession) error {
		called = true
		return nil
	})
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("oversized ledger directory error = %v, want ErrUnsupported", err)
	}
	if called {
		t.Fatal("read session accepted an unbounded private ledger directory")
	}
}

func TestPrivateLedgerCoverageEmptyRecordAndSnapshotDrift(t *testing.T) {
	rootFD, _, directory, rootID := stageTestDirectories(t)
	defer unix.Close(rootFD)
	defer directory.Close()
	lease := testPrivateDirectoryLease(t, directory, rootID, mounts.LayoutPrivateState)
	defer lease.Close()
	record := testPrivateLedgerRecordID(t, "1", "2", 0)
	if err := WithPrivateLedgerWriteSession(context.Background(), lease, func(session *PrivateLedgerSession) error {
		if err := session.Publish(context.Background(), record, nil); err != nil {
			return err
		}
		contents, err := session.Read(context.Background(), record, 1)
		if err != nil {
			return err
		}
		if len(contents) != 0 {
			t.Fatalf("empty record contents = %q", contents)
		}
		return nil
	}); err != nil {
		t.Fatalf("empty record session: %v", err)
	}
	if err := directory.withFD(func(directoryFD int) error {
		fd, err := unix.Openat(directoryFD, "coverage-snapshot", unix.O_RDWR|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC|unix.O_NOFOLLOW, durableFileMode)
		if err != nil {
			return err
		}
		defer unix.Close(fd)
		if _, err := unix.Write(fd, []byte("x")); err != nil {
			return err
		}
		if err := snapshotPrivateLedgerNewLock(fd); !errors.Is(err, ErrDrifted) {
			return errors.New("nonempty new lock was accepted")
		}
		if _, _, err := snapshotPrivateLedgerLockState(fd); !errors.Is(err, ErrDrifted) {
			return errors.New("malformed lock size was accepted")
		}
		if err := unix.Linkat(directoryFD, "coverage-snapshot", directoryFD, "coverage-snapshot-link", 0); err != nil {
			return err
		}
		if _, err := snapshotPrivateLedgerEntry(fd); !errors.Is(err, ErrDrifted) {
			return errors.New("hard-linked ledger entry was accepted")
		}
		return nil
	}); err != nil {
		t.Fatalf("snapshot drift coverage error = %v", err)
	}
}

func TestPrivateLedgerCoverageRejectsRetainedIdentityDrift(t *testing.T) {
	rootFD, source, directory, rootID := stageTestDirectories(t)
	defer unix.Close(rootFD)
	defer source.Close()
	defer directory.Close()
	lease := testPrivateDirectoryLease(t, directory, rootID, mounts.LayoutPrivateState)
	defer lease.Close()

	if err := WithPrivateLedgerWriteSession(context.Background(), lease, func(session *PrivateLedgerSession) error {
		operationFailure := errors.New("operation stopped")
		if err := session.withActive(context.Background(), func() error { return operationFailure }); !errors.Is(err, operationFailure) {
			t.Fatalf("operation failure = %v, want %v", err, operationFailure)
		}
		if err := replacePrivateLedgerLockForCoverage(session.directoryFD); err != nil {
			return err
		}
		if err := session.withActive(context.Background(), func() error { return nil }); !errors.Is(err, ErrDrifted) {
			t.Fatalf("pre-operation identity drift error = %v, want ErrDrifted", err)
		}
		return nil
	}); err != nil {
		t.Fatalf("ledger pre-operation identity drift session: %v", err)
	}
	if err := WithPrivateLedgerWriteSession(context.Background(), lease, func(session *PrivateLedgerSession) error {
		if err := session.withActive(context.Background(), func() error {
			return replacePrivateLedgerLockForCoverage(session.directoryFD)
		}); !errors.Is(err, ErrDrifted) {
			t.Fatalf("post-operation identity drift error = %v, want ErrDrifted", err)
		}
		return nil
	}); err != nil {
		t.Fatalf("ledger post-operation identity drift session: %v", err)
	}

	closed := testPrivateDirectoryLease(t, directory, rootID, mounts.LayoutPrivateState)
	if err := closed.Close(); err != nil {
		t.Fatalf("close probe lease: %v", err)
	}
	if err := (&PrivateLedgerSession{directory: closed, directoryFD: 1}).recheckLocked(context.Background()); err == nil {
		t.Fatal("closed directory recheck unexpectedly succeeded")
	}
	foreignFD, err := source.duplicate()
	if err != nil {
		t.Fatalf("duplicate foreign private directory: %v", err)
	}
	defer unix.Close(foreignFD)
	if err := (&PrivateLedgerSession{directory: lease, directoryFD: foreignFD}).recheckLocked(context.Background()); !errors.Is(err, ErrDrifted) {
		t.Fatalf("foreign descriptor recheck error = %v, want ErrDrifted", err)
	}
	var nilSession *PrivateLedgerSession
	nilSession.deactivate()
}

func replacePrivateLedgerLockForCoverage(directoryFD int) error {
	if err := unix.Unlinkat(directoryFD, privateLedgerLockBasename, 0); err != nil {
		return err
	}
	lockFD, err := unix.Openat(directoryFD, privateLedgerLockBasename, unix.O_RDWR|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC|unix.O_NOFOLLOW, durableFileMode)
	if err != nil {
		return err
	}
	if err := initializePrivateLedgerLock(lockFD, directoryFD); err != nil {
		_ = unix.Close(lockFD)
		return err
	}
	return unix.Close(lockFD)
}

func TestPrivateLedgerCoverageRejectsMalformedRetainedLock(t *testing.T) {
	rootFD, _, directory, rootID := stageTestDirectories(t)
	defer unix.Close(rootFD)
	defer directory.Close()
	lease := testPrivateDirectoryLease(t, directory, rootID, mounts.LayoutPrivateState)
	defer lease.Close()
	if err := directory.withFD(func(directoryFD int) error {
		fd, err := unix.Openat(directoryFD, privateLedgerLockBasename, unix.O_RDWR|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC|unix.O_NOFOLLOW, durableFileMode)
		if err != nil {
			return err
		}
		if _, err := unix.Write(fd, []byte("bad")); err != nil {
			_ = unix.Close(fd)
			return err
		}
		if err := unix.Close(fd); err != nil {
			return err
		}
		if fd, _, _, _, err := openPrivateLedgerLock(directoryFD, false); err == nil {
			_ = unix.Close(fd)
			return errors.New("read-only open accepted malformed static lock")
		}
		if fd, _, _, _, err := openPrivateLedgerLock(directoryFD, true); err == nil {
			_ = unix.Close(fd)
			return errors.New("write open accepted malformed static lock")
		}
		return nil
	}); err != nil {
		t.Fatalf("malformed lock helper error = %v", err)
	}
	if err := WithPrivateLedgerReadSession(context.Background(), lease, func(*PrivateLedgerSession) error { return nil }); !errors.Is(err, ErrDrifted) {
		t.Fatalf("malformed retained lock read session error = %v, want ErrDrifted", err)
	}
}

func TestPrivateLedgerCoverageReadOnlyIncompleteAndReadFaults(t *testing.T) {
	rootFD, _, directory, rootID := stageTestDirectories(t)
	defer unix.Close(rootFD)
	defer directory.Close()
	lease := testPrivateDirectoryLease(t, directory, rootID, mounts.LayoutPrivateState)
	defer lease.Close()
	missing := testPrivateLedgerRecordID(t, "3", "4", 0)
	bad := testPrivateLedgerRecordID(t, "5", "6", 0)

	if _, err := (&PrivateLedgerSession{}).Read(context.Background(), PrivateLedgerRecordID{}, 1); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("invalid record Read() error = %v, want ErrUnsupported", err)
	}
	if err := classifyMissingReadOnlyLedger(context.Background(), -1); err == nil {
		t.Fatal("missing ledger classifier accepted an invalid directory descriptor")
	}
	if err := directory.withFD(func(directoryFD int) error {
		fd, err := unix.Openat(directoryFD, privateLedgerLockBasename, unix.O_RDWR|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC|unix.O_NOFOLLOW, durableFileMode)
		if err != nil {
			return err
		}
		if err := unix.Close(fd); err != nil {
			return err
		}
		return nil
	}); err != nil {
		t.Fatalf("create incomplete lock: %v", err)
	}
	if err := WithPrivateLedgerReadSession(context.Background(), lease, func(*PrivateLedgerSession) error { return nil }); !errors.Is(err, ErrDrifted) {
		t.Fatalf("incomplete lock read session error = %v, want ErrDrifted", err)
	}

	if err := WithPrivateLedgerWriteSession(context.Background(), lease, func(*PrivateLedgerSession) error { return nil }); err != nil {
		t.Fatalf("complete retained lock: %v", err)
	}
	if err := directory.withFD(func(directoryFD int) error {
		if _, err := readPrivateLedgerRecord(context.Background(), directoryFD, missing, 1); err == nil {
			return errors.New("missing record read unexpectedly succeeded")
		}
		fd, err := unix.Openat(directoryFD, bad.basename(), unix.O_RDWR|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC|unix.O_NOFOLLOW, durableFileMode)
		if err != nil {
			return err
		}
		if err := unix.Fchmod(fd, 0o644); err != nil {
			_ = unix.Close(fd)
			return err
		}
		if err := unix.Close(fd); err != nil {
			return err
		}
		if _, err := readPrivateLedgerRecord(context.Background(), directoryFD, bad, 1); !errors.Is(err, ErrDrifted) {
			return errors.New("wrong-mode record read did not fail closed")
		}
		return nil
	}); err != nil {
		t.Fatalf("read fault coverage error = %v", err)
	}
}

func TestPrivateLedgerCoverageRejectsHostileLockOpenAndReleaseReuse(t *testing.T) {
	rootFD, _, directory, rootID := stageTestDirectories(t)
	defer unix.Close(rootFD)
	defer directory.Close()
	lease := testPrivateDirectoryLease(t, directory, rootID, mounts.LayoutPrivateState)
	defer lease.Close()
	if err := directory.withFD(func(directoryFD int) error {
		if err := unix.Symlinkat("outside", directoryFD, privateLedgerLockBasename); err != nil {
			return err
		}
		if fd, _, _, _, err := openPrivateLedgerLock(directoryFD, false); err == nil {
			_ = unix.Close(fd)
			return errors.New("read-only open accepted a hostile lock symlink")
		}
		if fd, _, _, _, err := openPrivateLedgerLock(directoryFD, true); err == nil {
			_ = unix.Close(fd)
			return errors.New("write open accepted a hostile lock symlink")
		}
		if err := unix.Unlinkat(directoryFD, privateLedgerLockBasename, 0); err != nil {
			return err
		}
		session, release, err := startPrivateLedgerSession(context.Background(), lease, directoryFD, true)
		if err != nil {
			return err
		}
		if session == nil {
			return errors.New("direct session startup returned nil session")
		}
		if err := release(); err != nil {
			return err
		}
		if err := release(); !errors.Is(err, ErrInterrupted) {
			return errors.New("reused release did not report interrupted cleanup")
		}
		return nil
	}); err != nil {
		t.Fatalf("hostile lock/release coverage error = %v", err)
	}
}

func TestPrivateLedgerCoverageRefusesRecordCapacityOverflow(t *testing.T) {
	rootFD, _, directory, rootID := stageTestDirectories(t)
	defer unix.Close(rootFD)
	defer directory.Close()
	lease := testPrivateDirectoryLease(t, directory, rootID, mounts.LayoutPrivateState)
	defer lease.Close()
	if err := (&PrivateLedgerSession{}).Publish(context.Background(), PrivateLedgerRecordID{}, nil); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("invalid record Publish() error = %v, want ErrUnsupported", err)
	}
	if err := WithPrivateLedgerWriteSession(context.Background(), lease, func(*PrivateLedgerSession) error { return nil }); err != nil {
		t.Fatalf("initialize ledger lock: %v", err)
	}
	if err := directory.withFD(func(directoryFD int) error {
		token := "ldc-" + strings.Repeat("a", privateLedgerTokenHexBytes)
		for ordinal := 0; ordinal < maximumPrivateLedgerRecords; ordinal++ {
			digest := strings.Repeat("0", privateLedgerDigestHexBytes-len(strconv.Itoa(ordinal+1))) + strconv.Itoa(ordinal+1)
			id, err := NewPrivateLedgerRecordID(digest, token, 0)
			if err != nil {
				return err
			}
			fd, err := unix.Openat(directoryFD, id.basename(), unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC|unix.O_NOFOLLOW, durableFileMode)
			if err != nil {
				return err
			}
			if err := unix.Close(fd); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("populate capacity ledger records: %v", err)
	}
	extra := testPrivateLedgerRecordID(t, "f", "b", 1)
	err := WithPrivateLedgerWriteSession(context.Background(), lease, func(session *PrivateLedgerSession) error {
		return session.Publish(context.Background(), extra, []byte("extra"))
	})
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("capacity Publish() error = %v, want ErrUnsupported", err)
	}
}

func TestPrivateLedgerCoverageStopsOnMalformedRecordBeforeInitialization(t *testing.T) {
	rootFD, _, directory, rootID := stageTestDirectories(t)
	defer unix.Close(rootFD)
	defer directory.Close()
	lease := testPrivateDirectoryLease(t, directory, rootID, mounts.LayoutPrivateState)
	defer lease.Close()
	if err := directory.withFD(func(directoryFD int) error {
		for _, name := range []string{privateLedgerLockBasename, "ldc-ledger-v1-not-a-record.cbor"} {
			fd, err := unix.Openat(directoryFD, name, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC|unix.O_NOFOLLOW, durableFileMode)
			if err != nil {
				return err
			}
			if err := unix.Close(fd); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("create malformed retained state: %v", err)
	}
	called := false
	err := WithPrivateLedgerWriteSession(context.Background(), lease, func(*PrivateLedgerSession) error {
		called = true
		return nil
	})
	if !errors.Is(err, ErrDrifted) {
		t.Fatalf("malformed retained record write session error = %v, want ErrDrifted", err)
	}
	if called {
		t.Fatal("write session initialized around a malformed retained record")
	}
}
