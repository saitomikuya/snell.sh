package anyconnect

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/proxy-panel/proxy-panel/internal/database"
	"github.com/proxy-panel/proxy-panel/internal/nodes"
)

type AssetResult struct {
	OK          bool   `json:"ok"`
	Changed     bool   `json:"changed"`
	Kind        string `json:"kind"`
	Fingerprint string `json:"fingerprint"`
	Count       int    `json:"count,omitempty"`
	UpdatedAt   string `json:"updatedAt"`
	Message     string `json:"message"`
}

type Service struct {
	dataDir string
	nodes   *nodes.Store
	client  *http.Client
	now     func() time.Time
}

func New(dataDir string, nodeStore *nodes.Store) *Service {
	return &Service{dataDir: dataDir, nodes: nodeStore, client: safeHTTPClient(), now: time.Now}
}

func (s *Service) Ensure(ctx context.Context, node nodes.Node) error {
	if node.Type != "anyconnect" {
		return nil
	}
	if _, err := os.Stat(s.CertificatePath(node.ID)); err != nil {
		if _, err = s.RefreshCertificates(ctx, node); err != nil {
			return fmt.Errorf("准备 AnyConnect 证书: %w", err)
		}
	}
	if !chinaRoutesUsable(s.ChinaRoutesPath(node.ID)) {
		if _, err := s.RefreshCIDRs(ctx, node); err != nil {
			return fmt.Errorf("准备中国 CIDR: %w", err)
		}
	}
	return nil
}

func chinaRoutesUsable(path string) bool {
	content, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	_, err = ParseChinaCIDRs(content, "cidr")
	return err == nil
}

func (s *Service) CertificatePath(nodeID string) string {
	return filepath.Join(s.dataDir, "anyconnect", "assets", nodeID, "certificate-current", "server.crt.pem")
}

func (s *Service) PrivateKeyPath(nodeID string) string {
	return filepath.Join(s.dataDir, "anyconnect", "assets", nodeID, "certificate-current", "server.key.pem")
}

func (s *Service) ChinaRoutesPath(nodeID string) string {
	return filepath.Join(s.dataDir, "anyconnect", "assets", nodeID, "china-routes.conf")
}

func (s *Service) RefreshCertificates(ctx context.Context, node nodes.Node) (result AssetResult, returnErr error) {
	if node.Type != "anyconnect" {
		return result, errors.New("节点不是 AnyConnect 类型")
	}
	attempted := database.Now()
	defer func() {
		if returnErr != nil {
			_ = s.nodes.RecordAnyConnectCertificate(context.Background(), node.ID, attempted, "", "", "", returnErr.Error())
		}
	}()
	secrets, err := s.nodes.AnyConnectSecrets(ctx, node.ID)
	if err != nil {
		return result, err
	}
	certificatePEM, err := download(ctx, s.client, node.Config.CertificateURL, node.Config.CertificateUsername, secrets.CertificatePassword)
	if err != nil {
		return result, fmt.Errorf("下载服务器证书: %w", err)
	}
	privateKeyPEM, err := download(ctx, s.client, node.Config.PrivateKeyURL, node.Config.CertificateUsername, secrets.CertificatePassword)
	if err != nil {
		return result, fmt.Errorf("下载服务器私钥: %w", err)
	}
	leaf, chainCount, err := parseCertificateChain(certificatePEM)
	if err != nil {
		return result, err
	}
	now := s.now()
	if now.Before(leaf.NotBefore) || !now.Before(leaf.NotAfter) {
		return result, errors.New("服务器证书尚未生效或已经过期")
	}
	if leaf.NotAfter.Sub(now) < 24*time.Hour {
		return result, errors.New("服务器证书剩余有效期不足 24 小时")
	}
	if err = leaf.VerifyHostname(node.Config.ServerName); err != nil {
		return result, fmt.Errorf("服务器证书不匹配 %s: %w", node.Config.ServerName, err)
	}
	staging, err := os.MkdirTemp(filepath.Join(s.dataDir, "runtime"), "anyconnect-certificate-")
	if err != nil {
		return result, err
	}
	defer os.RemoveAll(staging)
	encryptedPath := filepath.Join(staging, "encrypted.key")
	decryptedPath := filepath.Join(staging, "server.key.pem")
	if err = os.WriteFile(encryptedPath, privateKeyPEM, 0600); err != nil {
		return result, err
	}
	if err = decryptPrivateKey(encryptedPath, decryptedPath, secrets.PrivateKeyPassphrase); err != nil {
		return result, err
	}
	keyPublic, err := privateKeyPublicDER(decryptedPath)
	if err != nil {
		return result, err
	}
	if !bytes.Equal(keyPublic, leaf.RawSubjectPublicKeyInfo) {
		return result, errors.New("服务器证书与私钥不匹配")
	}
	decryptedKey, err := os.ReadFile(decryptedPath)
	if err != nil {
		return result, err
	}
	// Fingerprint the complete bundle so an intermediate-chain correction is
	// installed even when the leaf certificate itself did not change.
	fp := fingerprint(certificatePEM)
	state, _ := s.nodes.GetAnyConnectAssetState(ctx, node.ID)
	_, currentErr := os.Stat(s.CertificatePath(node.ID))
	changed := state.CertificateFingerprint != fp || currentErr != nil
	if changed || state.CertificateFingerprint == "" {
		if err = s.installCertificate(node.ID, fp, certificatePEM, decryptedKey); err != nil {
			return result, err
		}
	}
	succeeded := database.Now()
	if err = s.nodes.RecordAnyConnectCertificate(ctx, node.ID, attempted, succeeded, fp, leaf.NotAfter.UTC().Format(time.RFC3339), ""); err != nil {
		return result, err
	}
	message := fmt.Sprintf("证书链校验通过（%d 段），有效期至 %s", chainCount, leaf.NotAfter.UTC().Format(time.RFC3339))
	return AssetResult{OK: true, Changed: changed, Kind: "certificate", Fingerprint: fp, UpdatedAt: succeeded, Message: message}, nil
}

func (s *Service) RefreshCIDRs(ctx context.Context, node nodes.Node) (result AssetResult, returnErr error) {
	if node.Type != "anyconnect" {
		return result, errors.New("节点不是 AnyConnect 类型")
	}
	attempted := database.Now()
	defer func() {
		if returnErr != nil {
			_ = s.nodes.RecordAnyConnectCIDR(context.Background(), node.ID, attempted, "", "", 0, returnErr.Error())
		}
	}()
	requestedSource := strings.TrimSpace(node.Config.ChinaCIDRSourceURL)
	if requestedSource == "" {
		requestedSource = nodes.DefaultChinaCIDRSource
	}
	content, source, err := downloadChinaCIDRs(ctx, s.client, requestedSource)
	if err != nil {
		return result, fmt.Errorf("下载中国 CIDR: %w", err)
	}
	prefixes, err := ParseChinaCIDRs(content, node.Config.ChinaCIDRSourceFormat)
	if err != nil {
		return result, err
	}
	rendered := renderNoRoutes(prefixes)
	fp := fingerprint(rendered)
	state, _ := s.nodes.GetAnyConnectAssetState(ctx, node.ID)
	_, currentErr := os.Stat(s.ChinaRoutesPath(node.ID))
	changed := state.CIDRFingerprint != fp || currentErr != nil
	if changed || state.CIDRFingerprint == "" {
		path := s.ChinaRoutesPath(node.ID)
		if err = os.MkdirAll(filepath.Dir(path), 0750); err != nil {
			return result, err
		}
		candidate := path + ".candidate"
		if err = os.WriteFile(candidate, rendered, 0640); err != nil {
			return result, err
		}
		if err = os.Rename(candidate, path); err != nil {
			_ = os.Remove(candidate)
			return result, err
		}
		if err = repairPersistentOwnership(filepath.Dir(path)); err != nil {
			return result, err
		}
	}
	succeeded := database.Now()
	if err = s.nodes.RecordAnyConnectCIDR(ctx, node.ID, attempted, succeeded, fp, len(prefixes), ""); err != nil {
		return result, err
	}
	message := fmt.Sprintf("已加载 %d 条中国大陆 IPv4 路由", len(prefixes))
	if source != requestedSource {
		message += "（主数据源不可用，已切换 APNIC 官方镜像）"
	}
	return AssetResult{OK: true, Changed: changed, Kind: "cidr", Fingerprint: fp, Count: len(prefixes), UpdatedAt: succeeded, Message: message}, nil
}

func (s *Service) RefreshDue(ctx context.Context) []string {
	list, err := s.nodes.List(ctx)
	if err != nil {
		return nil
	}
	locationName := os.Getenv("TZ")
	if locationName == "" {
		locationName = "Asia/Shanghai"
	}
	location, err := time.LoadLocation(locationName)
	if err != nil {
		location = time.UTC
	}
	now := s.now()
	changed := make([]string, 0)
	for _, node := range list {
		if node.Type != "anyconnect" {
			continue
		}
		state, stateErr := s.nodes.GetAnyConnectAssetState(ctx, node.ID)
		if stateErr != nil {
			continue
		}
		certLast, _ := time.Parse(time.RFC3339Nano, state.CertificateLastSuccess)
		certAttempt, _ := time.Parse(time.RFC3339Nano, state.CertificateLastAttempt)
		if due(node.Config.CertificateSchedule, node.Config.CertificateScheduleDay, node.Config.CertificateScheduleTime, certLast, certAttempt, state.CertificateError, now, location) {
			if result, refreshErr := s.RefreshCertificates(ctx, node); refreshErr == nil && result.Changed {
				changed = append(changed, node.ID)
			}
		}
		cidrLast, _ := time.Parse(time.RFC3339Nano, state.CIDRLastSuccess)
		cidrAttempt, _ := time.Parse(time.RFC3339Nano, state.CIDRLastAttempt)
		if due(node.Config.ChinaCIDRSchedule, node.Config.ChinaCIDRScheduleDay, node.Config.ChinaCIDRScheduleTime, cidrLast, cidrAttempt, state.CIDRError, now, location) {
			if result, refreshErr := s.RefreshCIDRs(ctx, node); refreshErr == nil && result.Changed && !contains(changed, node.ID) {
				changed = append(changed, node.ID)
			}
		}
	}
	return changed
}

func due(frequency string, weekday int, at string, lastSuccess, lastAttempt time.Time, lastError string, now time.Time, location *time.Location) bool {
	if frequency == "manual" {
		return false
	}
	if lastError != "" && !lastAttempt.IsZero() && now.Sub(lastAttempt) < 30*time.Minute {
		return false
	}
	parts := strings.Split(at, ":")
	if len(parts) != 2 {
		return false
	}
	hour, hourErr := strconv.Atoi(parts[0])
	minute, minuteErr := strconv.Atoi(parts[1])
	if hourErr != nil || minuteErr != nil {
		return false
	}
	localNow := now.In(location)
	scheduled := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), hour, minute, 0, 0, location)
	if frequency == "weekly" {
		daysBack := (int(localNow.Weekday()) - weekday + 7) % 7
		scheduled = scheduled.AddDate(0, 0, -daysBack)
	}
	if scheduled.After(localNow) {
		if frequency == "weekly" {
			scheduled = scheduled.AddDate(0, 0, -7)
		} else {
			scheduled = scheduled.AddDate(0, 0, -1)
		}
	}
	return lastSuccess.IsZero() || lastSuccess.Before(scheduled.UTC())
}

func parseCertificateChain(content []byte) (*x509.Certificate, int, error) {
	rest := content
	certificates := make([]*x509.Certificate, 0, 3)
	for {
		block, next := pem.Decode(rest)
		if block == nil {
			if len(strings.TrimSpace(string(rest))) != 0 {
				return nil, 0, errors.New("服务器证书包含无法解析的 PEM 内容")
			}
			break
		}
		if block.Type != "CERTIFICATE" {
			return nil, 0, fmt.Errorf("服务器证书包含不允许的 PEM 块：%s", block.Type)
		}
		certificate, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, 0, fmt.Errorf("解析服务器证书: %w", err)
		}
		certificates = append(certificates, certificate)
		if len(certificates) > 10 {
			return nil, 0, errors.New("服务器证书链不能超过 10 段")
		}
		rest = next
	}
	if len(certificates) == 0 {
		return nil, 0, errors.New("下载内容中没有 X.509 证书")
	}
	for index := 0; index+1 < len(certificates); index++ {
		if err := certificates[index].CheckSignatureFrom(certificates[index+1]); err != nil {
			return nil, 0, fmt.Errorf("服务器证书链第 %d 段无法由下一段签发: %w", index+1, err)
		}
	}
	return certificates[0], len(certificates), nil
}

func decryptPrivateKey(source, target, passphrase string) error {
	path, err := exec.LookPath("openssl")
	if err != nil {
		return errors.New("镜像中缺少 openssl，无法解密服务器私钥")
	}
	command := exec.Command(path, "pkey", "-in", source, "-passin", "stdin", "-out", target)
	command.Stdin = strings.NewReader(passphrase + "\n")
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("服务器私钥无法使用配置的口令解密: %s", strings.TrimSpace(string(output)))
	}
	if err = os.Chmod(target, 0600); err != nil {
		return err
	}
	return nil
}

func privateKeyPublicDER(path string) ([]byte, error) {
	openssl, err := exec.LookPath("openssl")
	if err != nil {
		return nil, errors.New("镜像中缺少 openssl")
	}
	output, err := exec.Command(openssl, "pkey", "-in", path, "-pubout", "-outform", "DER").CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("读取服务器私钥公钥部分: %s", strings.TrimSpace(string(output)))
	}
	return output, nil
}

func (s *Service) installCertificate(nodeID, fp string, certificatePEM, privateKeyPEM []byte) error {
	root := filepath.Join(s.dataDir, "anyconnect", "assets", nodeID)
	certificates := filepath.Join(root, "certificates")
	if err := os.MkdirAll(certificates, 0750); err != nil {
		return err
	}
	versionName := fp[:16]
	versionDir := filepath.Join(certificates, versionName)
	if _, err := os.Stat(versionDir); os.IsNotExist(err) {
		candidate, candidateErr := os.MkdirTemp(certificates, ".candidate-")
		if candidateErr != nil {
			return candidateErr
		}
		defer os.RemoveAll(candidate)
		if candidateErr = os.WriteFile(filepath.Join(candidate, "server.crt.pem"), certificatePEM, 0640); candidateErr != nil {
			return candidateErr
		}
		if candidateErr = os.WriteFile(filepath.Join(candidate, "server.key.pem"), privateKeyPEM, 0600); candidateErr != nil {
			return candidateErr
		}
		if candidateErr = os.Rename(candidate, versionDir); candidateErr != nil {
			if _, statErr := os.Stat(versionDir); statErr != nil {
				return candidateErr
			}
		}
	}
	current := filepath.Join(root, "certificate-current")
	candidateLink := current + ".candidate"
	_ = os.Remove(candidateLink)
	if err := os.Symlink(filepath.Join("certificates", versionName), candidateLink); err != nil {
		return err
	}
	if info, err := os.Lstat(current); err == nil && info.Mode()&os.ModeSymlink == 0 {
		if err = os.RemoveAll(current); err != nil {
			_ = os.Remove(candidateLink)
			return err
		}
	}
	if err := os.Rename(candidateLink, current); err != nil {
		_ = os.Remove(candidateLink)
		return err
	}
	s.pruneCertificateVersions(certificates, versionName)
	if err := repairPersistentOwnership(root); err != nil {
		return err
	}
	return nil
}

func (s *Service) pruneCertificateVersions(root, current string) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	type version struct {
		name    string
		modTime time.Time
	}
	versions := make([]version, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") || entry.Name() == current {
			continue
		}
		info, infoErr := entry.Info()
		if infoErr == nil {
			versions = append(versions, version{name: entry.Name(), modTime: info.ModTime()})
		}
	}
	sort.Slice(versions, func(left, right int) bool { return versions[left].modTime.After(versions[right].modTime) })
	if len(versions) <= 1 {
		return
	}
	for _, old := range versions[1:] {
		_ = os.RemoveAll(filepath.Join(root, old.name))
	}
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
