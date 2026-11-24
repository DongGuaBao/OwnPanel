package panel

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type dockerEngine struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type dockerRunner func(context.Context, ...string) (string, error)

type dockerRequest struct {
	Target  string `json:"target"`
	Name    string `json:"name"`
	Image   string `json:"image"`
	Network string `json:"network"`
	Restart string `json:"restart"`
	Ports   []struct {
		Address   string `json:"address"`
		Host      int    `json:"host"`
		Container int    `json:"container"`
		Protocol  string `json:"protocol"`
	} `json:"ports"`
	Volumes []struct {
		Name     string `json:"name"`
		Path     string `json:"path"`
		ReadOnly bool   `json:"readOnly"`
	} `json:"volumes"`
}

var dockerName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,254}$`)
var dockerImage = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.:/@-]{0,511}$`)

type dockerInventory struct {
	args   []string
	fields string
}

// Project only the fields used in the table. Labels and container commands can contain credentials.
var dockerInventories = map[string]dockerInventory{
	"containers": {[]string{"container", "ls", "-a", "--no-trunc"}, `"id":{{json .ID}},"name":{{json .Names}},"image":{{json .Image}},"state":{{json .State}},"status":{{json .Status}},"ports":{{json .Ports}},"project":{{json (.Label "com.docker.compose.project")}}`},
	"images":     {[]string{"image", "ls", "--no-trunc", "--digests"}, `"id":{{json .ID}},"repository":{{json .Repository}},"tag":{{json .Tag}},"digest":{{json .Digest}},"size":{{json .Size}}`},
	"networks":   {[]string{"network", "ls", "--no-trunc"}, `"id":{{json .ID}},"name":{{json .Name}},"driver":{{json .Driver}},"scope":{{json .Scope}}`},
	"volumes":    {[]string{"volume", "ls"}, `"name":{{json .Name}},"driver":{{json .Driver}},"scope":{{json .Scope}}`},
}

func dockerAction(resource, action string, req dockerRequest) ([]string, error) {
	switch resource + "/" + action {
	case "images/pull", "images/remove":
		if !dockerImage.MatchString(req.Target) {
			return nil, fmt.Errorf("镜像名称无效")
		}
		if action == "pull" {
			return []string{"image", "pull", "--quiet", req.Target}, nil
		}
		return []string{"image", "rm", req.Target}, nil
	case "containers/create":
		if !dockerName.MatchString(req.Name) || !dockerImage.MatchString(req.Image) {
			return nil, fmt.Errorf("请输入有效的容器名和镜像")
		}
		args := []string{"container", "run", "--detach", "--pull=never", "--name", req.Name}
		switch req.Restart {
		case "no", "always", "unless-stopped", "on-failure":
			args = append(args, "--restart", req.Restart)
		case "":
		default:
			return nil, fmt.Errorf("重启策略无效")
		}
		if req.Network != "" {
			if !dockerName.MatchString(req.Network) {
				return nil, fmt.Errorf("网络名称无效")
			}
			args = append(args, "--network", req.Network)
		}
		if len(req.Ports) > 64 || len(req.Volumes) > 64 {
			return nil, fmt.Errorf("映射数量过多")
		}
		for _, p := range req.Ports {
			ip := net.ParseIP(p.Address)
			if ip == nil || p.Host < 1 || p.Host > 65535 || p.Container < 1 || p.Container > 65535 || (p.Protocol != "tcp" && p.Protocol != "udp") {
				return nil, fmt.Errorf("请检查绑定地址、端口和协议")
			}
			args = append(args, "--publish", net.JoinHostPort(ip.String(), strconv.Itoa(p.Host))+":"+strconv.Itoa(p.Container)+"/"+p.Protocol)
		}
		for _, v := range req.Volumes {
			if !dockerName.MatchString(v.Name) || !strings.HasPrefix(v.Path, "/") || strings.ContainsAny(v.Path, ",\"\x00\r\n") {
				return nil, fmt.Errorf("请使用有效的卷名和容器内绝对路径")
			}
			mount := "type=volume,source=" + v.Name + ",target=" + v.Path
			if v.ReadOnly {
				mount += ",readonly"
			}
			args = append(args, "--mount", mount)
		}
		return append(args, req.Image), nil
	case "networks/create", "volumes/create":
		if !dockerName.MatchString(req.Name) {
			return nil, fmt.Errorf("资源名称无效")
		}
		return []string{strings.TrimSuffix(resource, "s"), "create", req.Name}, nil
	case "containers/start", "containers/stop", "containers/restart", "containers/remove", "networks/remove", "volumes/remove":
		if !dockerName.MatchString(req.Target) {
			return nil, fmt.Errorf("资源标识无效")
		}
		if action == "remove" {
			action = "rm"
		}
		return []string{strings.TrimSuffix(resource, "s"), action, req.Target}, nil
	default:
		return nil, fmt.Errorf("不支持的 Docker 操作")
	}
}

func serveDocker(w http.ResponseWriter, r *http.Request, engines []dockerEngine, runner func(string) dockerRunner) {
	path := strings.TrimPrefix(r.URL.Path, "/api/docker/")
	if path == "engines" && r.Method == http.MethodGet {
		respond(w, map[string]any{"engines": engines})
		return
	}
	id := r.URL.Query().Get("engine")
	var run dockerRunner
	for _, engine := range engines {
		if engine.ID == id {
			run = runner(id)
			break
		}
	}
	if run == nil {
		fail(w, 400, fmt.Errorf("请选择当前主机可用的 Docker 引擎"))
		return
	}
	resource, action, _ := strings.Cut(path, "/")
	inventory, ok := dockerInventories[resource]
	if !ok {
		http.NotFound(w, r)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	if action == "" && r.Method == http.MethodGet {
		out, err := run(ctx, append(inventory.args, "--format", "{"+inventory.fields+"}")...)
		if err != nil {
			fail(w, 502, err)
			return
		}
		rows := []map[string]string{}
		for line := range strings.Lines(out) {
			if strings.TrimSpace(line) == "" {
				continue
			}
			var row map[string]string
			if err := json.Unmarshal([]byte(line), &row); err != nil {
				fail(w, 502, fmt.Errorf("无法读取 Docker 列表：%w", err))
				return
			}
			rows = append(rows, row)
		}
		respond(w, map[string]any{"rows": rows})
		return
	}
	if resource == "containers" && action == "logs" && r.Method == http.MethodGet {
		target := r.URL.Query().Get("target")
		if !dockerName.MatchString(target) {
			fail(w, 400, fmt.Errorf("容器标识无效"))
			return
		}
		out, err := run(ctx, "container", "logs", "--tail", "200", target)
		if err != nil {
			fail(w, 502, err)
			return
		}
		respond(w, map[string]string{"text": out})
		return
	}
	if r.Method != http.MethodPost {
		fail(w, 405, fmt.Errorf("请求方法无效"))
		return
	}
	var req dockerRequest
	if err := decode(w, r, &req); err != nil {
		fail(w, 400, err)
		return
	}
	args, err := dockerAction(resource, action, req)
	if err != nil {
		fail(w, 400, err)
		return
	}
	if resource == "images" && action == "pull" {
		serveDockerPull(w, r, run, args)
		return
	}
	if _, err = run(ctx, args...); err != nil {
		fail(w, 409, err)
		return
	}
	respond(w, map[string]bool{"ok": true})
}

// Whitespace keeps proxies alive while preserving a single JSON response for the client.
func serveDockerPull(w http.ResponseWriter, r *http.Request, run dockerRunner, args []string) {
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Minute)
	defer cancel()
	result := make(chan error, 1)
	go func() { _, err := run(ctx, args...); result <- err }()
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Accel-Buffering", "no")
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case err := <-result:
			if err != nil {
				respond(w, map[string]string{"error": err.Error()})
			} else {
				respond(w, map[string]bool{"ok": true})
			}
			return
		case <-ctx.Done():
			respond(w, map[string]string{"error": "镜像拉取已取消或超时，请刷新列表核实"})
			return
		case <-ticker.C:
			if _, err := w.Write([]byte(" ")); err != nil {
				return
			}
			_ = http.NewResponseController(w).Flush()
		}
	}
}
