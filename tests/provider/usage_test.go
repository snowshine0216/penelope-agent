package provider_test

import (
	"testing"

	"github.com/snowshine0216/penelope-agent/internal/provider"
)

func TestUsageZeroValueIsAllZeros(t *testing.T) {
	u := provider.Usage{}
	if u.InputTokens != 0 || u.OutputTokens != 0 {
		t.Fatalf("zero Usage non-zero: %+v", u)
	}
}

func TestResponseHoldsBothMessageAndUsage(t *testing.T) {
	r := &provider.Response{
		Usage: provider.Usage{InputTokens: 1234, OutputTokens: 56},
	}
	if r.Usage.InputTokens != 1234 || r.Usage.OutputTokens != 56 {
		t.Fatalf("usage round-trip failed: %+v", r.Usage)
	}
}
