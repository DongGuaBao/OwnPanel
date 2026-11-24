package panel

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func siteVersionPath(id, version string) (string, error) {
	suffix, matched := strings.CutPrefix(version, id+"-")
	if !validName(id) || !matched || !validName(suffix) {
		return "", fmt.Errorf("配置版本无效")
	}
	dir := filepath.Join(cfg.nginxPrefix, "backups", version)
	raw, err := os.ReadFile(filepath.Join(dir, "site.json"))
	var snapshot site
	if err != nil || json.Unmarshal(raw, &snapshot) != nil || snapshot.ID != id {
		return "", fmt.Errorf("找不到该站点的完整配置版本")
	}
	return dir, nil
}

func registerSiteVersions(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/nginx/sites/{id}/versions", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if !validName(id) {
			fail(w, 400, fmt.Errorf("站点 ID 无效"))
			return
		}
		nginxMu.Lock()
		defer nginxMu.Unlock()
		entries, err := os.ReadDir(filepath.Join(cfg.nginxPrefix, "backups"))
		if err != nil {
			fail(w, 500, err)
			return
		}
		type versionInfo struct {
			ID   string    `json:"id"`
			Time time.Time `json:"time"`
		}
		versions := []versionInfo{}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			if _, err := siteVersionPath(id, entry.Name()); err != nil {
				continue
			}
			if info, err := entry.Info(); err == nil {
				versions = append(versions, versionInfo{entry.Name(), info.ModTime()})
			}
		}
		sort.Slice(versions, func(i, j int) bool { return versions[i].Time.After(versions[j].Time) })
		respond(w, map[string]any{"versions": versions})
	})
	mux.HandleFunc("POST /api/nginx/sites/{id}/versions/{version}/restore", func(w http.ResponseWriter, r *http.Request) {
		id, version := r.PathValue("id"), r.PathValue("version")
		nginxMu.Lock()
		defer nginxMu.Unlock()
		if err := restoreSiteVersion(id, version); err != nil {
			fail(w, 400, err)
			return
		}
		audit("nginx_site_restored", map[string]string{"site": id, "version": version})
		respond(w, map[string]bool{"ok": true})
	})
}

func restoreSiteVersion(id, version string) error {
	source, err := siteVersionPath(id, version)
	if err != nil {
		return err
	}
	stage, err := os.MkdirTemp(filepath.Join(cfg.nginxPrefix, "staging"), "restore-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	if err := copySiteSnapshot(source, stage); err != nil {
		return err
	}
	_, err = activateSiteSnapshot(id, stage)
	return err
}

func copySiteSnapshot(source, stage string) error {
	files, err := os.ReadDir(source)
	if err != nil {
		return err
	}
	for _, file := range files {
		info, err := file.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("快照包含非普通文件")
		}
		data, err := os.ReadFile(filepath.Join(source, file.Name()))
		if err != nil {
			return err
		}
		mode := info.Mode().Perm()
		if file.Name() == "private.key" {
			mode = 0600
		}
		if err := os.WriteFile(filepath.Join(stage, file.Name()), data, mode); err != nil {
			return err
		}
	}
	return nil
}

func activateSiteSnapshot(id, stage string) (string, error) {
	var out string
	var err error
	target := filepath.Join(cfg.nginxPrefix, "sites", id)
	backup := filepath.Join(cfg.nginxPrefix, "backups", id+"-restore-"+token()[:16])
	previous := true
	if err := os.Rename(target, backup); os.IsNotExist(err) {
		previous = false
	} else if err != nil {
		return out, err
	}
	if err = os.Rename(stage, target); err == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		out, err = ng(ctx, "-t")
		if err == nil && nginxRunning() {
			out, err = ng(ctx, "-s", "reload")
		}
		if err != nil {
			if moveErr := os.Rename(target, stage); moveErr != nil {
				return out, fmt.Errorf("恢复验证失败: %v，撤销失败: %w", err, moveErr)
			}
		}
	}
	if err != nil && previous {
		if restoreErr := os.Rename(backup, target); restoreErr != nil {
			return out, fmt.Errorf("恢复验证失败: %v，原配置恢复失败: %w", err, restoreErr)
		}
	}
	return out, err
}
