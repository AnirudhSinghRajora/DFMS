package cli

import (
	"errors"
	"testing"

	"github.com/AnirudhSinghRajora/DFMS/internal/dfmsclient"
	apierrors "github.com/AnirudhSinghRajora/DFMS/pkg/errors"
)

func TestExitCode(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{"nil", nil, exitOK},
		{"generic", errors.New("boom"), exitGeneric},
		{"usage", &usageError{errors.New("bad flag")}, exitUsage},
		{"no credentials", dfmsclient.ErrNoCredentials, exitAuth},
		{"auth code", &dfmsclient.APIError{Code: apierrors.CodeAuthInvalidCredentials}, exitAuth},
		{"forbidden status", &dfmsclient.APIError{StatusCode: 403}, exitAuth},
		{"not found", &dfmsclient.APIError{Code: apierrors.CodeFileNotFound}, exitNotFound},
		{"quota", &dfmsclient.APIError{Code: apierrors.CodeQuotaExceeded}, exitQuota},
		{"rate limited", &dfmsclient.APIError{Code: apierrors.CodeRateLimitExceeded}, exitRateLimited},
		{"unmapped api error", &dfmsclient.APIError{Code: "WEIRD", StatusCode: 400}, exitGeneric},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ExitCode(tt.err); got != tt.want {
				t.Errorf("ExitCode(%v) = %d, want %d", tt.err, got, tt.want)
			}
		})
	}
}

func TestExitCode_WrappedNoCredentials(t *testing.T) {
	// errors.Is must see through wrapping (Do wraps RoundTrip errors).
	wrapped := errors.Join(errors.New("context"), dfmsclient.ErrNoCredentials)
	if got := ExitCode(wrapped); got != exitAuth {
		t.Errorf("ExitCode(wrapped no-credentials) = %d, want %d", got, exitAuth)
	}
}
