package panel

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestDevelopmentFirewallDirectory(t *testing.T) {
	t.Setenv("OWNPANEL_FIREWALL_ROOT", "")
	if got := sharedFirewallDirectory(); got != "/var/lib/ownpanel-ssh-firewall" {
		t.Fatalf("default root = %q", got)
	}
	root := filepath.Join(t.TempDir(), "firewall")
	t.Setenv("OWNPANEL_FIREWALL_ROOT", root)
	old := cfg
	t.Cleanup(func() { cfg = old })
	cfg.data = t.TempDir()
	release, err := lockSharedFirewall(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	release()
	if _, err := os.Stat(filepath.Join(root, "native-lease.json")); err != nil {
		t.Fatal(err)
	}
}

func TestDevelopmentPythonFirewallDirectory(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 unavailable")
	}
	root := filepath.Join(t.TempDir(), "firewall")
	t.Setenv("OWNPANEL_FIREWALL_ROOT", root)
	request := map[string]any{"action": "iptables-status", "program": "iptables"}
	program := firewallPython(request)
	// Inspect definitions without dispatching a real firewall operation.
	index := strings.Index(program, "def main(")
	if index < 0 {
		t.Fatal("missing firewall main")
	}
	program = program[:index] + `
assert str(ROOT) == os.environ['OWNPANEL_FIREWALL_ROOT']
captured = {}
def command(*args):
    return ''
def write_atomic(path, source, mode):
    captured[str(path)] = source
install_recovery({'unit':'ownpanel-test', 'id':'test', 'deadline':2000000000}, 'assert True')
source = captured[str(RECOVERY)]
del os.environ['OWNPANEL_FIREWALL_ROOT']
exec(source)
assert os.environ['OWNPANEL_FIREWALL_ROOT'] == str(ROOT)
`
	cmd := exec.Command("python3", "-")
	cmd.Stdin = strings.NewReader(program)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("Python recovery path: %v: %s", err, out)
	}
}
