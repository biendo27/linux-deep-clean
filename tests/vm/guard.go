//go:build vmtest

// Package vmtest contains the opt-in guard for destructive VM qualification.
package vmtest

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"syscall"
)

const disposableGuestSentinel = "/run/linux-deep-clean/disposable-guest"

type guardDependencies struct {
	getenv func(string) string
	lstat  func(string) (fs.FileInfo, error)
	open   func(string) (sentinelFile, error)
}

// sentinelFile retains the descriptor opened after the path metadata check.
// Reading through that descriptor prevents a second pathname lookup from
// silently accepting a sentinel that was replaced during verification.
type sentinelFile interface {
	io.Reader
	Stat() (fs.FileInfo, error)
	Close() error
}

type sentinelIdentity struct {
	device uint64
	inode  uint64
}

func requireDisposableGuest() error {
	return requireDisposableGuestWith(guardDependencies{
		getenv: os.Getenv,
		lstat:  os.Lstat,
		open:   openDisposableGuestSentinel,
	})
}

// openDisposableGuestSentinel is intentionally a one-line adapter. The
// unguarded vmguardunit source contract permits os.Open only at this fixed
// read-only boundary, where its result is immediately constrained to the
// sentinelFile interface.
func openDisposableGuestSentinel(path string) (sentinelFile, error) {
	return os.Open(path)
}

// requireDisposableGuestWith exists only to make unsafe sentinel states unit-testable.
// Production always uses requireDisposableGuest, which has no caller-controlled path
// or dependencies.
func requireDisposableGuestWith(deps guardDependencies) error {
	if deps.getenv == nil || deps.lstat == nil || deps.open == nil {
		return errors.New("vmtest guard dependencies are unavailable")
	}

	if deps.getenv("LDCLEAN_VMTEST") != "1" {
		return errors.New("vmtest requires LDCLEAN_VMTEST=1")
	}

	token := deps.getenv("LDCLEAN_VMTEST_TOKEN")
	if token == "" {
		return errors.New("vmtest requires a nonempty LDCLEAN_VMTEST_TOKEN")
	}

	info, err := deps.lstat(disposableGuestSentinel)
	if err != nil {
		return fmt.Errorf("vmtest disposable-guest sentinel: %w", err)
	}
	if info == nil {
		return errors.New("vmtest disposable-guest sentinel metadata is unavailable")
	}
	expectedIdentity, err := sentinelIdentityFromMetadata(info)
	if err != nil {
		return err
	}

	file, err := deps.open(disposableGuestSentinel)
	if err != nil {
		return fmt.Errorf("open vmtest disposable-guest sentinel: %w", err)
	}
	if file == nil {
		return errors.New("vmtest disposable-guest sentinel file is unavailable")
	}
	defer file.Close()

	openedInfo, err := file.Stat()
	if err != nil {
		return fmt.Errorf("stat opened vmtest disposable-guest sentinel: %w", err)
	}
	openedIdentity, err := sentinelIdentityFromMetadata(openedInfo)
	if err != nil {
		return err
	}
	if openedIdentity != expectedIdentity {
		return errors.New("vmtest disposable-guest sentinel changed during verification")
	}

	contents, err := io.ReadAll(io.LimitReader(file, int64(len(token)+1)))
	if err != nil {
		return fmt.Errorf("read vmtest disposable-guest sentinel: %w", err)
	}
	if string(contents) != token {
		return errors.New("vmtest disposable-guest sentinel token does not match LDCLEAN_VMTEST_TOKEN exactly")
	}

	return nil
}

func sentinelIdentityFromMetadata(info fs.FileInfo) (sentinelIdentity, error) {
	if info == nil {
		return sentinelIdentity{}, errors.New("vmtest disposable-guest sentinel metadata is unavailable")
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		return sentinelIdentity{}, errors.New("vmtest disposable-guest sentinel must not be a symlink")
	}
	if !info.Mode().IsRegular() {
		return sentinelIdentity{}, errors.New("vmtest disposable-guest sentinel must be a regular file")
	}
	if info.Mode().Perm()&0o022 != 0 {
		return sentinelIdentity{}, errors.New("vmtest disposable-guest sentinel must not be group or world writable")
	}

	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != 0 {
		return sentinelIdentity{}, errors.New("vmtest disposable-guest sentinel must be root-owned")
	}

	return sentinelIdentity{device: uint64(stat.Dev), inode: stat.Ino}, nil
}
