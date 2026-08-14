package main

import (
	"crypto/sha256"
	"fmt"
	"testing"
)

func TestParseSHA256(t *testing.T) {
	want := sha256.Sum256([]byte("firmware fixture"))
	got, err := parseSHA256(fmt.Sprintf("%x", want))
	if err != nil {
		t.Fatalf("parseSHA256: %v", err)
	}
	if got != want {
		t.Fatalf("hash = %x, want %x", got, want)
	}
}

func TestParseSHA256RejectsInvalidValues(t *testing.T) {
	for _, value := range []string{"xyz", "abcd"} {
		if _, err := parseSHA256(value); err == nil {
			t.Fatalf("parseSHA256(%q) succeeded; want error", value)
		}
	}
}
