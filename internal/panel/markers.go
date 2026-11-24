package panel

import (
	"fmt"
	"strings"
)

const certStart = "#CERT-APPLY-CHECK--START"
const certEnd = "#CERT-APPLY-CHECK--END"

// Each SNI server has its own challenge block; reject nesting or unmatched tags.
func replaceMarkedBlocks(source, start, end, content string) (string, error) {
	var out strings.Builder
	inside, found := false, false
	for _, line := range strings.SplitAfter(source, "\n") {
		switch strings.TrimSpace(line) {
		case start:
			if inside {
				return "", fmt.Errorf("配置标记嵌套")
			}
			inside = true
			found = true
			out.WriteString(line)
			out.WriteString(strings.TrimSuffix(content, "\n") + "\n")
		case end:
			if !inside {
				return "", fmt.Errorf("配置标记顺序无效")
			}
			inside = false
			out.WriteString(line)
		default:
			if !inside {
				out.WriteString(line)
			}
		}
	}
	if inside || !found {
		return "", fmt.Errorf("配置标记缺失或顺序无效")
	}
	return out.String(), nil
}

// replaceMarkedBlock deliberately matches whole comment lines. Quoted values or
// comments mentioning a marker are not treated as delimiters.
func replaceMarkedBlock(source, start, end, content string) (string, error) {
	lines := strings.SplitAfter(source, "\n")
	startIndex, endIndex := -1, -1
	for i, line := range lines {
		switch strings.TrimSpace(line) {
		case start:
			if startIndex != -1 {
				return "", fmt.Errorf("重复的开始标记: %s", start)
			}
			startIndex = i
		case end:
			if endIndex != -1 {
				return "", fmt.Errorf("重复的结束标记: %s", end)
			}
			endIndex = i
		}
	}
	if startIndex < 0 || endIndex <= startIndex {
		return "", fmt.Errorf("配置标记缺失或顺序无效")
	}
	if content != "" && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	return strings.Join(lines[:startIndex+1], "") + content + strings.Join(lines[endIndex:], ""), nil
}
