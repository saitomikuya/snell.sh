package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestInstallUploadedRawRuntime(t *testing.T) {
	if runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" {
		t.Skip("manual uploads support linux amd64 and arm64")
	}
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "upstream", "checksums"), 0750); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(dir, "shadow-tls.upload")
	raw := testELF(runtime.GOARCH)
	if err := os.WriteFile(source, raw, 0600); err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(raw)
	checksum := hex.EncodeToString(hash[:])
	installer, err := NewInstaller(dir)
	if err != nil {
		t.Fatal(err)
	}
	record, err := installer.InstallUploaded(context.Background(), "shadowtls", "v0.3.0", runtime.GOARCH, "raw", source, checksum)
	if err != nil {
		t.Fatal(err)
	}
	if record.URL != "manual-upload" || record.SHA256 != checksum {
		t.Fatalf("unexpected install record: %+v", record)
	}
	target := filepath.Join(dir, "runtime", "shadowtls", "v0.3.0", runtime.GOARCH, "shadow-tls")
	installed, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(installed) != string(raw) {
		t.Fatal("installed runtime differs from uploaded file")
	}
}

func TestInstallUploadedRejectsWrongArchitectureAndChecksum(t *testing.T) {
	if runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" {
		t.Skip("manual uploads support linux amd64 and arm64")
	}
	dir := t.TempDir()
	other := "arm64"
	if runtime.GOARCH == "arm64" {
		other = "amd64"
	}
	raw := testELF(other)
	source := filepath.Join(dir, "runtime.upload")
	if err := os.WriteFile(source, raw, 0600); err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(raw)
	checksum := hex.EncodeToString(hash[:])
	installer, err := NewInstaller(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = installer.InstallUploaded(context.Background(), "shadowtls", "v0.3.0", runtime.GOARCH, "raw", source, strings.Repeat("0", 64)); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("expected checksum mismatch, got %v", err)
	}
	if _, err = installer.InstallUploaded(context.Background(), "shadowtls", "v0.3.0", runtime.GOARCH, "raw", source, checksum); err == nil || !strings.Contains(err.Error(), "architecture mismatch") {
		t.Fatalf("expected architecture mismatch, got %v", err)
	}
}

func TestValidateManualUploadRejectsUnsafeVersion(t *testing.T) {
	if err := ValidateManualUpload("snell", "../../escape", "zip", strings.Repeat("a", 64)); err == nil {
		t.Fatal("unsafe version was accepted")
	}
}

func testELF(arch string) []byte {
	raw := make([]byte, 64)
	copy(raw, []byte("\x7fELF"))
	raw[4] = 2
	raw[5] = 1
	machine := uint16(62)
	if arch == "arm64" {
		machine = 183
	}
	binary.LittleEndian.PutUint16(raw[18:20], machine)
	return raw
}
