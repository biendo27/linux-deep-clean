package application

import "errors"

// ErrRootExecution indicates that the user-facing process was started with
// effective UID zero. The CLI deliberately rejects that boundary before it
// parses commands or accesses local state.
var ErrRootExecution = errors.New("ldclean must not run with effective UID 0")

// RequireUnprivileged rejects the privileged main-process boundary. It is
// intentionally pure so callers can prove the rule without changing process
// credentials.
func RequireUnprivileged(euid int) error {
	if euid == 0 {
		return ErrRootExecution
	}
	return nil
}
