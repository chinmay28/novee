package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// notifyOwner sends one message to the owner the same way repo-sync does:
// $NOVEE_RECEIPTS_NOTIFY_CMD TEXT if set, otherwise `openclaw message send`
// to the first owner in the OpenClaw config. Failing to notify is not an
// error; the sync state already records what happened.
func notifyOwner(text string) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if custom := os.Getenv("NOVEE_RECEIPTS_NOTIFY_CMD"); custom != "" {
		_ = exec.CommandContext(ctx, custom, text).Run()
		return
	}
	home, _ := os.UserHomeDir()
	data, err := os.ReadFile(filepath.Join(home, ".openclaw", "openclaw.json"))
	if err != nil {
		return
	}
	var cfg struct {
		Commands struct {
			OwnerAllowFrom []string `json:"ownerAllowFrom"`
		} `json:"commands"`
	}
	if json.Unmarshal(data, &cfg) != nil || len(cfg.Commands.OwnerAllowFrom) == 0 {
		return
	}
	channel, target, ok := strings.Cut(cfg.Commands.OwnerAllowFrom[0], ":")
	if !ok {
		return
	}
	exe, err := exec.LookPath("openclaw")
	if err != nil {
		exe = filepath.Join(home, ".npm-global", "bin", "openclaw")
	}
	_ = exec.CommandContext(ctx, exe, "message", "send", "--channel", channel, "--target", target, "-m", text).Run()
}
