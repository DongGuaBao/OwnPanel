package panel

import (
	"encoding/json"
	"fmt"
	"strings"
)

// lineMatches aligns the last generated version with another version. Keeping
// source indexes lets a patch preserve user lines inserted between owned lines.
func lineMatches(base, other []string) ([]int, error) {
	if (len(base)+1)*(len(other)+1) > 4000000 {
		return nil, fmt.Errorf("配置过大，无法安全合并，请使用原始配置编辑")
	}
	width := len(other) + 1
	table := make([]uint16, (len(base)+1)*width)
	for i := len(base) - 1; i >= 0; i-- {
		for j := len(other) - 1; j >= 0; j-- {
			if base[i] == other[j] {
				table[i*width+j] = table[(i+1)*width+j+1] + 1
			} else {
				a, b := table[(i+1)*width+j], table[i*width+j+1]
				if a > b {
					table[i*width+j] = a
				} else {
					table[i*width+j] = b
				}
			}
		}
	}
	result := make([]int, len(base))
	for i := range result {
		result[i] = -1
	}
	for i, j := 0, 0; i < len(base) && j < len(other); {
		if base[i] == other[j] {
			result[i] = j
			i++
			j++
		} else if table[(i+1)*width+j] > table[i*width+j+1] {
			i++
		} else {
			j++
		}
	}
	return result, nil
}

// patchNginxText applies only form changes, never replacing the user's file.
// Editing the same generated directive in both versions is an explicit conflict.
func patchNginxText(base, current, desired string) (string, error) {
	if base == desired || current == desired {
		return current, nil
	}
	if current == base {
		return desired, nil
	}
	old, live, next := strings.SplitAfter(base, "\n"), strings.SplitAfter(current, "\n"), strings.SplitAfter(desired, "\n")
	toLive, err := lineMatches(old, live)
	if err != nil {
		return "", err
	}
	toNext, err := lineMatches(old, next)
	if err != nil {
		return "", err
	}
	deleted := map[int]bool{}
	insertions := map[int][]string{}
	beforeOld, beforeNext := -1, -1
	for anchor := 0; anchor <= len(old); anchor++ {
		if anchor < len(old) && toNext[anchor] < 0 {
			continue
		}
		afterNext := len(next)
		if anchor < len(old) {
			afterNext = toNext[anchor]
		}
		from, to := beforeOld+1, anchor
		if from < to || beforeNext+1 < afterNext {
			for i := from; i < to; i++ {
				if toLive[i] < 0 {
					return "", fmt.Errorf("同一配置指令已被手动修改，无法自动合并：%s", strings.TrimSpace(old[i]))
				}
				deleted[toLive[i]] = true
			}
			position := len(live)
			for i := anchor; i < len(old); i++ {
				if toLive[i] >= 0 {
					position = toLive[i]
					break
				}
			}
			if from < to {
				position = toLive[from]
			}
			start := 0
			for i := beforeOld; i >= 0; i-- {
				if toLive[i] >= 0 {
					start = toLive[i] + 1
					break
				}
			}
			// Reuse identical manual additions, notably WebSocket proxy headers.
			additions := append([]string(nil), next[beforeNext+1:afterNext]...)
			for _, line := range additions {
				exists := false
				for i := start; i < position; i++ {
					if !deleted[i] && strings.TrimSpace(live[i]) == strings.TrimSpace(line) {
						exists = true
						break
					}
				}
				if !exists {
					insertions[position] = append(insertions[position], line)
				}
			}
		}
		beforeOld, beforeNext = anchor, afterNext
	}
	var result strings.Builder
	for i := 0; i <= len(live); i++ {
		for _, line := range insertions[i] {
			result.WriteString(line)
		}
		if i < len(live) && !deleted[i] {
			result.WriteString(live[i])
		}
	}
	return result.String(), nil
}

func patchSiteFiles(previous site, prefix string, current map[string]string, generated map[string][]byte) error {
	baseline := renderSiteAt(previous, prefix)
	manual := previous.Manual
	for _, name := range []string{"server.conf", "routing.conf", "tls.conf", "custom.conf"} {
		live, ok := current[name]
		if !ok {
			return fmt.Errorf("缺少配置文件 %s", name)
		}
		patched, err := patchNginxText(string(baseline[name]), live, string(generated[name]))
		if err != nil {
			return fmt.Errorf("%s：%w", name, err)
		}
		if patched != string(generated[name]) {
			manual = true
		}
		generated[name] = []byte(patched)
	}
	var metadata map[string]any
	if err := json.Unmarshal(generated["site.json"], &metadata); err != nil {
		return err
	}
	metadata["manual"] = manual
	raw, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return err
	}
	generated["site.json"] = raw
	return nil
}
