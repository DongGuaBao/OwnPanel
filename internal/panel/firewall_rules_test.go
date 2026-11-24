package panel

import "testing"

func TestFirewallRuleEditIdentity(t *testing.T) {
	added := "Added user rules (see 'ufw status' for running firewall):\nufw allow 443/tcp\nufw deny from 192.0.2.1 to any port 22 proto tcp\n"
	change := firewallChange{Action: "replace", Original: "allow 443/tcp", Port: "8443", Protocol: "tcp", Source: "any", Policy: "allow"}
	commands, e := firewallCommands(change, added)
	if e != nil || len(commands) != 2 || commands[0][1] != "delete" || commands[1][0] != "allow" {
		t.Fatal(commands, e)
	}
	change.Original = "allow 80/tcp"
	if _, e = firewallCommands(change, added); e == nil {
		t.Fatal("stale rule edit allowed")
	}
	change.Original = "allow 443/tcp"
	change.Action = "delete-rule"
	commands, e = firewallCommands(change, added)
	if e != nil || len(commands) != 1 {
		t.Fatal(commands, e)
	}
	change.Original = "allow '443/tcp'"
	if _, e = firewallCommands(change, added); e == nil {
		t.Fatal("ambiguous quoted rule accepted")
	}
}

func TestFirewallArgumentsRejectUnsafeInputs(t *testing.T) {
	valid := firewallChange{Action: "add", Port: "8000:8100", Source: "2001:db8::/32", Policy: "allow", Protocol: "tcp"}
	arguments, err := firewallArguments(valid)
	if err != nil || len(arguments) != 9 {
		t.Fatalf("valid IPv6 range rule rejected: %v %v", arguments, err)
	}
	for _, change := range []firewallChange{
		{Action: "add", Port: "22;reboot", Source: "any", Policy: "allow", Protocol: "tcp"},
		{Action: "add", Port: "8100:8000", Source: "any", Policy: "allow", Protocol: "tcp"},
		{Action: "add", Port: "443", Source: "--force", Policy: "allow", Protocol: "tcp"},
		{Action: "add", Port: "443", Source: "any", Policy: "allow;reboot", Protocol: "tcp"},
		{Action: "delete", Number: 0},
		{Action: "unknown"},
	} {
		if _, err := firewallArguments(change); err == nil {
			t.Fatalf("unsafe firewall request accepted: %+v", change)
		}
	}
}
