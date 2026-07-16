package linuxfs

import "errors"

var (
	// ErrUnsupported means the kernel, filesystem, or required safety property
	// cannot establish the requested operation without a weaker fallback.
	ErrUnsupported = errors.New("linux filesystem operation unsupported")
	// ErrDrifted means the held descriptor or named precondition no longer
	// matches the reviewed filesystem facts.
	ErrDrifted = errors.New("linux filesystem precondition drifted")
	// ErrInterrupted means work stopped at a recoverable boundary and callers
	// must preserve or reconcile the recorded state rather than retry blindly.
	ErrInterrupted = errors.New("linux filesystem operation interrupted")
	// ErrRetained means content remains safely staged for recovery and must not
	// be counted as freed space.
	ErrRetained = errors.New("linux filesystem object retained for recovery")
)
