package panel

import (
	"strconv"
	"strings"
)

func fileIdentities(passwd, group string) map[string]any {
	users := []map[string]any{}
	groups := []map[string]any{}
	for _, line := range strings.Split(passwd, "\n") {
		fields := strings.Split(line, ":")
		if len(fields) < 4 {
			continue
		}
		uid, e := strconv.Atoi(fields[2])
		gid, g := strconv.Atoi(fields[3])
		if e == nil && g == nil && uid >= 0 && gid >= 0 {
			users = append(users, map[string]any{"name": fields[0], "uid": uid, "gid": gid})
		}
	}
	for _, line := range strings.Split(group, "\n") {
		fields := strings.Split(line, ":")
		if len(fields) < 3 {
			continue
		}
		gid, e := strconv.Atoi(fields[2])
		if e == nil && gid >= 0 {
			groups = append(groups, map[string]any{"name": fields[0], "gid": gid})
		}
	}
	return map[string]any{"users": users, "groups": groups}
}
