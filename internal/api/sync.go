package api

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/proxy-panel/proxy-panel/internal/nodes"
	"github.com/proxy-panel/proxy-panel/internal/nodesync"
)

type syncImportResult struct {
	NodesCreated      int    `json:"nodesCreated"`
	NodesUpdated      int    `json:"nodesUpdated"`
	UsersCreated      int    `json:"usersCreated"`
	UsersUpdated      int    `json:"usersUpdated"`
	UserQuotasUpdated int    `json:"userQuotasUpdated"`
	PreBackupID       string `json:"preBackupId"`
}

func (s *Server) exportSync(w http.ResponseWriter, r *http.Request) {
	list, err := s.nodes.List(r.Context())
	if err != nil {
		internal(w, err)
		return
	}
	items := make([]nodesync.Node, 0, len(list))
	for _, node := range list {
		if node.Type != "anyconnect" {
			continue
		}
		item := nodesync.Node{ID: node.ID, Type: node.Type, Name: node.Name, DesiredState: node.DesiredState, RuntimeVersion: node.RuntimeVersion, ListenHost: node.ListenHost, ListenPort: node.ListenPort, BackendNodeID: node.BackendNodeID, Config: node.Config}
		secrets, secretErr := s.nodes.AnyConnectSecrets(r.Context(), node.ID)
		if secretErr != nil {
			internal(w, secretErr)
			return
		}
		item.AnyConnectSecrets = &secrets
		users, userErr := s.nodes.ListAnyConnectUsers(r.Context(), node.ID)
		if userErr != nil {
			internal(w, userErr)
			return
		}
		item.Users = make([]nodesync.User, 0, len(users))
		for _, user := range users {
			password, passwordErr := s.nodes.AnyConnectUserPassword(r.Context(), user.ID)
			if passwordErr != nil {
				internal(w, passwordErr)
				return
			}
			userQuota, quotaErr := s.traffic.GetUser(r.Context(), user.ID)
			if quotaErr != nil {
				internal(w, quotaErr)
				return
			}
			item.Users = append(item.Users, nodesync.User{
				Username: user.Username, Password: password, RouteGroup: user.RouteGroup, Enabled: user.Enabled,
				Quota: &nodesync.UserQuota{QuotaBytes: userQuota.QuotaBytes, ResetDay: userQuota.ResetDay},
			})
		}
		items = append(items, item)
	}
	data, err := nodesync.Encode(nodesync.New(items))
	if err != nil {
		internal(w, err)
		return
	}
	filename := "proxy-panel-anyconnect-sync-" + time.Now().UTC().Format("20060102T150405Z") + ".json"
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
	s.store.Audit(r.Context(), "sync.export", "sync", "anyconnect", remoteIP(r), auditJSON(map[string]any{"nodes": len(items), "type": "anyconnect"}), true)
}

func (s *Server) importSync(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, nodesync.MaxFileBytes+1024*1024)
	if err := r.ParseMultipartForm(nodesync.MaxFileBytes); err != nil {
		writeError(w, 400, "INVALID_SYNC_FILE", "请上传不超过 8 MiB 的 JSON 同步文件")
		return
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		writeError(w, 400, "INVALID_SYNC_FILE", "请选择同步 JSON 文件")
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, nodesync.MaxFileBytes+1))
	if err != nil || len(data) > nodesync.MaxFileBytes {
		writeError(w, 400, "INVALID_SYNC_FILE", "同步文件为空或超过 8 MiB 限制")
		return
	}
	payload, err := nodesync.Decode(data)
	if err != nil {
		writeError(w, 400, "INVALID_SYNC_FILE", err.Error())
		return
	}
	if err = validateSyncPayload(payload); err != nil {
		writeError(w, 400, "INVALID_SYNC_FILE", err.Error())
		return
	}
	if err = s.validateSyncPorts(r.Context(), payload); err != nil {
		writeError(w, 409, "SYNC_PORT_CONFLICT", err.Error())
		return
	}
	replaceUsers := strings.EqualFold(r.FormValue("replaceUsers"), "true")
	preBackup, err := s.backups.Create(r.Context())
	if err != nil {
		writeError(w, 503, "PRE_IMPORT_BACKUP_FAILED", "导入前自动备份失败："+err.Error())
		return
	}
	result, err := s.applySyncImport(r, payload, replaceUsers)
	result.PreBackupID = preBackup.ID
	if err != nil {
		s.store.Audit(r.Context(), "sync.import", "sync", "anyconnect", remoteIP(r), auditJSON(map[string]any{"backupId": preBackup.ID, "error": err.Error()}), false)
		writeError(w, 409, "SYNC_IMPORT_FAILED", fmt.Sprintf("导入未完成（已创建备份 %s）：%v", preBackup.ID, err))
		return
	}
	s.store.Audit(r.Context(), "sync.import", "sync", "anyconnect", remoteIP(r), auditJSON(map[string]any{"backupId": preBackup.ID, "nodesCreated": result.NodesCreated, "nodesUpdated": result.NodesUpdated, "usersCreated": result.UsersCreated, "usersUpdated": result.UsersUpdated, "userQuotasUpdated": result.UserQuotasUpdated, "replaceUsers": replaceUsers}), true)
	writeJSON(w, 200, result)
}

// validateSyncPorts mirrors the create-node guard for imported nodes. Import
// used to bypass this check, so a first import could leave a desired-running
// AnyConnect node in an endless restart loop when the host's port 443 was
// already occupied by another service.
func (s *Server) validateSyncPorts(ctx context.Context, payload nodesync.File) error {
	for _, item := range payload.Nodes {
		if item.DesiredState != "running" {
			continue
		}
		target, found, err := s.findSyncTarget(ctx, item)
		if err != nil {
			return err
		}
		// A running local process already owns this exact endpoint; a port
		// probe would report its own listener as a conflict.
		if found && target.ActualState == "running" && target.ListenHost == item.ListenHost && target.ListenPort == item.ListenPort {
			continue
		}
		for _, network := range nodeNetworks(item.Type, item.Config) {
			available, checkErr := s.agent.CheckPort(item.ListenHost, item.ListenPort, network)
			if checkErr == nil && !available.Available {
				return fmt.Errorf("节点 %s 的 %s 端口 %s:%d 已被占用：%s", item.Name, strings.ToUpper(network), item.ListenHost, item.ListenPort, available.Message)
			}
		}
	}
	return nil
}

func (s *Server) applySyncImport(r *http.Request, payload nodesync.File, replaceUsers bool) (syncImportResult, error) {
	ctx := r.Context()
	localIDs := make(map[string]string, len(payload.Nodes))
	changed := make([]string, 0, len(payload.Nodes))
	result := syncImportResult{}
	for _, item := range payload.Nodes {
		local, found, err := s.findSyncTarget(ctx, item)
		if err != nil {
			return result, err
		}
		req := syncCreateRequest(item)
		if err = nodes.Validate(req); err != nil {
			return result, fmt.Errorf("节点 %s 校验失败：%w", item.Name, err)
		}
		if found {
			updated, updateErr := s.nodes.Update(ctx, local.ID, nodes.UpdateRequest{Name: req.Name, RuntimeVersion: req.RuntimeVersion, ListenHost: req.ListenHost, ListenPort: req.ListenPort, BackendNodeID: req.BackendNodeID, Config: req.Config, Secret: req.Secret, CertificatePassword: req.CertificatePassword, PrivateKeyPassphrase: req.PrivateKeyPassphrase}, "sync-import")
			if updateErr != nil {
				return result, fmt.Errorf("更新节点 %s 失败：%w", item.Name, updateErr)
			}
			local = updated
			result.NodesUpdated++
		} else {
			created, createErr := s.nodes.CreateWithID(ctx, item.ID, req, "sync-import")
			if createErr != nil {
				return result, fmt.Errorf("创建节点 %s 失败：%w", item.Name, createErr)
			}
			local = created
			result.NodesCreated++
		}
		localIDs[item.ID] = local.ID
		if desired := item.DesiredState; desired == "running" || desired == "stopped" {
			if err = s.nodes.SetDesired(ctx, local.ID, desired); err != nil {
				return result, fmt.Errorf("设置节点 %s 状态失败：%w", item.Name, err)
			}
		}
		changed = append(changed, local.ID)
	}

	for _, item := range payload.Nodes {
		if item.Type != "anyconnect" {
			continue
		}
		localID := localIDs[item.ID]
		users, err := s.nodes.ListAnyConnectUsers(ctx, localID)
		if err != nil {
			return result, err
		}
		byName := make(map[string]nodes.AnyConnectUser, len(users))
		for _, user := range users {
			byName[user.Username] = user
		}
		seen := map[string]struct{}{}
		for _, imported := range item.Users {
			seen[imported.Username] = struct{}{}
			req := nodes.AnyConnectUserRequest{Username: imported.Username, Password: imported.Password, RouteGroup: imported.RouteGroup, Enabled: imported.Enabled}
			var syncedUser nodes.AnyConnectUser
			if current, ok := byName[imported.Username]; ok {
				syncedUser, err = s.nodes.UpdateAnyConnectUser(ctx, localID, current.ID, req)
				if err != nil {
					return result, fmt.Errorf("更新 AnyConnect 用户 %s 失败：%w", imported.Username, err)
				}
				result.UsersUpdated++
			} else {
				syncedUser, err = s.nodes.CreateAnyConnectUser(ctx, localID, req)
				if err != nil {
					return result, fmt.Errorf("创建 AnyConnect 用户 %s 失败：%w", imported.Username, err)
				}
				result.UsersCreated++
			}
			if imported.Quota != nil {
				if err = s.traffic.SetUserQuota(ctx, syncedUser.ID, imported.Quota.QuotaBytes, imported.Quota.ResetDay); err != nil {
					return result, fmt.Errorf("设置 AnyConnect 用户 %s 流量限额失败：%w", imported.Username, err)
				}
				result.UserQuotasUpdated++
			}
		}
		if replaceUsers {
			for _, current := range users {
				if _, ok := seen[current.Username]; !ok {
					if err = s.nodes.DeleteAnyConnectUser(ctx, localID, current.ID); err != nil {
						return result, fmt.Errorf("删除本地 AnyConnect 用户 %s 失败：%w", current.Username, err)
					}
				}
			}
		}
	}

	for _, id := range changed {
		if _, err := s.agent.Apply(id); err != nil {
			return result, fmt.Errorf("应用节点 %s 失败：%w", id, err)
		}
		_ = s.syncNodeTrafficPolicy(ctx, id)
	}
	return result, nil
}

func (s *Server) findSyncTarget(ctx context.Context, item nodesync.Node) (nodes.Node, bool, error) {
	target, err := s.nodes.Get(ctx, item.ID)
	if err == nil {
		if target.Type != item.Type {
			return nodes.Node{}, false, fmt.Errorf("节点 ID %s 已被不同类型节点占用", item.ID)
		}
		return target, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nodes.Node{}, false, err
	}
	target, err = s.nodes.FindByNameType(ctx, item.Type, item.Name)
	if err == nil {
		return target, true, nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return nodes.Node{}, false, nil
	}
	return nodes.Node{}, false, err
}

func syncCreateRequest(item nodesync.Node) nodes.CreateRequest {
	req := nodes.CreateRequest{Type: item.Type, Name: item.Name, RuntimeVersion: item.RuntimeVersion, ListenHost: item.ListenHost, ListenPort: item.ListenPort, BackendNodeID: item.BackendNodeID, Config: item.Config, Secret: item.Secret}
	if item.AnyConnectSecrets != nil {
		req.CertificatePassword = item.AnyConnectSecrets.CertificatePassword
		req.PrivateKeyPassphrase = item.AnyConnectSecrets.PrivateKeyPassphrase
	}
	return req
}

func validateSyncPayload(payload nodesync.File) error {
	for _, item := range payload.Nodes {
		if item.Type != "anyconnect" {
			return fmt.Errorf("同步文件只支持 AnyConnect 节点（收到 %s）", item.Type)
		}
		if err := nodes.Validate(syncCreateRequest(item)); err != nil {
			return fmt.Errorf("节点 %s 校验失败：%w", item.Name, err)
		}
		for _, user := range item.Users {
			if err := nodes.ValidateAnyConnectUserRequest(nodes.AnyConnectUserRequest{Username: user.Username, Password: user.Password, RouteGroup: user.RouteGroup, Enabled: user.Enabled}); err != nil {
				return fmt.Errorf("AnyConnect 节点 %s 用户 %s 校验失败：%w", item.Name, user.Username, err)
			}
		}
	}
	return nil
}
