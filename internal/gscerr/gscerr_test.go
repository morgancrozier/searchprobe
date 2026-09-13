package gscerr

import (
	"errors"
	"fmt"
	"testing"
)

func TestFromAndExitCode(t *testing.T) {
	if From(nil) != nil || ExitCode(nil) != ExitOK {
		t.Fatal("nil handling")
	}
	plain := errors.New("boom")
	if got := From(plain); got.Code != CodeInternal || got.Message != "boom" || got.Cause != plain {
		t.Errorf("From(plain) = %+v", got)
	}
	wrapped := fmt.Errorf("context: %w", New(CodeAuthRequired, "m", "a"))
	if From(wrapped).Code != CodeAuthRequired {
		t.Errorf("From should unwrap: %v", From(wrapped))
	}
	cases := map[string]int{
		CodeAuthRequired:                ExitAuthRequired,
		CodeAuthRevoked:                 ExitAuthRequired,
		CodeAuthScopeInsufficient:       ExitAuthRequired,
		CodeInvalidArgument:             ExitUsage,
		CodeInvalidDateRange:            ExitUsage,
		CodeInvalidDimensionCombination: ExitUsage,
		CodeURLOutsideProperty:          ExitUsage,
		CodePropertyAccessDenied:        ExitFailure,
		CodeQuotaExceeded:               ExitFailure,
		CodeGoogleAPIError:              ExitFailure,
		CodeInternal:                    ExitFailure,
	}
	for code, want := range cases {
		if got := ExitCode(New(code, "m", "")); got != want {
			t.Errorf("%s: exit %d, want %d", code, got, want)
		}
	}
	e := Wrap(plain, CodeNetworkError, "msg", "act")
	if !errors.Is(e, plain) || e.Error() != "NETWORK_ERROR: msg act" {
		t.Errorf("wrap: %v", e)
	}
	if AuthRequired().Action == "" {
		t.Error("AuthRequired must carry an action")
	}
}
