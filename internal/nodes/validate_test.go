package nodes

import (
	"encoding/base64"
	"testing"
)

func TestSS2022KeyLengths(t *testing.T) {
	good := base64.StdEncoding.EncodeToString(make([]byte, 16))
	if err := ValidateSS2022Key("2022-blake3-aes-128-gcm", good); err != nil {
		t.Fatal(err)
	}
	bad := base64.StdEncoding.EncodeToString(make([]byte, 15))
	if err := ValidateSS2022Key("2022-blake3-aes-128-gcm", bad); err == nil {
		t.Fatal("expected invalid length")
	}
}
func TestValidationRejectsHostnamesAsListenAddresses(t *testing.T) {
	req := CreateRequest{Type: "snell", Name: "node", RuntimeVersion: "v5", ListenHost: "example.com", ListenPort: 1234, Config: Config{Version: "v5"}}
	if err := Validate(req); err == nil {
		t.Fatal("expected validation error")
	}
}

func TestValidationAllowsAnOmittedListenAddress(t *testing.T) {
	req := CreateRequest{Type: "snell", Name: "node", RuntimeVersion: "v5", ListenPort: 1234, Config: Config{Version: "v5"}}
	if err := Validate(req); err != nil {
		t.Fatalf("omitted listen address should use %s: %v", DefaultListenHost, err)
	}
}
