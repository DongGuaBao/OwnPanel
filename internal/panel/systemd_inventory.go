package panel

import (
	"context"
	"path"
	"regexp"
	"strings"
)

var ownpanelRecoveryUnit = regexp.MustCompile(`^ownpanel-(ssh-)?fw-[a-f0-9]{16}\.(service|timer)$`)

func systemdOrigin(fragment string) string {
	switch {
	case fragment == "":
		return "unknown"
	case fragment == "/dev/null":
		return "masked"
	case strings.HasPrefix(fragment, "/run/systemd/generator"):
		return "generated"
	case strings.HasPrefix(fragment, "/run/"):
		return "transient"
	case strings.HasPrefix(fragment, "/usr/lib/") || strings.HasPrefix(fragment, "/lib/"):
		return "vendor"
	default:
		return "local"
	}
}
func enrichSystemdInventory(ctx context.Context, run systemdRunner, units map[string]map[string]any) {
	names := []string{}
	for name := range units {
		if units[name]["load"] == "not-loaded" {
			continue
		}
		if systemdUnitName.MatchString(name) && len(name) <= 255 {
			names = append(names, name)
		}
	}
	records := map[string]map[string]string{}
	for offset := 0; offset < len(names); offset += 200 {
		script := systemdCLI(ctx, "systemctl") + " show --property=Id,Names,FragmentPath,SourcePath,Transient --"
		end := min(offset+200, len(names))
		for _, name := range names[offset:end] {
			script += " " + shQuote(name)
		}
		// A template without an instance may fail to load; preserve metadata for all
		// other units in the batch instead of failing the inventory request.
		out, err := run(ctx, false, script+" 2>/dev/null || true\n")
		if err != nil {
			continue
		}
		for _, block := range strings.Split(out, "\n\n") {
			p := map[string]string{}
			for _, line := range strings.Split(block, "\n") {
				if k, v, ok := strings.Cut(line, "="); ok {
					p[k] = v
				}
			}
			aliases := append(strings.Fields(p["Names"]), p["Id"])
			for _, name := range aliases {
				if name != "" {
					records[name] = p
				}
			}
		}
	}
	// Uninstantiated local templates do not appear in `show`; read their actual
	// file targets too. Vendor aliases in /etc remain classified as system units.
	patterns := "/etc/systemd/system/* /usr/local/lib/systemd/system/*"
	if systemdUserScope(ctx) {
		patterns = `"$HOME"/.config/systemd/user/* /etc/systemd/user/* /usr/local/lib/systemd/user/*`
	}
	out, _ := run(ctx, false, "for f in "+patterns+"; do test -f \"$f\" || continue; p=$(readlink -f -- \"$f\") || continue; printf '%s\\t%s\\n' \"${f##*/}\" \"$p\"; done\n")
	for _, line := range strings.Split(out, "\n") {
		name, p, ok := strings.Cut(line, "\t")
		if ok && path.IsAbs(p) {
			if records[name] == nil {
				records[name] = map[string]string{}
			}
			records[name]["FragmentPath"] = p
		}
	}
	for name, u := range units {
		p := records[name]
		origin := systemdOrigin(p["FragmentPath"])
		if ownpanelRecoveryUnit.MatchString(name) {
			origin = "generated"
		}
		u["fragmentPath"] = p["FragmentPath"]
		u["sourcePath"] = p["SourcePath"]
		u["origin"] = origin
		u["custom"] = origin == "local" && p["Transient"] != "yes"
	}
}
