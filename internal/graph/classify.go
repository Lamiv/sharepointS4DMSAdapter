package graph

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Lamiv/zaks-drive/internal/apperr"
	"github.com/Lamiv/zaks-drive/internal/safelog"
)

// Classify turns an error from the Graph client into a coded error that
// tells the caller or operator what is wrong and how to fix it. It returns
// nil for errors it does not recognise.
func Classify(err error) *apperr.Error {
	if err == nil {
		return nil
	}
	if ae, ok := apperr.As(err); ok {
		return ae
	}
	var ge *Error
	if errors.As(err, &ge) {
		said := fmt.Sprintf("Microsoft Graph answered HTTP %d (%s): %s", ge.Status, ge.Code, shorten(safelog.Redact(ge.Message), 240))
		code := strings.ToLower(ge.Code)
		switch {
		case ge.Status == http.StatusUnauthorized:
			return apperr.New(apperr.SPUnauthorized, "%s", said)
		case ge.Status == http.StatusForbidden:
			return apperr.New(apperr.SPAccessDenied, "%s", said)
		case ge.Status == http.StatusNotFound:
			return apperr.New(apperr.SPNotFound, "%s", said)
		case ge.Status == http.StatusPreconditionFailed:
			return apperr.New(apperr.ETagMismatch, "the document changed since you read it (%s)", ge.Code)
		case ge.Status == http.StatusConflict && code == "namealreadyexists":
			return apperr.New(apperr.NameConflict, "a file or folder with that name already exists")
		case ge.Status == http.StatusLocked || code == "resourcelocked":
			return apperr.New(apperr.SPLocked, "%s", said)
		case ge.Status == http.StatusTooManyRequests || ge.Status == http.StatusServiceUnavailable:
			wait := ge.RetryAfter
			if wait <= 0 {
				wait = time.Second
			}
			return apperr.New(apperr.SPThrottled, "%s; retry in %d s", said, int(wait.Seconds())).WithRetryAfter(wait)
		case ge.Status == http.StatusInsufficientStorage || code == "quotalimitreached" || code == "insufficientstorage":
			return apperr.New(apperr.SPQuotaExceeded, "%s", said)
		case ge.Status >= 500:
			return apperr.New(apperr.SPUnavailable, "%s", said)
		case ge.Status >= 400:
			return apperr.New(apperr.SPRejected, "%s", said)
		}
	}
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return apperr.New(apperr.SPTimeout, "no answer from Microsoft Graph within the time limit")
	}
	var ne net.Error
	var ue *url.Error
	if errors.As(err, &ne) || errors.As(err, &ue) {
		return apperr.New(apperr.SPUnreachable, "could not reach Microsoft Graph (%s)", safelog.Summary(err))
	}
	return nil
}

func shorten(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}
