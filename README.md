# novee

A self hosted bot born in November.

Novee is a personal assistant that runs on [OpenClaw](https://openclaw.ai) inside a small Ubuntu virtual machine, talks to its owner over Telegram, and thinks with a local 9B model (Qwen3.5 9B, 170k context) served by Ollama on the GPU of the host PC. A hosted model is configured only as a fallback.

This repository is Novee's public workspace and the source of truth for it. The VM keeps a clone of it as its OpenClaw workspace, pulls it every 8 hours, and can push its own changes back.

## What is here

- `AGENTS.md`, `SOUL.md`, `HEARTBEAT.md`: the instructions and personality OpenClaw loads into every session. They are written for a small local model: short, concrete, with explicit routing from question to tool.
- `skills/`: every skill Novee has.
  - `linux-sysadmin`: system health, processes, services, logs, network, LAN scan, docker, hardware; changes only through an approval-gated `manage` command.
  - `gpu-stats`: what is using the host PC's GPU, loaded models, and which machine called Ollama.
  - `disk-health`: SMART, bad sectors, filesystem state, mounts, USB hub chain and power, kernel I/O errors for every drive on the host PC, with a verdict per drive and per hub. Read-only; tests only on request.
  - `news-headlines`: real headlines from RSS feeds, with the same story from several outlets merged.
  - `web-browse`: a real browser the model drives one small step at a time (numbered elements, not pixels). This is how Novee answers every shop, order and receipt question. When a site asks for a password, Novee sends the owner a link to that browser and he signs in himself from his phone. Clicks that could buy, send or delete need the owner's approval; passwords and card numbers are never typed by the model. Written in Go.
  - `receipts`: an offline archive of shop receipts (Costco first) with a search that understands abbreviated receipt names. Written in Go. Not used by default any more: the browser does this job now.
  - `repo-sync`: pull, push and conflict handling for this repository, with a privacy check before every push.
  - `vbox-manage`: VirtualBox on the host PC: list, create (unattended Ubuntu), ISO search and download, start, stop, snapshots, console screenshots, SSH setup. Changes need the owner's approval; novee and umbrel are protected.
- `config/`: the exec approval policy and the systemd timer units.
- `examples/`: templates for the files that stay private.
- `docs/SETUP.md`: how the pieces are installed.
- `HISTORY.md`: how Novee got here.
- `avatars/`: the face.

## What is not here

Anything personal or secret stays on the VM: `USER.md`, `TOOLS.md`, `IDENTITY.md`, memory files, receipts and the browser profile that holds the shop sign-in, the OpenClaw config with its tokens, chat ids, addresses and device names. The `.gitignore` is a whitelist, and `repo-sync` refuses to push a file that contains a secret, an email address or a term from a local private list.

## Sync rules

- GitHub is the source of truth.
- Every 8 hours, and on request, Novee pulls. Local edits that merge cleanly are kept and pushed.
- When a local edit and GitHub changed the same lines, GitHub wins. The local version is saved on the VM and the owner gets a message.
