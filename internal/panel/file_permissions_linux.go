package panel

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"strconv"
	"syscall"
)

func applyFilePermissions(ctx context.Context, root *os.Root, path, mode string, uid, gid int, recursive bool) error {
	value, err := strconv.ParseUint(mode, 8, 12)
	if err != nil || len(mode) < 3 || len(mode) > 4 || uid < 0 || gid < 0 || uid > 2147483647 || gid > 2147483647 {
		return fmt.Errorf("权限或 UID/GID 无效")
	}
	info, err := root.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("不修改符号链接及其目标")
	}
	target := root
	if recursive {
		if path == "." || !info.IsDir() {
			return fmt.Errorf("递归权限只能应用到子目录")
		}
		target, err = root.OpenRoot(path)
		if err != nil {
			return err
		}
		defer target.Close()
		path = "."
	}
	paths := []string{path}
	if recursive {
		paths = nil
		err = fs.WalkDir(target.FS(), ".", func(name string, entry fs.DirEntry, walkErr error) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			if walkErr != nil {
				return walkErr
			}
			if entry.Type()&os.ModeSymlink != 0 {
				return nil
			}
			if len(paths) >= 100000 {
				return fmt.Errorf("文件数量超过单次操作限制")
			}
			paths = append(paths, name)
			return nil
		})
		if err != nil {
			return err
		}
	}
	changed := 0
	for i := len(paths) - 1; i >= 0; i-- {
		if err = ctx.Err(); err != nil {
			return err
		}
		err = func() error {
			file, e := target.OpenFile(paths[i], os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
			if e != nil {
				return e
			}
			defer file.Close()
			st, e := file.Stat()
			if e != nil {
				return e
			}
			if !st.Mode().IsRegular() && !st.IsDir() {
				return fmt.Errorf("仅支持普通文件和目录")
			}
			owner, ok := st.Sys().(*syscall.Stat_t)
			if !ok {
				return fmt.Errorf("无法读取文件属主")
			}
			if int(owner.Uid) != uid || int(owner.Gid) != gid {
				if e = file.Chown(uid, gid); e != nil {
					return e
				}
			}
			return file.Chmod(permissionMode(value))
		}()
		if err != nil {
			return fmt.Errorf("已修改 %d 项，%s：%w", changed, paths[i], err)
		}
		changed++
	}
	return nil
}
