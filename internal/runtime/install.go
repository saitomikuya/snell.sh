package runtime

import (
	"archive/tar"
	"archive/zip"
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/ulikunitz/xz"
	"gopkg.in/yaml.v3"
)

const maxRuntimeDownload = 100 << 20

//go:embed catalog.yaml
var catalogFS embed.FS

type catalog struct {
	SchemaVersion int                     `yaml:"schema_version"`
	Runtimes      map[string]runtimeEntry `yaml:"runtimes"`
}
type runtimeEntry struct {
	Version string           `yaml:"version"`
	Format  string           `yaml:"format"`
	Binary  string           `yaml:"binary"`
	Assets  map[string]asset `yaml:"assets"`
}
type asset struct {
	URL    string `yaml:"url"`
	SHA256 string `yaml:"sha256"`
}
type InstallRecord struct {
	Kind         string `json:"kind"`
	Version      string `json:"version"`
	Architecture string `json:"architecture"`
	URL          string `json:"url"`
	SHA256       string `json:"sha256"`
	InstalledAt  string `json:"installedAt"`
}

type Installer struct {
	dataDir string
	catalog catalog
	client  *http.Client
}

func NewInstaller(dataDir string) (*Installer, error) {
	raw, err := catalogFS.ReadFile("catalog.yaml")
	if err != nil {
		return nil, err
	}
	var values catalog
	if err = yaml.Unmarshal(raw, &values); err != nil {
		return nil, err
	}
	if values.SchemaVersion != 1 {
		return nil, errors.New("unsupported runtime catalog")
	}
	client := &http.Client{Timeout: 2 * time.Minute, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) > 5 {
			return errors.New("too many redirects")
		}
		return validateDownloadURL(req.URL)
	}}
	return &Installer{dataDir: dataDir, catalog: values, client: client}, nil
}

func (i *Installer) EnsureDefaults(ctx context.Context) []error {
	if runtime.GOOS != "linux" {
		return nil
	}
	var errs []error
	for _, kind := range []string{"snell", "shadowsocks-rust", "shadowtls"} {
		entry := i.catalog.Runtimes[kind]
		target := filepath.Join(i.dataDir, "runtime", kind, entry.Version, runtime.GOARCH, entry.Binary)
		if _, err := os.Stat(target); err == nil {
			continue
		}
		if _, err := i.Install(ctx, kind, entry.Version, runtime.GOARCH); err != nil {
			errs = append(errs, err)
		}
	}
	return errs
}

func (i *Installer) Install(ctx context.Context, kind, version, arch string) (InstallRecord, error) {
	entry, ok := i.catalog.Runtimes[kind]
	if !ok || entry.Version != version {
		return InstallRecord{}, errors.New("runtime version is not in the compatible catalog")
	}
	source, ok := entry.Assets[arch]
	if !ok {
		return InstallRecord{}, errors.New("runtime is unavailable for this architecture")
	}
	parsed, err := url.Parse(source.URL)
	if err != nil {
		return InstallRecord{}, err
	}
	if err = validateDownloadURL(parsed); err != nil {
		return InstallRecord{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, source.URL, nil)
	if err != nil {
		return InstallRecord{}, err
	}
	response, err := i.client.Do(request)
	if err != nil {
		return InstallRecord{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return InstallRecord{}, fmt.Errorf("runtime download returned %s", response.Status)
	}
	if response.ContentLength > maxRuntimeDownload {
		return InstallRecord{}, errors.New("runtime exceeds size limit")
	}
	temp, err := os.CreateTemp(filepath.Join(i.dataDir, "releases"), "runtime-*.download")
	if err != nil {
		return InstallRecord{}, err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	hash := sha256.New()
	written, err := io.Copy(io.MultiWriter(temp, hash), io.LimitReader(response.Body, maxRuntimeDownload+1))
	closeErr := temp.Close()
	if err != nil {
		return InstallRecord{}, err
	}
	if closeErr != nil {
		return InstallRecord{}, closeErr
	}
	if written > maxRuntimeDownload {
		return InstallRecord{}, errors.New("runtime exceeds size limit")
	}
	actual := hex.EncodeToString(hash.Sum(nil))
	if actual != source.SHA256 {
		return InstallRecord{}, fmt.Errorf("runtime checksum mismatch: got %s", actual)
	}
	targetDir := filepath.Join(i.dataDir, "runtime", kind, version, arch)
	if err = os.MkdirAll(targetDir, 0750); err != nil {
		return InstallRecord{}, err
	}
	candidate := filepath.Join(targetDir, "."+entry.Binary+".candidate")
	_ = os.Remove(candidate)
	if err = extractBinary(tempPath, entry.Format, entry.Binary, candidate); err != nil {
		return InstallRecord{}, err
	}
	if err = os.Chmod(candidate, 0750); err != nil {
		return InstallRecord{}, err
	}
	target := filepath.Join(targetDir, entry.Binary)
	if _, err = os.Stat(target); err == nil {
		_ = os.Rename(target, target+".previous")
	}
	if err = os.Rename(candidate, target); err != nil {
		return InstallRecord{}, err
	}
	record := InstallRecord{Kind: kind, Version: version, Architecture: arch, URL: source.URL, SHA256: actual, InstalledAt: time.Now().UTC().Format(time.RFC3339)}
	metadata, _ := json.MarshalIndent(record, "", "  ")
	recordPath := filepath.Join(i.dataDir, "upstream", "checksums", kind+"-"+version+"-"+arch+".json")
	if err = os.WriteFile(recordPath, metadata, 0640); err != nil {
		return InstallRecord{}, err
	}
	return record, nil
}

func validateDownloadURL(value *url.URL) error {
	if value.Scheme != "https" {
		return errors.New("runtime URL must use HTTPS")
	}
	allowed := map[string]bool{"dl.nssurge.com": true, "github.com": true, "objects.githubusercontent.com": true, "release-assets.githubusercontent.com": true}
	if !allowed[strings.ToLower(value.Hostname())] {
		return errors.New("runtime host is not allowlisted")
	}
	return nil
}
func extractBinary(archive, format, binary, target string) error {
	switch format {
	case "raw":
		return copyLimitedFile(archive, target, maxRuntimeDownload)
	case "zip":
		reader, err := zip.OpenReader(archive)
		if err != nil {
			return err
		}
		defer reader.Close()
		for _, file := range reader.File {
			if filepath.Base(file.Name) == binary && !file.FileInfo().IsDir() {
				if file.UncompressedSize64 > maxRuntimeDownload {
					return errors.New("binary exceeds size limit")
				}
				source, err := file.Open()
				if err != nil {
					return err
				}
				defer source.Close()
				return writeLimited(source, target, maxRuntimeDownload)
			}
		}
	case "tar.xz":
		source, err := os.Open(archive)
		if err != nil {
			return err
		}
		defer source.Close()
		xzReader, err := xz.NewReader(source)
		if err != nil {
			return err
		}
		tarReader := tar.NewReader(xzReader)
		for {
			header, err := tarReader.Next()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return err
			}
			if filepath.Base(header.Name) == binary && header.Typeflag == tar.TypeReg {
				if header.Size > maxRuntimeDownload {
					return errors.New("binary exceeds size limit")
				}
				return writeLimited(tarReader, target, maxRuntimeDownload)
			}
		}
	default:
		return errors.New("unsupported runtime archive format")
	}
	return errors.New("runtime archive does not contain expected binary")
}
func copyLimitedFile(source, target string, limit int64) error {
	file, err := os.Open(source)
	if err != nil {
		return err
	}
	defer file.Close()
	return writeLimited(file, target, limit)
}
func writeLimited(source io.Reader, target string, limit int64) error {
	file, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0700)
	if err != nil {
		return err
	}
	written, copyErr := io.Copy(file, io.LimitReader(source, limit+1))
	closeErr := file.Close()
	if copyErr != nil {
		os.Remove(target)
		return copyErr
	}
	if closeErr != nil {
		os.Remove(target)
		return closeErr
	}
	if written > limit {
		os.Remove(target)
		return errors.New("binary exceeds size limit")
	}
	return nil
}
