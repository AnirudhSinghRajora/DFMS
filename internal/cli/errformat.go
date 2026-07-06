package cli

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/AnirudhSinghRajora/DFMS/internal/dfmsclient"
	apierrors "github.com/AnirudhSinghRajora/DFMS/pkg/errors"
)

// formatError renders err for the user: a bold red "Error:" label, the message,
// and—where it helps—the server request ID and an actionable hint. It classifies
// the error so transport failures, missing credentials, and API errors each get
// the most useful guidance.
func formatError(w io.Writer, st *styler, err error) {
	label := st.Red(st.Bold("Error:"))

	var apiErr *dfmsclient.APIError
	var connErr *dfmsclient.ConnectionError
	switch {
	case errors.Is(err, dfmsclient.ErrNoCredentials):
		fmt.Fprintln(w, label, err.Error())
		fmt.Fprintln(w, st.Faint("  hint: run 'dfmsctl auth login' to authenticate."))

	case errors.As(err, &apiErr):
		msg := apiErr.Message
		if msg == "" {
			msg = fmt.Sprintf("request failed with status %d", apiErr.StatusCode)
		}
		if apiErr.Code != "" {
			msg = st.Yellow("["+apiErr.Code+"]") + " " + msg
		}
		fmt.Fprintln(w, label, msg)
		if apiErr.RequestID != "" {
			fmt.Fprintln(w, st.Faint("  request id: "+apiErr.RequestID))
		}
		if hint := apiHint(apiErr); hint != "" {
			fmt.Fprintln(w, st.Faint("  hint: "+hint))
		}

	case errors.As(err, &connErr):
		fmt.Fprintln(w, label, connErr.Error())
		fmt.Fprintln(w, st.Faint("  hint: check the context URL and that the server is reachable."))

	default:
		fmt.Fprintln(w, label, err.Error())
	}
}

// apiHint returns a short, actionable suggestion for an API error, or "" when
// none applies.
func apiHint(e *dfmsclient.APIError) string {
	switch e.Code {
	case apierrors.CodeQuotaExceeded:
		return "free up space or request a higher storage quota."
	case apierrors.CodeRateLimitExceeded:
		return "you are being rate limited; wait a moment and retry."
	}
	if strings.HasPrefix(e.Code, "AUTH_") || e.StatusCode == 401 || e.StatusCode == 403 {
		return "run 'dfmsctl auth login', or check that your account has access."
	}
	return ""
}
