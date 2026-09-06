package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/proxy-panel/proxy-panel/internal/nodes"
	panelruntime "github.com/proxy-panel/proxy-panel/internal/runtime"
)

const maxRuntimeUpload = 100 << 20

var stagedRuntimeUploadPattern = regexp.MustCompile(`^runtime-upload-[0-9]+\.part$`)

type runtimeUpload struct {
	Component string
	Version   string
	Format    string
	SHA256    string
	UploadID  string
	Path      string
	Size      int64
}

func (s *Server) uploadUpdate(w http.ResponseWriter, r *http.Request) {
	controller := http.NewResponseController(w)
	_ = controller.SetReadDeadline(time.Now().Add(5 * time.Minute))
	_ = controller.SetWriteDeadline(time.Now().Add(5 * time.Minute))
	upload, err := receiveRuntimeUpload(w, r, s.store.DataDir)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) || strings.Contains(err.Error(), "100 MiB") {
			writeError(w, http.StatusRequestEntityTooLarge, "RUNTIME_UPLOAD_TOO_LARGE", "上传文件不能超过 100 MiB")
			return
		}
		writeError(w, http.StatusBadRequest, "INVALID_RUNTIME_UPLOAD", err.Error())
		return
	}
	defer os.Remove(upload.Path)

	entry, err := s.backups.Create(r.Context())
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "PRE_UPDATE_BACKUP_FAILED", err.Error())
		return
	}
	details := map[string]any{"version": upload.Version, "format": upload.Format, "sha256": upload.SHA256, "size": upload.Size, "backupId": entry.ID}
	install, err := s.agent.InstallUploadedRuntime(upload.Component, upload.Version, upload.Format, upload.UploadID, upload.SHA256)
	if err != nil {
		details["error"] = err.Error()
		s.store.Audit(r.Context(), "update.upload", "update", upload.Component, remoteIP(r), auditJSON(details), false)
		writeError(w, http.StatusConflict, "RUNTIME_UPLOAD_INSTALL_FAILED", err.Error())
		return
	}
	nodesUpdated, err := s.applyRuntimeToNodes(r.Context(), upload.Component, upload.Version, "runtime-upload")
	if err != nil {
		details["error"] = err.Error()
		details["rolledBack"] = true
		s.store.Audit(r.Context(), "update.upload", "update", upload.Component, remoteIP(r), auditJSON(details), false)
		writeError(w, http.StatusConflict, "RUNTIME_APPLY_ROLLED_BACK", err.Error())
		return
	}
	details["nodesUpdated"] = nodesUpdated
	details["architecture"] = install.Architecture
	s.store.Audit(r.Context(), "update.upload", "update", upload.Component, remoteIP(r), auditJSON(details), true)
	updated, _ := s.updates.Status(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "backup": entry, "nodesUpdated": nodesUpdated, "runtime": install, "status": updated})
}

func receiveRuntimeUpload(w http.ResponseWriter, r *http.Request, dataDir string) (runtimeUpload, error) {
	r.Body = http.MaxBytesReader(w, r.Body, maxRuntimeUpload+(2<<20))
	reader, err := r.MultipartReader()
	if err != nil {
		return runtimeUpload{}, errors.New("请求必须使用 multipart/form-data")
	}
	uploadDir := filepath.Join(dataDir, "releases", "uploads")
	if err = os.MkdirAll(uploadDir, 0770); err != nil {
		return runtimeUpload{}, err
	}
	pruneStaleRuntimeUploads(uploadDir)
	fields := make(map[string]string, 4)
	var result runtimeUpload
	cleanup := func() {
		if result.Path != "" {
			_ = os.Remove(result.Path)
		}
	}
	for {
		part, nextErr := reader.NextPart()
		if errors.Is(nextErr, io.EOF) {
			break
		}
		if nextErr != nil {
			cleanup()
			return runtimeUpload{}, nextErr
		}
		if part.FormName() != "file" {
			raw, readErr := io.ReadAll(io.LimitReader(part, 4097))
			_ = part.Close()
			if readErr != nil || len(raw) > 4096 {
				cleanup()
				return runtimeUpload{}, errors.New("上传字段过长或无法读取")
			}
			fields[part.FormName()] = strings.TrimSpace(string(raw))
			continue
		}
		if result.Path != "" || part.FileName() == "" {
			_ = part.Close()
			cleanup()
			return runtimeUpload{}, errors.New("必须且只能上传一个运行时文件")
		}
		temp, createErr := os.CreateTemp(uploadDir, "runtime-upload-*.part")
		if createErr != nil {
			_ = part.Close()
			return runtimeUpload{}, createErr
		}
		result.Path = temp.Name()
		result.UploadID = filepath.Base(result.Path)
		_ = temp.Chmod(0640)
		hash := sha256.New()
		result.Size, err = io.Copy(io.MultiWriter(temp, hash), io.LimitReader(part, maxRuntimeUpload+1))
		closeErr := temp.Close()
		_ = part.Close()
		if err != nil || closeErr != nil {
			cleanup()
			if err != nil {
				return runtimeUpload{}, err
			}
			return runtimeUpload{}, closeErr
		}
		if result.Size == 0 || result.Size > maxRuntimeUpload {
			cleanup()
			return runtimeUpload{}, errors.New("上传文件必须非空且不能超过 100 MiB")
		}
		result.SHA256 = hex.EncodeToString(hash.Sum(nil))
	}
	if result.Path == "" {
		return runtimeUpload{}, errors.New("请选择运行时文件")
	}
	result.Component = fields["component"]
	result.Version = fields["version"]
	result.Format = fields["format"]
	expectedSHA256 := strings.ToLower(fields["sha256"])
	if err = panelruntime.ValidateManualUpload(result.Component, result.Version, result.Format, expectedSHA256); err != nil {
		cleanup()
		return runtimeUpload{}, err
	}
	if result.SHA256 != expectedSHA256 {
		cleanup()
		return runtimeUpload{}, fmt.Errorf("SHA-256 不匹配：上传文件为 %s", result.SHA256)
	}
	return result, nil
}

func pruneStaleRuntimeUploads(directory string) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-time.Hour)
	for _, entry := range entries {
		if !stagedRuntimeUploadPattern.MatchString(entry.Name()) {
			continue
		}
		info, infoErr := entry.Info()
		if infoErr == nil && info.Mode().IsRegular() && info.ModTime().Before(cutoff) {
			_ = os.Remove(filepath.Join(directory, entry.Name()))
		}
	}
}

func (s *Server) applyRuntimeToNodes(ctx context.Context, component, version, source string) (int, error) {
	kind := component
	if kind == "shadowsocks-rust" {
		kind = "ss2022"
	}
	list, err := s.nodes.List(ctx)
	if err != nil {
		return 0, err
	}
	changed := make([]nodes.Node, 0)
	for _, node := range list {
		if node.Type != kind || node.RuntimeVersion == version {
			continue
		}
		req := nodes.UpdateRequest{Name: node.Name, RuntimeVersion: version, ListenHost: node.ListenHost, ListenPort: node.ListenPort, BackendNodeID: node.BackendNodeID, Config: node.Config}
		if _, err = s.nodes.Update(ctx, node.ID, req, source); err == nil {
			changed = append(changed, node)
			_, err = s.agent.Apply(node.ID)
		}
		if err == nil {
			continue
		}
		for index := len(changed) - 1; index >= 0; index-- {
			old := changed[index]
			rollback := nodes.UpdateRequest{Name: old.Name, RuntimeVersion: old.RuntimeVersion, ListenHost: old.ListenHost, ListenPort: old.ListenPort, BackendNodeID: old.BackendNodeID, Config: old.Config}
			_, _ = s.nodes.Update(context.Background(), old.ID, rollback, source+"-rollback")
			_, _ = s.agent.Apply(old.ID)
		}
		return 0, err
	}
	return len(changed), nil
}
