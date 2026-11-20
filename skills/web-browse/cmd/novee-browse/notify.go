package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// notifyOwner sends one message to the owner the same way repo-sync does:
// $NOVEE_BROWSE_NOTIFY_CMD TEXT if set, otherwise `openclaw message send`
// to the first owner in the OpenClaw config.
func notifyOwner(text string) error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if custom := os.Getenv("NOVEE_BROWSE_NOTIFY_CMD"); custom != "" {
		return exec.CommandContext(ctx, custom, text).Run()
	}
	home, _ := os.UserHomeDir()
	data, err := os.ReadFile(filepath.Join(home, ".openclaw", "openclaw.json"))
	if err != nil {
		return err
	}
	var cfg struct {
		Commands struct {
			OwnerAllowFrom []string `json:"ownerAllowFrom"`
		} `json:"commands"`
	}
	if json.Unmarshal(data, &cfg) != nil || len(cfg.Commands.OwnerAllowFrom) == 0 {
		return errors.New("no owner in the OpenClaw config")
	}
	channel, target, ok := strings.Cut(cfg.Commands.OwnerAllowFrom[0], ":")
	if !ok {
		return errors.New("the owner entry in the OpenClaw config has no channel")
	}
	exe, err := exec.LookPath("openclaw")
	if err != nil {
		exe = filepath.Join(home, ".npm-global", "bin", "openclaw")
	}
	return exec.CommandContext(ctx, exe, "message", "send", "--channel", channel, "--target", target, "-m", text).Run()
}
