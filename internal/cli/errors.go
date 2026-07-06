package cli

import (
	"errors"
	"strings"

	"github.com/AnirudhSinghRajora/DFMS/internal/dfmsclient"
	apierrors "github.com/AnirudhSinghRajora/DFMS/pkg/errors"
)

// Process exit codes, mapped from error domains so scripts can branch on the
// kind of failure. See docs/CLI_DESIGN.md §9 for the contract.
const (
	exitOK          = 0
	exitGeneric     = 1
	exitUsage       = 2
	exitNetwork     = 3
	exitAuth        = 4
	exitNotFound    = 5
	exitQuota       = 6
	exitRateLimited = 7
)

// usageError marks an error as a command-usage problem (bad flags/args) so it
// maps to the usage exit code. The root command wraps Cobra's flag errors in it.
type usageError struct{ err error }

func (e *usageError) Error() string { return e.err.Error() }
func (e *usageError) Unwrap() error { return e.err }

// ExitCode maps an error returned by command execution to a process exit code.
func ExitCode(err error) int {
	if err == nil {
		return exitOK
	}

	if errors.Is(err, dfmsclient.ErrNoCredentials) {
		return exitAuth
	}

	var apiErr *dfmsclient.APIError
	if errors.As(err, &apiErr) {
		return exitCodeForAPI(apiErr)
	}

	var connErr *dfmsclient.ConnectionError
	if errors.As(err, &connErr) {
		return exitNetwork
	}

	var usageErr *usageError
	if errors.As(err, &usageErr) {
		return exitUsage
	}

	return exitGeneric
}

func exitCodeForAPI(e *dfmsclient.APIError) int {
	switch e.Code {
	case apierrors.CodeFileNotFound:
		return exitNotFound
	case apierrors.CodeQuotaExceeded:
		return exitQuota
	case apierrors.CodeRateLimitExceeded:
		return exitRateLimited
	}
	if strings.HasPrefix(e.Code, "AUTH_") {
		return exitAuth
	}
	switch e.StatusCode {
	case 401, 403:
		return exitAuth
	case 404:
		return exitNotFound
	case 429:
		return exitRateLimited
	}
	return exitGeneric
}
