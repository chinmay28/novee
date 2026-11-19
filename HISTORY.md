# History

A short log of how Novee was built. Dates are 2026.

## November 1: the VM

- Built a VirtualBox VM (Ubuntu, 4 vCPU, 8 GB RAM, 50 GB disk, bridged network) from an unattended install image, so the assistant has its own address on the LAN and is isolated from the host.
- Installed OpenClaw with the gateway as a systemd user service. Model: Qwen3.5 9B served by Ollama on the host PC's GPU, with a 170k token context.

## November 2: Telegram, search, headlines

- Connected a Telegram bot, restricted to the owner.
- OpenClaw hides its `web_search` tool completely when no search provider is configured. Added the key-free DuckDuckGo plugin.
- Search engines return news websites, not stories, so wrote the `news-headlines` skill on RSS feeds. It later learned to merge the same story from several outlets.
- Noticed Telegram "getUpdates conflict" errors roughly every minute.

## November 5: fresh install and a workspace for a small model

- Reinstalled OpenClaw from scratch and renamed the agent to Novee.
- Found the cause of the Telegram conflict: a second, forgotten OpenClaw instance on another machine was polling the same bot token. A test with the gateway stopped proved it (a new poll always wins, so earlier "it works" probes had been misleading). Rotating the token fixed it.
- Rewrote the workspace files for a 9B model: a short AGENTS.md that lists only the tools the model really sees, plain-text formatting rules for Telegram, and honesty rules.
- Wrote the `linux-sysadmin` skill: one script, read-only except for `manage`.
- Made the local model the default, with a hosted model as fallback. A session had been pinned to an auto-discovered model tag with no configured context size, which silently cut the context to 32k.

## November 5 and 6: guard rails

- Tests showed the small model ignores prompt-only rules for dangerous commands: asked to "deal with" a busy CPU, it killed one of its own workers with a raw `kill`, and once claimed a success that had not happened.
- Fix: an exec allowlist (see `config/exec-approvals.json`). Only skill scripts and read-only commands run freely; anything else needs the owner's approval in Telegram. `manage ... --yes` always needs approval.
- Lesson: allowlist path patterns are matched against the real path of the executable, so `/usr/bin/python3` did not match `python3.14` until the pattern was widened.
- Long sticky chat sessions degrade a small model's tool choices; fresh sessions and "thinking low" by default fixed a LAN scan question that had failed.
- Turned heartbeats off; an empty HEARTBEAT.md alone did not stop them.

## November 18: GPU questions and this repository

- Wrote the `gpu-stats` skill after the question "my GPU fans are spinning, what is using it". The GPU is in the host PC, so the host exposes one read-only report script through an SSH key that can run nothing else.
- Created this repository, moved the workspace into it, and wrote `repo-sync` with a privacy check and the rule that GitHub wins a conflict.
- Novee got a deploy key for this repository and made its first push from the VM.

## November 18, later: virtual machines

- Wrote the `vbox-manage` skill so Novee can do what was done by hand to create it: find and download an ISO, build an unattended Ubuntu install, pick the network, and set up SSH. The host PC exposes one helper over a forced-command SSH key; everything that changes state needs `--yes` and therefore the owner's approval, and the VMs `novee` and `umbrel` are protected from itself.
