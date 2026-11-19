#!/usr/bin/env python3
"""linux-sysadmin skill helper.

Read-only system, network and container inspection with plain-text output that
fits a chat message. Mutating actions live only under `manage` and require
--yes. Standard library only; every external tool is optional and the script
says so when one is missing.
"""

import argparse
import concurrent.futures as cf
import datetime as dt
import ipaddress
import json
import os
import pwd
import re
import shutil
import socket
import subprocess
import sys
import time
import xml.etree.ElementTree as ET
from pathlib import Path

VERSION = "1.0"
STATE_DIR = Path(os.environ.get("XDG_CACHE_HOME", Path.home() / ".cache")) / "sysadmin-skill"
NO_SUDO = False
CLK_TCK = os.sysconf("SC_CLK_TCK")
PAGE = os.sysconf("SC_PAGE_SIZE")

# ----------------------------------------------------------------------------
# helpers
# ----------------------------------------------------------------------------


def have(binary):
    return shutil.which(binary) is not None


def run(cmd, timeout=30, sudo=False, stdin=None):
    """Run a command list. Returns (rc, stdout, stderr). Never raises."""
    if sudo and os.geteuid() != 0 and not NO_SUDO and have("sudo"):
        cmd = ["sudo", "-n"] + list(cmd)
    try:
        p = subprocess.run(cmd, capture_output=True, text=True, timeout=timeout,
                           input=stdin, errors="replace")
        return p.returncode, p.stdout, p.stderr
    except FileNotFoundError:
        return 127, "", f"{cmd[0]}: not installed"
    except subprocess.TimeoutExpired:
        return 124, "", f"{cmd[0]}: timed out after {timeout}s"
    except PermissionError as e:
        return 126, "", str(e)


def sudo_ok():
    if os.geteuid() == 0:
        return True
    if NO_SUDO or not have("sudo"):
        return False
    rc, _, _ = run(["sudo", "-n", "true"], timeout=5)
    return rc == 0


def read(path, default=""):
    try:
        with open(path, errors="replace") as f:
            return f.read().strip()
    except OSError:
        return default


def human(n, suffix="B"):
    try:
        n = float(n)
    except (TypeError, ValueError):
        return "?"
    for unit in ("", "K", "M", "G", "T", "P"):
        if abs(n) < 1024 or unit == "P":
            if unit == "":
                return f"{int(n)} {suffix}"
            return f"{n:.1f} {unit}{suffix}"
        n /= 1024
    return f"{n:.1f} P{suffix}"


def duration(seconds):
    seconds = int(seconds)
    d, rem = divmod(seconds, 86400)
    h, rem = divmod(rem, 3600)
    m, s = divmod(rem, 60)
    if d:
        return f"{d}d {h}h {m}m"
    if h:
        return f"{h}h {m}m"
    if m:
        return f"{m}m {s}s"
    return f"{s}s"


def header(title):
    print(f"\n{title}")


def bullet(text):
    print(f"- {text}")


def warn(text):
    print(f"! {text}")


def missing(tool, hint=""):
    msg = f"{tool} is not installed"
    if hint:
        msg += f" ({hint})"
    warn(msg)


def username(uid):
    try:
        return pwd.getpwuid(uid).pw_name
    except KeyError:
        return str(uid)


def uptime_seconds():
    try:
        return float(read("/proc/uptime").split()[0])
    except (ValueError, IndexError):
        return 0.0


def meminfo():
    info = {}
    for line in read("/proc/meminfo").splitlines():
        k, _, v = line.partition(":")
        parts = v.split()
        if parts:
            info[k] = int(parts[0]) * 1024  # kB -> bytes
    return info


def os_release():
    rel = {}
    for line in read("/etc/os-release").splitlines():
        k, _, v = line.partition("=")
        rel[k] = v.strip().strip('"')
    return rel


def ip_json(args):
    rc, o, _ = run(["ip", "-j"] + args)
    if rc != 0 or not o.strip():
        return []
    try:
        return json.loads(o)
    except json.JSONDecodeError:
        return []


def default_route():
    routes = ip_json(["route", "show", "default"])
    if routes:
        return routes[0].get("dev"), routes[0].get("gateway")
    return None, None


def iface_ipv4(dev):
    for entry in ip_json(["-4", "addr", "show", "dev", dev]):
        for a in entry.get("addr_info", []):
            if a.get("family") == "inet":
                return a["local"], a["prefixlen"]
    return None, None


# ----------------------------------------------------------------------------
# sampling (cpu / processes / disk io / net io)
# ----------------------------------------------------------------------------


def _cpu_times():
    res = {}
    for line in read("/proc/stat").splitlines():
        if line.startswith("cpu"):
            parts = line.split()
            res[parts[0]] = [int(x) for x in parts[1:]]
    return res


def cpu_usage(interval=0.6):
    a = _cpu_times()
    time.sleep(interval)
    b = _cpu_times()
    usage = {}
    for k, da in a.items():
        db = b.get(k)
        if not db:
            continue
        tot = sum(db) - sum(da)
        idle = (db[3] + db[4]) - (da[3] + da[4])
        iow = db[4] - da[4]
        steal = (db[7] - da[7]) if len(db) > 7 else 0
        if tot <= 0:
            usage[k] = (0.0, 0.0, 0.0)
        else:
            usage[k] = (100.0 * (tot - idle) / tot, 100.0 * iow / tot, 100.0 * steal / tot)
    return usage


def _proc_snapshot():
    snap = {}
    for d in os.listdir("/proc"):
        if not d.isdigit():
            continue
        try:
            st = read(f"/proc/{d}/stat")
            rp = st.rfind(")")
            comm = st[st.find("(") + 1:rp]
            f = st[rp + 2:].split()
            # f[0]=state f[1]=ppid f[11]=utime f[12]=stime f[19]=starttime f[21]=rss
            snap[int(d)] = {
                "comm": comm, "state": f[0], "ppid": int(f[1]),
                "ticks": int(f[11]) + int(f[12]), "start": int(f[19]), "rss": int(f[21]) * PAGE,
            }
        except (OSError, IndexError, ValueError):
            continue
    return snap


def proc_sample(interval=1.0):
    a = _proc_snapshot()
    ta = sum(_cpu_times().get("cpu", [0]))
    time.sleep(interval)
    b = _proc_snapshot()
    tb = sum(_cpu_times().get("cpu", [0]))
    total = max(tb - ta, 1)
    ncpu = os.cpu_count() or 1
    up = uptime_seconds()
    rows = []
    for pid, info in b.items():
        t0 = a.get(pid, {}).get("ticks", info["ticks"])
        info = dict(info)
        info["pid"] = pid
        info["cpu"] = 100.0 * (info["ticks"] - t0) / total * ncpu
        info["etime"] = max(up - info["start"] / CLK_TCK, 0)
        try:
            info["user"] = username(os.stat(f"/proc/{pid}").st_uid)
        except OSError:
            info["user"] = "?"
        rows.append(info)
    return rows


def _proc_snapshot_one(pid):
    """Return the parent pid of `pid` (0 if unknown)."""
    st = read(f"/proc/{pid}/stat")
    rp = st.rfind(")")
    return int(st[rp + 2:].split()[1]) if rp > 0 else 0


def proc_cmdline(pid, limit=90):
    raw = read(f"/proc/{pid}/cmdline").replace("\0", " ").strip()
    if not raw:
        raw = f"[{read(f'/proc/{pid}/comm')}]"
    return raw if len(raw) <= limit else raw[:limit - 1] + "…"


DISK_RE = re.compile(r"^(sd[a-z]+|nvme\d+n\d+|vd[a-z]+|xvd[a-z]+|mmcblk\d+|hd[a-z]+)$")


def _diskstats():
    stats = {}
    for line in read("/proc/diskstats").splitlines():
        f = line.split()
        if len(f) < 14 or not DISK_RE.match(f[2]):
            continue
        stats[f[2]] = (int(f[5]) * 512, int(f[9]) * 512, int(f[3]), int(f[7]), int(f[12]))
    return stats


def disk_io_sample(interval=1.0):
    a = _diskstats()
    time.sleep(interval)
    b = _diskstats()
    rows = []
    for dev, (r1, w1, rio1, wio1, busy1) in b.items():
        r0, w0, rio0, wio0, busy0 = a.get(dev, (r1, w1, rio1, wio1, busy1))
        rows.append((dev, (r1 - r0) / interval, (w1 - w0) / interval,
                     (rio1 - rio0 + wio1 - wio0) / interval, min(100.0, (busy1 - busy0) / (10 * interval))))
    return rows


def _netdev():
    stats = {}
    for line in read("/proc/net/dev").splitlines()[2:]:
        name, _, rest = line.partition(":")
        f = rest.split()
        if len(f) >= 16:
            stats[name.strip()] = (int(f[0]), int(f[8]), int(f[2]), int(f[10]))  # rx, tx, rx_err, tx_err
    return stats


def net_io_sample(interval=1.0):
    a = _netdev()
    time.sleep(interval)
    b = _netdev()
    return {k: ((v[0] - a.get(k, v)[0]) / interval, (v[1] - a.get(k, v)[1]) / interval) for k, v in b.items()}


# ----------------------------------------------------------------------------
# temperatures (works without lm-sensors)
# ----------------------------------------------------------------------------


def temperatures():
    temps = []
    for hw in sorted(Path("/sys/class/hwmon").glob("hwmon*")):
        name = read(hw / "name")
        for t in sorted(hw.glob("temp*_input")):
            try:
                val = int(read(t)) / 1000
            except ValueError:
                continue
            label = read(t.with_name(t.name.replace("_input", "_label"))) or t.name.replace("_input", "")
            temps.append((f"{name}/{label}", val))
    if not temps:
        for z in sorted(Path("/sys/class/thermal").glob("thermal_zone*")):
            try:
                temps.append((read(z / "type") or z.name, int(read(z / "temp")) / 1000))
            except ValueError:
                continue
    return temps


# ----------------------------------------------------------------------------
# commands
# ----------------------------------------------------------------------------


def cmd_overview(args):
    rel = os_release()
    host = socket.gethostname()
    rc, virt, _ = run(["systemd-detect-virt"], timeout=5)
    virt = virt.strip() if rc == 0 and virt.strip() != "none" else ""
    header("System")
    bullet(f"host: {host}" + (f" (VM: {virt})" if virt else ""))
    bullet(f"OS: {rel.get('PRETTY_NAME', 'unknown')}, kernel {os.uname().release}, {os.uname().machine}")
    load = read("/proc/loadavg").split()[:3]
    bullet(f"uptime: {duration(uptime_seconds())}, load: {' '.join(load)} on {os.cpu_count()} CPUs")

    header("CPU")
    model = ""
    for line in read("/proc/cpuinfo").splitlines():
        if line.lower().startswith("model name"):
            model = line.split(":", 1)[1].strip()
            break
    u = cpu_usage().get("cpu", (0, 0, 0))
    extra = []
    if u[1] >= 1:
        extra.append(f"iowait {u[1]:.0f}%")
    if u[2] >= 1:
        extra.append(f"steal {u[2]:.0f}%")
    bullet(f"{model or 'unknown model'}: {u[0]:.0f}% busy" + (f" ({', '.join(extra)})" if extra else ""))
    temps = temperatures()
    if temps:
        hottest = max(temps, key=lambda t: t[1])
        bullet(f"hottest sensor: {hottest[0]} {hottest[1]:.0f}°C")

    header("Memory")
    m = meminfo()
    used = m["MemTotal"] - m["MemAvailable"]
    bullet(f"RAM: {human(used)} used of {human(m['MemTotal'])} ({100 * used / m['MemTotal']:.0f}%), {human(m['MemAvailable'])} available")
    if m.get("SwapTotal"):
        su = m["SwapTotal"] - m["SwapFree"]
        bullet(f"swap: {human(su)} used of {human(m['SwapTotal'])}")
    else:
        bullet("swap: none")

    header("Disks")
    for fs, typ, size, used_b, avail, pct, mnt in df_rows():
        flag = "  !! nearly full" if pct >= 90 else ""
        bullet(f"{mnt}: {human(used_b)} used of {human(size)} ({pct}%), {human(avail)} free{flag}")

    header("Network")
    dev, gw = default_route()
    for entry in ip_json(["addr"]):
        name = entry.get("ifname")
        if name == "lo":
            continue
        v4 = [a["local"] for a in entry.get("addr_info", []) if a.get("family") == "inet"]
        if not v4 and entry.get("operstate") != "UP":
            continue
        bullet(f"{name}: {', '.join(v4) or 'no IPv4'}, {entry.get('operstate', '?').lower()}, mac {entry.get('address', '?')}")
    if gw:
        bullet(f"gateway: {gw} via {dev}")
    dns = dns_servers()
    if dns:
        bullet(f"DNS: {', '.join(dns)}")

    header("Services and health")
    rc, o, _ = run(["systemctl", "list-units", "--type=service", "--state=failed", "--no-legend", "--no-pager", "--plain"])
    failed = [l.split()[0] for l in o.splitlines() if l.strip()]
    bullet("failed services: " + (", ".join(failed) if failed else "none"))
    if have("docker"):
        rc, o, e = docker(["ps", "-q"])
        if rc == 0:
            n = len([l for l in o.splitlines() if l.strip()])
            rc2, o2, _ = docker(["ps", "-aq"])
            total = len([l for l in o2.splitlines() if l.strip()]) if rc2 == 0 else n
            bullet(f"docker: {n} running, {total - n} stopped")
        else:
            bullet("docker: installed but not reachable (" + (e.strip().splitlines() or ["?"])[0][:80] + ")")
    else:
        bullet("docker: not installed")
    if os.path.exists("/var/run/reboot-required"):
        bullet("reboot required (kernel or core update pending)")
    upg = apt_upgradable()
    if upg is not None:
        bullet(f"package updates available: {len(upg)}")
    rc, o, _ = run(["who"], timeout=5)
    users = sorted({l.split()[0] for l in o.splitlines() if l.strip()})
    bullet("logged in: " + (", ".join(users) if users else "nobody"))


def cmd_cpu(args):
    header("CPU usage (sampled over 1 s)")
    usage = cpu_usage(1.0)
    total = usage.get("cpu", (0, 0, 0))
    bullet(f"total: {total[0]:.0f}% busy, iowait {total[1]:.0f}%, steal {total[2]:.0f}%")
    cores = [(k, v) for k, v in usage.items() if k != "cpu"]
    cores.sort(key=lambda kv: int(kv[0][3:]))
    bullet("per core: " + ", ".join(f"{k[3:]}={v[0]:.0f}%" for k, v in cores))
    load = read("/proc/loadavg").split()
    bullet(f"load 1/5/15 min: {load[0]} {load[1]} {load[2]} (CPUs: {os.cpu_count()}), running/total procs: {load[3]}")
    freqs = []
    for p in Path("/sys/devices/system/cpu").glob("cpu[0-9]*/cpufreq/scaling_cur_freq"):
        try:
            freqs.append(int(read(p)) / 1000)
        except ValueError:
            pass
    if freqs:
        bullet(f"frequency: {min(freqs):.0f}-{max(freqs):.0f} MHz")
    gov = read("/sys/devices/system/cpu/cpu0/cpufreq/scaling_governor")
    if gov:
        bullet(f"governor: {gov}")
    temps = temperatures()
    if temps:
        bullet("temps: " + ", ".join(f"{n} {v:.0f}°C" for n, v in temps[:6]))
    header(f"Top {args.top} processes by CPU")
    rows = sorted(proc_sample(1.0), key=lambda r: -r["cpu"])[:args.top]
    for r in rows:
        bullet(f"{r['cpu']:.1f}% pid {r['pid']} {r['user']} mem {human(r['rss'])} up {duration(r['etime'])}: {proc_cmdline(r['pid'])}")


def cmd_memory(args):
    m = meminfo()
    used = m["MemTotal"] - m["MemAvailable"]
    header("Memory")
    bullet(f"total {human(m['MemTotal'])}, used {human(used)} ({100 * used / m['MemTotal']:.0f}%), available {human(m['MemAvailable'])}")
    bullet(f"cache {human(m.get('Cached', 0) + m.get('SReclaimable', 0))}, buffers {human(m.get('Buffers', 0))}, shared/tmpfs {human(m.get('Shmem', 0))}, dirty {human(m.get('Dirty', 0))}")
    if m.get("SwapTotal"):
        su = m["SwapTotal"] - m["SwapFree"]
        bullet(f"swap: {human(su)} used of {human(m['SwapTotal'])} ({100 * su / m['SwapTotal']:.0f}%), swappiness {read('/proc/sys/vm/swappiness')}")
        rc, o, _ = run(["swapon", "--show=NAME,TYPE,SIZE,USED,PRIO", "--noheadings"])
        for l in o.splitlines():
            bullet("swap device: " + " ".join(l.split()))
    else:
        bullet("swap: none configured")
    if m.get("HugePages_Total"):
        bullet(f"hugepages: {m['HugePages_Total'] // 1024}")
    header(f"Top {args.top} processes by memory")
    rows = sorted(proc_sample(0.3), key=lambda r: -r["rss"])[:args.top]
    for r in rows:
        bullet(f"{human(r['rss'])} ({100 * r['rss'] / m['MemTotal']:.1f}%) pid {r['pid']} {r['user']}: {proc_cmdline(r['pid'])}")
    rc, o, _ = run(["journalctl", "-k", "--since", "-7d", "--no-pager", "-o", "cat", "-q"], timeout=20)
    oom = [l for l in o.splitlines() if "Out of memory" in l or "oom-kill" in l.lower()]
    header("OOM killer (last 7 days)")
    if oom:
        bullet(f"{len(oom)} event lines, latest: {oom[-1][:140]}")
    else:
        bullet("no out-of-memory kills")


def df_rows():
    rc, o, _ = run(["df", "-PTB1", "-x", "tmpfs", "-x", "devtmpfs", "-x", "squashfs", "-x", "efivarfs", "-x", "overlay"], timeout=20)
    rows = []
    for line in o.splitlines()[1:]:
        f = line.split()
        if len(f) < 7:
            continue
        try:
            rows.append((f[0], f[1], int(f[2]), int(f[3]), int(f[4]), int(f[5].rstrip("%")), " ".join(f[6:])))
        except ValueError:
            continue
    return rows


def cmd_disk(args):
    header("Filesystems")
    for fs, typ, size, used, avail, pct, mnt in df_rows():
        flag = "  !! nearly full" if pct >= 90 else ""
        bullet(f"{mnt} ({fs}, {typ}): {human(used)} used of {human(size)} ({pct}%), {human(avail)} free{flag}")
    rc, o, _ = run(["df", "-Pi", "-x", "tmpfs", "-x", "devtmpfs", "-x", "squashfs", "-x", "overlay"], timeout=20)
    high = []
    for line in o.splitlines()[1:]:
        f = line.split()
        try:
            if int(f[4].rstrip("%")) >= 80:
                high.append(f"{f[5]} {f[4]}")
        except (IndexError, ValueError):
            pass
    if high:
        bullet("inode usage high on: " + ", ".join(high))
    header("Block devices")
    rc, o, _ = run(["lsblk", "-o", "NAME,SIZE,TYPE,FSTYPE,MOUNTPOINTS,MODEL", "--noheadings"])
    for line in o.splitlines():
        if line.strip():
            bullet(" ".join(line.split()))
    if args.io:
        header("Disk I/O (sampled over 1 s)")
        rows = disk_io_sample(1.0)
        if not rows:
            bullet("no physical disks found in /proc/diskstats")
        for dev, r, w, iops, busy in rows:
            bullet(f"{dev}: read {human(r)}/s, write {human(w)}/s, {iops:.0f} IOPS, {busy:.0f}% busy")
    if args.path:
        p = args.path
        header(f"Largest items directly under {p} (du, same filesystem)")
        rc, o, e = run(["du", "-x", "-B1", "--max-depth=1", p], timeout=180, sudo=args.sudo)
        items = []
        for line in o.splitlines():
            try:
                size, name = line.split("\t", 1)
                items.append((int(size), name))
            except ValueError:
                continue
        items.sort(reverse=True)
        for size, name in items[:args.top]:
            bullet(f"{human(size)}  {name}")
        if rc == 124:
            warn("du timed out; try a deeper path")
        if args.big:
            header(f"Largest files under {p}")
            rc, o, _ = run(["find", p, "-xdev", "-type", "f", "-printf", "%s %p\n"], timeout=180, sudo=args.sudo)
            files = []
            for line in o.splitlines():
                try:
                    size, name = line.split(" ", 1)
                    files.append((int(size), name))
                except ValueError:
                    continue
            files.sort(reverse=True)
            for size, name in files[:args.top]:
                bullet(f"{human(size)}  {name}")
    if args.smart:
        header("SMART health")
        if not have("smartctl"):
            missing("smartctl", "apt install smartmontools")
        else:
            rc, o, _ = run(["lsblk", "-dn", "-o", "NAME,TYPE"])
            disks = [l.split()[0] for l in o.splitlines() if l.split()[-1:] == ["disk"]]
            for d in disks:
                rc, o, e = run(["smartctl", "-H", "-A", "-i", f"/dev/{d}"], timeout=30, sudo=True)
                if rc == 127 or "Permission denied" in e:
                    bullet(f"{d}: needs sudo")
                    continue
                healthy = re.search(r"(SMART overall-health self-assessment test result|SMART Health Status):\s*(\S+)", o)
                model = re.search(r"(Device Model|Model Number):\s*(.+)", o)
                temp = re.search(r"Temperature_Celsius.*?(\d+)\s*(?:\(|$)", o, re.M) or re.search(r"Temperature:\s*(\d+)", o)
                realloc = re.search(r"Reallocated_Sector_Ct.*?(\d+)\s*$", o, re.M)
                pending = re.search(r"Current_Pending_Sector.*?(\d+)\s*$", o, re.M)
                hours = re.search(r"Power_On_Hours.*?(\d+)\s*$", o, re.M) or re.search(r"Power On Hours:\s*([\d,]+)", o)
                pct_used = re.search(r"Percentage Used:\s*(\d+)%", o)
                bits = [healthy.group(2) if healthy else "unknown health"]
                if model:
                    bits.append(model.group(2).strip())
                if temp:
                    bits.append(f"{temp.group(1)}°C")
                if hours:
                    bits.append(f"{hours.group(1)} h on")
                if realloc:
                    bits.append(f"reallocated {realloc.group(1)}")
                if pending:
                    bits.append(f"pending {pending.group(1)}")
                if pct_used:
                    bits.append(f"NVMe wear {pct_used.group(1)}%")
                if re.search(r"Unable to detect device type|Operation not supported|SMART support is:\s+Unavailable|lacks SMART capability", o + e):
                    bits = ["SMART not available (virtual or unsupported disk)"] + ([model.group(2).strip()] if model else [])
                bullet(f"{d}: " + ", ".join(bits))


def cmd_processes(args):
    rows = proc_sample(1.0)
    if args.find:
        args.find = " ".join(args.find)
        try:
            pat = re.compile(args.find, re.I)
        except re.error:
            pat = re.compile(re.escape(args.find), re.I)
        rows = [r for r in rows if pat.search(r["comm"]) or pat.search(proc_cmdline(r["pid"], 400))]
    if args.user:
        rows = [r for r in rows if r["user"] == args.user]
    key = (lambda r: -r["rss"]) if args.sort == "mem" else (lambda r: -r["cpu"])
    rows.sort(key=key)
    states = {}
    for r in rows:
        states[r["state"]] = states.get(r["state"], 0) + 1
    header(f"Processes ({len(rows)} matched, zombies: {states.get('Z', 0)})")
    for r in rows[:args.top]:
        bullet(f"pid {r['pid']} {r['user']} cpu {r['cpu']:.1f}% mem {human(r['rss'])} up {duration(r['etime'])} [{r['state']}]: {proc_cmdline(r['pid'])}")
    if args.tree:
        header("Process tree (top of tree)")
        if have("pstree"):
            rc, o, _ = run(["pstree", "-p", "-a", "-l"] + ([str(rows[0]["pid"])] if args.find and rows else []))
        else:
            rc, o, _ = run(["ps", "-eo", "pid,ppid,user,comm", "--forest"])
        for line in o.splitlines()[:60]:
            print("  " + line[:150])


def systemctl(args, user=False, timeout=20):
    return run(["systemctl"] + (["--user"] if user else []) + args + ["--no-pager"], timeout=timeout)


def cmd_services(args):
    scope = args.user
    if args.status:
        header(f"Status of {args.status}")
        rc, o, _ = systemctl(["show", args.status, "-p", "ActiveState,SubState,UnitFileState,MainPID,NRestarts,MemoryCurrent,ActiveEnterTimestamp,ExecMainStartTimestamp,Result"], scope)
        props = dict(l.split("=", 1) for l in o.splitlines() if "=" in l)
        bullet(f"state: {props.get('ActiveState')}/{props.get('SubState')}, enabled: {props.get('UnitFileState')}, result: {props.get('Result')}")
        bullet(f"main pid: {props.get('MainPID')}, restarts: {props.get('NRestarts')}, memory: {human(props.get('MemoryCurrent')) if str(props.get('MemoryCurrent', '')).isdigit() else 'n/a'}")
        bullet(f"active since: {props.get('ActiveEnterTimestamp') or 'n/a'}")
        rc, o, _ = run(["journalctl"] + (["--user"] if scope else []) + ["-u", args.status, "-n", str(args.lines), "--no-pager", "-o", "short"], timeout=20)
        header(f"Last {args.lines} log lines")
        for line in o.splitlines():
            print("  " + line[:200])
        return
    if args.logs:
        rc, o, e = run(["journalctl"] + (["--user"] if scope else []) + ["-u", args.logs, "-n", str(args.lines), "--no-pager", "-o", "short"], timeout=20)
        header(f"Last {args.lines} log lines of {args.logs}")
        for line in (o or e).splitlines():
            print("  " + line[:200])
        return
    rc, o, _ = systemctl(["list-units", "--type=service", "--state=failed", "--no-legend", "--plain"], scope)
    failed = [l.split()[0] for l in o.splitlines() if l.strip()]
    header("Failed services")
    bullet(", ".join(failed) if failed else "none")
    if args.failed:
        for f in failed:
            rc, o, _ = run(["journalctl"] + (["--user"] if scope else []) + ["-u", f, "-n", "5", "--no-pager", "-o", "cat"], timeout=15)
            header(f"{f} recent log")
            for line in o.splitlines():
                print("  " + line[:200])
        return
    rc, o, _ = systemctl(["list-units", "--type=service", "--state=running", "--no-legend", "--plain"], scope)
    running = [l.split()[0].replace(".service", "") for l in o.splitlines() if l.strip()]
    header(f"Running services ({len(running)})")
    print("  " + ", ".join(running))
    if args.all:
        rc, o, _ = systemctl(["list-unit-files", "--type=service", "--state=enabled", "--no-legend", "--plain"], scope)
        enabled = [l.split()[0].replace(".service", "") for l in o.splitlines() if l.strip()]
        header(f"Enabled at boot ({len(enabled)})")
        print("  " + ", ".join(enabled))
    if args.timers:
        rc, o, _ = systemctl(["list-timers", "--all", "--no-legend", "--plain"], scope)
        header("Timers")
        for line in o.splitlines()[:30]:
            print("  " + " ".join(line.split())[:160])


def cmd_logs(args):
    base = ["journalctl", "--no-pager", "-o", "short", "-q"]
    if args.user:
        base.append("--user")
    if args.dmesg:
        rc, o, e = run(["dmesg", "-T", "--level=emerg,alert,crit,err,warn"], timeout=15)
        if rc != 0:
            rc, o, e = run(["dmesg", "-T", "--level=emerg,alert,crit,err,warn"], timeout=15, sudo=True)
        if rc != 0:
            rc, o, e = run(base + ["-k", "-p", "warning", "-n", str(args.lines)], timeout=15)
        header(f"Kernel messages, warnings and above (last {args.lines})")
        for line in o.splitlines()[-args.lines:]:
            print("  " + line[:200])
        return
    if args.boots:
        rc, o, _ = run(["journalctl", "--list-boots", "--no-pager", "-q"], timeout=15)
        header("Recent boots")
        for line in o.splitlines()[-args.lines:]:
            print("  " + " ".join(line.split())[:160])
        return
    cmd = list(base)
    title = []
    if args.unit:
        cmd += ["-u", args.unit]
        title.append(f"unit {args.unit}")
    if args.errors or not (args.unit or args.grep):
        cmd += ["-p", "err"]
        title.append("priority error and above")
    if args.grep:
        cmd += ["--grep", args.grep]
        title.append(f"matching '{args.grep}'")
    cmd += ["--since", args.since, "-n", str(args.lines)]
    header(f"Journal: {', '.join(title) or 'all'} since {args.since} (last {args.lines})")
    rc, o, e = run(cmd, timeout=30)
    lines = o.splitlines()
    if not lines:
        bullet("nothing matched" if rc in (0, 1) else f"journalctl failed: {e.strip()[:160]}")
    for line in lines:
        print("  " + line[:200])


def dns_servers():
    servers = []
    rc, o, _ = run(["resolvectl", "status"], timeout=5)
    if rc == 0:
        for line in o.splitlines():
            if "DNS Servers:" in line or "Current DNS Server:" in line:
                servers += line.split(":", 1)[1].split()
    if not servers:
        for line in read("/etc/resolv.conf").splitlines():
            if line.startswith("nameserver"):
                servers.append(line.split()[1])
    seen = []
    for s in servers:
        if s not in seen:
            seen.append(s)
    return seen


def cmd_network(args):
    dev, gw = default_route()
    header("Interfaces")
    for entry in ip_json(["addr"]):
        name = entry.get("ifname")
        if name == "lo":
            continue
        v4 = [f"{a['local']}/{a['prefixlen']}" for a in entry.get("addr_info", []) if a.get("family") == "inet"]
        v6 = [a["local"] for a in entry.get("addr_info", []) if a.get("family") == "inet6" and a.get("scope") == "global"]
        speed = read(f"/sys/class/net/{name}/speed")
        kind = "wifi" if os.path.exists(f"/sys/class/net/{name}/wireless") else ("virtual" if os.path.islink(f"/sys/class/net/{name}") and "/virtual/" in os.path.realpath(f"/sys/class/net/{name}") else "ethernet")
        bits = [entry.get("operstate", "?").lower(), kind, f"mac {entry.get('address', '?')}"]
        if v4:
            bits.append("ipv4 " + ", ".join(v4))
        if v6:
            bits.append("ipv6 " + ", ".join(v6))
        if speed and speed.lstrip("-").isdigit() and int(speed) > 0:
            bits.append(f"{speed} Mb/s")
        bits.append(f"mtu {entry.get('mtu')}")
        bullet(f"{name}: " + ", ".join(bits))
    bullet(f"default gateway: {gw or 'none'}" + (f" via {dev}" if dev else ""))
    bullet("DNS servers: " + (", ".join(dns_servers()) or "unknown"))
    bullet(f"hostname: {socket.gethostname()}" + (f", mDNS: {socket.gethostname()}.local" if have("avahi-daemon") or os.path.exists("/run/avahi-daemon") else ""))
    if args.stats:
        header("Throughput (sampled over 1 s)")
        for name, (rx, tx) in net_io_sample(1.0).items():
            if name != "lo":
                bullet(f"{name}: down {human(rx)}/s, up {human(tx)}/s")
    if args.routes:
        header("Routes")
        rc, o, _ = run(["ip", "route"])
        for line in o.splitlines():
            bullet(line)
    if args.ports or not (args.connections or args.routes or args.public_ip or args.stats or args.arp or args.firewall):
        header("Listening ports")
        # root sees every socket owner; without it other users' processes show as "?"
        rc, o, e = run(["ss", "-tulpnH"], timeout=10, sudo=sudo_ok())
        if rc != 0:
            rc, o, e = run(["ss", "-tulpnH"], timeout=10)
        seen = set()
        for line in o.splitlines():
            f = line.split()
            if len(f) < 5:
                continue
            proto, local = f[0], f[4]
            m = re.search(r'users:\(\("([^"]+)",pid=(\d+)', line)
            proc = f"{m.group(1)} (pid {m.group(2)})" if m else "?"
            key = (proto, local, proc)
            if key in seen:
                continue
            seen.add(key)
            scope = "all interfaces" if local.startswith(("0.0.0.0", "*", "[::]")) else ("localhost only" if local.startswith(("127.", "[::1]")) else "")
            bullet(f"{proto} {local} {proc}" + (f" [{scope}]" if scope else ""))
    if args.connections:
        header("Established connections")
        rc, o, e = run(["ss", "-tnpH", "state", "established"], timeout=10)
        counts = {}
        for line in o.splitlines():
            f = line.split()
            if len(f) < 4:
                continue
            peer = f[3]
            m = re.search(r'users:\(\("([^"]+)"', line)
            proc = m.group(1) if m else "?"
            host = peer.rsplit(":", 1)[0]
            counts[(host, proc)] = counts.get((host, proc), 0) + 1
        for (host, proc), n in sorted(counts.items(), key=lambda kv: -kv[1])[:args.top]:
            bullet(f"{host} via {proc}: {n} connection{'s' if n > 1 else ''}")
        if not counts:
            bullet("none")
    if args.arp:
        header("Neighbours (ARP cache)")
        for n in ip_json(["neigh"]):
            if n.get("lladdr"):
                bullet(f"{n['dst']} {n['lladdr']} {n.get('dev')} {','.join(n.get('state', []))}")
    if args.firewall:
        header("Firewall")
        if have("ufw"):
            rc, o, e = run(["ufw", "status"], timeout=10, sudo=True)
            bullet("ufw: " + (" ".join(o.split()[:2]) if rc == 0 else "needs sudo"))
            for line in o.splitlines()[4:14]:
                if line.strip():
                    bullet("  " + " ".join(line.split()))
        if have("nft"):
            rc, o, e = run(["nft", "list", "ruleset"], timeout=10, sudo=True)
            if rc == 0:
                bullet(f"nftables: {o.count(chr(10))} lines of rules, {o.count('chain ')} chains")
        if have("iptables"):
            rc, o, e = run(["iptables", "-S"], timeout=10, sudo=True)
            if rc == 0:
                bullet(f"iptables: {max(len(o.splitlines()) - 3, 0)} rules beyond defaults, INPUT policy {'ACCEPT' if '-P INPUT ACCEPT' in o else 'DROP/other'}")
    if args.public_ip:
        header("Public IP")
        ipv4 = None
        if have("curl"):
            rc, o, _ = run(["curl", "-4", "-s", "--max-time", "6", "https://api.ipify.org"], timeout=10)
            ipv4 = o.strip() if rc == 0 and re.match(r"^\d+\.\d+\.\d+\.\d+$", o.strip()) else None
        if not ipv4 and have("dig"):
            rc, o, _ = run(["dig", "+short", "-4", "myip.opendns.com", "@resolver1.opendns.com", "+time=3", "+tries=1"], timeout=10)
            ipv4 = o.strip() or None
        bullet(ipv4 or "could not determine (no internet or tools)")


# ---- LAN scan ---------------------------------------------------------------

OUI_FILES = ["/usr/share/nmap/nmap-mac-prefixes", "/usr/share/arp-scan/ieee-oui.txt",
             "/usr/share/ieee-data/oui.txt", "/var/lib/ieee-data/oui.txt", "/usr/share/misc/oui.txt"]
OUI_BUILTIN = {"080027": "VirtualBox", "525400": "QEMU/KVM", "DCA632": "Raspberry Pi", "D83ADD": "Raspberry Pi",
               "B827EB": "Raspberry Pi", "E45F01": "Raspberry Pi", "2CCF67": "Raspberry Pi", "001F54": "Lorex/Dahua camera",
               "0050F2": "Microsoft", "F0B429": "Xiaomi", "28CDC1": "Raspberry Pi"}
_OUI = None


def oui_vendor(mac):
    global _OUI
    if not mac:
        return ""
    key = mac.replace(":", "").replace("-", "").upper()[:6]
    if _OUI is None:
        _OUI = {}
        for path in OUI_FILES:
            if not os.path.exists(path):
                continue
            try:
                with open(path, errors="replace") as f:
                    for line in f:
                        m = re.match(r"^([0-9A-Fa-f]{6})\s+(.+)$", line.strip())
                        if m:
                            _OUI.setdefault(m.group(1).upper(), m.group(2).strip())
                            continue
                        m = re.match(r"^([0-9A-Fa-f]{2})-([0-9A-Fa-f]{2})-([0-9A-Fa-f]{2})\s+\(hex\)\s+(.+)$", line.strip())
                        if m:
                            _OUI.setdefault((m.group(1) + m.group(2) + m.group(3)).upper(), m.group(4).strip())
            except OSError:
                pass
            if _OUI:
                break
    if (mac[1].upper() in "26AE") and key not in _OUI:
        return "private/randomised MAC"
    return _OUI.get(key) or OUI_BUILTIN.get(key, "")


def nmap_ping_scan(cidr, use_sudo):
    rc, o, e = run(["nmap", "-sn", "-T4", "--max-retries", "2", "-oX", "-", cidr], timeout=240, sudo=use_sudo)
    hosts = {}
    if rc != 0:
        return hosts, e.strip()[:200]
    try:
        root = ET.fromstring(o)
    except ET.ParseError:
        return hosts, "could not parse nmap output"
    for h in root.iter("host"):
        st = h.find("status")
        if st is None or st.get("state") != "up":
            continue
        ip = mac = vendor = name = ""
        for a in h.findall("address"):
            if a.get("addrtype") == "ipv4":
                ip = a.get("addr")
            elif a.get("addrtype") == "mac":
                mac = a.get("addr", "").lower()
                vendor = a.get("vendor", "")
        hn = h.find("hostnames/hostname")
        if hn is not None:
            name = hn.get("name", "")
        if ip:
            hosts[ip] = {"ip": ip, "mac": mac, "vendor": vendor, "name": name}
    return hosts, ""


def ping_sweep(cidr):
    net = ipaddress.ip_network(cidr, strict=False)
    targets = [str(h) for h in net.hosts()]
    if len(targets) > 1024:
        targets = targets[:1024]
    alive = set()

    def ping(ip):
        rc, _, _ = run(["ping", "-c", "1", "-W", "1", ip], timeout=4)
        return ip if rc == 0 else None

    with cf.ThreadPoolExecutor(max_workers=128) as ex:
        for r in ex.map(ping, targets):
            if r:
                alive.add(r)
    hosts = {}
    neigh = {n["dst"]: n.get("lladdr", "") for n in ip_json(["neigh"]) if n.get("lladdr") and "FAILED" not in n.get("state", [])}
    for ip in alive | {ip for ip in neigh if ipaddress.ip_address(ip) in net}:
        hosts[ip] = {"ip": ip, "mac": neigh.get(ip, "").lower(), "vendor": "", "name": ""}
    return hosts


def mdns_names(timeout=8):
    names = {}
    if not have("avahi-browse"):
        return names
    rc, o, _ = run(["avahi-browse", "-atrp", "--no-db-lookup"], timeout=timeout)
    for line in o.splitlines():
        f = line.split(";")
        if len(f) >= 8 and f[0] == "=" and f[2] == "IPv4":
            names.setdefault(f[7], f[6].replace("\\032", " "))
    return names


def rdns(ip):
    rc, o, _ = run(["getent", "hosts", ip], timeout=3)
    if rc == 0 and o.split():
        return o.split()[1]
    if have("avahi-resolve"):
        rc, o, _ = run(["avahi-resolve", "-a", ip], timeout=3)
        if rc == 0 and len(o.split()) >= 2:
            return o.split()[1].rstrip(".")
    return ""


def cmd_scan(args):
    if args.ports:
        return port_scan(args)
    dev, gw = default_route()
    cidr = args.subnet
    my_ip = None
    if dev:
        my_ip, plen = iface_ipv4(dev)
        if not cidr and my_ip:
            cidr = f"{my_ip}/{plen}"
    if not cidr:
        warn("could not determine the LAN subnet; pass --subnet 192.168.1.0/24")
        return 1
    net = ipaddress.ip_network(cidr, strict=False)
    if net.num_addresses > 4096:
        warn(f"{net} is large ({net.num_addresses} addresses); scanning only the /24 around this host")
        net = ipaddress.ip_network(f"{my_ip or net.network_address}/24", strict=False)
    cidr = str(net)
    use_sudo = sudo_ok()
    method = ""
    t0 = time.time()
    hosts = {}
    err = ""
    if have("nmap"):
        hosts, err = nmap_ping_scan(cidr, use_sudo)
        method = "nmap ARP+ICMP sweep" + ("" if use_sudo else " (no sudo, so no MAC addresses)")
    if not hosts:
        if err:
            warn(f"nmap failed: {err}")
        hosts = ping_sweep(cidr)
        method = "ping sweep + ARP cache"
    if my_ip:
        own_mac = read(f"/sys/class/net/{dev}/address") if dev else ""
        hosts.setdefault(my_ip, {"ip": my_ip, "mac": "", "vendor": "", "name": socket.gethostname()})
        if not hosts[my_ip]["mac"]:
            hosts[my_ip]["mac"] = own_mac
    for h in hosts.values():
        if not h["vendor"] and h["mac"]:
            h["vendor"] = oui_vendor(h["mac"])
    if not args.fast:
        names = mdns_names()
        with cf.ThreadPoolExecutor(max_workers=16) as ex:
            futs = {ex.submit(rdns, ip): ip for ip, h in hosts.items() if not h["name"]}
            for fut, ip in futs.items():
                try:
                    hosts[ip]["name"] = fut.result() or names.get(ip, "")
                except Exception:
                    hosts[ip]["name"] = names.get(ip, "")
    for ip, h in hosts.items():
        tags = []
        if ip == my_ip:
            tags.append("this machine")
        if ip == gw:
            tags.append("gateway")
        h["tags"] = tags
    STATE_DIR.mkdir(parents=True, exist_ok=True)
    prev_path = STATE_DIR / "last-scan.json"
    prev = {}
    if prev_path.exists():
        try:
            prev = json.loads(prev_path.read_text())
        except json.JSONDecodeError:
            prev = {}
    prev_hosts = prev.get("hosts", {}) if prev.get("subnet") == cidr else {}
    ordered = sorted(hosts.values(), key=lambda h: ipaddress.ip_address(h["ip"]))
    if args.json:
        print(json.dumps({"subnet": cidr, "method": method, "hosts": ordered}, indent=1))
    else:
        header(f"LAN scan of {cidr} via {dev or '?'}: {len(hosts)} devices up ({method}, {time.time() - t0:.0f} s)")
        for h in ordered:
            bits = [h["ip"]]
            bits.append(h["mac"] or "mac unknown")
            if h["vendor"]:
                bits.append(f"({h['vendor']})")
            if h["name"]:
                bits.append(h["name"])
            if h["tags"]:
                bits.append("[" + ", ".join(h["tags"]) + "]")
            bullet(" ".join(bits))
        if prev_hosts:
            new = [ip for ip in hosts if ip not in prev_hosts]
            gone = [ip for ip in prev_hosts if ip not in hosts]
            header(f"Changes since last scan ({prev.get('when', '?')})")
            bullet("new: " + (", ".join(new) if new else "none"))
            bullet("missing: " + (", ".join(gone) if gone else "none"))
    try:
        prev_path.write_text(json.dumps({"when": dt.datetime.now().isoformat(timespec="minutes"), "subnet": cidr,
                                         "hosts": {h["ip"]: h for h in ordered}}))
    except OSError:
        pass
    return 0


COMMON_PORTS = [21, 22, 23, 25, 53, 80, 110, 111, 135, 139, 143, 443, 445, 465, 515, 548, 587, 631, 993, 995, 1433,
                1883, 2049, 3000, 3306, 3389, 5000, 5432, 5900, 6379, 8000, 8080, 8096, 8123, 8443, 8888, 9000, 9090,
                9100, 9999, 11434, 18789, 27017, 32400, 51413, 62078]


def service_name(port):
    try:
        return socket.getservbyport(port)
    except OSError:
        return {11434: "ollama", 18789: "openclaw", 8123: "home-assistant", 32400: "plex", 8096: "jellyfin", 1883: "mqtt", 9999: "homeapi"}.get(port, "")


def port_scan(args):
    host = args.ports
    header(f"Open TCP ports on {host}")
    if have("nmap"):
        rc, o, e = run(["nmap", "-Pn", "-T4", "--top-ports", "200", "-oG", "-", host], timeout=180)
        if rc == 0:
            found = False
            for line in o.splitlines():
                if "Ports:" in line:
                    for p in line.split("Ports:")[1].split(","):
                        f = p.strip().split("/")
                        if len(f) >= 5 and f[1] == "open":
                            bullet(f"{f[0]}/{f[2]} {f[4] or service_name(int(f[0]))}")
                            found = True
            if not found:
                bullet("none of the 200 most common ports are open (or host blocks probes)")
            return 0
        warn(f"nmap failed: {e.strip()[:120]}; falling back to a quick socket scan")
    try:
        target = socket.gethostbyname(host)
    except socket.gaierror:
        warn(f"cannot resolve {host}")
        return 1

    def probe(port):
        s = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
        s.settimeout(1.5)
        try:
            return port if s.connect_ex((target, port)) == 0 else None
        finally:
            s.close()

    with cf.ThreadPoolExecutor(max_workers=64) as ex:
        openp = [p for p in ex.map(probe, COMMON_PORTS) if p]
    for p in openp:
        bullet(f"{p}/tcp {service_name(p)}")
    if not openp:
        bullet(f"none of {len(COMMON_PORTS)} common ports open")
    return 0


# ---- wifi -------------------------------------------------------------------


def wifi_ifaces():
    return [p.name for p in Path("/sys/class/net").iterdir() if (p / "wireless").exists()]


def cmd_wifi(args):
    ifaces = wifi_ifaces()
    if args.iface:
        ifaces = [args.iface]
    if not ifaces:
        warn("no wireless interface on this machine")
        rc, virt, _ = run(["systemd-detect-virt"], timeout=5)
        if rc == 0 and virt.strip() != "none":
            bullet(f"this is a {virt.strip()} virtual machine; Wi-Fi scanning has to run on the physical host")
        bullet("if a USB Wi-Fi adapter is attached, check `ip link` and `lsusb` for it")
        return 1
    for iface in ifaces:
        header(f"Wi-Fi interface {iface}")
        if have("iw"):
            rc, o, _ = run(["iw", "dev", iface, "link"], timeout=10)
            if "Connected to" in o:
                ssid = re.search(r"SSID: (.+)", o)
                sig = re.search(r"signal: (-?\d+) dBm", o)
                freq = re.search(r"freq: (\d+)", o)
                rx = re.search(r"rx bitrate: ([\d.]+ MBit/s)", o)
                tx = re.search(r"tx bitrate: ([\d.]+ MBit/s)", o)
                bullet(f"connected to {ssid.group(1) if ssid else '?'}, signal {sig.group(1) if sig else '?'} dBm, {freq.group(1) if freq else '?'} MHz, rx {rx.group(1) if rx else '?'}, tx {tx.group(1) if tx else '?'}")
            else:
                bullet("not connected")
        ip4, plen = iface_ipv4(iface)
        if ip4:
            bullet(f"ipv4 {ip4}/{plen}, mac {read(f'/sys/class/net/{iface}/address')}")
        if not args.scan:
            continue
        header(f"Networks seen by {iface} (strongest first)")
        nets = []
        if have("nmcli"):
            rc, o, e = run(["nmcli", "-m", "multiline", "-f", "SSID,BSSID,CHAN,FREQ,SIGNAL,SECURITY", "dev", "wifi", "list", "ifname", iface, "--rescan", "yes"], timeout=40)
            cur = {}
            for line in o.splitlines():
                k, _, v = line.partition(":")
                k = k.strip()
                v = v.strip()
                if k == "SSID" and cur:
                    nets.append(cur)
                    cur = {}
                cur[k] = v
            if cur:
                nets.append(cur)
            nets = [{"ssid": n.get("SSID") or "<hidden>", "bssid": n.get("BSSID", ""), "chan": n.get("CHAN", ""),
                     "freq": n.get("FREQ", "").split()[0], "signal": f"{n.get('SIGNAL', '?')}%", "sec": n.get("SECURITY", "") or "open",
                     "sort": -int(n.get("SIGNAL", "0") or 0)} for n in nets]
        elif have("iw"):
            rc, o, e = run(["iw", "dev", iface, "scan"], timeout=40, sudo=True)
            if rc != 0:
                warn(f"iw scan failed: {e.strip()[:120]} (needs sudo, and the interface must be up)")
            cur = None
            for line in o.splitlines():
                m = re.match(r"^BSS ([0-9a-f:]{17})", line)
                if m:
                    cur = {"ssid": "<hidden>", "bssid": m.group(1), "chan": "", "freq": "", "signal": "", "sec": "open", "sort": 0}
                    nets.append(cur)
                    continue
                if cur is None:
                    continue
                s = line.strip()
                if s.startswith("SSID:"):
                    cur["ssid"] = s[5:].strip() or "<hidden>"
                elif s.startswith("signal:"):
                    m = re.search(r"(-?[\d.]+) dBm", s)
                    if m:
                        cur["signal"] = f"{float(m.group(1)):.0f} dBm"
                        cur["sort"] = -float(m.group(1))
                elif s.startswith("freq:"):
                    cur["freq"] = s.split(":")[1].strip().split(".")[0]
                elif s.startswith("DS Parameter set: channel"):
                    cur["chan"] = s.rsplit(" ", 1)[1]
                elif s.startswith("RSN:"):
                    cur["sec"] = "WPA2/3"
                elif s.startswith("WPA:") and cur["sec"] == "open":
                    cur["sec"] = "WPA"
        else:
            missing("iw or nmcli", "apt install iw")
            continue
        seen = set()
        for n in sorted(nets, key=lambda n: n["sort"]):
            key = (n["ssid"], n["bssid"])
            if key in seen:
                continue
            seen.add(key)
            band = ""
            try:
                f = int(n["freq"])
                band = "2.4 GHz" if f < 3000 else ("5 GHz" if f < 5900 else "6 GHz")
            except ValueError:
                pass
            bullet(f"{n['ssid']}: {n['signal']}, ch {n['chan'] or '?'} {band}, {n['sec']}, {n['bssid']}")
        if not nets:
            bullet("no networks found")
    return 0


# ---- docker -----------------------------------------------------------------


def docker(args, timeout=30):
    rc, o, e = run(["docker"] + args, timeout=timeout)
    if rc != 0 and ("permission denied" in e.lower() or "connect: permission" in e.lower()):
        rc, o, e = run(["docker"] + args, timeout=timeout, sudo=True)
    return rc, o, e


def cmd_docker(args):
    if not have("docker"):
        warn("Docker is not installed on this machine")
        bullet("install: `curl -fsSL https://get.docker.com | sh` then `sudo usermod -aG docker $USER` (ask Chinmay first)")
        return 1
    rc, o, e = docker(["info", "--format", "{{.ServerVersion}}|{{.ContainersRunning}}|{{.ContainersStopped}}|{{.Images}}|{{.Driver}}|{{.DockerRootDir}}"], timeout=20)
    if rc != 0:
        warn("docker daemon not reachable: " + (e.strip().splitlines() or ["?"])[-1][:160])
        return 1
    sub = args.what
    if sub == "ps":
        ver, running, stopped, images, driver, root = (o.strip().split("|") + ["?"] * 6)[:6]
        header(f"Docker {ver}: {running} running, {stopped} stopped, {images} images, storage {driver} at {root}")
        rc, o, e = docker(["ps"] + (["-a"] if args.all else []) + ["--format", "{{.Names}}|{{.Status}}|{{.Image}}|{{.Ports}}"])
        for line in o.splitlines():
            name, status, image, ports = (line.split("|") + [""] * 4)[:4]
            ports = re.sub(r"(?:0\.0\.0\.0|\[::\]):", "", ports)
            ports = ", ".join(dict.fromkeys(p.strip() for p in ports.split(",") if p.strip()))
            bullet(f"{name}: {status}, image {image}" + (f", ports {ports}" if ports else ""))
        if not o.strip():
            bullet("no containers" + ("" if args.all else " running (use --all for stopped ones)"))
    elif sub == "stats":
        header("Container resource usage (one sample)")
        rc, o, e = docker(["stats", "--no-stream", "--format", "{{.Name}}|{{.CPUPerc}}|{{.MemUsage}}|{{.MemPerc}}|{{.NetIO}}|{{.BlockIO}}|{{.PIDs}}"], timeout=40)
        for line in o.splitlines():
            n, cpu, mem, memp, net, blk, pids = (line.split("|") + [""] * 7)[:7]
            bullet(f"{n}: cpu {cpu}, mem {mem} ({memp}), net {net}, disk {blk}, pids {pids}")
        if not o.strip():
            bullet("no running containers")
    elif sub == "images":
        header("Images")
        rc, o, e = docker(["images", "--format", "{{.Repository}}:{{.Tag}}|{{.Size}}|{{.CreatedSince}}|{{.ID}}"])
        for line in o.splitlines()[:args.top]:
            repo, size, created, iid = (line.split("|") + [""] * 4)[:4]
            bullet(f"{repo}: {size}, created {created}, id {iid}")
        rc, o, e = docker(["images", "-f", "dangling=true", "-q"])
        n = len(o.split())
        if n:
            bullet(f"{n} dangling image layers (reclaim with: manage clean --docker --yes)")
    elif sub == "volumes":
        header("Volumes")
        rc, o, e = docker(["volume", "ls", "--format", "{{.Name}}|{{.Driver}}"])
        rc2, o2, _ = docker(["system", "df", "-v", "--format", "{{json .}}"], timeout=40)
        sizes = {}
        try:
            data = json.loads(o2) if o2.strip().startswith("{") else {}
            for v in data.get("Volumes", []):
                sizes[v.get("Name")] = v.get("Size")
        except json.JSONDecodeError:
            pass
        for line in o.splitlines():
            name, driver = (line.split("|") + [""])[:2]
            bullet(f"{name}" + (f": {sizes[name]}" if sizes.get(name) else ""))
        if not o.strip():
            bullet("none")
    elif sub == "networks":
        header("Networks")
        rc, o, e = docker(["network", "ls", "--format", "{{.Name}}|{{.Driver}}|{{.Scope}}"])
        for line in o.splitlines():
            n, d, s = (line.split("|") + [""] * 3)[:3]
            bullet(f"{n}: {d}, {s}")
    elif sub == "df":
        header("Docker disk usage")
        rc, o, e = docker(["system", "df"], timeout=60)
        for line in o.splitlines()[1:]:
            bullet(" ".join(line.split()))
    elif sub == "logs":
        if not args.name:
            warn("usage: docker logs NAME [-n N]")
            return 1
        header(f"Last {args.lines} log lines of {args.name}")
        rc, o, e = docker(["logs", "--tail", str(args.lines), "--timestamps", args.name], timeout=30)
        for line in (o + e).splitlines()[-args.lines:]:
            print("  " + line[:200])
    elif sub == "inspect":
        if not args.name:
            warn("usage: docker inspect NAME")
            return 1
        fmt = ("{{.Name}}|{{.Config.Image}}|{{.State.Status}}|{{.State.StartedAt}}|{{.RestartCount}}|{{.HostConfig.RestartPolicy.Name}}|"
               "{{if .State.Health}}{{.State.Health.Status}}{{end}}|{{range .NetworkSettings.Networks}}{{.IPAddress}} {{end}}|"
               "{{range .Mounts}}{{.Source}}->{{.Destination}} {{end}}|{{range $p, $v := .NetworkSettings.Ports}}{{$p}} {{end}}|{{.Config.Env}}")
        rc, o, e = docker(["inspect", "--format", fmt, args.name])
        if rc != 0:
            warn(e.strip()[:200])
            return 1
        name, image, status, started, restarts, policy, health, ips, mounts, ports, env = (o.strip().split("|") + [""] * 11)[:11]
        header(f"Container {name.lstrip('/')}")
        bullet(f"image {image}, status {status}" + (f", health {health}" if health else "") + f", started {started[:19]}")
        bullet(f"restart policy {policy}, restarts {restarts}, ip {ips.strip() or 'none'}")
        bullet(f"ports: {ports.strip() or 'none'}")
        bullet(f"mounts: {mounts.strip() or 'none'}")
        envs = [v for v in env.strip("[]").split() if "=" in v]
        safe = [v if not re.search(r"(pass|secret|token|key)", v.split("=")[0], re.I) else v.split("=")[0] + "=<hidden>" for v in envs]
        bullet(f"env: {', '.join(safe[:12]) or 'none'}" + (" …" if len(safe) > 12 else ""))
    elif sub == "top":
        if not args.name:
            warn("usage: docker top NAME")
            return 1
        rc, o, e = docker(["top", args.name, "-o", "pid,user,%cpu,%mem,args"])
        header(f"Processes in {args.name}")
        for line in o.splitlines()[1:21]:
            bullet(" ".join(line.split())[:160])
    elif sub == "compose":
        header("Compose projects")
        rc, o, e = docker(["compose", "ls", "-a", "--format", "json"], timeout=30)
        try:
            for p in json.loads(o or "[]"):
                bullet(f"{p.get('Name')}: {p.get('Status')}, file {p.get('ConfigFiles')}")
        except json.JSONDecodeError:
            bullet(o.strip() or e.strip()[:160] or "none")
    return 0


# ---- hardware ---------------------------------------------------------------


def cmd_hardware(args):
    header("Machine")
    dmi = Path("/sys/class/dmi/id")
    vendor, product, bios = read(dmi / "sys_vendor"), read(dmi / "product_name"), read(dmi / "bios_version")
    if vendor or product:
        bullet(f"{vendor} {product}" + (f", BIOS {bios}" if bios else ""))
    rc, virt, _ = run(["systemd-detect-virt"], timeout=5)
    if rc == 0 and virt.strip() != "none":
        bullet(f"virtualised: {virt.strip()}")
    rc, o, _ = run(["lscpu"], timeout=10)
    keep = ("Model name", "CPU(s)", "Thread(s) per core", "Core(s) per socket", "Socket(s)", "CPU max MHz", "Virtualization", "Hypervisor vendor", "L2 cache", "L3 cache", "Architecture")
    info = {}
    for line in o.splitlines():
        k, _, v = line.partition(":")
        if k.strip() in keep:
            info[k.strip()] = v.strip()
    if info:
        bullet("CPU: " + ", ".join(f"{k} {v}" for k, v in info.items()))
    m = meminfo()
    bullet(f"RAM: {human(m['MemTotal'])}")
    if args.dmi:
        rc, o, e = run(["dmidecode", "-t", "memory"], timeout=15, sudo=True)
        if rc == 0:
            dimms = re.findall(r"\n\s+Size: (\d+ [MG]B).*?\n\s+Form Factor: (\S+).*?\n\s+Locator: (\S+).*?Type: (\S+).*?Speed: ([^\n]+)", o, re.S)
            for size, form, loc, typ, speed in dimms:
                bullet(f"DIMM {loc}: {size} {typ} {form} {speed}")
        else:
            bullet("dmidecode needs sudo")
    rc, o, _ = run(["lspci"], timeout=10)
    gpus = [l.split(": ", 1)[1] for l in o.splitlines() if re.search(r"VGA|3D|Display", l)]
    for g in gpus:
        bullet(f"GPU: {g}")
    if have("nvidia-smi"):
        rc, o, _ = run(["nvidia-smi", "--query-gpu=name,memory.used,memory.total,utilization.gpu,temperature.gpu", "--format=csv,noheader"], timeout=15)
        for line in o.splitlines():
            bullet("NVIDIA: " + line.strip())
    header("Storage devices")
    rc, o, _ = run(["lsblk", "-dn", "-o", "NAME,SIZE,MODEL,ROTA,TRAN,TYPE"])
    for line in o.splitlines():
        f = line.split()
        if f and f[-1] == "disk":
            kind = "HDD" if len(f) >= 4 and f[-3] == "1" else "SSD/flash"
            bullet(" ".join(f[:-1]) + f" ({kind})")
    header("Temperatures")
    if have("sensors"):
        rc, o, _ = run(["sensors"], timeout=10)
        shown = 0
        for line in o.splitlines():
            if "°C" in line and shown < 12:
                bullet(" ".join(line.split())[:100])
                shown += 1
        if not shown:
            bullet("sensors found nothing (run `sudo sensors-detect` once on bare metal)")
    else:
        temps = temperatures()
        for n, v in temps[:10]:
            bullet(f"{n}: {v:.0f}°C")
        if not temps:
            bullet("no temperature sensors exposed (normal inside a VM)")
    for bat in Path("/sys/class/power_supply").glob("BAT*"):
        bullet(f"battery {bat.name}: {read(bat / 'capacity')}% {read(bat / 'status')}")
    if args.pci:
        header("PCI devices")
        rc, o, _ = run(["lspci"], timeout=10)
        for line in o.splitlines():
            bullet(line[:140])
        if not o.strip():
            bullet("none listed")
    if args.usb:
        header("USB devices")
        rc, o, _ = run(["lsusb"], timeout=10)
        for line in o.splitlines():
            bullet(line[:140])
        if not o.strip():
            bullet("none attached")


# ---- users ------------------------------------------------------------------


def cmd_users(args):
    header("Logged in now")
    rc, o, _ = run(["who"], timeout=5)
    for line in o.splitlines():
        bullet(" ".join(line.split()))
    if not o.strip():
        bullet("nobody")
    header("Accounts with a login shell")
    accts = []
    for line in read("/etc/passwd").splitlines():
        f = line.split(":")
        if len(f) >= 7 and not f[6].endswith(("nologin", "false", "sync", "halt", "shutdown")):
            accts.append(f"{f[0]} (uid {f[2]})")
    bullet(", ".join(accts))
    rc, o, _ = run(["getent", "group", "sudo", "wheel", "admin"], timeout=5)
    admins = sorted({u for l in o.splitlines() for u in l.split(":")[-1].split(",") if u})
    bullet("sudo/admin group members: " + (", ".join(admins) or "none"))
    if have("last"):
        header(f"Last {args.last} logins")
        rc, o, _ = run(["last", "-n", str(args.last), "-w", "-F"], timeout=10)
        for line in o.splitlines():
            if line.strip() and not line.startswith(("wtmp", "btmp")):
                bullet(" ".join(line.split())[:120])
    if args.failed:
        header("Failed SSH logins (last 24 h)")
        rc, o, _ = run(["journalctl", "-u", "ssh", "-u", "sshd", "--since", "-24h", "--no-pager", "-o", "cat", "-q"], timeout=20)
        fails = [l for l in o.splitlines() if "Failed password" in l or "Invalid user" in l]
        ips = {}
        for l in fails:
            m = re.search(r"from (\S+)", l)
            if m:
                ips[m.group(1)] = ips.get(m.group(1), 0) + 1
        bullet(f"{len(fails)} failed attempts")
        for ip, n in sorted(ips.items(), key=lambda kv: -kv[1])[:10]:
            bullet(f"{ip}: {n}")
    rc, o, _ = run(["sshd", "-T"], timeout=5, sudo=True)
    if rc == 0:
        conf = dict(l.split(None, 1) for l in o.splitlines() if " " in l)
        header("SSH server")
        bullet(f"password auth {conf.get('passwordauthentication')}, root login {conf.get('permitrootlogin')}, port {conf.get('port')}")


# ---- packages ---------------------------------------------------------------


def apt_upgradable():
    if not have("apt"):
        return None
    rc, o, _ = run(["apt", "list", "--upgradable"], timeout=60)
    if rc != 0:
        return None
    return [l.split("/")[0] for l in o.splitlines() if "/" in l and "upgradable" in l]


def pkg_manager():
    for m in ("apt", "dnf", "yum", "pacman", "apk", "zypper"):
        if have(m):
            return m
    return None


def cmd_packages(args):
    pm = pkg_manager()
    if pm != "apt":
        header(f"Package manager: {pm or 'unknown'}")
        hints = {"dnf": "dnf check-update; dnf list installed | wc -l", "yum": "yum check-update", "pacman": "pacman -Qu; pacman -Q | wc -l",
                 "apk": "apk version -l '<'", "zypper": "zypper list-updates"}
        bullet("this script has detailed support for apt only; use: " + hints.get(pm, "the distro's tool"))
        return 0
    if args.search:
        header(f"Packages matching '{args.search}' (apt-cache search)")
        rc, o, _ = run(["apt-cache", "search", "--names-only", args.search], timeout=30)
        for line in o.splitlines()[:args.top]:
            bullet(line[:140])
        return 0
    if args.installed:
        header(f"Installed packages matching '{args.installed}'")
        rc, o, _ = run(["dpkg-query", "-W", "-f=${db:Status-Status} ${binary:Package} ${Version} ${Installed-Size}\n", f"*{args.installed}*"], timeout=20)
        shown = 0
        for line in o.splitlines():
            st, n, v, s = (line.split() + ["", "", "", "0"])[:4]
            if st != "installed":
                continue
            bullet(f"{n} {v} ({human(int(s) * 1024) if s.isdigit() else '?'})")
            shown += 1
            if shown >= args.top:
                break
        if not shown:
            bullet("none installed")
        return 0
    if args.history:
        header("Recent apt operations")
        text = read("/var/log/apt/history.log")
        blocks = [b for b in text.split("\n\n") if b.strip()][-args.top:]
        for b in blocks:
            start = re.search(r"Start-Date: (.+)", b)
            cmd = re.search(r"Commandline: (.+)", b)
            bullet(f"{start.group(1) if start else '?'}: {cmd.group(1)[:120] if cmd else '?'}")
        return 0
    header("Packages (apt)")
    rc, o, _ = run(["dpkg-query", "-f", "${binary:Package}\n", "-W"], timeout=20)
    bullet(f"installed: {len(o.splitlines())}")
    stamp = "/var/lib/apt/periodic/update-success-stamp"
    if os.path.exists(stamp):
        age = time.time() - os.path.getmtime(stamp)
        bullet(f"package lists refreshed {duration(age)} ago")
    upg = apt_upgradable() or []
    sec = [p for p in upg if "security" in p.lower()]
    bullet(f"upgradable: {len(upg)}")
    if upg and (args.upgradable or len(upg) <= 15):
        bullet("  " + ", ".join(upg[:60]) + (" …" if len(upg) > 60 else ""))
    if args.security:
        rc, o, _ = run(["apt", "list", "--upgradable"], timeout=60)
        sec = [l.split("/")[0] for l in o.splitlines() if "-security" in l]
        bullet(f"security updates: {len(sec)}" + (": " + ", ".join(sec[:40]) if sec else ""))
    if os.path.exists("/var/run/reboot-required"):
        pk = read("/var/run/reboot-required.pkgs")
        bullet("reboot required" + (f" (by {', '.join(pk.split()[:5])})" if pk else ""))
    rc, o, _ = run(["systemctl", "is-enabled", "unattended-upgrades"], timeout=5)
    bullet(f"unattended-upgrades: {o.strip() or 'not installed'}")
    if have("snap"):
        rc, o, _ = run(["snap", "list"], timeout=20)
        bullet(f"snaps installed: {max(len(o.splitlines()) - 1, 0)}")
    rc, o, _ = run(["apt-mark", "showhold"], timeout=10)
    if o.strip():
        bullet("held packages: " + ", ".join(o.split()))
    return 0


# ---- quick network checks ---------------------------------------------------


def cmd_ping(args):
    rc, o, e = run(["ping", "-c", str(args.count), "-W", "2", args.host], timeout=args.count * 3 + 5)
    header(f"Ping {args.host}")
    loss = re.search(r"(\d+(?:\.\d+)?)% packet loss", o)
    rtt = re.search(r"= ([\d.]+)/([\d.]+)/([\d.]+)", o)
    if rc == 2 or "unknown host" in e.lower() or "not known" in e.lower():
        bullet(f"cannot resolve {args.host}")
        return 1
    bullet(f"{args.count} sent, loss {loss.group(1) if loss else '?'}%" + (f", rtt min/avg/max {rtt.group(1)}/{rtt.group(2)}/{rtt.group(3)} ms" if rtt else ", no replies"))
    return 0 if rtt else 1


def cmd_trace(args):
    header(f"Route to {args.host}")
    if have("traceroute"):
        rc, o, e = run(["traceroute", "-n", "-w", "2", "-q", "1", "-m", "20", args.host], timeout=90)
    elif have("tracepath"):
        rc, o, e = run(["tracepath", "-n", "-m", "20", args.host], timeout=90)
    else:
        missing("traceroute", "apt install traceroute")
        return 1
    for line in o.splitlines():
        if line.strip():
            bullet(" ".join(line.split())[:120])
    if e.strip() and not o.strip():
        bullet(e.strip()[:160])
    return rc


def cmd_dns(args):
    header(f"DNS lookup {args.name} ({args.type})")
    if have("dig"):
        cmd = ["dig", "+noall", "+answer", "+stats", args.name, args.type]
        if args.server:
            cmd.append(f"@{args.server}")
        rc, o, e = run(cmd, timeout=15)
        answers = [" ".join(l.split()) for l in o.splitlines() if l and not l.startswith(";")]
        qtime = re.search(r"Query time: (\d+) msec", o)
        server = re.search(r"SERVER: (\S+)", o)
        for a in answers:
            bullet(a)
        if not answers:
            bullet("no answer")
        bullet(f"server {server.group(1) if server else '?'}, {qtime.group(1) if qtime else '?'} ms")
    else:
        try:
            for fam, _, _, _, sa in socket.getaddrinfo(args.name, None):
                bullet(sa[0])
        except socket.gaierror as ex:
            bullet(f"lookup failed: {ex}")
    return 0


def cmd_portcheck(args):
    ports = []
    for part in args.ports.split(","):
        if "-" in part:
            a, b = part.split("-", 1)
            ports += list(range(int(a), int(b) + 1))
        else:
            ports.append(int(part))
    header(f"TCP port check on {args.host}")
    try:
        target = socket.gethostbyname(args.host)
    except socket.gaierror:
        bullet(f"cannot resolve {args.host}")
        return 1
    for p in ports[:200]:
        s = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
        s.settimeout(2.5)
        t = time.time()
        r = s.connect_ex((target, p))
        s.close()
        bullet(f"{p} {service_name(p)}: {'open' if r == 0 else 'closed/filtered'}" + (f" ({(time.time() - t) * 1000:.0f} ms)" if r == 0 else ""))
    return 0


# ---- manage (mutating, gated) -----------------------------------------------


def log_action(text):
    try:
        STATE_DIR.mkdir(parents=True, exist_ok=True)
        with open(STATE_DIR / "actions.log", "a") as f:
            f.write(f"{dt.datetime.now().isoformat(timespec='seconds')} {text}\n")
    except OSError:
        pass


def gate(args, plan):
    print("Planned action:")
    for p in plan:
        bullet(p)
    if not args.yes:
        print("Not executed. Confirm with Chinmay, then re-run the same command with --yes.")
        return False
    return True


def cmd_manage(args):
    act = args.action
    if act == "kill":
        target = args.target
        pids = []
        # never target ourselves, our shell ancestors, or other copies of this script
        ancestors = set()
        p = os.getpid()
        while p > 1 and p not in ancestors:
            ancestors.add(p)
            try:
                p = _proc_snapshot_one(p)
            except Exception:
                break
        if target.isdigit():
            pids = [int(target)]
        else:
            rc, o, _ = run(["pgrep", "-x", target])
            pids = [int(p) for p in o.split()]
            if not pids:
                rc, o, _ = run(["pgrep", "-f", target])
                pids = [int(p) for p in o.split()]
            pids = [p for p in pids if p not in ancestors and "sysadmin.py" not in proc_cmdline(p, 400)]
        if not pids:
            warn(f"no process matches {target}")
            return 1
        # hard safety: never touch the OpenClaw gateway (Novee itself), init, sshd, systemd, or the kernel
        protected = []
        for p in list(pids):
            cl = proc_cmdline(p, 400)
            comm = read(f"/proc/{p}/comm")
            if p <= 1 or "openclaw" in cl or comm in ("systemd", "sshd", "init", "kthreadd", "dbus-daemon", "systemd-journal", "systemd-logind") or cl.startswith("["):
                protected.append(f"pid {p} ({cl[:60]})")
                pids.remove(p)
        if protected:
            warn("refusing to signal protected processes: " + "; ".join(protected))
            bullet("the OpenClaw gateway is restarted with: manage service restart openclaw-gateway --user --yes")
        if not pids:
            return 1
        if len(pids) > 5 and not target.isdigit():
            warn(f"'{target}' matches {len(pids)} processes; refusing a mass kill. Use a pid or a more specific name.")
            return 1
        sig = args.signal.upper().replace("SIG", "")
        plan = [f"send SIG{sig} to pid {p} ({proc_cmdline(p, 60)})" for p in pids]
        if not gate(args, plan):
            return 0
        for p in pids:
            rc, o, e = run(["kill", f"-{sig}", str(p)])
            if rc != 0:
                rc, o, e = run(["kill", f"-{sig}", str(p)], sudo=True)
            bullet(f"pid {p}: {'signalled' if rc == 0 else 'failed: ' + e.strip()[:100]}")
            log_action(f"kill -{sig} {p} rc={rc}")
        return 0
    if act == "nice":
        plan = [f"renice pid {args.target} to {args.value}"]
        if not gate(args, plan):
            return 0
        rc, o, e = run(["renice", "-n", str(args.value), "-p", str(args.target)], sudo=int(args.value) < 0)
        bullet(o.strip() or e.strip())
        log_action(f"renice {args.target} {args.value} rc={rc}")
        return rc
    if act == "service":
        verb, unit = args.target, args.value
        if verb not in ("start", "stop", "restart", "reload", "enable", "disable"):
            warn("service verb must be start|stop|restart|reload|enable|disable")
            return 1
        scope = args.user
        if not scope:
            # user units (like openclaw-gateway) are invisible to system systemctl; detect them
            rc_u, _, _ = systemctl(["cat", unit], user=True, timeout=10)
            rc_s, _, _ = systemctl(["cat", unit], user=False, timeout=10)
            if rc_u == 0 and rc_s != 0:
                scope = True
                bullet(f"{unit} is a user-level unit; using systemctl --user")
        if not gate(args, [f"systemctl {'--user ' if scope else ''}{verb} {unit}"]):
            return 0
        rc, o, e = systemctl([verb, unit], scope) if scope else run(["systemctl", verb, unit, "--no-pager"], sudo=True)
        log_action(f"systemctl {verb} {unit} rc={rc}")
        if rc != 0:
            warn(e.strip()[:200] or "failed")
            return rc
        rc, o, _ = systemctl(["is-active", unit], scope)
        bullet(f"{unit} is now {o.strip()}")
        return 0
    if act == "docker":
        verb, name = args.target, args.value
        if verb not in ("start", "stop", "restart"):
            warn("docker verb must be start|stop|restart")
            return 1
        if not gate(args, [f"docker {verb} {name}"]):
            return 0
        rc, o, e = docker([verb, name], timeout=90)
        log_action(f"docker {verb} {name} rc={rc}")
        bullet(f"{name}: {'done' if rc == 0 else 'failed: ' + e.strip()[:160]}")
        return rc
    if act == "clean":
        plan = ["apt-get autoremove -y and apt-get clean (remove unused packages and the package cache)",
                "journalctl --vacuum-time=14d (keep two weeks of logs)",
                "empty the trash older than 30 days (trash-empty 30) if trash-cli is installed"]
        if args.docker:
            plan.append("docker system prune -f (remove stopped containers, unused networks, dangling images and build cache; NOT volumes)")
        if not gate(args, plan):
            return 0
        before = shutil.disk_usage("/").free
        if have("apt-get"):
            run(["apt-get", "autoremove", "-y", "-q"], timeout=300, sudo=True)
            run(["apt-get", "clean"], timeout=60, sudo=True)
        run(["journalctl", "--vacuum-time=14d"], timeout=60, sudo=True)
        if have("trash-empty"):
            run(["trash-empty", "30", "-f"], timeout=60)
        if args.docker and have("docker"):
            rc, o, e = docker(["system", "prune", "-f"], timeout=300)
            m = re.search(r"Total reclaimed space: (.+)", o)
            bullet(f"docker reclaimed {m.group(1) if m else '?'}")
        after = shutil.disk_usage("/").free
        bullet(f"root filesystem free: {human(before)} -> {human(after)} (gained {human(max(after - before, 0))})")
        log_action(f"clean docker={args.docker} gained={after - before}")
        return 0
    warn("unknown manage action")
    return 1


# ----------------------------------------------------------------------------
# argument parsing
# ----------------------------------------------------------------------------


def build_parser():
    p = argparse.ArgumentParser(prog="sysadmin.py", description="Linux sysadmin helper (read-only unless `manage --yes`).")
    p.add_argument("--no-sudo", action="store_true", help="never use sudo")
    p.add_argument("--version", action="version", version=VERSION)
    sp = p.add_subparsers(dest="cmd", required=True)

    sp.add_parser("overview", help="one-screen health summary: OS, CPU, memory, disks, network, services, docker, updates")
    s = sp.add_parser("cpu", help="CPU usage per core, load, frequency, temperatures, top CPU processes")
    s.add_argument("--top", type=int, default=8)
    s = sp.add_parser("memory", help="RAM and swap breakdown, top memory processes, recent OOM kills")
    s.add_argument("--top", type=int, default=8)
    s = sp.add_parser("disk", help="filesystem usage, block devices; --io rates; --path P biggest items; --smart health")
    s.add_argument("--path")
    s.add_argument("--big", action="store_true", help="with --path: also list the largest files")
    s.add_argument("--io", action="store_true")
    s.add_argument("--smart", action="store_true")
    s.add_argument("--top", type=int, default=12)
    s.add_argument("--sudo", action="store_true", help="use sudo for du/find (to see root-only dirs)")
    s = sp.add_parser("processes", aliases=["top", "ps"], help="processes sorted by cpu or mem; --find PATTERN; --user U; --tree")
    s.add_argument("--sort", choices=["cpu", "mem"], default="cpu")
    s.add_argument("--top", type=int, default=10)
    s.add_argument("--find", nargs="+", help="regex or words to match in the name/command line")
    s.add_argument("--user")
    s.add_argument("--tree", action="store_true")
    s = sp.add_parser("services", help="systemd: running/failed services; --status UNIT; --logs UNIT; --timers; --all; --user")
    s.add_argument("--status")
    s.add_argument("--logs")
    s.add_argument("--failed", action="store_true", help="show recent log lines of each failed unit")
    s.add_argument("--all", action="store_true")
    s.add_argument("--timers", action="store_true")
    s.add_argument("--user", action="store_true", help="user-level systemd units")
    s.add_argument("-n", "--lines", type=int, default=20)
    s = sp.add_parser("logs", help="journal errors (default last 24h); --unit U; --grep X; --since S; --dmesg; --boots")
    s.add_argument("--unit")
    s.add_argument("--grep")
    s.add_argument("--since", default="-24h")
    s.add_argument("--errors", action="store_true")
    s.add_argument("--dmesg", action="store_true")
    s.add_argument("--boots", action="store_true")
    s.add_argument("--user", action="store_true")
    s.add_argument("-n", "--lines", type=int, default=25)
    s = sp.add_parser("network", aliases=["net"], help="interfaces, gateway, DNS, listening ports; --connections --routes --stats --arp --firewall --public-ip")
    s.add_argument("--ports", action="store_true")
    s.add_argument("--connections", action="store_true")
    s.add_argument("--routes", action="store_true")
    s.add_argument("--stats", action="store_true")
    s.add_argument("--arp", action="store_true")
    s.add_argument("--firewall", action="store_true")
    s.add_argument("--public-ip", action="store_true")
    s.add_argument("--top", type=int, default=15)
    s = sp.add_parser("scan", help="discover LAN devices (IP, MAC, vendor, hostname); --subnet CIDR; --fast; --json; --ports HOST")
    s.add_argument("--subnet")
    s.add_argument("--fast", action="store_true", help="skip hostname lookups")
    s.add_argument("--json", action="store_true")
    s.add_argument("--ports", metavar="HOST", help="scan the common TCP ports of one host instead")
    s = sp.add_parser("wifi", help="wireless interfaces, current link; --scan lists nearby networks")
    s.add_argument("--scan", action="store_true")
    s.add_argument("--iface")
    s = sp.add_parser("docker", help="containers; WHAT = ps|stats|images|volumes|networks|df|logs NAME|inspect NAME|top NAME|compose")
    s.add_argument("what", nargs="?", default="ps", choices=["ps", "stats", "images", "volumes", "networks", "df", "logs", "inspect", "top", "compose"])
    s.add_argument("name", nargs="?")
    s.add_argument("--all", action="store_true")
    s.add_argument("-n", "--lines", type=int, default=40)
    s.add_argument("--top", type=int, default=25)
    s = sp.add_parser("hardware", aliases=["hw"], help="machine, CPU, RAM, GPU, disks, temperatures; --dmi --pci --usb")
    s.add_argument("--dmi", action="store_true")
    s.add_argument("--pci", action="store_true")
    s.add_argument("--usb", action="store_true")
    s = sp.add_parser("users", help="logged in users, accounts, admins, last logins; --failed shows SSH brute-force attempts")
    s.add_argument("--last", type=int, default=8)
    s.add_argument("--failed", action="store_true")
    s = sp.add_parser("packages", aliases=["pkg"], help="apt status; --upgradable --security --history --search Q --installed Q")
    s.add_argument("--upgradable", action="store_true")
    s.add_argument("--security", action="store_true")
    s.add_argument("--history", action="store_true")
    s.add_argument("--search")
    s.add_argument("--installed")
    s.add_argument("--top", type=int, default=20)
    s = sp.add_parser("ping", help="ping HOST")
    s.add_argument("host")
    s.add_argument("-c", "--count", type=int, default=4)
    s = sp.add_parser("trace", help="traceroute HOST")
    s.add_argument("host")
    s = sp.add_parser("dns", help="dns NAME [--type A|AAAA|MX|TXT|CNAME|PTR] [--server 1.1.1.1]")
    s.add_argument("name")
    s.add_argument("--type", default="A")
    s.add_argument("--server")
    s = sp.add_parser("portcheck", help="portcheck HOST 22,80,443 or 8000-8010")
    s.add_argument("host")
    s.add_argument("ports")
    s = sp.add_parser("manage", help="MUTATING: kill PID|NAME | nice PID VALUE | service VERB UNIT | docker VERB NAME | clean [--docker]; needs --yes")
    s.add_argument("action", choices=["kill", "nice", "service", "docker", "clean"])
    s.add_argument("target", nargs="?")
    s.add_argument("value", nargs="?")
    s.add_argument("--yes", action="store_true")
    s.add_argument("--signal", default="TERM")
    s.add_argument("--docker", action="store_true", help="with clean: also docker system prune")
    s.add_argument("--user", action="store_true", help="with service: user-level unit")
    return p


def main(argv=None):
    global NO_SUDO
    parser = build_parser()
    args = parser.parse_args(argv)
    NO_SUDO = args.no_sudo
    alias = {"top": "processes", "ps": "processes", "net": "network", "hw": "hardware", "pkg": "packages"}
    cmd = alias.get(args.cmd, args.cmd)
    if cmd == "manage" and args.action in ("kill", "nice", "service", "docker") and not args.target:
        parser.error(f"manage {args.action} needs a target")
    if cmd == "manage" and args.action in ("nice", "service", "docker") and not args.value:
        parser.error(f"manage {args.action} needs two arguments")
    func = globals()[f"cmd_{cmd}"]
    try:
        rc = func(args)
    except KeyboardInterrupt:
        return 130
    except Exception as ex:  # keep the agent informed instead of a traceback wall
        print(f"ERROR in {cmd}: {type(ex).__name__}: {ex}")
        return 1
    return rc or 0


if __name__ == "__main__":
    sys.exit(main())
