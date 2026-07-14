package api

import "testing"

func TestInputStringMaxBytes(t *testing.T) {
	if InputStringMaxBytes != 4_096 {
		t.Fatalf("InputStringMaxBytes = %d, want 4096", InputStringMaxBytes)
	}
}
