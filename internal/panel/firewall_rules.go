package panel

import (
	"fmt"
	"net"
	"strconv"
	"strings"
)

type firewallChange struct {
	Action   string `json:"action"`
	Port     string `json:"port"`
	Protocol string `json:"protocol"`
	Source   string `json:"source"`
	Policy   string `json:"policy"`
	Number   int    `json:"number"`
	Original string `json:"original,omitempty"`
}

// Edits and deletes identify a configured rule by its exact `ufw show added`
// command, so this also works while UFW is inactive and has no numbered status.
func firewallCommands(change firewallChange, added string) ([][]string, error) {
	if change.Action != "replace" && change.Action != "delete-rule" {
		args, e := firewallArguments(change)
		return [][]string{args}, e
	}
	original := strings.TrimSpace(change.Original)
	found := false
	for _, line := range strings.Split(added, "\n") {
		if strings.TrimSpace(line) == "ufw "+original {
			found = true
			break
		}
	}
	if !found || strings.ContainsAny(original, "\r\n\x00\"'\\") {
		return nil, fmt.Errorf("规则已变化或含复杂引号，请重新读取并使用原始规则管理")
	}
	parts := strings.Fields(original)
	if len(parts) < 2 || (parts[0] != "allow" && parts[0] != "deny" && parts[0] != "reject" && parts[0] != "limit") {
		return nil, fmt.Errorf("此 UFW 规则不支持表单编辑")
	}
	commands := [][]string{append([]string{"--force", "delete"}, parts...)}
	if change.Action == "replace" {
		change.Action = "add"
		args, e := firewallArguments(change)
		if e != nil {
			return nil, e
		}
		commands = append(commands, args)
	}
	return commands, nil
}

func firewallArguments(change firewallChange) ([]string, error) {
	switch change.Action {
	case "add":
		ports := strings.Split(change.Port, ":")
		if len(ports) > 2 {
			return nil, fmt.Errorf("端口格式无效")
		}
		last := 0
		for _, port := range ports {
			value, err := strconv.Atoi(port)
			if err != nil || value < 1 || value > 65535 || value < last {
				return nil, fmt.Errorf("端口范围无效")
			}
			last = value
		}
		if change.Protocol != "tcp" && change.Protocol != "udp" {
			return nil, fmt.Errorf("协议无效")
		}
		if change.Policy != "allow" && change.Policy != "deny" {
			return nil, fmt.Errorf("策略无效")
		}
		if change.Source == "" {
			change.Source = "any"
		}
		if change.Source != "any" && net.ParseIP(change.Source) == nil {
			if _, _, err := net.ParseCIDR(change.Source); err != nil {
				return nil, fmt.Errorf("来源 IP/CIDR 无效")
			}
		}
		return []string{change.Policy, "from", change.Source, "to", "any", "port", change.Port, "proto", change.Protocol}, nil
	case "delete":
		if change.Number < 1 || change.Number > 10000 {
			return nil, fmt.Errorf("规则序号无效")
		}
		return []string{"--force", "delete", strconv.Itoa(change.Number)}, nil
	case "enable":
		return []string{"--force", "enable"}, nil
	case "disable":
		return []string{"disable"}, nil
	default:
		return nil, fmt.Errorf("操作无效")
	}
}
