# Linux sysadmin command reference

Raw commands for things `sysadmin.py` does not wrap. Read-only unless marked MUTATES. Commands marked (sudo) need root; use `sudo -n` on this VM (passwordless). Prefer `trash` over `rm`.

## System identity and uptime
- `hostnamectl` — hostname, OS, kernel, virtualization, machine id
- `cat /etc/os-release` — distro and version
- `uname -a` — kernel version and architecture
- `uptime -p` / `uptime -s` — pretty uptime / boot time
- `systemd-detect-virt` — VM or container type
- `timedatectl` — time, timezone, NTP sync state; `timedatectl set-timezone America/Los_Angeles` MUTATES (sudo)
- `cat /proc/cmdline` — kernel boot parameters
- `last reboot | head` — reboot history
- `journalctl --list-boots` — boots known to the journal

## CPU and load
- `lscpu` — CPU model, cores, threads, flags, caches, virtualization
- `nproc` — usable CPU count
- `cat /proc/loadavg` — 1/5/15 min load, running/total tasks
- `mpstat -P ALL 1 3` — per-core utilization (sysstat)
- `top -bn1 | head -20` — snapshot of top; `top -o %MEM -bn1` sort by memory
- `htop` — interactive (not for exec)
- `pidstat 1 5` — per-process CPU over time
- `cat /sys/devices/system/cpu/cpu*/cpufreq/scaling_cur_freq` — current clock per core
- `cat /proc/pressure/cpu` — PSI pressure stall info (also /proc/pressure/memory, /proc/pressure/io)
- `vmstat 1 5` — run queue, memory, swap, io, context switches, cpu split

## Memory
- `free -h` — RAM and swap summary
- `cat /proc/meminfo` — full breakdown
- `smem -rs pss | head` — proportional memory per process (if installed)
- `ps -eo pid,user,rss,pmem,comm --sort=-rss | head -15` — top RSS processes
- `cat /proc/<pid>/status | grep -E 'VmRSS|VmSwap|Threads'` — one process
- `swapon --show` — swap devices; `cat /proc/sys/vm/swappiness`
- `journalctl -k | grep -iE 'out of memory|oom-kill'` — OOM events
- `slabtop -o | head -20` — kernel slab usage (sudo)
- `sudo sysctl vm.drop_caches=3` — drop page cache, MUTATES (safe but affects performance briefly)

## Disks, filesystems, storage
- `df -hT -x tmpfs -x devtmpfs` — filesystem usage; `df -i` — inodes
- `lsblk -o NAME,SIZE,TYPE,FSTYPE,MOUNTPOINTS,MODEL,SERIAL` — block device tree
- `du -xh --max-depth=1 /var | sort -rh | head` — space per directory
- `ncdu -x /` — interactive disk usage (not for exec); `ncdu -o report.json -x /` then inspect
- `find / -xdev -type f -size +500M -printf '%s %p\n' 2>/dev/null | sort -rn | head` — big files
- `find /var/log -type f -mtime -1` — recently changed logs
- `iostat -dxz 1 3` — device throughput, await, utilization
- `iotop -obn1` — processes doing IO (sudo)
- `cat /proc/diskstats` — raw counters
- `smartctl -a /dev/sda` — full SMART (sudo); `smartctl -t short /dev/sda` MUTATES (starts a self-test)
- `nvme smart-log /dev/nvme0` — NVMe health (sudo, nvme-cli)
- `blkid` — UUIDs and fs types (sudo for all)
- `cat /etc/fstab` / `findmnt` — mount configuration and tree; `findmnt --verify` checks fstab
- `mount | column -t` — current mounts with options
- `lsof +D /mnt/x` / `fuser -vm /mnt/x` — who holds a mount busy
- `tune2fs -l /dev/sda1` — ext4 metadata (sudo); `fsck` only on unmounted fs, MUTATES
- `lvs; vgs; pvs` — LVM layout (sudo); `lvextend -r -L +10G vg/lv` MUTATES
- `cat /proc/mdstat`; `mdadm --detail /dev/md0` — software RAID status (sudo)
- `btrfs filesystem usage /` / `zpool status` — btrfs / ZFS health
- `losetup -a` — loop devices
- `dmesg | grep -iE 'error|fail|I/O' | tail` — disk errors (sudo on some systems)

## Processes and jobs
- `ps aux --sort=-%cpu | head` / `ps -eo pid,ppid,user,stat,etime,cmd --forest` — process list and tree
- `pstree -p` — tree with pids; `pstree -p <pid>` subtree
- `pgrep -af pattern` — find pids by name/cmdline
- `cat /proc/<pid>/cmdline | tr '\0' ' '` — exact command line
- `ls -l /proc/<pid>/cwd /proc/<pid>/exe` — working dir and binary
- `lsof -p <pid>` — open files/sockets of a process; `lsof -i :8080` — who owns a port
- `strace -p <pid> -f -e trace=network` — live syscalls (sudo, heavy, for debugging only)
- `cat /proc/<pid>/limits` — ulimits of a running process; `ulimit -a` for the shell
- `nice -n 10 cmd` / `renice -n 5 -p <pid>` — priority, MUTATES; `ionice -c3 -p <pid>` IO priority
- `kill -TERM <pid>`, then `kill -KILL <pid>` only if it ignores TERM — MUTATES
- `nohup cmd &` / `setsid cmd` — detach; `systemd-run --user --unit=name cmd` — run as a transient unit
- `jobs; fg; bg; disown` — shell job control (interactive only)
- `timeout 30 cmd` — bound a command's runtime

## systemd services, timers, boot
- `systemctl status` — overall state; `systemctl --failed` — failed units
- `systemctl list-units --type=service --state=running` — running services
- `systemctl list-unit-files --state=enabled` — enabled at boot
- `systemctl status X`; `systemctl show X -p MainPID,NRestarts,ActiveEnterTimestamp,MemoryCurrent`
- `systemctl cat X` — unit file contents; `systemctl list-dependencies X`
- `systemctl start|stop|restart|reload|enable|disable X` — MUTATES (sudo); `--user` for user units
- `systemctl daemon-reload` — after editing unit files, MUTATES
- `systemctl edit X` — override drop-in (interactive); drop-ins live in /etc/systemd/system/X.service.d/
- `systemctl list-timers --all` — scheduled timers; `systemctl status X.timer`
- `systemd-analyze` / `systemd-analyze blame` / `systemd-analyze critical-chain` — boot time breakdown
- `systemd-analyze verify X.service` — lint a unit
- `loginctl list-sessions`; `loginctl enable-linger user` — keep user units running without login (MUTATES)
- `systemd-cgtop` — resource use per cgroup/service
- `crontab -l` / `sudo crontab -l -u root` / `ls /etc/cron.*` — cron jobs; `crontab -e` MUTATES

## Logs
- `journalctl -p err -b` — errors this boot; `-p warning` warnings and above
- `journalctl -u X -n 100 --no-pager` — a unit's recent log; `-f` follows (not for exec)
- `journalctl --since "1 hour ago"` / `--since today` / `--since "2026-11-01" --until "2026-11-02"`
- `journalctl -k` — kernel ring buffer via journal; `dmesg -T --level=err,warn` (sudo on some systems)
- `journalctl --grep "pattern"` — regex search; `journalctl _PID=1234`; `journalctl _COMM=sshd`
- `journalctl --disk-usage` — journal size; `journalctl --vacuum-time=2w` MUTATES (sudo)
- `journalctl --user -u openclaw-gateway -n 50` — OpenClaw's own gateway log on this VM
- `tail -n 100 /var/log/syslog` / `/var/log/auth.log` / `/var/log/kern.log` — classic logs if rsyslog is present
- `ls -lt /var/log | head` — most recently written logs
- `logrotate -d /etc/logrotate.conf` — dry-run rotation config
- `lnav /var/log/syslog` — interactive log viewer (not for exec)

## Network: interfaces, addresses, routes
- `ip -br addr` — one line per interface with addresses; `ip -br link` — MACs and state
- `ip addr show dev enp0s3` — full detail; `ip -s link` — rx/tx counters and errors
- `ip route` / `ip -6 route` — routing table; `ip route get 8.8.8.8` — which interface/gateway a destination uses
- `ip neigh` — ARP/neighbour cache (IP to MAC); `ip neigh flush all` MUTATES (sudo)
- `ethtool enp0s3` — link speed/duplex; `ethtool -S enp0s3` NIC stats; `ethtool -i` driver
- `cat /sys/class/net/*/address` — MAC addresses; `cat /sys/class/net/enp0s3/speed`
- `resolvectl status` / `resolvectl query host` — systemd-resolved DNS config and lookups
- `cat /etc/hosts`; `getent hosts name` — local resolution
- `hostname -I` — all IPv4 addresses; `hostname -f` FQDN
- `nmcli dev status` / `nmcli con show` — NetworkManager devices and connections (if installed); `nmcli con up X` MUTATES
- `networkctl status` — systemd-networkd state; configs in /etc/netplan/*.yaml (Ubuntu); `netplan try` MUTATES (sudo)
- `cat /proc/net/dev` — raw interface counters; `sar -n DEV 1 3` — rates (sysstat)
- `vnstat` — historical bandwidth (if installed); `iftop -i enp0s3` / `nethogs` — live per-host/process bandwidth (sudo, interactive)
- `ss -s` — socket summary; `ss -tulpn` listening; `ss -tnp state established` connections; `ss -tn dst 192.168.1.244`
- `lsof -i -P -n | grep LISTEN` — listening processes
- `conntrack -L` — NAT/connection tracking table (sudo)
- `tcpdump -nni enp0s3 -c 50 'port 53'` — packet capture (sudo); `tcpdump -w cap.pcap` to file
- `curl -sI https://example.com` — HTTP headers; `curl -w '%{time_total}\n' -o /dev/null -s URL` — timing
- `curl -4 -s https://api.ipify.org` — public IPv4
- `mtr -rwc 10 host` — combined ping+traceroute report (if installed); `traceroute -n host`; `tracepath -n host`
- `dig +short name` / `dig name MX` / `dig -x 192.168.1.1` / `dig @1.1.1.1 name` — DNS; `dig +trace name` delegation path
- `nslookup name`; `host name`
- `nc -zv host 22` — port probe; `nc -l 9000` listen (testing); `nc host 25` talk to a service
- `iperf3 -s` on one machine, `iperf3 -c host` on the other — LAN throughput test (if installed)
- `avahi-browse -atr` — mDNS/Bonjour services on the LAN; `avahi-resolve -a 192.168.1.109` — IP to .local name
- `nmblookup -A 192.168.1.10` — NetBIOS name of a Windows host (samba-common-bin)
- `whois 8.8.8.8` / `whois example.com` — ownership (if installed)
- `ipcalc 192.168.1.109/24` — subnet math (if installed); python: `python3 -c "import ipaddress;print(list(ipaddress.ip_network('192.168.1.0/24').hosts())[:3])"`

## Network: scanning and discovery (own LAN only)
- `sudo nmap -sn 192.168.1.0/24` — host discovery with MAC and vendor; add `-oG -` or `-oX -` for parseable output
- `sudo arp-scan --localnet` — fast ARP sweep with vendor names; `arp-scan -I enp0s3 192.168.1.0/24`
- `nmap -Pn --top-ports 100 host` — common ports; `nmap -sV -p 22,80,443 host` — service versions; `nmap -O host` OS guess (sudo)
- `nmap -p- host` — all 65535 ports (slow); `nmap -sU --top-ports 20 host` — UDP (sudo, slow)
- `nmap --script=ssl-cert -p 443 host` — TLS certificate; `nmap --script=smb-os-discovery host`
- `fping -ag 192.168.1.0/24` — quick alive sweep (if installed)
- `for i in $(seq 1 254); do ping -c1 -W1 192.168.1.$i >/dev/null && echo 192.168.1.$i; done` — plain ping sweep
- `sudo tcpdump -nni enp0s3 -c 100 arp` — watch who is asking for whom
- `ip neigh | grep -v FAILED` — devices this machine has talked to recently

## Wi-Fi (needs a wireless interface; this VM has none, the host PC has wlo1)
- `iw dev` — wireless interfaces and their mode; `iw dev wlo1 link` — current connection, signal, bitrate
- `sudo iw dev wlo1 scan | grep -E 'SSID|signal|freq|channel'` — nearby networks (sudo)
- `nmcli dev wifi list --rescan yes` — networks with signal %, channel, security (NetworkManager)
- `nmcli dev wifi connect SSID password PASS` — join, MUTATES; `nmcli con show --active`
- `nmcli -f ALL dev wifi` / `nmcli -t -f SSID,SIGNAL,CHAN,SECURITY dev wifi` — terse fields
- `iwconfig` / `iwlist wlo1 scan` — legacy tools (wireless-tools)
- `cat /proc/net/wireless` — link quality, level, noise
- `rfkill list` — radio blocks; `rfkill unblock wifi` MUTATES
- `wpa_cli status` / `wpa_cli scan && wpa_cli scan_results` — wpa_supplicant directly
- `iw reg get` — regulatory domain; `iw phy` — radio capabilities (bands, channels)
- `hostapd`/`create_ap` — run an access point (project-specific)
- `sudo airmon-ng`/`aircrack-ng` — only for networks you own; not installed here

## Firewall and security
- `sudo ufw status verbose` — ufw rules; `ufw allow 22/tcp` MUTATES; `ufw enable` MUTATES (confirm SSH is allowed first)
- `sudo nft list ruleset` — nftables; `sudo iptables -L -n -v` / `iptables -S` — legacy view
- `sudo fail2ban-client status sshd` — bans (if installed)
- `sudo sshd -T | grep -iE 'passwordauth|permitroot|port'` — effective sshd config
- `sudo journalctl -u ssh --since -24h | grep -c 'Failed password'` — brute force count
- `sudo lastb | head` — failed logins (btmp); `last -F` — successful logins; `lastlog`
- `w` / `who` — current sessions; `loginctl list-sessions`
- `sudo -l` — what this user may run; `getent group sudo`
- `cat /etc/passwd | awk -F: '$3>=1000'` — human accounts; `passwd -S user` status (sudo)
- `find / -perm -4000 -type f 2>/dev/null` — setuid binaries
- `ls -la ~/.ssh; cat ~/.ssh/authorized_keys` — SSH keys for this user
- `openssl x509 -in cert.pem -noout -dates -subject` — certificate details; `openssl s_client -connect host:443 -servername host </dev/null | openssl x509 -noout -dates`
- `sudo apt list --upgradable 2>/dev/null | grep -i security` — pending security updates
- `sudo unattended-upgrade --dry-run -d` — what auto-updates would do
- `getenforce` / `aa-status` — SELinux / AppArmor state (sudo)
- `sudo auditctl -l` — audit rules (if auditd)
- `chkrootkit` / `rkhunter --check` — rootkit scans (if installed)
- `sudo debsums -s` — verify installed package files (debsums)

## Users, groups, permissions
- `id user` — uid, gid, groups; `groups user`
- `getent passwd user` / `getent group name`
- `sudo useradd -m -s /bin/bash name` / `sudo usermod -aG docker name` / `sudo userdel -r name` — MUTATES
- `sudo passwd name` / `sudo chage -l name` — password state; `chage -E` expiry MUTATES
- `ls -la`, `stat file` — owner, mode, times; `namei -l /path/to/file` — permission along the path
- `chmod`, `chown`, `chgrp` — MUTATES; `umask`
- `getfacl file` / `setfacl -m u:name:rw file` (MUTATES) — ACLs
- `lsattr file` / `chattr +i file` (MUTATES) — immutable and other attributes
- `sudo visudo -c` — validate sudoers; files in /etc/sudoers.d/

## Packages (Debian/Ubuntu apt)
- `apt list --upgradable` — pending updates; `apt-cache policy pkg` — versions and sources
- `apt show pkg` — package info; `apt-cache search --names-only term` / `apt-file search path` (apt-file)
- `dpkg -l | grep term` / `dpkg -L pkg` (files of a package) / `dpkg -S /usr/bin/x` (which package owns a file)
- `sudo apt update && sudo apt upgrade` — MUTATES; `sudo apt full-upgrade`; `sudo apt install pkg`; `sudo apt remove --purge pkg`; `sudo apt autoremove`
- `apt-mark showhold` / `sudo apt-mark hold pkg` (MUTATES)
- `cat /var/log/apt/history.log` — install history; `/var/log/dpkg.log`
- `ls /etc/apt/sources.list.d/` — repositories; `apt-key list` (deprecated) / `/etc/apt/keyrings/`
- `snap list` / `sudo snap refresh` (MUTATES); `flatpak list`
- `pip list` / `pipx list` / `npm ls -g --depth=0` — language package managers
- Other distros: `dnf check-update`, `dnf history`; `pacman -Syu`, `pacman -Qi pkg`; `apk list -u`; `zypper lu`

## Docker and containers
- `docker ps -a` — all containers; `docker ps --format '{{.Names}}\t{{.Status}}\t{{.Ports}}'`
- `docker stats --no-stream` — CPU/mem/net/io per container
- `docker logs --tail 100 -t name` / `docker logs -f name` (follow, not for exec); `docker logs --since 1h name`
- `docker inspect name` — full JSON; `docker inspect -f '{{.State.Status}} {{.RestartCount}}' name`
- `docker exec -it name sh` — shell inside (interactive); `docker exec name cmd` — one command
- `docker top name` — processes in container; `docker port name` — port mappings
- `docker images` / `docker image prune` (MUTATES dangling only) / `docker system df` / `docker system prune -a` (MUTATES, removes unused images)
- `docker volume ls` / `docker volume inspect v` / `docker volume prune` (MUTATES, destroys data: confirm twice)
- `docker network ls` / `docker network inspect bridge` — container IPs
- `docker start|stop|restart name` — MUTATES; `docker update --restart unless-stopped name`
- `docker events --since 1h --until now` — lifecycle events
- `docker info` — daemon, storage driver, cgroup, root dir; `docker version`
- `docker compose ls`; in a project dir: `docker compose ps`, `docker compose logs --tail 100`, `docker compose pull && docker compose up -d` (MUTATES), `docker compose down` (MUTATES)
- `journalctl -u docker -n 50` — daemon log; `cat /etc/docker/daemon.json`
- `podman ps` / `podman stats` — same verbs if podman is used; `ctr -n k8s.io containers list` (containerd)
- `systemd-cgls` / `systemd-cgtop` — cgroup tree and usage including containers
- `lxc list` / `incus list` — LXD/Incus containers and VMs; `virsh list --all` — libvirt VMs; `VBoxManage list runningvms` — VirtualBox (on the host)

## Hardware
- `lscpu`, `lsmem`, `lspci -nnk` (devices with drivers), `lsusb -t`, `lsblk`, `lshw -short` (sudo), `inxi -Fxz` (if installed)
- `sudo dmidecode -t system|bios|memory|processor` — SMBIOS details (RAM slots, speeds, serials)
- `cat /proc/cpuinfo | grep -m1 'model name'`; `cat /proc/meminfo | head -3`
- `sensors` (lm-sensors; run `sudo sensors-detect` once on bare metal); `cat /sys/class/thermal/thermal_zone*/temp`
- `nvidia-smi` — NVIDIA GPU usage; `nvidia-smi -q -d TEMPERATURE,POWER`; `rocm-smi` AMD; `intel_gpu_top` Intel
- `upower -i $(upower -e | grep BAT)` / `cat /sys/class/power_supply/BAT0/capacity` — battery
- `acpi -V` — ACPI battery/thermal (if installed)
- `lsmod` / `modinfo mod` / `sudo modprobe mod` (MUTATES) — kernel modules; `dmesg | grep -i firmware`
- `udevadm info -q all -n /dev/sda` — device attributes; `udevadm monitor` — live hotplug events
- `hdparm -I /dev/sda` (sudo) — disk identity/features; `hdparm -tT /dev/sda` quick read benchmark
- `cat /proc/interrupts` — IRQ distribution; `cat /proc/scsi/scsi`
- `fwupdmgr get-devices` / `fwupdmgr get-updates` — firmware updates
- `sudo ethtool -p enp0s3 10` — blink a NIC's LED to find the port (MUTATES briefly)

## Performance troubleshooting playbook
- Slow system: `uptime` (load vs cores) → `top -bn1 | head -15` → `vmstat 1 5` (r, b, si/so, wa) → `iostat -dxz 1 3` → `cat /proc/pressure/*`
- Memory pressure: `free -h` → `ps -eo rss,comm --sort=-rss | head` → `journalctl -k | grep -i oom` → `cat /proc/pressure/memory`
- Disk full: `df -h` → `du -xh --max-depth=1 / | sort -rh | head` → check journal (`journalctl --disk-usage`), apt cache, docker (`docker system df`), old kernels, `/var/log`, `~/.cache`, deleted-but-open files (`lsof +L1`)
- Network slow: `ping -c5 gateway` → `ping -c5 1.1.1.1` → `dig +stats example.com` → `mtr -rwc 10 1.1.1.1` → `ethtool iface` (speed/duplex) → `ip -s link` (errors/drops)
- Service not starting: `systemctl status X` → `journalctl -u X -n 50` → `systemctl cat X` → `systemd-analyze verify X.service` → check ports (`ss -tulpn`), permissions, env files
- Port in use: `ss -tulpn | grep :PORT` → `lsof -i :PORT`
- High CPU from one process: `top -Hp PID` (threads) → `cat /proc/PID/stack` (sudo) → `strace -c -p PID` (short sample) → `perf top -p PID` (if installed)
- Random reboots/freezes: `journalctl -b -1 -e` (previous boot's end) → `last -x reboot shutdown` → `sensors` → `sudo dmesg | grep -iE 'mce|hardware error'`

## Files, backup, transfer, text
- `rsync -avh --progress src/ dest/` — sync; `--dry-run` first; `--delete` MUTATES dest; over SSH `rsync -avz src/ user@host:dest/`
- `tar czf out.tgz dir/`; `tar xzf file.tgz -C dest/`; `tar tzf file.tgz` list
- `zip -r out.zip dir/`; `unzip -l file.zip`
- `scp file user@host:path`; `sftp user@host`
- `dd if=/dev/zero of=/tmp/test bs=1M count=1024 oflag=direct` — write speed test (writes 1 GB, delete after)
- `sha256sum file`; `md5sum -c sums.txt`
- `find . -name '*.log' -mtime +30` — old files; `-delete` MUTATES; `-exec cmd {} +`
- `grep -rn 'pattern' /etc` ; `grep -c`; `rg` (ripgrep) if installed
- `awk '{print $1}'`, `cut -d: -f1`, `sort | uniq -c | sort -rn`, `wc -l`, `column -t`, `jq .` for JSON, `yq` for YAML
- `diff -u a b`; `cmp`; `watch -n2 cmd` (interactive); `tee`; `xargs -P4`
- `trash file` (trash-cli) — recoverable delete; `trash-list`; `trash-restore`; `trash-empty 30`
- `stat -c '%y %n' file`; `touch -d '2026-01-01' file`
- `ln -s target link`; `readlink -f link`
- `lsof +L1` — deleted files still held open (space not freed until the process exits)

## Scheduling and automation
- `crontab -l`; `crontab -e` (MUTATES); format `m h dom mon dow cmd`; `@reboot`, `@daily`
- `systemctl list-timers`; create a timer: unit `X.service` + `X.timer` with `OnCalendar=*-*-* 03:00:00`, then `systemctl enable --now X.timer` (MUTATES)
- `systemd-run --on-active=30m cmd` — one-shot delayed job; `at now + 1 hour` (if atd installed)
- `/etc/cron.d/`, `/etc/cron.daily/` — system cron drop-ins; `run-parts --test /etc/cron.daily`
- `anacron -T` — validate anacrontab

## Kernel and tuning
- `sysctl -a | grep net.ipv4.ip_forward`; `sudo sysctl -w key=value` (MUTATES, runtime); persist in /etc/sysctl.d/*.conf
- `cat /proc/sys/fs/file-nr` — open file handles; `ulimit -n`
- `cat /proc/sys/kernel/hostname`; `cat /proc/version`
- `lsmod`; `dkms status` — out-of-tree modules (NVIDIA, VirtualBox guest additions)
- `ls /boot; dpkg -l 'linux-image*' | grep ^ii` — installed kernels; `sudo apt autoremove --purge` to drop old ones (MUTATES)
- `cat /sys/kernel/mm/transparent_hugepage/enabled`
- `systemctl status systemd-oomd` / `oomctl` — userspace OOM daemon

## SSH and remote access
- `ssh -v user@host` — verbose connect for debugging; `ssh -o ConnectTimeout=5 host true` — reachability test
- `ssh-keygen -t ed25519 -C comment`; `ssh-copy-id user@host` (MUTATES remote authorized_keys)
- `ssh -L 8080:localhost:80 host` — local port forward; `-R` remote; `-D 1080` SOCKS proxy; `-J jump` jump host
- `cat ~/.ssh/config` — host aliases; `ssh -G host` — effective config for a host
- `ssh-keygen -R host` — remove a changed host key; `ssh-keyscan host` — fetch host keys
- `tmux new -s name` / `tmux ls` / `tmux attach -t name` — persistent sessions (interactive)
- `tailscale status` / `tailscale ip -4` — tailnet peers and this node's IP (if installed)
- `sudo systemctl status ssh` — sshd state; config `/etc/ssh/sshd_config` and `/etc/ssh/sshd_config.d/*.conf`; `sudo sshd -t` validates before restart

## Virtualization (host side; this VM itself runs under VirtualBox)
- `VBoxManage list vms` / `VBoxManage list runningvms` / `VBoxManage showvminfo name --machinereadable`
- `VBoxManage controlvm name poweroff|acpipowerbutton|pause|resume` — MUTATES; `VBoxManage startvm name --type headless`
- `VBoxManage guestproperty enumerate name` — guest IPs if Guest Additions are installed
- `virsh list --all`; `virsh dominfo name`; `virsh domifaddr name` — libvirt/KVM
- `cat /proc/cpuinfo | grep -cE 'vmx|svm'` — hardware virtualization support; `kvm-ok`
- `systemd-detect-virt`; `lscpu | grep Hypervisor`
