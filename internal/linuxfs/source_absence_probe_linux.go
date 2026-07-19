//go:build linux

package linuxfs

import (
	"context"
	"errors"
	"fmt"

	"github.com/biendo27/linux-deep-clean/internal/domain"
	"golang.org/x/sys/unix"
)

type resolvedTargetAbsenceOpen func(int, string, *unix.OpenHow) (int, error)
type resolvedTargetAbsenceClose func(int) error

// ProbeResolvedTargetAbsence proves that one exact precondition-bound source
// basename is absent beneath its held parent descriptor. It is read-only: it
// performs only constrained no-follow lookups and never scans, reads content,
// creates, renames, restores, removes, or otherwise mutates an entry.
//
// An absence result is positive only after two independent ENOENT lookups.
// Any present, unsafe, unstable, interrupted, or otherwise uncertain lookup
// returns absent=false with an error so reconciliation cannot infer that the
// source was moved from incomplete evidence.
func ProbeResolvedTargetAbsence(
	ctx context.Context,
	parent *ParentLease,
	basename string,
	expected domain.FilesystemPrecondition,
) (absent bool, err error) {
	return probeResolvedTargetAbsenceWith(ctx, parent, basename, expected, unix.Openat2)
}

func probeResolvedTargetAbsenceWith(
	ctx context.Context,
	parent *ParentLease,
	basename string,
	expected domain.FilesystemPrecondition,
	open resolvedTargetAbsenceOpen,
) (bool, error) {
	if err := contextError(ctx); err != nil {
		return false, err
	}
	if open == nil {
		return false, fmt.Errorf("%w: constrained source absence open is required", ErrUnsupported)
	}
	if _, err := validateResolvedTargetAbsenceRequest(parent, basename, expected); err != nil {
		return false, err
	}

	for observation := 0; observation < 2; observation++ {
		absent, err := observeResolvedTargetAbsenceWith(ctx, parent, basename, open)
		if err != nil {
			return false, err
		}
		if !absent {
			return false, fmt.Errorf("%w: source target %q remains present", ErrDrifted, basename)
		}
	}
	return true, nil
}

func validateResolvedTargetAbsenceRequest(
	parent *ParentLease,
	basename string,
	expected domain.FilesystemPrecondition,
) (domain.FilesystemPrecondition, error) {
	clonedExpected := cloneFilesystemPrecondition(expected)
	if err := clonedExpected.Validate(); err != nil {
		return domain.FilesystemPrecondition{}, fmt.Errorf("%w: invalid filesystem precondition: %v", ErrUnsupported, err)
	}
	required, err := RequiredStatMask(domain.ActionTrashPath)
	if err != nil {
		return domain.FilesystemPrecondition{}, err
	}
	if clonedExpected.Required != required {
		return domain.FilesystemPrecondition{}, fmt.Errorf("%w: source absence probe requires Trash move mask %b, got %b", ErrUnsupported, required, clonedExpected.Required)
	}
	if clonedExpected.Target.Filesystem == nil {
		return domain.FilesystemPrecondition{}, fmt.Errorf("%w: filesystem precondition has no filesystem target", ErrUnsupported)
	}
	if err := clonedExpected.Target.Filesystem.Root.Validate(); err != nil {
		return domain.FilesystemPrecondition{}, fmt.Errorf("%w: precondition root identity: %v", ErrUnsupported, err)
	}
	if err := ValidateResolvedParentForTarget(parent, basename, clonedExpected); err != nil {
		return domain.FilesystemPrecondition{}, err
	}
	return clonedExpected, nil
}

func openResolvedTargetAbsenceBeneathWith(
	ctx context.Context,
	parentFD int,
	basename string,
	open resolvedTargetAbsenceOpen,
) (bool, error) {
	return openResolvedTargetAbsenceBeneathWithClose(ctx, parentFD, basename, open, unix.Close)
}

func openResolvedTargetAbsenceBeneathWithClose(
	ctx context.Context,
	parentFD int,
	basename string,
	open resolvedTargetAbsenceOpen,
	closeFD resolvedTargetAbsenceClose,
) (bool, error) {
	if parentFD < 0 {
		return false, fmt.Errorf("%w: source parent has no held descriptor", ErrDrifted)
	}
	if err := validateBasename(basename); err != nil {
		return false, err
	}
	if open == nil {
		return false, fmt.Errorf("%w: constrained source absence open is required", ErrUnsupported)
	}
	if closeFD == nil {
		return false, fmt.Errorf("%w: constrained source absence close is required", ErrUnsupported)
	}

	// O_PATH observes every final object kind without reading content or
	// activating a special file. O_NOFOLLOW and RESOLVE_* bind the lookup to
	// exactly one name beneath the held parent descriptor.
	how := &unix.OpenHow{
		Flags:   uint64(unix.O_PATH | unix.O_CLOEXEC | unix.O_NOFOLLOW),
		Resolve: requiredOpenat2ResolveFlags,
	}
	for lookup := 0; lookup < 2; lookup++ {
		absent, err := openResolvedTargetAbsenceOnce(ctx, parentFD, basename, how, open, closeFD)
		if err != nil {
			return false, err
		}
		if !absent {
			return false, fmt.Errorf("%w: source target %q remains present", ErrDrifted, basename)
		}
	}
	return true, nil
}

// observeResolvedTargetAbsenceWith obtains a fresh descriptor for the
// immutable parent path from the retained trusted-root namespace before one
// constrained lookup. The fresh parent must still be the held parent; a
// detached or replaced lexical parent is drift, not evidence of source
// absence.
func observeResolvedTargetAbsenceWith(
	ctx context.Context,
	parent *ParentLease,
	basename string,
	open resolvedTargetAbsenceOpen,
) (bool, error) {
	parentFD, err := parent.requalifiedPlannedParent(ctx)
	if err != nil {
		return false, err
	}
	absent, lookupErr := openResolvedTargetAbsenceOnce(ctx, parentFD, basename, &unix.OpenHow{
		Flags:   uint64(unix.O_PATH | unix.O_CLOEXEC | unix.O_NOFOLLOW),
		Resolve: requiredOpenat2ResolveFlags,
	}, open, unix.Close)
	closeErr := unix.Close(parentFD)
	if lookupErr != nil {
		return false, lookupErr
	}
	if closeErr != nil {
		if errors.Is(closeErr, unix.EINTR) {
			return false, fmt.Errorf("%w: close requalified source parent: %v", ErrInterrupted, closeErr)
		}
		return false, fmt.Errorf("%w: close requalified source parent: %v", ErrDrifted, closeErr)
	}
	return absent, nil
}

func openResolvedTargetAbsenceOnce(
	ctx context.Context,
	parentFD int,
	basename string,
	how *unix.OpenHow,
	open resolvedTargetAbsenceOpen,
	closeFD resolvedTargetAbsenceClose,
) (bool, error) {
	for attempt := 0; attempt < openat2AttemptLimit; attempt++ {
		if err := contextError(ctx); err != nil {
			return false, err
		}
		fd, err := open(parentFD, basename, how)
		if err == nil {
			if fd < 3 {
				if fd >= 0 {
					_ = closeFD(fd)
				}
				return false, fmt.Errorf("%w: constrained source absence open returned a reserved descriptor", ErrDrifted)
			}
			if closeErr := closeFD(fd); closeErr != nil {
				if errors.Is(closeErr, unix.EINTR) {
					return false, fmt.Errorf("%w: close present source target: %v", ErrInterrupted, closeErr)
				}
				return false, fmt.Errorf("%w: close present source target: %v", ErrDrifted, closeErr)
			}
			return false, nil
		}
		if errors.Is(err, unix.ENOENT) {
			return true, nil
		}
		if errors.Is(err, unix.EINTR) {
			return false, fmt.Errorf("%w: constrained source absence open: %v", ErrInterrupted, err)
		}
		if !errors.Is(err, unix.EAGAIN) {
			return false, classifyOpenat2Error(err)
		}
	}
	return false, fmt.Errorf("%w: source absence lookup could not stabilize after %d attempts", ErrDrifted, openat2AttemptLimit)
}
