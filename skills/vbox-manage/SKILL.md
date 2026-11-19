---
name: vbox-manage
description: Create and manage VirtualBox virtual machines on Chinmay's host PC (list VMs, VM status or IP, create a new VM with CPUs/RAM/disk, unattended Ubuntu install, find and download ISO images, start/stop/delete, snapshots, console screenshot, set up SSH to a VM). Use for "list my VMs", "create a VM", "spin up a VM", "download an ISO", "is VM X running", "VM X's IP", "ssh to VM X", "delete VM X".
metadata:
  {
    "openclaw":
      {
        "emoji": "📦",
        "os": ["linux"],
        "requires": { "bins": ["python3", "ssh"] },
      },
  }
---

# vbox-manage

The virtual machines run in VirtualBox on Chinmay's host PC (the GPU machine; TOOLS.md has its name). This VM, novee, is one of them. The script below already reaches the host by itself; never run `VBoxManage` or `ssh` to the host yourself, and never ask for keys or passwords.

```bash
python3 {baseDir}/scripts/vboxmanage.py <command> [options]
```

Read-only commands run freely. Every command that changes something needs `--yes`, and `--yes` makes OpenClaw ask Chinmay for approval in Telegram before it runs. Run a `--yes` command only when Chinmay asked for exactly that action, run it once, and if the result says the approval is pending or denied, stop and tell Chinmay.

The VMs `novee` (this one) and `umbrel` are protected: the host refuses to stop, change or delete them even with approval.

## Commands

Read-only:
- `host`: host PC resources (CPUs, RAM available, disk free), running VMs, network interfaces, cached ISOs. Run it before creating a VM to size it sensibly.
- `list`: every VM with state, CPUs, RAM, network and IP.
- `info NAME`, `ip NAME`: details or just the address. `NAME.local` also resolves on the LAN.
- `iso list`: ISO images already on the host. `iso search ubuntu [26.04]`, `iso search debian`, `iso search alpine`: images available for download (newest last; Ubuntu `live-server` images support unattended install). `iso status`: progress of downloads. `iso verify NAME`: checksum (takes a minute).
- `install-status NAME`: phase of an unattended install (`installing`, `booting`, `running` with IP). Add `--wait` to block until done.
- `snapshot NAME list`, `screenshot NAME` (saves a PNG you can send with the message tool), `log NAME`.
- `ssh-setup NAME`: makes `ssh NAME` work from this VM and from the host PC (installs keys, writes the SSH config entry) and tests it. Safe to repeat.

Change state (each needs `--yes`):
- `iso download URL --yes`: fetch an ISO to the host (runs in the background; check with `iso status`).
- `create NAME --cpus N --ram MB --disk GB [--net bridged|nat] --iso ISO-NAME --unattended --wait --yes`: new VM. With an Ubuntu live-server ISO and `--unattended`, Ubuntu installs itself (user `chinmay`, random password saved on the host, this VM's SSH key preinstalled, hostname NAME), the ISO is detached, the VM boots, and `--wait` then runs `ssh-setup` and reports the IP. Takes 7 to 12 minutes. Without `--unattended` the ISO is attached for a manual install (use `screenshot` to see the console).
- `start NAME --yes`, `stop NAME --yes` (clean shutdown), `poweroff NAME --yes`, `reset`, `pause`, `resume`.
- `modify NAME --cpus N --ram MB --net bridged|nat --yes` (VM must be off).
- `snapshot NAME take|restore|delete SNAPNAME --yes`.
- `delete NAME --yes`: removes the VM, its disks and folder. Only after Chinmay named that VM.

## How to handle requests

- "List / show my VMs", "is X running", "what is X's IP": run `list` or `ip X` in the first turn, no questions.
- "Create a VM" with sizes given: run `host` first (fits? ISO cached?), then one `create ... --yes`. Defaults when not stated: 2 CPUs, 2048 MB, 20 GB, bridged network, Ubuntu live-server (newest cached), `--unattended --wait`. If no suitable ISO is cached, run `iso search ubuntu`, then `iso download URL --yes`, wait with `iso status`, then create.
- Ask only when a required fact is missing and cannot be defaulted (for example which of two cached ISOs, or a VM name).
- After `create --wait` finishes, report: name, IP, user, how to log in (`ssh NAME` from this VM; `ssh user@IP` from the host PC), and that the password is in `CREDENTIALS.txt` in the VM's folder on the host PC. Never print the password.
- "Stop/delete X": confirm X is not protected and is the VM Chinmay named, then run the command once with `--yes`.
- Sizes: `--ram` is in MB (8 GB = 8192), `--disk` in GB. Never give a VM more CPUs or RAM than `host` shows available.
- Copy names, IPs and numbers exactly from the output. If the script prints `ERROR`, quote it; do not guess or retry with a different approach.

## Setup (already done on Novee's VM)

Needs `~/.config/novee/vbox-manage.json` (address of the host PC, see `examples/vbox-manage.example.json`), the key `~/.ssh/vbox-manage` that the host accepts only for `host/novee-vbox`, and the host helper installed as `~/.local/bin/novee-vbox`. Details in `docs/SETUP.md`.
