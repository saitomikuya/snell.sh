// Package nodesync defines the portable, human-readable configuration file
// used to copy AnyConnect nodes and users between independent panels.
package nodesync

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/proxy-panel/proxy-panel/internal/nodes"
)

const (
	Format       = "proxy-panel-sync-v1"
	Version      = 1
	MaxFileBytes = 8 << 20
)

type File struct {
	Format    string `json:"format"`
	Version   int    `json:"version"`
	CreatedAt string `json:"createdAt"`
	Nodes     []Node `json:"nodes"`
}

type Node struct {
	ID                string                   `json:"id"`
	Type              string                   `json:"type"`
	Name              string                   `json:"name"`
	DesiredState      string                   `json:"desiredState,omitempty"`
	RuntimeVersion    string                   `json:"runtimeVersion"`
	ListenHost        string                   `json:"listenHost"`
	ListenPort        int                      `json:"listenPort"`
	BackendNodeID     string                   `json:"backendNodeId,omitempty"`
	Config            nodes.Config             `json:"config"`
	Secret            string                   `json:"secret,omitempty"`
	AnyConnectSecrets *nodes.AnyConnectSecrets `json:"anyconnectSecrets,omitempty"`
	Users             []User                   `json:"users,omitempty"`
}

type User struct {
	Username   string `json:"username"`
	Password   string `json:"password"`
	RouteGroup string `json:"routeGroup"`
	Enabled    bool   `json:"enabled"`
	// Quota is optional so files exported before per-user limits were added
	// remain valid and do not overwrite an existing target limit on import.
	Quota *UserQuota `json:"quota,omitempty"`
}

type UserQuota struct {
	QuotaBytes int64 `json:"quotaBytes"`
	ResetDay   int   `json:"resetDay"`
}

func New(nodesList []Node) File {
	return File{Format: Format, Version: Version, CreatedAt: time.Now().UTC().Format(time.RFC3339), Nodes: nodesList}
}

func Encode(value File) ([]byte, error) {
	if err := Validate(value); err != nil {
		return nil, err
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func Decode(data []byte) (File, error) {
	if len(data) == 0 || len(data) > MaxFileBytes {
		return File{}, errors.New("同步文件为空或超过 8 MiB 限制")
	}
	decoder := json.NewDecoder(io.LimitReader(bytes.NewReader(data), MaxFileBytes+1))
	var value File
	if err := decoder.Decode(&value); err != nil {
		return File{}, errors.New("同步文件不是有效 JSON")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return File{}, errors.New("同步文件包含多个 JSON 文档")
	}
	if err := Validate(value); err != nil {
		return File{}, err
	}
	return value, nil
}

func Validate(value File) error {
	if value.Format != Format || value.Version != Version {
		return fmt.Errorf("不支持的同步文件格式（需要 %s v%d）", Format, Version)
	}
	if len(value.Nodes) > 256 {
		return errors.New("同步文件最多包含 256 个节点")
	}
	seenIDs := map[string]struct{}{}
	for index, item := range value.Nodes {
		if strings.TrimSpace(item.ID) == "" || strings.TrimSpace(item.Type) == "" || strings.TrimSpace(item.Name) == "" {
			return fmt.Errorf("第 %d 个节点缺少 ID、类型或名称", index+1)
		}
		if _, ok := seenIDs[item.ID]; ok {
			return fmt.Errorf("节点 ID 重复：%s", item.ID)
		}
		seenIDs[item.ID] = struct{}{}
		if item.Type != "anyconnect" {
			return errors.New("同步文件只支持 AnyConnect 节点")
		}
		if item.AnyConnectSecrets == nil {
			return fmt.Errorf("AnyConnect 节点 %s 缺少证书凭据", item.Name)
		}
		if len(item.Users) > 4096 {
			return fmt.Errorf("AnyConnect 节点 %s 用户数超过 4096", item.Name)
		}
		seenUsers := map[string]struct{}{}
		for _, user := range item.Users {
			if user.Username == "" || user.Password == "" {
				return fmt.Errorf("AnyConnect 节点 %s 存在缺少用户名或密码的用户", item.Name)
			}
			if user.Quota != nil {
				if user.Quota.QuotaBytes < 0 {
					return fmt.Errorf("AnyConnect 节点 %s 用户 %s 的流量限额不能为负数", item.Name, user.Username)
				}
				if user.Quota.ResetDay < 1 || user.Quota.ResetDay > 28 {
					return fmt.Errorf("AnyConnect 节点 %s 用户 %s 的重置日必须为 1–28", item.Name, user.Username)
				}
			}
			if _, ok := seenUsers[user.Username]; ok {
				return fmt.Errorf("AnyConnect 节点 %s 用户名重复：%s", item.Name, user.Username)
			}
			seenUsers[user.Username] = struct{}{}
		}
	}
	return nil
}
