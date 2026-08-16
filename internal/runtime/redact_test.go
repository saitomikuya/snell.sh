package runtime

import (
	"strings"
	"testing"
)

func TestRedact(t *testing.T) {
	value := Redact("\x1b[32mINFO\x1b[0m psk=topsecret Authorization: Bearer abc.def")
	if strings.Contains(value, "\x1b") || !strings.Contains(value, "INFO") {
		t.Fatalf("ANSI control sequences were not removed: %q", value)
	}
	if strings.Contains(value, "topsecret") || strings.Contains(value, "abc.def") {
		t.Fatalf("secret leaked: %s", value)
	}
}
