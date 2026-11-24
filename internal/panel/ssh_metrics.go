package panel

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
)

func num(s string) float64 { v, _ := strconv.ParseFloat(s, 64); return v }
func (d *sshDriver) metrics(ctx context.Context) (map[string]any, error) {
	d.metricsMu.Lock()
	defer d.metricsMu.Unlock()
	script := `set -eu
cat /proc/stat
echo __OP_MEM__
cat /proc/meminfo
echo __OP_HOST__
hostname; uname -r; uname -m; cat /proc/uptime; cat /proc/loadavg
echo __OP_OS__
cat /etc/os-release
echo __OP_DISK__
df -Pk -- "$1"
echo __OP_NET__
cat /proc/net/dev
echo __OP_BOOT__
cat /proc/sys/kernel/random/boot_id
`
	out, e := d.run(ctx, false, script, d.n.FileRoot)
	if e != nil {
		return nil, e
	}
	sections := map[string]string{}
	key := "cpu"
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "__OP_") {
			key = line
			continue
		}
		sections[key] += line + "\n"
	}
	fields := strings.Fields(strings.Split(sections["cpu"], "\n")[0])
	if len(fields) < 5 {
		return nil, fmt.Errorf("无法解析 /proc/stat")
	}
	values := []float64{}
	for _, f := range fields[1:] {
		values = append(values, num(f))
	}
	total, idle := 0.0, values[3]
	for i, v := range values {
		if i < 8 {
			total += v
		}
	}
	if len(values) > 4 {
		idle += values[4]
	}
	boot := strings.TrimSpace(sections["__OP_BOOT__"])
	pct := 0.0
	if d.previousBoot == boot && len(d.previousCPU) == 2 {
		delta := total - d.previousCPU[0]
		if delta > 0 {
			pct = max(0, min(100, (1-(idle-d.previousCPU[1])/delta)*100))
		}
	}
	d.previousCPU = []float64{total, idle}
	d.previousBoot = boot
	mem := map[string]float64{}
	for _, line := range strings.Split(sections["__OP_MEM__"], "\n") {
		f := strings.Fields(line)
		if len(f) > 1 {
			mem[strings.TrimSuffix(f[0], ":")] = num(f[1]) * 1024
		}
	}
	available := mem["MemAvailable"]
	if available == 0 {
		available = mem["MemFree"] + mem["Buffers"] + mem["Cached"]
	}
	used := max(0, mem["MemTotal"]-available)
	usedPct := 0.0
	if mem["MemTotal"] > 0 {
		usedPct = used / mem["MemTotal"] * 100
	}
	host := strings.Split(strings.TrimSpace(sections["__OP_HOST__"]), "\n")
	if len(host) < 5 {
		return nil, fmt.Errorf("无法解析系统信息")
	}
	osData := map[string]string{}
	for _, line := range strings.Split(sections["__OP_OS__"], "\n") {
		p := strings.SplitN(line, "=", 2)
		if len(p) == 2 {
			osData[p[0]] = strings.Trim(p[1], "\"'")
		}
	}
	loads := strings.Fields(host[4])
	uptime := strings.Fields(host[3])
	load1, load5, load15 := 0.0, 0.0, 0.0
	if len(loads) > 2 {
		load1, load5, load15 = num(loads[0]), num(loads[1]), num(loads[2])
	}
	up := 0.0
	if len(uptime) > 0 {
		up = num(uptime[0])
	}
	disk := map[string]any{}
	for _, line := range strings.Split(sections["__OP_DISK__"], "\n")[1:] {
		f := strings.Fields(line)
		if len(f) >= 6 {
			disk = map[string]any{"total": num(f[1]) * 1024, "used": num(f[2]) * 1024, "free": num(f[3]) * 1024, "usedPercent": num(strings.TrimSuffix(f[4], "%"))}
			break
		}
	}
	network := []map[string]any{}
	for _, line := range strings.Split(sections["__OP_NET__"], "\n") {
		p := strings.SplitN(line, ":", 2)
		if len(p) != 2 {
			continue
		}
		f := strings.Fields(p[1])
		if len(f) < 16 {
			continue
		}
		network = append(network, map[string]any{"name": strings.TrimSpace(p[0]), "bytesRecv": num(f[0]), "bytesSent": num(f[8]), "packetsRecv": num(f[1]), "packetsSent": num(f[9])})
	}
	return map[string]any{"time": time.Now().UnixMilli(), "bootId": boot, "cpu": pct, "memory": map[string]any{"total": mem["MemTotal"], "used": used, "available": available, "usedPercent": usedPct}, "swap": map[string]any{"total": mem["SwapTotal"], "used": mem["SwapTotal"] - mem["SwapFree"]}, "host": map[string]any{"hostname": host[0], "kernelVersion": host[1], "kernelArch": host[2], "platform": osData["ID"], "platformVersion": osData["VERSION_ID"], "uptime": up}, "load": map[string]any{"load1": load1, "load5": load5, "load15": load15}, "disk": disk, "network": network}, nil
}
