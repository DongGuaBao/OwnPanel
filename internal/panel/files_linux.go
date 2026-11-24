package panel

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func relative(p string) (string, error) {
	if strings.ContainsRune(p, 0) || strings.Contains(p, "\\") {
		return "", fmt.Errorf("路径无效")
	}
	p = strings.TrimPrefix(p, "/")
	if p == "" {
		return ".", nil
	}
	if !fs.ValidPath(p) {
		return "", fmt.Errorf("路径无效")
	}
	if cfg.fileRoot == "/" {
		current, suffix := "/"+p, ""
		for {
			resolved, err := filepath.EvalSymlinks(current)
			if err == nil {
				p = strings.TrimPrefix(filepath.Join(resolved, suffix), "/")
				if p == "" {
					p = "."
				}
				break
			}
			if !os.IsNotExist(err) {
				return "", err
			}
			suffix = filepath.Join(filepath.Base(current), suffix)
			current = filepath.Dir(current)
		}
	}
	return p, nil
}

func relativeUnfollowed(p string) (string, error) {
	parent, err := relative(path.Dir(p))
	if err != nil {
		return "", err
	}
	return path.Join(parent, path.Base(p)), nil
}
func fileRoot() (*os.Root, error) { return os.OpenRoot(cfg.fileRoot) }
func registerFiles(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/files/directory-sizes", handleDirectorySizes)
	mux.HandleFunc("GET /api/files/search", handleFileSearch)
	mux.HandleFunc("GET /api/files/identities", func(w http.ResponseWriter, r *http.Request) {
		passwd, e := os.ReadFile("/etc/passwd")
		if e != nil {
			fail(w, 500, e)
			return
		}
		group, e := os.ReadFile("/etc/group")
		if e != nil {
			fail(w, 500, e)
			return
		}
		respond(w, fileIdentities(string(passwd), string(group)))
	})
	mux.HandleFunc("GET /api/files", func(w http.ResponseWriter, r *http.Request) {
		root, e := fileRoot()
		if e != nil {
			fail(w, 500, e)
			return
		}
		defer root.Close()
		p, e := relative(r.URL.Query().Get("path"))
		if e != nil {
			fail(w, 400, e)
			return
		}
		f, e := root.Open(p)
		if e != nil {
			fail(w, 400, e)
			return
		}
		defer f.Close()
		entries, e := f.ReadDir(-1)
		if e != nil {
			fail(w, 400, e)
			return
		}
		result := []map[string]any{}
		for _, d := range entries {

			st, e := root.Lstat(path.Join(p, d.Name()))
			if e != nil {
				continue
			}
			uid, gid := uint32(0), uint32(0)
			if sys, ok := st.Sys().(*syscall.Stat_t); ok {
				uid, gid = sys.Uid, sys.Gid
			}
			result = append(result, map[string]any{"name": d.Name(), "directory": func() bool {
				if d.Type()&os.ModeSymlink != 0 {
					canonical, err := relative(path.Join(p, d.Name()))
					if err != nil {
						return false
					}
					resolved, err := root.Stat(canonical)
					return err == nil && resolved.IsDir()
				}
				return d.IsDir()
			}(), "symlink": st.Mode()&os.ModeSymlink != 0, "size": st.Size(), "mode": fmt.Sprintf("%04o", unixPermission(st.Mode())), "uid": uid, "gid": gid, "modified": st.ModTime()})
		}
		sort.Slice(result, func(i, j int) bool {
			a, b := result[i], result[j]
			if a["directory"] != b["directory"] {
				return a["directory"].(bool)
			}
			return a["name"].(string) < b["name"].(string)
		})
		respond(w, map[string]any{"entries": result, "root": cfg.fileRoot})
	})
	mux.HandleFunc("GET /api/files/content", func(w http.ResponseWriter, r *http.Request) {
		root, e := fileRoot()
		if e != nil {
			fail(w, 500, e)
			return
		}
		defer root.Close()
		p, e := relative(r.URL.Query().Get("path"))
		if e != nil {
			fail(w, 400, e)
			return
		}
		f, e := root.Open(p)
		if e != nil {
			fail(w, 400, e)
			return
		}
		defer f.Close()
		st, e := f.Stat()
		if e != nil || !st.Mode().IsRegular() {
			fail(w, 400, fmt.Errorf("仅支持普通文件"))
			return
		}
		if r.URL.Query().Get("download") == "1" {
			w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filepath.Base(p)))
			w.Header().Set("Content-Type", "application/octet-stream")
			http.ServeContent(w, r, st.Name(), st.ModTime(), f)
			return
		}

		previewFile(w, f, st.Size())
	})
	mux.HandleFunc("POST /api/files/action", func(w http.ResponseWriter, r *http.Request) {
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
		if e := json.NewDecoder(r.Body).Decode(&b); e != nil {
			fail(w, 400, e)
			return
		}
		root, e := fileRoot()
		if e != nil {
			fail(w, 500, e)
			return
		}
		defer root.Close()
		p, e := relative(b.Path)
		if b.Action == "delete" || b.Action == "rename" || b.Action == "remove" || b.Action == "copy" || b.Action == "move" || b.Action == "permissions" {
			p, e = relativeUnfollowed(b.Path)
		}
		if e != nil {
			fail(w, 400, fmt.Errorf("不能修改该路径"))
			return
		}
		target := ""
		if b.Target != "" {
			target, e = relative(b.Target)
			if b.Action == "copy" || b.Action == "move" {
				target, e = relativeUnfollowed(b.Target)
			}
			if e != nil {
				fail(w, 400, fmt.Errorf("目标路径无效"))
				return
			}
		}
		switch b.Action {
		case "copy", "move":
			if p == "." || target == "" || target == "." || target == p || strings.HasPrefix(target, p+"/") {
				e = fmt.Errorf("无效的复制或移动目标")
				break
			}
			_, e = command(r.Context(), "bash", "-c", fileTransferScript, "ownpanel-transfer", b.Action, filepath.Join(cfg.fileRoot, p), filepath.Join(cfg.fileRoot, target))
		case "write":
			if info, err := root.Lstat(p); err == nil && info.Mode()&os.ModeSymlink != 0 {
				resolved, err := filepath.EvalSymlinks(filepath.Join(cfg.fileRoot, p))
				if err != nil {
					e = err
					break
				}
				p, err = filepath.Rel(cfg.fileRoot, resolved)
				if err != nil {
					e = err
					break
				}
			}
			tmp := path.Join(path.Dir(p), ".ownpanel-"+token()[:16])
			mode := os.FileMode(0644)
			uid, gid := -1, -1
			if st, err := root.Stat(p); err == nil {
				if !st.Mode().IsRegular() {
					e = fmt.Errorf("仅支持普通文件")
					break
				}
				mode = st.Mode() & (os.ModePerm | os.ModeSetuid | os.ModeSetgid | os.ModeSticky)
				if s, ok := st.Sys().(*syscall.Stat_t); ok {
					uid, gid = int(s.Uid), int(s.Gid)
				}
			}
			f, err := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
			if err != nil {
				e = err
				break
			}
			_, e = f.WriteString(b.Content)
			if e == nil && uid >= 0 {
				e = f.Chown(uid, gid)
			}
			if e == nil {
				e = f.Chmod(mode)
			}
			if e == nil {
				e = f.Sync()
			}
			ce := f.Close()
			if e == nil {
				e = ce
			}
			if e == nil {
				e = root.Rename(tmp, p)
			}
			_ = root.Remove(tmp)
		case "mkdir":
			e = root.Mkdir(p, 0755)
		case "remove":
			e = root.RemoveAll(p)
		case "delete":
			trash := path.Join(path.Dir(p), ".ownpanel-trash")
			_ = root.Mkdir(trash, 0700)
			target = path.Join(trash, time.Now().Format("20060102-150405")+"-"+token()[:8]+"-"+path.Base(p))
			e = root.Rename(p, target)
		case "rename":
			if target == "" {
				e = fmt.Errorf("缺少目标路径")
				break
			}
			if _, err := root.Lstat(target); err == nil {
				e = fmt.Errorf("目标已存在")
				break
			}
			e = root.Rename(p, target)
		case "permissions":
			if b.UID == nil || b.GID == nil {
				e = fmt.Errorf("请选择所有者和用户组")
				break
			}
			e = applyFilePermissions(r.Context(), root, p, b.Mode, *b.UID, *b.GID, b.Recursive)
		case "chmod":
			var v uint64
			v, e = strconv.ParseUint(b.Mode, 8, 12)
			if e == nil {
				e = root.Chmod(p, permissionMode(v))
			}
		case "chown":
			if b.UID == nil || b.GID == nil || *b.UID < 0 || *b.GID < 0 {
				e = fmt.Errorf("UID/GID 无效")
				break
			}
			e = root.Chown(p, *b.UID, *b.GID)
		case "compress":
			if target == "" {
				target = p + ".zip"
			}
			e = compressZip(root, p, target)
		case "extract":
			if target == "" {
				e = fmt.Errorf("缺少解压目录")
				break
			}
			e = extractZip(root, p, target)
		default:
			e = fmt.Errorf("未知操作")
		}
		if e != nil {
			fail(w, 400, e)
			return
		}
		audit("file_"+b.Action, map[string]string{"path": p, "target": target})
		respond(w, map[string]any{"ok": true, "target": target})
	})
	mux.HandleFunc("POST /api/files/upload", func(w http.ResponseWriter, r *http.Request) {
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
		p, e := relative(path.Join(r.FormValue("path"), path.Base(h.Filename)))
		if e != nil {
			fail(w, 400, e)
			return
		}
		root, e := fileRoot()
		if e != nil {
			fail(w, 500, e)
			return
		}
		defer root.Close()
		dst, e := root.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
		if e != nil {
			fail(w, 400, e)
			return
		}
		_, e = io.Copy(dst, f)
		ce := dst.Close()
		if e == nil {
			e = ce
		}
		if e != nil {
			_ = root.Remove(p)
			fail(w, 500, e)
			return
		}
		audit("file_upload", p)
		respond(w, map[string]bool{"ok": true})
	})
	mux.HandleFunc("GET /api/files/scan", func(w http.ResponseWriter, r *http.Request) {
		p, e := relative(r.URL.Query().Get("path"))
		if e != nil {
			fail(w, 400, e)
			return
		}
		root, e := fileRoot()
		if e != nil {
			fail(w, 500, e)
			return
		}
		defer root.Close()
		var size int64
		count := 0
		skipped := 0
		truncated := false
		items := []map[string]any{}
		e = fs.WalkDir(root.FS(), p, func(n string, d fs.DirEntry, err error) error {
			if err != nil {
				if n == p {
					return err
				}
				skipped++
				return nil
			}
			if r.Context().Err() != nil {
				return r.Context().Err()
			}
			if d.Type().IsRegular() {
				st, err := d.Info()
				if err == nil {
					size += st.Size()
					count++
					items = append(items, map[string]any{"path": n, "size": st.Size()})
					if len(items) > 100 {
						sort.Slice(items, func(i, j int) bool { return items[i]["size"].(int64) > items[j]["size"].(int64) })
						items = items[:20]
					}
				} else {
					skipped++
				}
			}
			return nil
		})
		if e != nil {
			fail(w, 400, e)
			return
		}
		sort.Slice(items, func(i, j int) bool { return items[i]["size"].(int64) > items[j]["size"].(int64) })
		if len(items) > 20 {
			items = items[:20]
		}
		respond(w, map[string]any{"bytes": size, "files": count, "largest": items, "truncated": truncated, "skipped": skipped})
	})
}
func compressZip(root *os.Root, source, target string) error {
	if target == source || strings.HasPrefix(target, source+"/") {
		return fmt.Errorf("压缩包不能位于源目录内")
	}
	out, e := root.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if e != nil {
		return e
	}
	z := zip.NewWriter(out)
	e = fs.WalkDir(root.FS(), source, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("暂不压缩符号链接: %s", p)
		}
		if d.IsDir() {
			return nil
		}
		st, err := d.Info()
		if err != nil {
			return err
		}
		if !st.Mode().IsRegular() {
			return fmt.Errorf("不支持特殊文件")
		}
		h, err := zip.FileInfoHeader(st)
		if err != nil {
			return err
		}
		h.Name = strings.TrimPrefix(p, path.Dir(source)+"/")
		if path.Dir(source) == "." {
			h.Name = p
		}
		h.Method = zip.Deflate
		w, err := z.CreateHeader(h)
		if err != nil {
			return err
		}
		in, err := root.Open(p)
		if err != nil {
			return err
		}
		_, err = io.Copy(w, in)
		closeErr := in.Close()
		if err != nil {
			return err
		}
		return closeErr
	})
	ze := z.Close()
	oe := out.Close()
	if e == nil {
		e = ze
	}
	if e == nil {
		e = oe
	}
	if e != nil {
		_ = root.Remove(target)
	}
	return e
}
func extractZip(root *os.Root, source, target string) error {
	if _, e := root.Lstat(target); !os.IsNotExist(e) {
		return fmt.Errorf("请解压到一个尚不存在的目录")
	}
	f, e := root.Open(source)
	if e != nil {
		return e
	}
	defer f.Close()
	st, e := f.Stat()
	if e != nil {
		return e
	}
	if !acquireFileSlot() {
		return fmt.Errorf("文件任务繁忙")
	}
	defer releaseFileSlot()
	if !st.Mode().IsRegular() || st.Size() > maxZIPBytes {
		return fmt.Errorf("仅支持 128 MiB 以内的 ZIP 普通文件")
	}
	z, e := openLimitedZIP(f, st.Size())
	if e != nil {
		return e
	}
	if e = root.MkdirAll(path.Dir(target), 0755); e != nil {
		return e
	}
	if e = root.Mkdir(target, 0755); e != nil {
		return e
	}
	ok := false
	defer func() {
		if !ok {
			_ = root.RemoveAll(target)
		}
	}()
	for _, entry := range z.File {
		n := path.Join(target, entry.Name)
		if entry.FileInfo().IsDir() {
			if e = root.MkdirAll(n, 0755); e != nil {
				return e
			}
			continue
		}
		if e = root.MkdirAll(path.Dir(n), 0755); e != nil {
			return e
		}
		in, err := entry.Open()
		if err != nil {
			return err
		}
		out, err := root.OpenFile(n, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
		if err != nil {
			in.Close()
			return err
		}
		err = copyZIPEntry(out, in, entry.UncompressedSize64)
		ce := out.Close()
		ie := in.Close()
		if err != nil {
			return err
		}
		if ce != nil {
			return ce
		}
		if ie != nil {
			return ie
		}

	}
	ok = true
	return nil
}
