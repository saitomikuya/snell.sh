package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/proxy-panel/proxy-panel/internal/agent"
	"github.com/proxy-panel/proxy-panel/internal/api"
	"github.com/proxy-panel/proxy-panel/internal/auth"
	"github.com/proxy-panel/proxy-panel/internal/config"
	"github.com/proxy-panel/proxy-panel/internal/database"
	"github.com/proxy-panel/proxy-panel/internal/nodes"
	panelruntime "github.com/proxy-panel/proxy-panel/internal/runtime"
	secretstore "github.com/proxy-panel/proxy-panel/internal/secrets"
	"github.com/proxy-panel/proxy-panel/internal/traffic"
)

var version = "dev"

func main() {
	syscall.Umask(0007)
	command := ""
	if filepath.Base(os.Args[0]) == "panelctl" && len(os.Args) > 1 {
		command = os.Args[1]
	} else if len(os.Args) > 1 {
		command = os.Args[1]
	}
	var err error
	switch command {
	case "server":
		err = runServer()
	case "agent":
		err = runAgent()
	case "healthcheck":
		err = healthcheck()
	case "reset-password":
		err = resetPassword()
	case "cleanup-firewall":
		err = cleanupFirewall()
	case "version":
		fmt.Println(version)
		return
	default:
		fmt.Fprintln(os.Stderr, "usage: panel {server|agent|healthcheck|version} | panelctl {reset-password|cleanup-firewall}")
		os.Exit(2)
	}
	if err != nil {
		log.Printf("%s failed: %v", command, err)
		os.Exit(1)
	}
}

func prepare() (config.Config, *database.Store, *auth.Service, *nodes.Store, error) {
	cfg, err := config.Load()
	if err != nil {
		return cfg, nil, nil, nil, err
	}
	if err = cfg.InitDirectories(); err != nil {
		return cfg, nil, nil, nil, err
	}
	store, err := database.Open(cfg.DataDir)
	if err != nil {
		return cfg, nil, nil, nil, err
	}
	authService := auth.New(store.DB)
	if err = authService.Initialize(context.Background()); err != nil {
		store.Close()
		return cfg, nil, nil, nil, err
	}
	secrets, err := secretstore.Open(store.DB, cfg.DataDir)
	if err != nil {
		store.Close()
		return cfg, nil, nil, nil, err
	}
	return cfg, store, authService, nodes.NewStore(store.DB, secrets), nil
}

func runServer() error {
	cfg, store, authService, nodeStore, err := prepare()
	if err != nil {
		return err
	}
	defer store.Close()
	if err = nodeStore.InitializeDefaults(context.Background()); err != nil {
		return err
	}
	handler := api.New(store, authService, nodeStore, agent.NewClient(cfg.AgentSocket), cfg.SecureCookie, version).Handler()
	server := &http.Server{Addr: cfg.Address(), Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second}
	errChannel := make(chan error, 1)
	go func() {
		log.Printf("Proxy Panel %s listening on http://%s", version, cfg.Address())
		log.Printf("首次安装默认密码为 password，登录后必须立即修改；公网监听必须配置 HTTPS")
		errChannel <- server.ListenAndServe()
	}()
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	select {
	case <-sig:
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return server.Shutdown(ctx)
	case err = <-errChannel:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}
func runAgent() error {
	cfg, store, _, nodeStore, err := prepare()
	if err != nil {
		return err
	}
	defer store.Close()
	manager := agent.NewManager(cfg.DataDir, nodeStore, traffic.New(store.DB))
	if os.Getenv("PANEL_AUTO_INSTALL_RUNTIMES") != "0" {
		installer, installErr := panelruntime.NewInstaller(cfg.DataDir)
		if installErr != nil {
			return installErr
		}
		for _, installErr = range installer.EnsureDefaults(context.Background()) {
			log.Printf("runtime install skipped: %v", installErr)
		}
	}
	defer manager.Shutdown()
	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
		<-sig
		manager.Shutdown()
		os.Exit(0)
	}()
	return agent.Serve(cfg.AgentSocket, manager)
}
func healthcheck() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	client := http.Client{Timeout: 4 * time.Second}
	response, err := client.Get("http://" + cfg.Address() + "/healthz")
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return fmt.Errorf("unhealthy status %d", response.StatusCode)
	}
	return nil
}
func resetPassword() error {
	if len(os.Args) != 3 || os.Args[2] != auth.DefaultPassword {
		return errors.New("usage: panelctl reset-password password")
	}
	_, store, service, _, err := prepare()
	if err != nil {
		return err
	}
	defer store.Close()
	if err = service.ResetPassword(context.Background()); err != nil {
		return err
	}
	store.Audit(context.Background(), "auth.password_reset", "auth", "", "local", "{}", true)
	fmt.Println("管理员密码已重置为 password；全部会话已注销，下次登录必须修改密码")
	return nil
}
func cleanupFirewall() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	result, err := agent.NewClient(cfg.AgentSocket).CleanupFirewall()
	if err != nil {
		return err
	}
	fmt.Println(result.Message)
	return nil
}
