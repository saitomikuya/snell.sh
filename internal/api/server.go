package api

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/proxy-panel/proxy-panel/internal/agent"
	"github.com/proxy-panel/proxy-panel/internal/auth"
	"github.com/proxy-panel/proxy-panel/internal/backup"
	"github.com/proxy-panel/proxy-panel/internal/configgen"
	"github.com/proxy-panel/proxy-panel/internal/database"
	"github.com/proxy-panel/proxy-panel/internal/metrics"
	"github.com/proxy-panel/proxy-panel/internal/nodes"
	"github.com/proxy-panel/proxy-panel/internal/settings"
	"github.com/proxy-panel/proxy-panel/internal/traffic"
	"github.com/proxy-panel/proxy-panel/internal/updates"
)

//go:embed ui/*
var uiFS embed.FS

type Server struct {
	store        *database.Store
	auth         *auth.Service
	nodes        *nodes.Store
	agent        *agent.Client
	traffic      *traffic.Store
	backups      *backup.Service
	updates      *updates.Service
	settings     *settings.Store
	limiter      *auth.Limiter
	loginGate    chan struct{}
	secureCookie bool
	version      string
}
type sessionContext struct {
	ID         string
	Restricted bool
}
type contextKey string

const sessionKey contextKey = "session"

func New(store *database.Store, authService *auth.Service, nodeStore *nodes.Store, agentClient *agent.Client, secure bool, version string) *Server {
	return &Server{store: store, auth: authService, nodes: nodeStore, agent: agentClient, traffic: traffic.New(store.DB), backups: backup.New(store.DB, store.DataDir), updates: updates.New(store.DB, store.DataDir, version), settings: settings.New(store.DB), limiter: auth.NewLimiter(), loginGate: make(chan struct{}, 1), secureCookie: secure, version: version}
}

func (s *Server) Handler() http.Handler {
	r := chi.NewRouter()
	r.Use(s.securityHeaders, s.noCacheAPI)
	r.Get("/healthz", s.health)
	r.Route("/api/v1", func(r chi.Router) {
		r.Post("/auth/login", s.login)
		r.Group(func(r chi.Router) {
			r.Use(s.requireSession)
			r.Get("/auth/session", s.session)
			r.Group(func(r chi.Router) {
				r.Use(s.requireCSRF)
				r.Post("/auth/change-password", s.changePassword)
				r.Post("/auth/logout", s.logout)
			})
			r.Group(func(r chi.Router) {
				r.Use(s.requireChangedPassword)
				r.Get("/dashboard", s.dashboard)
				r.Get("/system/metrics", s.systemMetrics)
				r.Get("/system/health", s.systemHealth)
				r.Get("/nodes", s.listNodes)
				r.With(s.requireCSRF).Post("/nodes", s.createNode)
				r.Route("/nodes/{id}", func(r chi.Router) {
					r.Get("/", s.getNode)
					r.Get("/client-configs", s.clientConfigs)
					r.Get("/logs", s.nodeLogs)
					r.Get("/anyconnect/users", s.listAnyConnectUsers)
					r.Get("/anyconnect/assets", s.anyConnectAssets)
					r.Get("/anyconnect/profile.xml", s.anyConnectProfile)
					r.Group(func(r chi.Router) {
						r.Use(s.requireCSRF)
						r.Put("/", s.updateNode)
						r.Delete("/", s.deleteNode)
						r.Post("/validate", s.validateNode)
						r.Post("/apply", s.applyNode)
						r.Post("/start", s.startNode)
						r.Post("/stop", s.stopNode)
						r.Post("/restart", s.restartNode)
						r.Post("/anyconnect/users", s.createAnyConnectUser)
						r.Put("/anyconnect/users/{userID}", s.updateAnyConnectUser)
						r.Delete("/anyconnect/users/{userID}", s.deleteAnyConnectUser)
						r.Put("/anyconnect/users/{userID}/traffic/quota", s.setAnyConnectUserQuota)
						r.Post("/anyconnect/users/{userID}/traffic/reset", s.resetAnyConnectUserTraffic)
						r.Post("/anyconnect/assets/{kind}/refresh", s.refreshAnyConnectAsset)
					})
				})
				r.Get("/traffic", s.listTraffic)
				r.Get("/traffic/project", s.projectTraffic)
				r.Get("/settings", s.getSettings)
				r.Group(func(r chi.Router) {
					r.Use(s.requireCSRF)
					r.Post("/sync/export", s.exportSync)
					r.Post("/sync/import", s.importSync)
					r.Put("/traffic/{id}/quota", s.setQuota)
					r.Post("/traffic/{id}/pause", s.pauseTraffic)
					r.Post("/traffic/{id}/resume", s.resumeTraffic)
					r.Post("/traffic/{id}/reset", s.resetTraffic)
					r.Put("/traffic/project/quota", s.setProjectQuota)
					r.Post("/traffic/project/pause", s.pauseProjectTraffic)
					r.Post("/traffic/project/resume", s.resumeProjectTraffic)
					r.Post("/traffic/project/reset", s.resetProjectTraffic)
					r.Put("/settings/logs", s.updateLogSettings)
					r.Post("/backups", s.createBackup)
					r.Post("/backups/{id}/restore", s.restoreBackup)
					r.Delete("/backups/{id}", s.deleteBackup)
					r.Post("/updates/check", s.checkUpdates)
					r.Post("/updates/apply", s.applyUpdate)
					r.Post("/updates/upload", s.uploadUpdate)
				})
				r.Get("/backups", s.listBackups)
				r.Get("/updates/status", s.updateStatus)
				r.Get("/audit", s.auditLogs)
			})
		})
	})
	sub, _ := fs.Sub(uiFS, "ui")
	files := http.FileServer(http.FS(sub))
	r.Handle("/*", spa(files, sub))
	return r
}

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; script-src 'self'; connect-src 'self'")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}
func (s *Server) noCacheAPI(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	ip := remoteIP(r)
	if !s.limiter.Allow(ip) {
		writeError(w, 429, "LOGIN_RATE_LIMITED", "登录尝试过多，请稍后再试")
		return
	}
	var body struct {
		Password string `json:"password"`
	}
	if !decode(w, r, &body) {
		return
	}
	select {
	case s.loginGate <- struct{}{}:
		defer func() { <-s.loginGate }()
	default:
		writeError(w, 429, "LOGIN_BUSY", "登录验证繁忙，请稍后再试")
		return
	}
	session, err := s.auth.Login(r.Context(), body.Password, ip, r.UserAgent())
	if err != nil {
		s.limiter.Failure(ip)
		s.store.Audit(r.Context(), "auth.login", "auth", "", ip, "{}", false)
		writeError(w, 401, "INVALID_CREDENTIALS", "密码错误")
		return
	}
	s.limiter.Success(ip)
	s.setCookies(w, session.ID, session.CSRF, session.ExpiresAt)
	s.store.Audit(r.Context(), "auth.login", "auth", "", ip, "{}", true)
	writeJSON(w, 200, map[string]any{"mustChangePassword": session.Restricted, "expiresAt": session.ExpiresAt})
}
func (s *Server) session(w http.ResponseWriter, r *http.Request) {
	session := currentSession(r)
	writeJSON(w, 200, map[string]any{"authenticated": true, "mustChangePassword": session.Restricted})
}
func (s *Server) changePassword(w http.ResponseWriter, r *http.Request) {
	var body struct {
		NewPassword string `json:"newPassword"`
	}
	if !decode(w, r, &body) {
		return
	}
	if err := s.auth.ChangePassword(r.Context(), body.NewPassword); err != nil {
		writeError(w, 400, "INVALID_PASSWORD", "密码必须为 4–128 个 Unicode 字符，且不能继续使用 password")
		return
	}
	s.clearCookies(w)
	s.store.Audit(r.Context(), "auth.password_changed", "auth", "", remoteIP(r), "{}", true)
	writeJSON(w, 200, map[string]bool{"ok": true})
}
func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	_ = s.auth.Logout(r.Context(), currentSession(r).ID)
	s.store.Audit(r.Context(), "auth.logout", "auth", "", remoteIP(r), "{}", true)
	s.clearCookies(w)
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) requireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie("panel_session")
		if err != nil {
			writeError(w, 401, "UNAUTHORIZED", "请先登录")
			return
		}
		session, err := s.auth.Authenticate(r.Context(), cookie.Value)
		if err != nil {
			s.clearCookies(w)
			writeError(w, 401, "UNAUTHORIZED", "会话已失效")
			return
		}
		ctx := context.WithValue(r.Context(), sessionKey, sessionContext{ID: session.ID, Restricted: session.Restricted})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
func (s *Server) requireChangedPassword(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if currentSession(r).Restricted {
			writeError(w, 403, "PASSWORD_CHANGE_REQUIRED", "必须先修改默认密码")
			return
		}
		next.ServeHTTP(w, r)
	})
}
func (s *Server) requireCSRF(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if origin := r.Header.Get("Origin"); origin != "" && !sameOrigin(origin, r) {
			writeError(w, 403, "CSRF_FAILED", "请求来源无效")
			return
		}
		cookie, err := r.Cookie("panel_csrf")
		header := r.Header.Get("X-CSRF-Token")
		if err != nil || cookie.Value != header || !s.auth.CheckCSRF(r.Context(), currentSession(r).ID, header) {
			writeError(w, 403, "CSRF_FAILED", "CSRF 校验失败")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) dashboard(w http.ResponseWriter, r *http.Request) {
	list, err := s.nodes.List(r.Context())
	if err != nil {
		internal(w, err)
		return
	}
	counts := map[string]int{}
	running := 0
	for _, node := range list {
		counts[node.Type]++
		if node.ActualState == "running" {
			running++
		}
	}
	writeJSON(w, 200, map[string]any{"version": s.version, "nodes": list, "counts": counts, "running": running, "system": metrics.Read()})
}
func (s *Server) systemMetrics(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, metrics.Read())
}
func (s *Server) systemHealth(w http.ResponseWriter, r *http.Request) {
	status, err := s.agent.Status()
	if err != nil {
		writeJSON(w, 200, map[string]any{"status": "degraded", "agent": false, "message": "Runtime Agent 不可用"})
		return
	}
	writeJSON(w, 200, map[string]any{"status": "healthy", "agent": true, "instances": status.Instances})
}
func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DB.PingContext(r.Context()); err != nil {
		writeError(w, 503, "UNHEALTHY", "database unavailable")
		return
	}
	writeJSON(w, 200, map[string]string{"status": "ok"})
}

func (s *Server) listNodes(w http.ResponseWriter, r *http.Request) {
	list, err := s.nodes.List(r.Context())
	if err != nil {
		internal(w, err)
		return
	}
	writeJSON(w, 200, list)
}
func (s *Server) getNode(w http.ResponseWriter, r *http.Request) {
	node, err := s.nodes.Get(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		notFound(w)
		return
	}
	writeJSON(w, 200, node)
}
func (s *Server) createNode(w http.ResponseWriter, r *http.Request) {
	var req nodes.CreateRequest
	if !decode(w, r, &req) {
		return
	}
	if req.ListenHost == "" {
		req.ListenHost = nodes.DefaultListenHost
	}
	for _, network := range nodeNetworks(req.Type, req.Config) {
		if available, checkErr := s.agent.CheckPort(req.ListenHost, req.ListenPort, network); checkErr == nil && !available.Available {
			writeError(w, 409, "PORT_CONFLICT", available.Message)
			return
		}
	}
	node, err := s.nodes.Create(r.Context(), req, "api")
	if err != nil {
		writeError(w, 400, "VALIDATION_FAILED", err.Error())
		return
	}
	result, rpcErr := s.agent.Apply(node.ID)
	if rpcErr == nil {
		_ = s.syncNodeTrafficPolicy(r.Context(), node.ID)
	}
	message := ""
	if rpcErr != nil {
		message = rpcErr.Error()
	}
	s.store.Audit(r.Context(), "node.create", "node", node.ID, remoteIP(r), auditJSON(map[string]any{"type": node.Type, "name": node.Name}), true)
	writeJSON(w, 201, map[string]any{"node": node, "runtime": result, "runtimeMessage": message})
}
func (s *Server) updateNode(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	old, err := s.nodes.Get(r.Context(), id)
	if err != nil {
		notFound(w)
		return
	}
	oldSecret, _ := s.nodes.Secret(r.Context(), id)
	var req nodes.UpdateRequest
	if !decode(w, r, &req) {
		return
	}
	endpointChanged := old.ListenHost != req.ListenHost || old.ListenPort != req.ListenPort
	var dependents []nodes.Node
	if endpointChanged && old.Type != "shadowtls" {
		dependents, err = s.dependentNodes(r.Context(), id)
		if err != nil {
			internal(w, err)
			return
		}
	}
	if endpointChanged {
		for _, network := range nodeNetworks(old.Type, req.Config) {
			if available, checkErr := s.agent.CheckPort(req.ListenHost, req.ListenPort, network); checkErr == nil && !available.Available {
				writeError(w, 409, "PORT_CONFLICT", available.Message)
				return
			}
		}
	}
	node, err := s.nodes.Update(r.Context(), id, req, "api")
	if err != nil {
		writeError(w, 400, "VALIDATION_FAILED", err.Error())
		return
	}
	var result agent.Result
	var rpcErr error
	certificateChanged := old.Type == "anyconnect" && (old.Config.ServerName != node.Config.ServerName || old.Config.CertificateURL != node.Config.CertificateURL || old.Config.PrivateKeyURL != node.Config.PrivateKeyURL || old.Config.CertificateUsername != node.Config.CertificateUsername || req.CertificatePassword != "" || req.PrivateKeyPassphrase != "")
	if certificateChanged {
		_, rpcErr = s.agent.RefreshAnyConnectAsset(id, "certificate")
	}
	if rpcErr == nil {
		result, rpcErr = s.agent.Apply(id)
	}
	dependentApplyAttempted := false
	if rpcErr == nil {
		for _, dependent := range dependents {
			dependentApplyAttempted = true
			if _, applyErr := s.agent.Apply(dependent.ID); applyErr != nil {
				rpcErr = fmt.Errorf("apply dependent node %s: %w", dependent.ID, applyErr)
				break
			}
		}
		_ = s.syncNodeTrafficPolicy(r.Context(), id)
	}
	if rpcErr != nil {
		rollback := nodes.UpdateRequest{Name: old.Name, RuntimeVersion: old.RuntimeVersion, ListenHost: old.ListenHost, ListenPort: old.ListenPort, BackendNodeID: old.BackendNodeID, Config: old.Config, Secret: oldSecret}
		_, _ = s.nodes.Update(r.Context(), id, rollback, "automatic-rollback")
		if certificateChanged {
			_, _ = s.agent.RefreshAnyConnectAsset(id, "certificate")
		}
		_, _ = s.agent.Apply(id)
		if dependentApplyAttempted {
			for _, dependent := range dependents {
				_, _ = s.agent.Apply(dependent.ID)
			}
		}
		s.store.Audit(r.Context(), "node.update", "node", id, remoteIP(r), `{"rolledBack":true}`, false)
		writeError(w, 409, "APPLY_ROLLED_BACK", rpcErr.Error())
		return
	}
	s.store.Audit(r.Context(), "node.update", "node", id, remoteIP(r), auditJSON(map[string]any{"revision": node.Revision, "name": node.Name}), true)
	writeJSON(w, 200, map[string]any{"node": node, "runtime": result})
}

func (s *Server) dependentNodes(ctx context.Context, backendID string) ([]nodes.Node, error) {
	list, err := s.nodes.List(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]nodes.Node, 0)
	for _, node := range list {
		if node.BackendNodeID == backendID {
			result = append(result, node)
		}
	}
	return result, nil
}
func (s *Server) deleteNode(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	node, _ := s.nodes.Get(r.Context(), id)
	_, _ = s.agent.SetBlocked(id, false)
	_, _ = s.agent.Stop(id)
	if node.Type == "anyconnect" {
		_, _ = s.agent.RemoveAnyConnect(id)
	}
	if err := s.nodes.Delete(r.Context(), id); err != nil {
		_ = s.syncNodeTrafficPolicy(r.Context(), id)
		writeError(w, 409, "DEPENDENCY_EXISTS", err.Error())
		return
	}
	s.store.Audit(r.Context(), "node.delete", "node", id, remoteIP(r), auditJSON(map[string]any{"name": node.Name}), true)
	w.WriteHeader(204)
}
func (s *Server) validateNode(w http.ResponseWriter, r *http.Request) {
	var req nodes.CreateRequest
	if !decode(w, r, &req) {
		return
	}
	if req.ListenHost == "" {
		req.ListenHost = nodes.DefaultListenHost
	}
	if err := nodes.Validate(req); err != nil {
		writeError(w, 400, "VALIDATION_FAILED", err.Error())
		return
	}
	writeJSON(w, 200, map[string]bool{"valid": true})
}
func (s *Server) applyNode(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	result, err := s.agent.Apply(id)
	if err != nil {
		s.store.Audit(r.Context(), "node.apply", "node", id, remoteIP(r), auditJSON(map[string]any{"error": err.Error()}), false)
		writeError(w, 409, "APPLY_FAILED", err.Error())
		return
	}
	_ = s.syncNodeTrafficPolicy(r.Context(), id)
	s.store.Audit(r.Context(), "node.apply", "node", id, remoteIP(r), "{}", true)
	writeJSON(w, 200, result)
}
func (s *Server) startNode(w http.ResponseWriter, r *http.Request)   { s.nodeAction(w, r, "start") }
func (s *Server) stopNode(w http.ResponseWriter, r *http.Request)    { s.nodeAction(w, r, "stop") }
func (s *Server) restartNode(w http.ResponseWriter, r *http.Request) { s.nodeAction(w, r, "restart") }
func (s *Server) nodeAction(w http.ResponseWriter, r *http.Request, action string) {
	id := chi.URLParam(r, "id")
	node, _ := s.nodes.Get(r.Context(), id)
	var result agent.Result
	var err error
	switch action {
	case "start":
		err = s.nodes.SetDesired(r.Context(), id, "running")
		if err == nil {
			result, err = s.agent.Start(id)
		}
	case "stop":
		err = s.nodes.SetDesired(r.Context(), id, "stopped")
		if err == nil {
			result, err = s.agent.Stop(id)
		}
	default:
		result, err = s.agent.Restart(id)
	}
	if err != nil {
		writeError(w, 409, "RUNTIME_FAILED", err.Error())
		return
	}
	if action == "stop" {
		_, _ = s.agent.SetBlocked(id, false)
	} else {
		_ = s.syncNodeTrafficPolicy(r.Context(), id)
	}
	s.store.Audit(r.Context(), "node."+action, "node", id, remoteIP(r), auditJSON(map[string]any{"name": node.Name}), true)
	writeJSON(w, 200, result)
}

func (s *Server) syncNodeTrafficPolicy(ctx context.Context, id string) error {
	node, err := s.nodes.Get(ctx, id)
	if err != nil || node.ListenHost == "127.0.0.1" || node.ListenHost == "::1" {
		return err
	}
	counter, err := s.traffic.Get(ctx, id)
	if err != nil {
		return err
	}
	project, err := s.traffic.Project(ctx)
	if err != nil {
		return err
	}
	_, err = s.agent.SetBlocked(id, counter.Paused || project.Paused)
	return err
}
func (s *Server) nodeLogs(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	node, err := s.nodes.Get(r.Context(), id)
	if err != nil {
		notFound(w)
		return
	}
	tail := 300
	if raw := r.URL.Query().Get("tail"); raw != "" {
		if value, parseErr := strconv.Atoi(raw); parseErr == nil && value >= 1 && value <= 1000 {
			tail = value
		}
	}
	lines, err := s.agent.Logs(id, tail)
	if err != nil {
		writeError(w, 503, "AGENT_UNAVAILABLE", err.Error())
		return
	}
	s.store.Audit(r.Context(), "node.logs.view", "node", id, remoteIP(r), auditJSON(map[string]any{"name": node.Name, "tail": tail}), true)
	writeJSON(w, 200, lines)
}

func (s *Server) clientConfigs(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	node, err := s.nodes.Get(r.Context(), id)
	if err != nil {
		notFound(w)
		return
	}
	secret := ""
	if node.Type != "anyconnect" {
		secret, _ = s.nodes.Secret(r.Context(), id)
	}
	var backend *nodes.Node
	backendSecret := ""
	if node.BackendNodeID != "" {
		value, err := s.nodes.Get(r.Context(), node.BackendNodeID)
		if err == nil {
			backend = &value
			backendSecret, _ = s.nodes.Secret(r.Context(), node.BackendNodeID)
		}
	}
	publicHost := strings.TrimSpace(r.URL.Query().Get("host"))
	if publicHost == "" {
		publicHost = requestHost(r.Host)
	}
	cfg, err := configgen.Client(node, secret, publicHost, backend, backendSecret)
	if err != nil {
		internal(w, err)
		return
	}
	s.store.Audit(r.Context(), "node.client_config.view", "node", id, remoteIP(r), auditJSON(map[string]any{"name": node.Name, "protocol": node.Type}), true)
	writeJSON(w, 200, cfg)
}

func (s *Server) listAnyConnectUsers(w http.ResponseWriter, r *http.Request) {
	// Refresh the account-level counters on demand so the AnyConnect page does
	// not have to wait for the agent's periodic sampler after a reload.
	if s.agent != nil {
		_, _ = s.agent.SampleAnyConnectTraffic()
	}
	users, err := s.nodes.ListAnyConnectUsers(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, 400, "NOT_ANYCONNECT", err.Error())
		return
	}
	type userWithTraffic struct {
		nodes.AnyConnectUser
		Traffic traffic.UserCounter `json:"traffic"`
	}
	result := make([]userWithTraffic, 0, len(users))
	for _, user := range users {
		counter, counterErr := s.traffic.GetUser(r.Context(), user.ID)
		if counterErr != nil {
			internal(w, counterErr)
			return
		}
		result = append(result, userWithTraffic{AnyConnectUser: user, Traffic: counter})
	}
	writeJSON(w, 200, result)
}

func (s *Server) setAnyConnectUserQuota(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "userID")
	var body struct {
		QuotaBytes int64 `json:"quotaBytes"`
		ResetDay   int   `json:"resetDay"`
	}
	if !decode(w, r, &body) {
		return
	}
	if err := s.traffic.SetUserQuota(r.Context(), userID, body.QuotaBytes, body.ResetDay); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			notFound(w)
			return
		}
		writeError(w, 400, "VALIDATION_FAILED", err.Error())
		return
	}
	updated, err := s.traffic.GetUser(r.Context(), userID)
	if err != nil {
		internal(w, err)
		return
	}
	s.store.Audit(r.Context(), "anyconnect.user.traffic.quota.update", "anyconnect_user", userID, remoteIP(r), auditJSON(map[string]any{"quotaBytes": body.QuotaBytes, "resetDay": body.ResetDay}), true)
	writeJSON(w, 200, updated)
}

func (s *Server) resetAnyConnectUserTraffic(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "userID")
	if err := s.traffic.ResetUser(r.Context(), userID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			notFound(w)
			return
		}
		internal(w, err)
		return
	}
	s.store.Audit(r.Context(), "anyconnect.user.traffic.reset", "anyconnect_user", userID, remoteIP(r), "{}", true)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) createAnyConnectUser(w http.ResponseWriter, r *http.Request) {
	nodeID := chi.URLParam(r, "id")
	var req nodes.AnyConnectUserRequest
	if !decode(w, r, &req) {
		return
	}
	user, err := s.nodes.CreateAnyConnectUser(r.Context(), nodeID, req)
	if err != nil {
		writeError(w, 400, "VALIDATION_FAILED", err.Error())
		return
	}
	if _, err = s.agent.Apply(nodeID); err != nil {
		_ = s.nodes.DeleteAnyConnectUser(r.Context(), nodeID, user.ID)
		_, _ = s.agent.Apply(nodeID)
		writeError(w, 409, "APPLY_ROLLED_BACK", err.Error())
		return
	}
	s.store.Audit(r.Context(), "anyconnect.user.create", "node", nodeID, remoteIP(r), auditJSON(map[string]any{"username": user.Username, "routeGroup": user.RouteGroup}), true)
	writeJSON(w, 201, user)
}

func (s *Server) updateAnyConnectUser(w http.ResponseWriter, r *http.Request) {
	nodeID, userID := chi.URLParam(r, "id"), chi.URLParam(r, "userID")
	old, err := s.nodes.GetAnyConnectUser(r.Context(), nodeID, userID)
	if err != nil {
		notFound(w)
		return
	}
	oldPassword, err := s.nodes.AnyConnectUserPassword(r.Context(), userID)
	if err != nil {
		internal(w, err)
		return
	}
	var req nodes.AnyConnectUserRequest
	if !decode(w, r, &req) {
		return
	}
	user, err := s.nodes.UpdateAnyConnectUser(r.Context(), nodeID, userID, req)
	if err != nil {
		writeError(w, 400, "VALIDATION_FAILED", err.Error())
		return
	}
	if _, err = s.agent.Apply(nodeID); err != nil {
		_, _ = s.nodes.UpdateAnyConnectUser(r.Context(), nodeID, userID, nodes.AnyConnectUserRequest{Username: old.Username, Password: oldPassword, RouteGroup: old.RouteGroup, Enabled: old.Enabled})
		_, _ = s.agent.Apply(nodeID)
		writeError(w, 409, "APPLY_ROLLED_BACK", err.Error())
		return
	}
	s.store.Audit(r.Context(), "anyconnect.user.update", "node", nodeID, remoteIP(r), auditJSON(map[string]any{"username": user.Username, "routeGroup": user.RouteGroup, "enabled": user.Enabled}), true)
	writeJSON(w, 200, user)
}

func (s *Server) deleteAnyConnectUser(w http.ResponseWriter, r *http.Request) {
	nodeID, userID := chi.URLParam(r, "id"), chi.URLParam(r, "userID")
	old, err := s.nodes.GetAnyConnectUser(r.Context(), nodeID, userID)
	if err != nil {
		notFound(w)
		return
	}
	oldPassword, err := s.nodes.AnyConnectUserPassword(r.Context(), userID)
	if err != nil {
		internal(w, err)
		return
	}
	if err = s.nodes.DeleteAnyConnectUser(r.Context(), nodeID, userID); err != nil {
		internal(w, err)
		return
	}
	if _, err = s.agent.Apply(nodeID); err != nil {
		_, _ = s.nodes.CreateAnyConnectUser(r.Context(), nodeID, nodes.AnyConnectUserRequest{Username: old.Username, Password: oldPassword, RouteGroup: old.RouteGroup, Enabled: old.Enabled})
		_, _ = s.agent.Apply(nodeID)
		writeError(w, 409, "APPLY_ROLLED_BACK", err.Error())
		return
	}
	s.store.Audit(r.Context(), "anyconnect.user.delete", "node", nodeID, remoteIP(r), auditJSON(map[string]any{"username": old.Username}), true)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) anyConnectAssets(w http.ResponseWriter, r *http.Request) {
	nodeID := chi.URLParam(r, "id")
	node, err := s.nodes.Get(r.Context(), nodeID)
	if err != nil {
		notFound(w)
		return
	}
	if node.Type != "anyconnect" {
		writeError(w, 400, "NOT_ANYCONNECT", "节点不是 AnyConnect 类型")
		return
	}
	state, err := s.nodes.GetAnyConnectAssetState(r.Context(), nodeID)
	if err != nil {
		internal(w, err)
		return
	}
	writeJSON(w, 200, state)
}

func (s *Server) refreshAnyConnectAsset(w http.ResponseWriter, r *http.Request) {
	nodeID, kind := chi.URLParam(r, "id"), chi.URLParam(r, "kind")
	if kind != "certificate" && kind != "cidr" {
		writeError(w, 400, "INVALID_ASSET", "只能刷新 certificate 或 cidr")
		return
	}
	result, err := s.agent.RefreshAnyConnectAsset(nodeID, kind)
	if err == nil {
		_, err = s.agent.Apply(nodeID)
	}
	if err != nil {
		s.store.Audit(r.Context(), "anyconnect.asset.refresh", "node", nodeID, remoteIP(r), auditJSON(map[string]any{"kind": kind, "error": err.Error()}), false)
		writeError(w, 409, "ASSET_REFRESH_FAILED", err.Error())
		return
	}
	s.store.Audit(r.Context(), "anyconnect.asset.refresh", "node", nodeID, remoteIP(r), auditJSON(map[string]any{"kind": kind, "changed": result.Changed}), true)
	writeJSON(w, 200, result)
}

func (s *Server) listTraffic(w http.ResponseWriter, r *http.Request) {
	values, err := s.traffic.List(r.Context())
	if err != nil {
		internal(w, err)
		return
	}
	writeJSON(w, 200, values)
}
func (s *Server) projectTraffic(w http.ResponseWriter, r *http.Request) {
	value, err := s.traffic.Project(r.Context())
	if err != nil {
		internal(w, err)
		return
	}
	writeJSON(w, 200, value)
}
func (s *Server) setQuota(w http.ResponseWriter, r *http.Request) {
	var body struct {
		QuotaBytes int64 `json:"quotaBytes"`
		ResetDay   int   `json:"resetDay"`
	}
	if !decode(w, r, &body) {
		return
	}
	if err := s.traffic.SetQuota(r.Context(), chi.URLParam(r, "id"), body.QuotaBytes, body.ResetDay); err != nil {
		writeError(w, 400, "VALIDATION_FAILED", err.Error())
		return
	}
	s.store.Audit(r.Context(), "traffic.quota.update", "node", chi.URLParam(r, "id"), remoteIP(r), auditJSON(map[string]any{"quotaBytes": body.QuotaBytes, "resetDay": body.ResetDay}), true)
	writeJSON(w, 200, map[string]bool{"ok": true})
}
func (s *Server) pauseTraffic(w http.ResponseWriter, r *http.Request)  { s.trafficAction(w, r, true) }
func (s *Server) resumeTraffic(w http.ResponseWriter, r *http.Request) { s.trafficAction(w, r, false) }
func (s *Server) trafficAction(w http.ResponseWriter, r *http.Request, paused bool) {
	id := chi.URLParam(r, "id")
	old, err := s.traffic.Get(r.Context(), id)
	if err != nil {
		notFound(w)
		return
	}
	project, err := s.traffic.Project(r.Context())
	if err != nil {
		internal(w, err)
		return
	}
	if _, err = s.agent.SetBlocked(id, paused || project.Paused); err != nil {
		writeError(w, 503, "FIREWALL_FAILED", err.Error())
		return
	}
	if err = s.traffic.Pause(r.Context(), id, paused); err != nil {
		_, _ = s.agent.SetBlocked(id, old.Paused || project.Paused)
		internal(w, err)
		return
	}
	action := "traffic.resume"
	if paused {
		action = "traffic.pause"
	}
	s.store.Audit(r.Context(), action, "node", id, remoteIP(r), "{}", true)
	writeJSON(w, 200, map[string]bool{"ok": true})
}
func (s *Server) resetTraffic(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := s.traffic.Reset(r.Context(), id); err != nil {
		internal(w, err)
		return
	}
	project, err := s.traffic.Project(r.Context())
	if err != nil {
		internal(w, err)
		return
	}
	if _, err = s.agent.SetBlocked(id, project.Paused); err != nil {
		writeError(w, 503, "FIREWALL_FAILED", err.Error())
		return
	}
	s.store.Audit(r.Context(), "traffic.reset", "node", id, remoteIP(r), "{}", true)
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) setProjectQuota(w http.ResponseWriter, r *http.Request) {
	var body struct {
		QuotaBytes int64 `json:"quotaBytes"`
		ResetDay   int   `json:"resetDay"`
	}
	if !decode(w, r, &body) {
		return
	}
	if err := s.traffic.SetProjectQuota(r.Context(), body.QuotaBytes, body.ResetDay); err != nil {
		writeError(w, 400, "VALIDATION_FAILED", err.Error())
		return
	}
	result, err := s.traffic.SampleProject(r.Context(), 0, 0, time.Now())
	if err != nil {
		internal(w, err)
		return
	}
	if result.Firewall != "" {
		if _, err = s.agent.SetProjectBlocked(result.Paused); err != nil {
			writeError(w, 503, "FIREWALL_FAILED", err.Error())
			return
		}
	}
	s.store.Audit(r.Context(), "traffic.project.quota.update", "project", "all", remoteIP(r), auditJSON(map[string]any{"quotaBytes": body.QuotaBytes, "resetDay": body.ResetDay}), true)
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) pauseProjectTraffic(w http.ResponseWriter, r *http.Request) {
	s.projectTrafficAction(w, r, true)
}
func (s *Server) resumeProjectTraffic(w http.ResponseWriter, r *http.Request) {
	s.projectTrafficAction(w, r, false)
}
func (s *Server) projectTrafficAction(w http.ResponseWriter, r *http.Request, paused bool) {
	old, err := s.traffic.Project(r.Context())
	if err != nil {
		internal(w, err)
		return
	}
	if _, err = s.agent.SetProjectBlocked(paused); err != nil {
		writeError(w, 503, "FIREWALL_FAILED", err.Error())
		return
	}
	if err = s.traffic.PauseProject(r.Context(), paused); err != nil {
		_, _ = s.agent.SetProjectBlocked(old.Paused)
		internal(w, err)
		return
	}
	action := "traffic.project.resume"
	if paused {
		action = "traffic.project.pause"
	}
	s.store.Audit(r.Context(), action, "project", "all", remoteIP(r), "{}", true)
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) resetProjectTraffic(w http.ResponseWriter, r *http.Request) {
	if err := s.traffic.ResetProject(r.Context()); err != nil {
		internal(w, err)
		return
	}
	if _, err := s.agent.SetProjectBlocked(false); err != nil {
		writeError(w, 503, "FIREWALL_FAILED", err.Error())
		return
	}
	s.store.Audit(r.Context(), "traffic.project.reset", "project", "all", remoteIP(r), "{}", true)
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) getSettings(w http.ResponseWriter, r *http.Request) {
	values, err := s.settings.Get(r.Context())
	if err != nil {
		internal(w, err)
		return
	}
	writeJSON(w, 200, values)
}

func (s *Server) updateLogSettings(w http.ResponseWriter, r *http.Request) {
	var body struct {
		LogMaxMB   int   `json:"logMaxMB"`
		LogEnabled *bool `json:"logEnabled"`
	}
	if !decode(w, r, &body) {
		return
	}
	current, err := s.settings.Get(r.Context())
	if err != nil {
		internal(w, err)
		return
	}
	if body.LogMaxMB == 0 {
		body.LogMaxMB = current.LogMaxMB
	}
	enabled := current.LogEnabled
	if body.LogEnabled != nil {
		enabled = *body.LogEnabled
	}
	values, err := s.settings.SetLogs(r.Context(), body.LogMaxMB, enabled)
	if err != nil {
		writeError(w, 400, "VALIDATION_FAILED", err.Error())
		return
	}
	maintenance, maintenanceErr := s.agent.MaintainLogs()
	s.store.Audit(r.Context(), "settings.logs.update", "settings", "logs", remoteIP(r), auditJSON(map[string]any{"logMaxMB": values.LogMaxMB, "logEnabled": values.LogEnabled}), maintenanceErr == nil)
	writeJSON(w, 200, map[string]any{"settings": values, "cleanup": maintenance, "cleanupPending": maintenanceErr != nil})
}
func (s *Server) createBackup(w http.ResponseWriter, r *http.Request) {
	entry, err := s.backups.Create(r.Context())
	if err != nil {
		internal(w, err)
		return
	}
	s.store.Audit(r.Context(), "backup.create", "backup", entry.ID, remoteIP(r), auditJSON(map[string]any{"name": entry.Filename}), true)
	writeJSON(w, 201, entry)
}
func (s *Server) listBackups(w http.ResponseWriter, r *http.Request) {
	entries, err := s.backups.List(r.Context())
	if err != nil {
		internal(w, err)
		return
	}
	writeJSON(w, 200, entries)
}
func (s *Server) deleteBackup(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := s.backups.Delete(r.Context(), id); err != nil {
		notFound(w)
		return
	}
	s.store.Audit(r.Context(), "backup.delete", "backup", id, remoteIP(r), "{}", true)
	w.WriteHeader(204)
}
func (s *Server) restoreBackup(w http.ResponseWriter, r *http.Request) {
	list, err := s.nodes.List(r.Context())
	if err != nil {
		internal(w, err)
		return
	}
	for _, kind := range []string{"shadowtls", "anyconnect", "snell", "ss2022"} {
		for _, node := range list {
			if node.Type == kind {
				_, _ = s.agent.Stop(node.ID)
			}
		}
	}
	preBackup, err := s.backups.Restore(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, 409, "RESTORE_FAILED", err.Error())
		return
	}
	restored, _ := s.nodes.List(r.Context())
	for _, kind := range []string{"snell", "ss2022", "anyconnect", "shadowtls"} {
		for _, node := range restored {
			if node.Type == kind && node.DesiredState == "running" {
				_, _ = s.agent.Apply(node.ID)
			}
		}
	}
	s.store.Audit(r.Context(), "backup.restore", "backup", chi.URLParam(r, "id"), remoteIP(r), `{"preBackupId":"`+preBackup.ID+`"}`, true)
	s.clearCookies(w)
	writeJSON(w, 200, map[string]any{"ok": true, "preRestoreBackup": preBackup, "sessionInvalidated": true})
}
func (s *Server) updateStatus(w http.ResponseWriter, r *http.Request) {
	status, err := s.updates.Status(r.Context())
	if err != nil {
		writeError(w, 503, "UPDATE_METADATA_UNAVAILABLE", err.Error())
		return
	}
	writeJSON(w, 200, status)
}
func (s *Server) checkUpdates(w http.ResponseWriter, r *http.Request) {
	status, err := s.updates.Check(r.Context())
	if err != nil {
		s.store.Audit(r.Context(), "update.check", "update", "all", remoteIP(r), auditJSON(map[string]any{"error": err.Error()}), false)
		writeError(w, 503, "UPDATE_CHECK_FAILED", err.Error())
		return
	}
	s.store.Audit(r.Context(), "update.check", "update", "all", remoteIP(r), "{}", true)
	writeJSON(w, 200, status)
}
func (s *Server) applyUpdate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Component string `json:"component"`
		Version   string `json:"version"`
	}
	if !decode(w, r, &body) {
		return
	}
	status, err := s.updates.Status(r.Context())
	if err != nil {
		writeError(w, 503, "UPDATE_METADATA_UNAVAILABLE", err.Error())
		return
	}
	allowed := false
	for _, component := range status.Components {
		if component.Key == body.Component && component.Category == "runtime" && component.CompatibleVersion == body.Version && component.CanApply {
			allowed = true
			break
		}
	}
	if !allowed {
		writeError(w, 409, "UPDATE_NOT_ALLOWED", "该版本尚未通过兼容性和校验目录验证，或当前已是该版本")
		return
	}
	entry, err := s.backups.Create(r.Context())
	if err != nil {
		writeError(w, 503, "PRE_UPDATE_BACKUP_FAILED", err.Error())
		return
	}
	if _, err = s.agent.InstallRuntime(body.Component, body.Version); err != nil {
		s.store.Audit(r.Context(), "update.apply", "update", body.Component, remoteIP(r), auditJSON(map[string]any{"version": body.Version, "backupId": entry.ID, "error": err.Error()}), false)
		writeError(w, 409, "RUNTIME_INSTALL_FAILED", err.Error())
		return
	}
	nodesUpdated, err := s.applyRuntimeToNodes(r.Context(), body.Component, body.Version, "runtime-update")
	if err != nil {
		s.store.Audit(r.Context(), "update.apply", "update", body.Component, remoteIP(r), auditJSON(map[string]any{"version": body.Version, "backupId": entry.ID, "error": err.Error(), "rolledBack": true}), false)
		writeError(w, 409, "RUNTIME_APPLY_ROLLED_BACK", err.Error())
		return
	}
	s.store.Audit(r.Context(), "update.apply", "update", body.Component, remoteIP(r), auditJSON(map[string]any{"version": body.Version, "backupId": entry.ID, "nodesUpdated": nodesUpdated}), true)
	updated, _ := s.updates.Check(r.Context())
	writeJSON(w, 200, map[string]any{"ok": true, "backup": entry, "nodesUpdated": nodesUpdated, "status": updated})
}
func (s *Server) auditLogs(w http.ResponseWriter, r *http.Request) {
	rows, err := s.store.DB.QueryContext(r.Context(), `SELECT id,action,target_type,target_id,remote_ip,details,success,created_at FROM audit_logs ORDER BY id DESC LIMIT 200`)
	if err != nil {
		internal(w, err)
		return
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var id int
		var action, targetType, targetID, ip, details, created string
		var success bool
		if err = rows.Scan(&id, &action, &targetType, &targetID, &ip, &details, &success, &created); err != nil {
			internal(w, err)
			return
		}
		description, targetDescription, detailDescription := describeAudit(action, targetType, details)
		out = append(out, map[string]any{"id": id, "action": action, "description": description, "targetType": targetType, "targetDescription": targetDescription, "targetId": targetID, "remoteIp": ip, "details": json.RawMessage(details), "detailDescription": detailDescription, "success": success, "createdAt": created})
	}
	writeJSON(w, 200, out)
}

func (s *Server) setCookies(w http.ResponseWriter, token, csrf string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{Name: "panel_session", Value: token, Path: "/", Expires: expires, HttpOnly: true, Secure: s.secureCookie, SameSite: http.SameSiteStrictMode})
	http.SetCookie(w, &http.Cookie{Name: "panel_csrf", Value: csrf, Path: "/", Expires: expires, HttpOnly: false, Secure: s.secureCookie, SameSite: http.SameSiteStrictMode})
}
func (s *Server) clearCookies(w http.ResponseWriter) {
	for _, name := range []string{"panel_session", "panel_csrf"} {
		http.SetCookie(w, &http.Cookie{Name: name, Value: "", Path: "/", MaxAge: -1, HttpOnly: name == "panel_session", Secure: s.secureCookie, SameSite: http.SameSiteStrictMode})
	}
}
func currentSession(r *http.Request) sessionContext {
	value, _ := r.Context().Value(sessionKey).(sessionContext)
	return value
}
func remoteIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return r.RemoteAddr
}
func requestHost(value string) string {
	host, _, err := net.SplitHostPort(value)
	if err == nil {
		return host
	}
	return strings.Trim(value, "[]")
}
func sameOrigin(origin string, r *http.Request) bool {
	return origin == "http://"+r.Host || origin == "https://"+r.Host
}
func decode(w http.ResponseWriter, r *http.Request, target any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1024*1024)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeError(w, 400, "INVALID_JSON", "请求格式无效")
		return false
	}
	return true
}
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}
func internal(w http.ResponseWriter, err error) {
	log.Printf("api internal error: %v", err)
	writeError(w, 500, "INTERNAL_ERROR", "内部错误")
}
func notFound(w http.ResponseWriter) { writeError(w, 404, "NOT_FOUND", "资源不存在") }
func spa(files http.Handler, sub fs.FS) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path != "" {
			if _, err := fs.Stat(sub, path); err == nil {
				files.ServeHTTP(w, r)
				return
			}
		}
		r.URL.Path = "/"
		files.ServeHTTP(w, r)
	})
}
func nodeNetworks(kind string, config nodes.Config) []string {
	if kind == "anyconnect" {
		if config.UDPEnabled {
			return []string{"tcp", "udp"}
		}
		return []string{"tcp"}
	}
	if kind != "ss2022" {
		return []string{"tcp"}
	}
	switch config.Mode {
	case "tcp_only":
		return []string{"tcp"}
	case "udp_only":
		return []string{"udp"}
	default:
		return []string{"tcp", "udp"}
	}
}
