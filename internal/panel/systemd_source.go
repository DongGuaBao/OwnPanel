package panel

import (
	"context"
	"fmt"
	"path"
	"strings"
)

func systemdFragmentPath(ctx context.Context, run systemdRunner, name string) (string, error) {
	value, e := systemdCommand(run, ctx, false, "show", "--property=FragmentPath", "--value", "--", name)
	if e != nil {
		return "", e
	}
	value = strings.TrimSpace(value)
	if !path.IsAbs(value) || strings.ContainsAny(value, "\x00\r\n") {
		return "", fmt.Errorf("此单元没有可编辑的源文件")
	}
	value, e = systemdRead(ctx, run, "readlink -f -- "+shQuote(value)+"\n")
	if e != nil {
		return "", e
	}
	value = strings.TrimSpace(value)
	if !path.IsAbs(value) || strings.ContainsAny(value, "\x00\r\n") || strings.HasPrefix(value, "/dev/") || strings.HasPrefix(value, "/proc/") || strings.HasPrefix(value, "/sys/") {
		return "", fmt.Errorf("此单元没有可编辑的普通源文件，请使用覆盖配置")
	}
	return value, nil
}
func systemdSource(ctx context.Context, run systemdRunner, name string) map[string]any {
	p, e := systemdFragmentPath(ctx, run, name)
	if e != nil {
		return map[string]any{"path": "", "content": "", "revision": "", "error": e.Error()}
	}
	text, e := systemdRead(ctx, run, "test -f "+shQuote(p)+" && cat -- "+shQuote(p)+"\n")
	if e != nil {
		return map[string]any{"path": p, "content": "", "revision": "", "error": e.Error()}
	}
	return map[string]any{"path": p, "content": text, "revision": systemdRevision(text), "error": ""}
}
