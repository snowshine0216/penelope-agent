package provider_test

import (
	"reflect"
	"testing"

	"github.com/snowshine0216/penelope-agent/internal/provider"
)

func TestExtractRequiredStringsAcceptsTypedSlice(t *testing.T) {
	got := provider.ExtractRequiredStrings([]string{"a", "b"})
	want := []string{"a", "b"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestExtractRequiredStringsAcceptsInterfaceSlice(t *testing.T) {
	got := provider.ExtractRequiredStrings([]interface{}{"a", "b"})
	want := []string{"a", "b"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestExtractRequiredStringsSkipsNonStrings(t *testing.T) {
	got := provider.ExtractRequiredStrings([]interface{}{"a", 42, "b"})
	want := []string{"a", "b"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestExtractRequiredStringsReturnsNilForOtherTypes(t *testing.T) {
	if got := provider.ExtractRequiredStrings(nil); got != nil {
		t.Fatalf("nil input got %v, want nil", got)
	}
	if got := provider.ExtractRequiredStrings("not-a-slice"); got != nil {
		t.Fatalf("string input got %v, want nil", got)
	}
}
