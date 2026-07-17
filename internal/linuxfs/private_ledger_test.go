//go:build linux

package linuxfs

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/biendo27/linux-deep-clean/internal/mounts"
	"golang.org/x/sys/unix"
)

func TestPrivateLedgerSessionPublishesReadsAndReloadsBoundedRecords(t *testing.T) {
	rootFD, _, directory, rootID := stageTestDirectories(t)
	defer unix.Close(rootFD)
	defer directory.Close()

	lease := testPrivateDirectoryLease(t, directory, rootID, mounts.LayoutPrivateState)
	defer lease.Close()
	first := testPrivateLedgerRecordID(t, "a", "1", 0)
	second := testPrivateLedgerRecordID(t, "b", "2", 1)

	err := WithPrivateLedgerWriteSession(context.Background(), lease, func(session *PrivateLedgerSession) error {
		if err := session.Publish(context.Background(), first, []byte("first")); err != nil {
			return err
		}
		if err := session.Publish(context.Background(), second, []byte("second")); err != nil {
			return err
		}
		contents, err := session.Read(context.Background(), first, 32)
		if err != nil {
			return err
		}
		if string(contents) != "first" {
			t.Fatalf("session.Read() = %q, want first", contents)
		}
		records, err := session.List(context.Background(), 2)
		if err != nil {
			return err
		}
		if len(records) != 2 || records[0] != first || records[1] != second {
			t.Fatalf("session.List() = %#v, want [%#v %#v]", records, first, second)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("WithPrivateLedgerWriteSession() error = %v", err)
	}

	var reloaded []PrivateLedgerRecordID
	err = WithPrivateLedgerReadSession(context.Background(), lease, func(session *PrivateLedgerSession) error {
		var err error
		reloaded, err = session.List(context.Background(), 2)
		return err
	})
	if err != nil {
		t.Fatalf("WithPrivateLedgerReadSession() error = %v", err)
	}
	if len(reloaded) != 2 || reloaded[0] != first || reloaded[1] != second {
		t.Fatalf("reloaded records = %#v, want [%#v %#v]", reloaded, first, second)
	}
}

func TestPrivateLedgerSessionRejectsWrongLeaseAndReadDoesNotInitialize(t *testing.T) {
	rootFD, _, directory, rootID := stageTestDirectories(t)
	defer unix.Close(rootFD)
	defer directory.Close()

	stateLease := testPrivateDirectoryLease(t, directory, rootID, mounts.LayoutPrivateState)
	defer stateLease.Close()
	wrongLease := testPrivateDirectoryLease(t, directory, rootID, mounts.LayoutPrivateStaging)
	defer wrongLease.Close()

	called := false
	err := WithPrivateLedgerWriteSession(context.Background(), wrongLease, func(*PrivateLedgerSession) error {
		called = true
		return nil
	})
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("WithPrivateLedgerWriteSession() wrong kind error = %v, want ErrUnsupported", err)
	}
	if called {
		t.Fatal("wrong private layout invoked ledger callback")
	}

	err = WithPrivateLedgerReadSession(context.Background(), stateLease, func(*PrivateLedgerSession) error {
		t.Fatal("uninitialized read session invoked callback")
		return nil
	})
	if !errors.Is(err, ErrLedgerNotInitialized) {
		t.Fatalf("WithPrivateLedgerReadSession() uninitialized error = %v, want ErrLedgerNotInitialized", err)
	}
	if testEntryExists(t, directory, privateLedgerLockBasename) {
		t.Fatal("read-only ledger session created a lock entry")
	}
}

func TestPrivateLedgerSessionFailsClosedOnCapacityAndHostileRecord(t *testing.T) {
	rootFD, _, directory, rootID := stageTestDirectories(t)
	defer unix.Close(rootFD)
	defer directory.Close()

	lease := testPrivateDirectoryLease(t, directory, rootID, mounts.LayoutPrivateState)
	defer lease.Close()
	first := testPrivateLedgerRecordID(t, "c", "3", 0)
	second := testPrivateLedgerRecordID(t, "d", "4", 1)

	err := WithPrivateLedgerWriteSession(context.Background(), lease, func(session *PrivateLedgerSession) error {
		if err := session.Publish(context.Background(), first, []byte("first")); err != nil {
			return err
		}
		return session.Publish(context.Background(), second, []byte("second"))
	})
	if err != nil {
		t.Fatalf("publish records: %v", err)
	}

	err = WithPrivateLedgerReadSession(context.Background(), lease, func(session *PrivateLedgerSession) error {
		_, err := session.List(context.Background(), 1)
		return err
	})
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("bounded list error = %v, want ErrUnsupported", err)
	}

	hostile := testPrivateLedgerRecordID(t, "e", "5", 2)
	err = directory.withFD(func(directoryFD int) error {
		return unix.Symlinkat("outside-sentinel", directoryFD, hostile.basename())
	})
	if err != nil {
		t.Fatalf("create hostile ledger symlink: %v", err)
	}
	err = WithPrivateLedgerReadSession(context.Background(), lease, func(session *PrivateLedgerSession) error {
		_, err := session.Read(context.Background(), hostile, 32)
		return err
	})
	if !errors.Is(err, ErrDrifted) {
		t.Fatalf("hostile record read error = %v, want ErrDrifted", err)
	}
}

func TestPrivateLedgerSessionSerializesConcurrentNoReplacePublication(t *testing.T) {
	rootFD, _, directory, rootID := stageTestDirectories(t)
	defer unix.Close(rootFD)
	defer directory.Close()

	firstLease := testPrivateDirectoryLease(t, directory, rootID, mounts.LayoutPrivateState)
	defer firstLease.Close()
	secondLease := testPrivateDirectoryLease(t, directory, rootID, mounts.LayoutPrivateState)
	defer secondLease.Close()
	record := testPrivateLedgerRecordID(t, "f", "6", 0)

	start := make(chan struct{})
	results := make(chan error, 2)
	var ready sync.WaitGroup
	ready.Add(2)
	for _, lease := range []*PrivateDirectoryLease{firstLease, secondLease} {
		go func(lease *PrivateDirectoryLease) {
			defer ready.Done()
			<-start
			results <- WithPrivateLedgerWriteSession(context.Background(), lease, func(session *PrivateLedgerSession) error {
				return session.Publish(context.Background(), record, []byte("owned"))
			})
		}(lease)
	}
	close(start)
	ready.Wait()
	close(results)

	successes := 0
	for err := range results {
		if err == nil {
			successes++
			continue
		}
		if !errors.Is(err, ErrDrifted) {
			t.Fatalf("concurrent session error = %v, want ErrDrifted collision", err)
		}
	}
	if successes != 1 {
		t.Fatalf("concurrent session successes = %d, want 1", successes)
	}
}

func TestPrivateLedgerRecordIDRejectsNonCanonicalValues(t *testing.T) {
	validDigest := strings.Repeat("a", privateLedgerDigestHexBytes)
	validToken := "ldc-" + strings.Repeat("1", privateLedgerTokenHexBytes)
	for _, test := range []struct {
		name    string
		digest  string
		token   string
		ordinal uint8
	}{
		{name: "short digest", digest: validDigest[:len(validDigest)-1], token: validToken},
		{name: "uppercase digest", digest: "A" + validDigest[1:], token: validToken},
		{name: "short token", digest: validDigest, token: validToken[:len(validToken)-1]},
		{name: "foreign token", digest: validDigest, token: "foreign"},
		{name: "ordinal out of range", digest: validDigest, token: validToken, ordinal: maximumPrivateLedgerOrdinal + 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewPrivateLedgerRecordID(test.digest, test.token, test.ordinal); !errors.Is(err, ErrUnsupported) {
				t.Fatalf("NewPrivateLedgerRecordID() error = %v, want ErrUnsupported", err)
			}
		})
	}
}

func testPrivateLedgerRecordID(t *testing.T, digestByte, tokenByte string, ordinal uint8) PrivateLedgerRecordID {
	t.Helper()
	record, err := NewPrivateLedgerRecordID(
		strings.Repeat(digestByte, privateLedgerDigestHexBytes),
		"ldc-"+strings.Repeat(tokenByte, privateLedgerTokenHexBytes),
		ordinal,
	)
	if err != nil {
		t.Fatalf("NewPrivateLedgerRecordID() error = %v", err)
	}
	return record
}

func TestPrivateLedgerSessionCancelledLockDoesNotPublishRecord(t *testing.T) {
	rootFD, _, directory, rootID := stageTestDirectories(t)
	defer unix.Close(rootFD)
	defer directory.Close()

	firstLease := testPrivateDirectoryLease(t, directory, rootID, mounts.LayoutPrivateState)
	defer firstLease.Close()
	secondLease := testPrivateDirectoryLease(t, directory, rootID, mounts.LayoutPrivateState)
	defer secondLease.Close()
	record := testPrivateLedgerRecordID(t, "a", "7", 0)

	entered := make(chan struct{})
	release := make(chan struct{})
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- WithPrivateLedgerWriteSession(context.Background(), firstLease, func(session *PrivateLedgerSession) error {
			close(entered)
			<-release
			return session.Publish(context.Background(), record, []byte("first"))
		})
	}()
	<-entered

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	err := WithPrivateLedgerWriteSession(ctx, secondLease, func(session *PrivateLedgerSession) error {
		return session.Publish(context.Background(), record, []byte("second"))
	})
	if !errors.Is(err, ErrInterrupted) {
		t.Fatalf("cancelled ledger lock error = %v, want ErrInterrupted", err)
	}
	close(release)
	if err := <-firstDone; err != nil {
		t.Fatalf("first ledger session error = %v", err)
	}
	if contents := readTestFile(t, directory, record.basename()); string(contents) != "first" {
		t.Fatalf("published record = %q, want first", contents)
	}
}

func TestPrivateLedgerReadSessionNeverBlocksOnHostileLockEntry(t *testing.T) {
	rootFD, _, directory, rootID := stageTestDirectories(t)
	defer unix.Close(rootFD)
	defer directory.Close()
	lease := testPrivateDirectoryLease(t, directory, rootID, mounts.LayoutPrivateState)
	defer lease.Close()

	if err := directory.withFD(func(directoryFD int) error {
		return unix.Mkfifoat(directoryFD, privateLedgerLockBasename, durableFileMode)
	}); err != nil {
		t.Fatalf("create hostile lock FIFO: %v", err)
	}

	result := make(chan error, 1)
	go func() {
		result <- WithPrivateLedgerReadSession(context.Background(), lease, func(*PrivateLedgerSession) error {
			return nil
		})
	}()
	select {
	case err := <-result:
		if !errors.Is(err, ErrDrifted) {
			t.Fatalf("hostile lock error = %v, want ErrDrifted", err)
		}
	case <-time.After(100 * time.Millisecond):
		// A blocking open would otherwise strand this test. Pair it with a
		// writer only to release the hostile FIFO before reporting failure.
		_ = directory.withFD(func(directoryFD int) error {
			fd, err := unix.Openat(directoryFD, privateLedgerLockBasename, unix.O_WRONLY|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
			if err == nil {
				return unix.Close(fd)
			}
			return nil
		})
		<-result
		t.Fatal("read-only private ledger session blocked on a hostile FIFO lock")
	}
}

func TestPrivateLedgerWriteSessionWillNotRecreateMissingLockAroundRecords(t *testing.T) {
	rootFD, _, directory, rootID := stageTestDirectories(t)
	defer unix.Close(rootFD)
	defer directory.Close()
	lease := testPrivateDirectoryLease(t, directory, rootID, mounts.LayoutPrivateState)
	defer lease.Close()
	record := testPrivateLedgerRecordID(t, "b", "8", 0)

	if err := WithPrivateLedgerWriteSession(context.Background(), lease, func(session *PrivateLedgerSession) error {
		return session.Publish(context.Background(), record, []byte("retained"))
	}); err != nil {
		t.Fatalf("create retained record: %v", err)
	}
	if err := directory.withFD(func(directoryFD int) error {
		return unix.Unlinkat(directoryFD, privateLedgerLockBasename, 0)
	}); err != nil {
		t.Fatalf("remove static lock: %v", err)
	}

	called := false
	err := WithPrivateLedgerWriteSession(context.Background(), lease, func(*PrivateLedgerSession) error {
		called = true
		return nil
	})
	if !errors.Is(err, ErrDrifted) {
		t.Fatalf("missing lock around record error = %v, want ErrDrifted", err)
	}
	if called {
		t.Fatal("write session invoked a callback after finding records without their static lock")
	}
	if testEntryExists(t, directory, privateLedgerLockBasename) {
		t.Fatal("write session recreated a lock around retained records")
	}
}

func TestPrivateLedgerSessionFailsClosedOnFutureReservedNamespace(t *testing.T) {
	rootFD, _, directory, rootID := stageTestDirectories(t)
	defer unix.Close(rootFD)
	defer directory.Close()
	lease := testPrivateDirectoryLease(t, directory, rootID, mounts.LayoutPrivateState)
	defer lease.Close()

	const futureEntry = "ldc-ledger-v2-future.cbor"
	if err := directory.withFD(func(directoryFD int) error {
		fd, err := unix.Openat(directoryFD, futureEntry, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC|unix.O_NOFOLLOW, durableFileMode)
		if err != nil {
			return err
		}
		return unix.Close(fd)
	}); err != nil {
		t.Fatalf("create future ledger entry: %v", err)
	}

	if err := PublishFileDurable(context.Background(), lease, futureEntry, []byte("bypass")); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("PublishFileDurable() future namespace error = %v, want ErrUnsupported", err)
	}

	called := false
	err := WithPrivateLedgerWriteSession(context.Background(), lease, func(*PrivateLedgerSession) error {
		called = true
		return nil
	})
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("future ledger namespace write session error = %v, want ErrUnsupported", err)
	}
	if called {
		t.Fatal("write session invoked a callback after finding an unsupported future ledger namespace entry")
	}
	if testEntryExists(t, directory, privateLedgerLockBasename) {
		t.Fatal("write session initialized a lock despite an unsupported future ledger namespace entry")
	}
}

func TestPrivateLedgerWriteSessionCompletesRetainedZeroByteLock(t *testing.T) {
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
		return unix.Close(fd)
	}); err != nil {
		t.Fatalf("create retained zero-byte lock: %v", err)
	}
	if err := WithPrivateLedgerWriteSession(context.Background(), lease, func(session *PrivateLedgerSession) error {
		records, err := session.List(context.Background(), 1)
		if err != nil {
			return err
		}
		if len(records) != 0 {
			t.Fatalf("initialized zero-byte lock records = %#v, want none", records)
		}
		return nil
	}); err != nil {
		t.Fatalf("complete retained zero-byte lock: %v", err)
	}
	if err := WithPrivateLedgerReadSession(context.Background(), lease, func(*PrivateLedgerSession) error { return nil }); err != nil {
		t.Fatalf("read initialized lock: %v", err)
	}
}

func TestPrivateLedgerWriteSessionWillNotInitializeZeroByteLockAroundRecords(t *testing.T) {
	rootFD, _, directory, rootID := stageTestDirectories(t)
	defer unix.Close(rootFD)
	defer directory.Close()
	lease := testPrivateDirectoryLease(t, directory, rootID, mounts.LayoutPrivateState)
	defer lease.Close()
	record := testPrivateLedgerRecordID(t, "d", "a", 0)

	if err := directory.withFD(func(directoryFD int) error {
		lockFD, err := unix.Openat(directoryFD, privateLedgerLockBasename, unix.O_RDWR|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC|unix.O_NOFOLLOW, durableFileMode)
		if err != nil {
			return err
		}
		if err := unix.Close(lockFD); err != nil {
			return err
		}
		recordFD, err := unix.Openat(directoryFD, record.basename(), unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC|unix.O_NOFOLLOW, durableFileMode)
		if err != nil {
			return err
		}
		return unix.Close(recordFD)
	}); err != nil {
		t.Fatalf("create incomplete lock with retained record: %v", err)
	}

	called := false
	err := WithPrivateLedgerWriteSession(context.Background(), lease, func(*PrivateLedgerSession) error {
		called = true
		return nil
	})
	if !errors.Is(err, ErrDrifted) {
		t.Fatalf("zero-byte lock around records error = %v, want ErrDrifted", err)
	}
	if called {
		t.Fatal("write session initialized a zero-byte lock around retained records")
	}
	if contents := readTestFile(t, directory, privateLedgerLockBasename); len(contents) != 0 {
		t.Fatalf("zero-byte lock contents = %q, want unchanged empty marker", contents)
	}
}

func TestPrivateLedgerSessionReleasesLockAfterCallbackPanic(t *testing.T) {
	rootFD, _, directory, rootID := stageTestDirectories(t)
	defer unix.Close(rootFD)
	defer directory.Close()
	firstLease := testPrivateDirectoryLease(t, directory, rootID, mounts.LayoutPrivateState)
	defer firstLease.Close()
	secondLease := testPrivateDirectoryLease(t, directory, rootID, mounts.LayoutPrivateState)
	defer secondLease.Close()

	func() {
		defer func() {
			if recovered := recover(); recovered == nil {
				t.Fatal("ledger callback panic did not propagate")
			}
		}()
		_ = WithPrivateLedgerWriteSession(context.Background(), firstLease, func(*PrivateLedgerSession) error {
			panic("ledger callback panic")
		})
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := WithPrivateLedgerWriteSession(ctx, secondLease, func(*PrivateLedgerSession) error { return nil }); err != nil {
		t.Fatalf("writer after callback panic error = %v", err)
	}
}

func TestPublishFileDurableRejectsReservedLedgerBasenamesOutsideSession(t *testing.T) {
	rootFD, _, directory, rootID := stageTestDirectories(t)
	defer unix.Close(rootFD)
	defer directory.Close()
	lease := testPrivateDirectoryLease(t, directory, rootID, mounts.LayoutPrivateState)
	defer lease.Close()

	for _, basename := range []string{
		privateLedgerLockBasename,
		testPrivateLedgerRecordID(t, "c", "9", 0).basename(),
	} {
		if err := PublishFileDurable(context.Background(), lease, basename, []byte("bypass")); !errors.Is(err, ErrUnsupported) {
			t.Fatalf("PublishFileDurable(%q) error = %v, want ErrUnsupported", basename, err)
		}
		if testEntryExists(t, directory, basename) {
			t.Fatalf("PublishFileDurable(%q) created a reserved ledger entry", basename)
		}
	}
}
