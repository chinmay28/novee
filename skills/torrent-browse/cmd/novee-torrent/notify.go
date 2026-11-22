package main

import (
	"bytes"
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
// $NOVEE_TORRENT_NOTIFY_CMD TEXT if set, otherwise `openclaw message send`
// to the first owner in the OpenClaw config.
func notifyOwner(text string) error { return notifyOwnerFile(text, "") }

// notifyOwnerFile sends the owner a message with a file attached (none if
// path is empty). The custom command gets the path as its second argument.
func notifyOwnerFile(text, path string) error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if custom := os.Getenv("NOVEE_TORRENT_NOTIFY_CMD"); custom != "" {
		if path != "" {
			return exec.CommandContext(ctx, custom, text, path).Run()
		}
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
	args := []string{"message", "send", "--channel", channel, "--target", target, "-m", text}
	if path != "" {
		args = append(args, "--media", path)
	}
	// The CLI reports some failures (a file it may not read, for one) in its
	// output while still exiting with success.
	out, err := exec.CommandContext(ctx, exe, args...).CombinedOutput()
	if err == nil && bytes.Contains(out, []byte("Error")) {
		lines := strings.Split(strings.TrimSpace(string(out)), "\n")
		err = errors.New(lines[len(lines)-1])
	}
	return err
}
