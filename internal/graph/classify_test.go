package graph

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"testing"
	"time"
)

func TestClassifyGraphErrors(t *testing.T) {
	cases := []struct {
		err    error
		code   string
		status int
	}{
		{&Error{Status: 401, Code: "InvalidAuthenticationToken", Message: "x"}, "SP_UNAUTHORIZED", 502},
		{&Error{Status: 403, Code: "accessDenied", Message: "x"}, "SP_ACCESS_DENIED", 502},
		{&Error{Status: 404, Code: "itemNotFound", Message: "x"}, "SP_NOT_FOUND", 502},
		{&Error{Status: 409, Code: "nameAlreadyExists", Message: "x"}, "NAME_CONFLICT", 409},
		{&Error{Status: 412, Code: "resourceModified"}, "ETAG_MISMATCH", 412},
		{&Error{Status: 423, Code: "notAllowed"}, "SP_LOCKED", 423},
		{&Error{Status: 429, Code: "activityLimitReached", RetryAfter: 7 * time.Second}, "SP_THROTTLED", 503},
		{&Error{Status: 507, Code: "quotaLimitReached"}, "SP_QUOTA_EXCEEDED", 507},
		{&Error{Status: 400, Code: "invalidRequest", Message: "The specified path is too long"}, "SP_REJECTED", 502},
		{&Error{Status: 500, Code: "generalException"}, "SP_UNAVAILABLE", 502},
		{fmt.Errorf("wrapped: %w", context.DeadlineExceeded), "SP_TIMEOUT", 504},
		{&url.Error{Op: "Get", URL: "https://graph.microsoft.com/x", Err: errors.New("dial tcp: lookup graph.microsoft.com: no such host")}, "SP_UNREACHABLE", 502},
	}
	for _, c := range cases {
		got := Classify(c.err)
		if got == nil || got.Code != c.code || got.Status() != c.status {
			t.Errorf("%v -> %+v, want %s/%d", c.err, got, c.code, c.status)
			continue
		}
		if got.Fix() == "" || got.Detail == "" {
			t.Errorf("%s: detail or fix empty", c.code)
		}
	}
	if got := Classify(&Error{Status: 429, RetryAfter: 7 * time.Second}); got.RetryAfter != 7*time.Second {
		t.Error("Retry-After must be kept")
	}
	if Classify(errors.New("something else")) != nil {
		t.Error("unknown errors must not be classified")
	}
}
