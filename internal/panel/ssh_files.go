package panel

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/pkg/sftp"
)

func (d *sshDriver) sftp(ctx context.Context) (*sftp.Client, error) {
	c, e := d.connect(ctx, true)
	if e != nil {
		return nil, e
	}
	type result struct {
		client *sftp.Client
		err    error
	}
	ch := make(chan result)
	go func() {
		client, e := d.openSFTP(ctx, c)
		select {
		case ch <- result{client, e}:
		case <-ctx.Done():
			if client != nil {
				client.Close()
			}
		}
	}()
	select {
	case v := <-ch:
		return v.client, v.err
	case <-ctx.Done():
		d.invalidate(c)
		return nil, ctx.Err()
	}
}

// This is a navigation boundary, not an SSH sandbox: the account's OS permissions
// remain the security boundary, including concurrent changes by that account.
func (d *sshDriver) checkedPath(c *sftp.Client, p string, missing bool) (string, error) {
	if strings.ContainsAny(p, "\x00") {
		return "", fmt.Errorf("路径无效")
	}
	full := path.Clean("/" + strings.TrimPrefix(p, "/"))
	resolved, e := c.RealPath(full)
	if e != nil && missing {
		parent, pe := c.RealPath(path.Dir(full))
		if pe != nil {
			return "", pe
		}
		resolved = path.Join(parent, path.Base(full))
		e = nil
	}
	if e != nil {
		return "", e
	}
	return resolved, nil
}
func (d *sshDriver) files(w http.ResponseWriter, r *http.Request, ctx context.Context) {
	if endpoint := strings.TrimPrefix(r.URL.Path, "/api/"); (endpoint == "files/directory-sizes" || endpoint == "files/search") && r.Method == "GET" {
		// SSH-only hosts have no Agent IPC bridge; do not open SFTP or walk
		// the remote tree just to render a cached directory-size column.
		respond(w, map[string]any{"available": false, "entries": map[string]any{}, "next": ""})
		return
	}
	caps, err := d.Probe(ctx)
	if err != nil {
		fail(w, 502, err)
		return
	}
	if !caps["terminal.root"].Supported {
		fail(w, 403, fmt.Errorf("全盘文件管理需要 root 登录或有效 sudo"))
		return
	}

	c, e := d.sftp(ctx)
	if e != nil {
		fail(w, 502, e)
		return
	}
	defer c.Close()
	stop := context.AfterFunc(ctx, func() { _ = c.Close() })
	defer stop()
	endpoint := strings.TrimPrefix(r.URL.Path, "/api/")
	switch {
	case endpoint == "files/identities" && r.Method == "GET":
		out, err := d.run(ctx, true, `python3 - <<'OWNPANEL_IDENTITIES'
import json,pwd,grp
print(json.dumps({'users':[{'name':u.pw_name,'uid':u.pw_uid,'gid':u.pw_gid} for u in pwd.getpwall()],'groups':[{'name':g.gr_name,'gid':g.gr_gid} for g in grp.getgrall()]}))
OWNPANEL_IDENTITIES`)
		if err != nil {
			fail(w, 502, err)
			return
		}
		var result map[string]any
		if err = json.Unmarshal([]byte(out), &result); err != nil {
			fail(w, 502, err)
			return
		}
		respond(w, result)
	case endpoint == "files" && r.Method == "GET":
		p, e := d.checkedPath(c, r.URL.Query().Get("path"), false)
		if e != nil {
			fail(w, 400, e)
			return
		}
		entries, e := c.ReadDir(p)
		if e != nil {
			fail(w, 403, e)
			return
		}
		out := []map[string]any{}
		for _, st := range entries {

			uid, gid := uint32(0), uint32(0)
			if s, ok := st.Sys().(*sftp.FileStat); ok {
				uid, gid = s.UID, s.GID
			}
			out = append(out, map[string]any{"name": st.Name(), "directory": func() bool {
				if st.Mode()&os.ModeSymlink != 0 {
					resolved, err := c.Stat(path.Join(p, st.Name()))
					return err == nil && resolved.IsDir()
				}
				return st.IsDir()
			}(), "symlink": st.Mode()&os.ModeSymlink != 0, "size": st.Size(), "mode": fmt.Sprintf("%04o", unixPermission(st.Mode())), "uid": uid, "gid": gid, "modified": st.ModTime()})
		}
		sort.Slice(out, func(i, j int) bool {
			if out[i]["directory"] != out[j]["directory"] {
				return out[i]["directory"].(bool)
			}
			return out[i]["name"].(string) < out[j]["name"].(string)
		})
		respond(w, map[string]any{"entries": out, "root": "/"})
	case endpoint == "files/content" && r.Method == "GET":
		p, e := d.checkedPath(c, r.URL.Query().Get("path"), false)
		if e != nil {
			fail(w, 400, e)
			return
		}
		f, e := c.Open(p)
		if e != nil {
			fail(w, 403, e)
			return
		}
		defer f.Close()
		st, e := f.Stat()
		if e != nil || !st.Mode().IsRegular() {
			fail(w, 400, fmt.Errorf("仅支持普通文件"))
			return
		}
		if r.URL.Query().Get("download") == "1" {
			w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", path.Base(p)))
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Header().Set("Content-Length", strconv.FormatInt(st.Size(), 10))
			_, _ = io.Copy(w, f)
			return
		}
		previewFile(w, f, st.Size())
	case endpoint == "files/action" && r.Method == "POST":
		var b struct {
			Action    string `json:"action"`
			Path      string `json:"path"`
			Target    string `json:"target"`
			Content   string `json:"content"`
			Mode      string `json:"mode"`
			Recursive bool   `json:"recursive"`
			UID       *int   `json:"uid"`
			GID       *int   `json:"gid"`
		}
		r.Body = http.MaxBytesReader(w, r.Body, 16<<20)
		if e = json.NewDecoder(r.Body).Decode(&b); e != nil {
			fail(w, 400, e)
			return
		}
		p, e := d.checkedPath(c, b.Path, true)
		if e != nil {
			fail(w, 400, e)
			return
		}
		if b.Action == "delete" || b.Action == "rename" || b.Action == "remove" {
			p = path.Clean("/" + strings.TrimPrefix(b.Path, "/"))
		}
		if b.Action == "permissions" {
			parent, err := d.checkedPath(c, path.Dir(b.Path), false)
			if err != nil {
				fail(w, 400, err)
				return
			}
			p = path.Join(parent, path.Base(b.Path))
		}
		target := ""
		if b.Target != "" {
			target, e = d.checkedPath(c, b.Target, true)
			if e != nil {
				fail(w, 400, e)
				return
			}
		}
		switch b.Action {
		case "copy", "move":
			source := path.Clean("/" + strings.TrimPrefix(b.Path, "/"))
			destination := path.Clean("/" + strings.TrimPrefix(b.Target, "/"))
			if source == "/" || destination == "/" || destination == source || strings.HasPrefix(destination, source+"/") {
				e = fmt.Errorf("无效的复制或移动目标")
				break
			}
			_, e = d.run(ctx, true, fileTransferScript, b.Action, source, destination)
		case "write":
			mode := os.FileMode(0644)
			uid, gid := -1, -1
			if st, err := c.Stat(p); err == nil {
				if !st.Mode().IsRegular() {
					e = fmt.Errorf("仅支持普通文件")
					break
				}
				mode = st.Mode() & (os.ModePerm | os.ModeSetuid | os.ModeSetgid | os.ModeSticky)
				if attrs, ok := st.Sys().(*sftp.FileStat); ok {
					uid, gid = int(attrs.UID), int(attrs.GID)
				}
			}
			tmp := path.Join(path.Dir(p), ".ownpanel-"+token()[:16])
			f, err := c.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY)
			if err != nil {
				e = err
				break
			}
			e = f.Chmod(0600)
			if e == nil {
				_, e = io.WriteString(f, b.Content)
			}
			if e == nil {
				if uid >= 0 {
					e = f.Chown(uid, gid)
				}
				if e == nil {
					e = f.Chmod(mode)
				}
			}
			ce := f.Close()
			if e == nil {
				e = ce
			}
			if e == nil {
				e = c.PosixRename(tmp, p)
			}
			_ = c.Remove(tmp)
		case "mkdir":
			e = c.Mkdir(p)
		case "rename":
			if target == "" || target == "/" {
				e = fmt.Errorf("目标路径无效")
				break
			}
			if _, err := c.Lstat(target); err == nil {
				e = fmt.Errorf("目标已存在")
				break
			}
			e = c.Rename(p, target)
		case "remove":
			info, err := c.Lstat(p)
			if err != nil {
				e = err
			} else if info.Mode()&os.ModeSymlink != 0 {
				e = c.Remove(p)
			} else {
				e = c.RemoveAll(p)
			}
		case "delete":
			trash := path.Join(path.Dir(p), ".ownpanel-trash")
			if _, err := c.Lstat(trash); os.IsNotExist(err) {
				e = c.Mkdir(trash)
				if e == nil {
					e = c.Chmod(trash, 0700)
				}
			} else if err != nil {
				e = err
			}
			if e != nil {
				break
			}
			trash, e = d.checkedPath(c, trash, false)
			if e == nil {
				target = path.Join(trash, token()[:16]+"-"+path.Base(p))
				e = c.Rename(p, target)
			}
		case "permissions":
			if b.UID == nil || b.GID == nil || *b.UID < 0 || *b.GID < 0 || *b.UID > 2147483647 || *b.GID > 2147483647 {
				e = fmt.Errorf("UID/GID 无效")
				break
			}
			var value uint64
			value, e = strconv.ParseUint(b.Mode, 8, 12)
			if e != nil || len(b.Mode) < 3 || len(b.Mode) > 4 {
				e = fmt.Errorf("权限无效")
				break
			}
			e = d.applySSHFilePermissions(ctx, c, p, value, *b.UID, *b.GID, b.Recursive)
		case "chmod":
			var v uint64
			v, e = strconv.ParseUint(b.Mode, 8, 12)
			if e == nil {
				e = c.Chmod(p, permissionMode(v))
			}
		case "chown":
			if b.UID == nil || b.GID == nil || *b.UID < 0 || *b.GID < 0 {
				e = fmt.Errorf("UID/GID 无效")
			} else {
				e = c.Chown(p, *b.UID, *b.GID)
			}
		case "compress":
			if target == "" {
				target, e = d.checkedPath(c, b.Path+".zip", true)
			}
			if e == nil {
				e = compressSSHZIP(c, p, target)
			}
		case "extract":
			if target == "" {
				e = fmt.Errorf("缺少解压目录")
			} else {
				e = extractSSHZIP(c, p, target)
			}
		default:
			e = fmt.Errorf("未知操作")
		}
		if e != nil {
			fail(w, 400, e)
			return
		}
		respond(w, map[string]any{"ok": true, "target": target})
	case endpoint == "files/upload" && r.Method == "POST":
		if !acquireFileSlot() {
			fail(w, 429, fmt.Errorf("文件任务繁忙"))
			return
		}
		defer releaseFileSlot()
		if !parseUpload(w, r) {
			return
		}
		defer r.MultipartForm.RemoveAll()
		f, h, e := r.FormFile("file")
		if e != nil {
			fail(w, 400, e)
			return
		}
		defer f.Close()
		p, e := d.checkedPath(c, path.Join(r.FormValue("path"), path.Base(h.Filename)), true)
		if e != nil {
			fail(w, 400, e)
			return
		}
		out, e := c.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY)
		if e != nil {
			fail(w, 403, e)
			return
		}
		_, e = io.Copy(out, f)
		ce := out.Close()
		if e == nil {
			e = ce
		}
		if e != nil {
			_ = c.Remove(p)
			fail(w, 502, e)
			return
		}
		respond(w, map[string]bool{"ok": true})
	case endpoint == "files/scan" && r.Method == "GET":
		p, e := d.checkedPath(c, r.URL.Query().Get("path"), false)
		if e != nil {
			fail(w, 400, e)
			return
		}
		walker := c.Walk(p)
		count := 0
		skipped := 0
		var total int64
		largest := []map[string]any{}
		truncated := false
		for walker.Step() {
			if ctx.Err() != nil {
				truncated = true
				break
			}
			if walker.Err() != nil {
				if walker.Path() == p {
					fail(w, 400, walker.Err())
					return
				}
				skipped++
				continue
			}
			st := walker.Stat()
			if st.Mode().IsRegular() {
				count++
				total += st.Size()
				largest = append(largest, map[string]any{"path": walker.Path(), "size": st.Size()})
				if len(largest) > 100 {
					sort.Slice(largest, func(i, j int) bool { return largest[i]["size"].(int64) > largest[j]["size"].(int64) })
					largest = largest[:20]
				}
			}

		}
		sort.Slice(largest, func(i, j int) bool { return largest[i]["size"].(int64) > largest[j]["size"].(int64) })
		if len(largest) > 20 {
			largest = largest[:20]
		}
		respond(w, map[string]any{"bytes": total, "files": count, "largest": largest, "truncated": truncated, "skipped": skipped})
	default:
		fail(w, 501, fmt.Errorf("SSH 模式不支持该文件操作"))
	}
}

func (d *sshDriver) applySSHFilePermissions(ctx context.Context, c *sftp.Client, p string, mode uint64, uid, gid int, recursive bool) error {
	st, err := c.Lstat(p)
	if err != nil {
		return err
	}
	if st.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("不修改符号链接及其目标")
	}
	paths := []string{p}
	if recursive {
		if p == "/" || !st.IsDir() {
			return fmt.Errorf("递归权限只能应用到子目录")
		}
		paths = nil
		walker := c.Walk(p)
		for walker.Step() {
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := walker.Err(); err != nil {
				return err
			}
			if walker.Stat().Mode()&os.ModeSymlink != 0 {
				continue
			}
			paths = append(paths, walker.Path())
			if len(paths) > 100000 {
				return fmt.Errorf("文件数量超过单次操作限制")
			}
		}
	}
	changed := 0
	for i := len(paths) - 1; i >= 0; i-- {
		if err = ctx.Err(); err != nil {
			return err
		}
		info, err := c.Lstat(paths[i])
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			continue
		}
		if !info.Mode().IsRegular() && !info.IsDir() {
			return fmt.Errorf("仅支持普通文件和目录")
		}
		owner, ok := info.Sys().(*sftp.FileStat)
		if !ok {
			return fmt.Errorf("无法读取文件属主")
		}
		if int(owner.UID) != uid || int(owner.GID) != gid {
			err = c.Chown(paths[i], uid, gid)
		}
		if err == nil {
			err = c.Chmod(paths[i], permissionMode(mode))
		}
		if err != nil {
			return fmt.Errorf("已修改 %d 项，%s：%w", changed, paths[i], err)
		}
		changed++
	}
	return nil
}
