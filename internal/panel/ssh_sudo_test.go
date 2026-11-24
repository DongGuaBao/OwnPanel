package panel

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestSudoPromptFraming(t *testing.T) {
	const prompt = "password-token-12345"
	const ready = "ready-token-67890"
	for width := 1; width < 40; width++ {
		var output bytes.Buffer
		events := make(chan string, 8)
		filter := &sudoOutput{dest: &output, prompt: prompt, ready: ready, events: events}
		text := "lecture\n" + prompt + ready + "root output\n"
		for start := 0; start < len(text); start += width {
			end := min(start+width, len(text))
			if _, err := filter.Write([]byte(text[start:end])); err != nil {
				t.Fatal(err)
			}
		}
		if len(events) != 2 || <-events != "prompt" || <-events != "ready" {
			t.Fatalf("handshake lost at frame size %d", width)
		}
		filter.active.Store(true)
		if err := filter.flush(); err != nil {
			t.Fatal(err)
		}
		if _, err := filter.Write([]byte("later output")); err != nil {
			t.Fatal(err)
		}
		if output.String() != "lecture\nroot output\nlater output" {
			t.Fatalf("corrupt output at frame size %d: %q", width, output.String())
		}
	}
}

func TestSudoFailureStopsAutomaticRetry(t *testing.T) {
	driver := &sshDriver{sudoAuthBlocked: true}
	if _, _, err := driver.startPasswordSudo(context.Background(), nil, "/bin/sh", false); err == nil || !strings.Contains(err.Error(), "自动重试已停止") {
		t.Fatal("failed password was automatically retried")
	}
}
