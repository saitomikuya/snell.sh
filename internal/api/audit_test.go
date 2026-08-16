package api

import "testing"

func TestDescribeAuditInChinese(t *testing.T) {
	description, target, details := describeAudit("node.client_config.view", "node", `{"protocol":"shadowtls","name":"Vultr"}`)
	if description != "查看完整客户端配置和二维码" || target != "代理节点" {
		t.Fatalf("unexpected description: %q / %q", description, target)
	}
	if details != "名称：Vultr；协议：ShadowTLS" {
		t.Fatalf("unexpected detail description: %q", details)
	}
}
