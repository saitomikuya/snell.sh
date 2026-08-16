package updates

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const maxMetadataSize = 2 << 20

//go:embed upstream-adapter.yaml
var embeddedAdapter embed.FS

type Adapter struct {
	SchemaVersion int                   `yaml:"schema_version" json:"schemaVersion"`
	Repositories  map[string]Repository `yaml:"repositories" json:"repositories"`
	Runtimes      map[string]Runtime    `yaml:"runtimes" json:"runtimes"`
}
type Repository struct {
	URL             string `yaml:"url" json:"url"`
	SupportedCommit string `yaml:"supported_commit" json:"supportedCommit"`
}
type Runtime struct {
	Source         string   `yaml:"source" json:"source"`
	Version        string   `yaml:"version" json:"version"`
	Architectures  []string `yaml:"architectures" json:"architectures"`
	Redistribution string   `yaml:"redistribution" json:"redistribution"`
}
type Component struct {
	Key               string `json:"key"`
	Name              string `json:"name"`
	Category          string `json:"category"`
	CurrentVersion    string `json:"currentVersion"`
	LatestVersion     string `json:"latestVersion"`
	CompatibleVersion string `json:"compatibleVersion,omitempty"`
	UpdateAvailable   bool   `json:"updateAvailable"`
	Compatible        bool   `json:"compatible"`
	CanApply          bool   `json:"canApply"`
	SourceURL         string `json:"sourceUrl"`
	Message           string `json:"message"`
}
type Status struct {
	PanelVersion string      `json:"panelVersion"`
	CheckedAt    string      `json:"checkedAt"`
	Compatible   bool        `json:"compatible"`
	Message      string      `json:"message"`
	Adapter      Adapter     `json:"adapter"`
	Components   []Component `json:"components"`
}
type Service struct {
	db         *sql.DB
	path       string
	statusPath string
	version    string
	client     *http.Client
}

func New(db *sql.DB, dataDir, version string) *Service {
	root := filepath.Join(dataDir, "upstream", "manifests")
	return &Service{
		db: db, path: filepath.Join(root, "upstream-adapter.yaml"), statusPath: filepath.Join(root, "update-status.json"), version: version,
		client: &http.Client{Timeout: 12 * time.Second},
	}
}

func (s *Service) Status(ctx context.Context) (Status, error) {
	adapter, err := s.adapter()
	if err != nil {
		return Status{}, err
	}
	if raw, readErr := os.ReadFile(s.statusPath); readErr == nil {
		var cached Status
		if json.Unmarshal(raw, &cached) == nil && cached.Adapter.SchemaVersion == adapter.SchemaVersion {
			cached.PanelVersion = s.version
			if err = s.refreshCurrent(ctx, &cached); err != nil {
				return Status{}, err
			}
			return cached, nil
		}
	}
	return s.baseline(ctx, adapter)
}

func (s *Service) Check(ctx context.Context) (Status, error) {
	adapter, err := s.adapter()
	if err != nil {
		return Status{}, err
	}
	status, err := s.baseline(ctx, adapter)
	if err != nil {
		return Status{}, err
	}
	status.CheckedAt = time.Now().UTC().Format(time.RFC3339)
	status.Message = "已对比原 GitHub 脚本仓库和官方运行时版本；可更新项仅在通过兼容目录校验后开放一键更新。"

	latest := map[string]string{}
	latest["snell"], err = s.latestSnell(ctx)
	if err != nil {
		setCheckError(status.Components, "snell", err)
	}
	if value, checkErr := s.latestRelease(ctx, "shadowsocks/shadowsocks-rust"); checkErr != nil {
		setCheckError(status.Components, "shadowsocks-rust", checkErr)
	} else {
		latest["shadowsocks-rust"] = value
	}
	if value, checkErr := s.latestRelease(ctx, "ihciah/shadow-tls"); checkErr != nil {
		setCheckError(status.Components, "shadowtls", checkErr)
	} else {
		latest["shadowtls"] = value
	}
	for index := range status.Components {
		component := &status.Components[index]
		if component.Category != "runtime" || latest[component.Key] == "" {
			continue
		}
		component.LatestVersion = latest[component.Key]
		component.Compatible = component.LatestVersion == component.CompatibleVersion
		component.UpdateAvailable = component.CurrentVersion != "未使用" && !versionsMatch(component.CurrentVersion, component.LatestVersion)
		component.CanApply = component.UpdateAvailable && component.Compatible
		switch {
		case component.CanApply:
			component.Message = "发现已验证的新版本，可自动备份并一键更新"
		case component.UpdateAvailable:
			component.Message = "发现上游新版本，需先加入校验目录后才能更新"
		default:
			component.Message = "当前已是最新兼容版本"
		}
	}
	for _, item := range []struct{ key, repo string }{{"snell_scripts", "jinqians/snell.sh"}, {"ss_2022_scripts", "jinqians/ss-2022.sh"}} {
		value, checkErr := s.latestCommit(ctx, item.repo)
		if checkErr != nil {
			setCheckError(status.Components, item.key, checkErr)
			continue
		}
		for index := range status.Components {
			component := &status.Components[index]
			if component.Key != item.key {
				continue
			}
			component.LatestVersion = shortCommit(value)
			component.UpdateAvailable = !strings.HasPrefix(value, component.CurrentVersion) && !strings.HasPrefix(component.CurrentVersion, value)
			component.Compatible = !component.UpdateAvailable
			if component.UpdateAvailable {
				component.Message = "原脚本已有变化；面板不会直接执行远程脚本，等待适配审查"
			} else {
				component.Message = "已与当前适配的原脚本提交一致"
			}
		}
	}
	status.Compatible = adapter.SchemaVersion == 1
	raw, _ := json.MarshalIndent(status, "", "  ")
	if err = os.WriteFile(s.statusPath, raw, 0640); err != nil {
		return Status{}, err
	}
	return status, nil
}

func (s *Service) baseline(ctx context.Context, adapter Adapter) (Status, error) {
	current, err := s.currentRuntimeVersions(ctx)
	if err != nil {
		return Status{}, err
	}
	components := make([]Component, 0, len(adapter.Runtimes)+len(adapter.Repositories))
	for _, key := range []string{"snell", "shadowsocks-rust", "shadowtls"} {
		item, ok := adapter.Runtimes[key]
		if !ok {
			item, ok = adapter.Runtimes[strings.ReplaceAll(key, "-", "_")]
		}
		if !ok {
			continue
		}
		version := strings.Join(current[key], "、")
		if version == "" {
			version = "未使用"
		}
		components = append(components, Component{Key: key, Name: componentName(key), Category: "runtime", CurrentVersion: version, LatestVersion: item.Version, CompatibleVersion: item.Version, Compatible: true, SourceURL: runtimeSourceURL(key), Message: "点击“检查上游版本”获取实时结果"})
	}
	for _, key := range []string{"snell_scripts", "ss_2022_scripts"} {
		item, ok := adapter.Repositories[key]
		if !ok {
			continue
		}
		components = append(components, Component{Key: key, Name: componentName(key), Category: "script", CurrentVersion: shortCommit(item.SupportedCommit), LatestVersion: "待检查", Compatible: true, SourceURL: item.URL, Message: "用于对照原 GitHub 脚本模块，不会直接覆盖面板"})
	}
	return Status{PanelVersion: s.version, Compatible: adapter.SchemaVersion == 1, Message: "点击“检查上游版本”后实时对比；更新前自动创建一致性备份。", Adapter: adapter, Components: components}, nil
}

func (s *Service) adapter() (Adapter, error) {
	raw, err := embeddedAdapter.ReadFile("upstream-adapter.yaml")
	if err != nil {
		return Adapter{}, err
	}
	_ = os.MkdirAll(filepath.Dir(s.path), 0750)
	_ = os.WriteFile(s.path, raw, 0640)
	var adapter Adapter
	if err = yaml.Unmarshal(raw, &adapter); err != nil {
		return Adapter{}, err
	}
	if adapter.SchemaVersion != 1 {
		return Adapter{}, errors.New("unsupported update adapter schema")
	}
	return adapter, nil
}

func (s *Service) currentRuntimeVersions(ctx context.Context) (map[string][]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT type,runtime_version FROM nodes ORDER BY type,runtime_version`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := map[string]map[string]bool{"snell": {}, "shadowsocks-rust": {}, "shadowtls": {}}
	for rows.Next() {
		var kind, version string
		if err = rows.Scan(&kind, &version); err != nil {
			return nil, err
		}
		if kind == "ss2022" {
			kind = "shadowsocks-rust"
		}
		values[kind][version] = true
	}
	result := map[string][]string{}
	for kind, versions := range values {
		for version := range versions {
			result[kind] = append(result[kind], version)
		}
		sort.Strings(result[kind])
	}
	return result, rows.Err()
}

func (s *Service) refreshCurrent(ctx context.Context, status *Status) error {
	current, err := s.currentRuntimeVersions(ctx)
	if err != nil {
		return err
	}
	for index := range status.Components {
		component := &status.Components[index]
		if component.Category != "runtime" {
			continue
		}
		component.CurrentVersion = strings.Join(current[component.Key], "、")
		if component.CurrentVersion == "" {
			component.CurrentVersion = "未使用"
		}
		component.UpdateAvailable = component.CurrentVersion != "未使用" && component.LatestVersion != "检查失败" && !versionsMatch(component.CurrentVersion, component.LatestVersion)
		component.CanApply = component.UpdateAvailable && component.Compatible && component.LatestVersion == component.CompatibleVersion
	}
	return nil
}

func (s *Service) latestRelease(ctx context.Context, repo string) (string, error) {
	var result struct {
		TagName string `json:"tag_name"`
	}
	if err := s.getJSON(ctx, "https://api.github.com/repos/"+repo+"/releases/latest", &result); err != nil {
		return "", err
	}
	if result.TagName == "" {
		return "", errors.New("GitHub release 未返回版本")
	}
	return result.TagName, nil
}

func (s *Service) latestCommit(ctx context.Context, repo string) (string, error) {
	var result struct {
		SHA string `json:"sha"`
	}
	if err := s.getJSON(ctx, "https://api.github.com/repos/"+repo+"/commits/main", &result); err != nil {
		return "", err
	}
	if result.SHA == "" {
		return "", errors.New("GitHub 未返回提交")
	}
	return result.SHA, nil
}

func (s *Service) latestSnell(ctx context.Context) (string, error) {
	endpoints := []string{
		"https://manual.nssurge.com/others/snell.html",
		"https://kb.nssurge.com/surge-knowledge-base/zh/release-notes/snell",
		"https://kb.nssurge.com/surge-knowledge-base/release-notes/snell",
	}
	for _, endpoint := range endpoints {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			continue
		}
		request.Header.Set("User-Agent", "proxy-panel/"+s.version)
		response, err := s.client.Do(request)
		if err != nil {
			continue
		}
		raw, readErr := io.ReadAll(io.LimitReader(response.Body, maxMetadataSize+1))
		response.Body.Close()
		if response.StatusCode != http.StatusOK || readErr != nil || len(raw) > maxMetadataSize {
			continue
		}
		match := regexp.MustCompile(`snell-server-v(5\.[0-9]+\.[0-9]+[a-z0-9]*)`).FindSubmatch(raw)
		if len(match) == 2 {
			return "v" + string(match[1]), nil
		}
	}
	return "", errors.New("未能从 Snell 官方发布说明识别 v5 最新版本")
}

func (s *Service) getJSON(ctx context.Context, endpoint string, target any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("User-Agent", "proxy-panel/"+s.version)
	response, err := s.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("上游接口返回 %s", response.Status)
	}
	return json.NewDecoder(io.LimitReader(response.Body, maxMetadataSize)).Decode(target)
}

func setCheckError(components []Component, key string, err error) {
	for index := range components {
		if components[index].Key == key {
			components[index].Compatible = false
			components[index].CanApply = false
			components[index].LatestVersion = "检查失败"
			components[index].Message = err.Error()
		}
	}
}
func versionsMatch(current, version string) bool {
	for _, value := range strings.Split(current, "、") {
		if value != version {
			return false
		}
	}
	return current != ""
}
func shortCommit(value string) string {
	if len(value) > 12 {
		return value[:12]
	}
	return value
}
func componentName(key string) string {
	return map[string]string{"snell": "Snell Server", "shadowsocks-rust": "shadowsocks-rust", "shadowtls": "ShadowTLS", "snell_scripts": "原 Snell 管理脚本", "ss_2022_scripts": "原 SS-2022 管理脚本"}[key]
}
func runtimeSourceURL(key string) string {
	return map[string]string{"snell": "https://kb.nssurge.com/surge-knowledge-base/zh/release-notes/snell", "shadowsocks-rust": "https://github.com/shadowsocks/shadowsocks-rust/releases", "shadowtls": "https://github.com/ihciah/shadow-tls/releases"}[key]
}
