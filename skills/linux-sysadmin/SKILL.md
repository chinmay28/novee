---
name: linux-sysadmin
description: Inspect and manage this Linux machine and its LAN. Use for CPU, memory, disk or swap usage, "is the server ok", slow or busy system, top processes, kill a process, systemd services and their logs, journal errors, dmesg, listening ports, network interfaces, IP and MAC addresses, "what devices are on my network", LAN scan, Wi-Fi networks, open ports on a host, ping, traceroute, DNS lookups, docker containers and their logs, hardware info, temperatures, SMART disk health, logged-in users, failed SSH logins, package updates, and disk cleanup.
metadata:
  {
    "openclaw":
      {
        "emoji": "🛠️",
        "os": ["linux"],
        "requires": { "bins": ["python3"] },
      },
  }
---

# linux-sysadmin

One script answers almost every system question with short plain-text bullets. Run it with `exec`, read the output, then summarize for Chinmay. Everything is read-only except `manage`, which refuses to act without `--yes`.

```bash
python3 {baseDir}/scripts/sysadmin.py <command> [options]
```

## Pick the command from the question

Health and resources:
- "how is the system", "status", "is the VM ok" → `overview`
- "cpu usage", "load", "what is using the cpu", "temperature" → `cpu`
- "memory", "ram", "swap", "what is eating memory", "oom" → `memory`
- "disk space", "disk full", "storage" → `disk`; "what is taking space in /var" → `disk --path /var` (add `--big` for largest files, `--sudo` for root-only dirs); "disk busy / io" → `disk --io`; "disk health / smart" → `disk --smart`
- "top processes", "what is running" → `processes` (`--sort mem`, `--top 15`); "is nginx running", "find process X" → `processes --find X`; "process tree" → `processes --tree`
- "services", "failed services" → `services` (`--failed` adds their logs); "status of unit X" → `services --status X`; "logs of unit X" → `services --logs X -n 50`; "timers / cron-like jobs" → `services --timers`; OpenClaw's own gateway is a user unit: `services --user --status openclaw-gateway`
- "errors in the logs", "anything wrong recently" → `logs`; "logs of X" → `logs --unit X`; "search logs for Y" → `logs --grep Y --since -3d`; "kernel messages" → `logs --dmesg`; "when did it reboot" → `logs --boots`

Network:
- "ip address", "interfaces", "mac address of this machine", "what ports are open here" → `network`
- "who is connected to this machine", "outgoing connections" → `network --connections`
- "bandwidth right now" → `network --stats`; "routes" → `network --routes`; "arp table" → `network --arp`; "firewall" → `network --firewall`; "public ip" → `network --public-ip`
- "what devices are on my network", "what hosts are connected on this lan", "who is on my wifi", "which machines are online", "scan the lan", "list ip and mac addresses", "is there a new device" → `scan` (10-40 s; prints IP, MAC, vendor, hostname, and what changed since the last scan). Run it immediately, do not offer it as a follow-up. It is already the thorough sweep (nmap ARP + ICMP with sudo), so never substitute `network`, the ARP cache, or raw `nmap`. Other subnet: `scan --subnet 192.168.1.0/24`. Quicker, no names: `scan --fast`
- "what ports are open on 192.168.1.244", "what is running on host X" → `scan --ports X`
- "wifi networks", "wifi signal", "which wifi am I on" → `wifi --scan` (this VM has no radio; the script explains that, so say the Wi-Fi scan must run on the host PC)
- "is X up", "can you reach X" → `ping X`; "route to X" → `trace X`; "resolve X", "dns for X" → `dns X` (`--type MX`, `--server 1.1.1.1`); "is port 22 open on X" → `portcheck X 22` or `portcheck X 22,80,443`

Containers, hardware, users, packages:
- "docker containers", "what containers are running" → `docker` (`--all` includes stopped); "container resource usage" → `docker stats`; "images" → `docker images`; "docker disk usage" → `docker df`; "logs of container X" → `docker logs X -n 100`; "details of container X" → `docker inspect X`; "compose projects" → `docker compose`. If Docker is not installed the script says so; do not pretend containers exist.
- "what hardware", "cpu model", "gpu", "how much ram is installed", "temperatures" → `hardware` (`--dmi` for RAM slots, `--pci`, `--usb`)
- "who is logged in", "user accounts", "last logins" → `users`; "ssh brute force", "failed logins" → `users --failed`
- "updates available", "security updates", "needs reboot" → `packages` (`--upgradable`, `--security`); "is package X installed" → `packages --installed X`; "find a package for X" → `packages --search X`; "what was installed recently" → `packages --history`

Changing things (`manage`, always needs confirmation):
- "kill process X" → `manage kill X` then, after Chinmay confirms, `manage kill X --yes` (`--signal KILL` only if TERM failed)
- "restart service X" → `manage service restart X --yes` (`--user` for user units such as openclaw-gateway)
- "restart container X" → `manage docker restart X --yes`
- "lower priority of pid N" → `manage nice N 10 --yes`
- "free up disk space", "clean up" → `manage clean` (shows the plan), then `manage clean --yes` (`--docker` to also prune Docker). Never prunes volumes.

How confirmation works: `manage` without `--yes` only prints the plan and is always allowed. `manage ... --yes` is never allowed to run silently: OpenClaw sends Chinmay an approve/deny prompt in Telegram, and the command runs only if Chinmay approves. So the flow is:
1. Run `manage <action> <target>` (no `--yes`) and show Chinmay the plan in one short message.
2. If Chinmay agrees, or if Chinmay's own message already named that exact target, run the same command with `--yes` once.
3. If the tool result says the command is pending approval or was denied, stop. Tell Chinmay it is waiting for the approval button (or was denied). Do not retry, do not try other commands, do not kill anything another way.
The script refuses to touch the OpenClaw gateway, sshd, systemd or PID 1 even with `--yes`, and refuses to kill more than 5 processes by name. Always use `manage` rather than raw `kill`, `systemctl` or `docker` commands; raw ones are blocked anyway. There is no reboot or shutdown command on purpose; if asked, explain how (`sudo reboot`) but do not run it.

## How to answer

- Run exactly one command per question. Chain a second only when the first output points to it (for example `overview` shows a failed service, then `services --status X`).
- Summarize. Keep the numbers and names, drop the noise. Lines starting with `!` are warnings; always repeat those.
- Quote values exactly as printed (percentages, sizes, IPs, MACs). Never estimate or fill in a value the script did not print.
- If the script prints `ERROR` or `not installed`, say so and, when a hint is printed, offer it.
- For `scan`, list each device as "IP, MAC, vendor, name" and point out the gateway, this machine, and anything new or missing.
- For long outputs (logs, processes) pick the relevant lines; offer to show more.

## Beyond the script

For anything the script does not cover, read `{baseDir}/reference.md` (a categorized command sheet) and run the matching command with `exec`. Prefer read-only commands. Ask before anything that modifies the system.
