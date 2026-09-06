package api

import (
	"encoding/json"
	"fmt"
	"strings"
)

var auditActionLabels = map[string]string{
	"auth.login":                   "登录管理控制台",
	"auth.logout":                  "退出管理控制台",
	"auth.password_changed":        "修改管理员密码",
	"auth.password_reset":          "将管理员密码重置为默认值",
	"node.create":                  "创建节点并应用配置",
	"node.update":                  "更新节点配置",
	"node.delete":                  "删除节点",
	"node.apply":                   "重新应用节点配置",
	"node.start":                   "启动节点",
	"node.stop":                    "停止节点",
	"node.restart":                 "重启节点",
	"node.logs.view":               "查看节点运行日志",
	"node.client_config.view":      "查看完整客户端配置和二维码",
	"traffic.quota.update":         "修改节点流量限额",
	"traffic.pause":                "暂停节点网络访问",
	"traffic.resume":               "恢复节点网络访问",
	"traffic.reset":                "重置节点流量统计",
	"traffic.project.quota.update": "修改项目总流量限额",
	"traffic.project.pause":        "暂停项目全部公网节点",
	"traffic.project.resume":       "恢复项目公网节点",
	"traffic.project.reset":        "重置项目总流量统计",
	"settings.logs.update":         "修改节点日志设置",
	"backup.create":                "创建一致性备份",
	"backup.delete":                "删除备份",
	"backup.restore":               "恢复备份",
	"update.check":                 "检查上游版本",
	"update.apply":                 "一键更新运行时",
	"update.upload":                "上传并适配运行时",
}

var auditTargetLabels = map[string]string{
	"auth":     "管理员账户",
	"node":     "代理节点",
	"backup":   "备份",
	"update":   "更新组件",
	"project":  "整个项目",
	"settings": "系统设置",
}

func auditJSON(values map[string]any) string {
	raw, err := json.Marshal(values)
	if err != nil {
		return "{}"
	}
	return string(raw)
}

func describeAudit(action, targetType, raw string) (string, string, string) {
	description := auditActionLabels[action]
	if description == "" {
		description = "执行操作：" + action
	}
	target := auditTargetLabels[targetType]
	if target == "" {
		target = targetType
	}
	var details map[string]any
	if json.Unmarshal([]byte(raw), &details) != nil {
		return description, target, ""
	}
	parts := make([]string, 0, 4)
	if value, ok := details["name"].(string); ok && value != "" {
		parts = append(parts, "名称："+value)
	}
	if value, ok := details["type"].(string); ok && value != "" {
		parts = append(parts, "类型："+protocolName(value))
	}
	if value, ok := details["protocol"].(string); ok && value != "" {
		parts = append(parts, "协议："+protocolName(value))
	}
	if value, ok := details["version"].(string); ok && value != "" {
		parts = append(parts, "版本："+value)
	}
	if value, ok := details["architecture"].(string); ok && value != "" {
		parts = append(parts, "架构："+value)
	}
	if value, ok := details["format"].(string); ok && value != "" {
		parts = append(parts, "包格式："+value)
	}
	if value, ok := details["revision"].(float64); ok {
		parts = append(parts, fmt.Sprintf("配置修订：r%.0f", value))
	}
	if value, ok := details["tail"].(float64); ok {
		parts = append(parts, fmt.Sprintf("读取最近 %.0f 行", value))
	}
	if value, ok := details["quotaBytes"].(float64); ok {
		parts = append(parts, fmt.Sprintf("限额：%.0f 字节", value))
	}
	if value, ok := details["resetDay"].(float64); ok {
		parts = append(parts, fmt.Sprintf("每月 %0.f 日重置", value))
	}
	if value, ok := details["logMaxMB"].(float64); ok {
		parts = append(parts, fmt.Sprintf("日志总上限：%.0f MB", value))
	}
	if value, ok := details["logEnabled"].(bool); ok {
		if value {
			parts = append(parts, "节点运行日志：开启")
		} else {
			parts = append(parts, "节点运行日志：关闭")
		}
	}
	if value, ok := details["preBackupId"].(string); ok && value != "" {
		parts = append(parts, "恢复前备份："+value)
	}
	if value, ok := details["backupId"].(string); ok && value != "" {
		parts = append(parts, "自动备份："+value)
	}
	if value, ok := details["nodesUpdated"].(float64); ok {
		parts = append(parts, fmt.Sprintf("更新节点：%.0f 个", value))
	}
	if value, ok := details["rolledBack"].(bool); ok && value {
		parts = append(parts, "应用失败并已自动回滚")
	}
	return description, target, strings.Join(parts, "；")
}

func protocolName(value string) string {
	switch value {
	case "snell":
		return "Snell"
	case "ss2022", "shadowsocks-rust":
		return "Shadowsocks 2022"
	case "shadowtls":
		return "ShadowTLS"
	default:
		return value
	}
}
