#!/usr/bin/env python3
"""disk-health: how the hard drives and USB hubs on the host PC are doing.

The drives are attached to the host PC, not to this VM. This script asks the
host for a read-only report over SSH (a key that can run nothing else) and
prints short plain-text bullets. By default it changes nothing anywhere.
"""
import argparse, json, os, shutil, subprocess, sys
from datetime import datetime

CONFIG = os.path.expanduser(os.environ.get("DISK_HEALTH_CONFIG", "~/.config/novee/disk-health.json"))
KEY = os.path.expanduser("~/.ssh/disk-health")
LOCAL = os.path.expanduser("~/.local/bin/novee-disk-report")


def load_config():
    try:
        with open(CONFIG) as f:
            cfg = json.load(f)
    except (OSError, ValueError) as e:
        if os.access(LOCAL, os.X_OK):
            return {"local": True}
        sys.exit(f"ERROR: cannot read {CONFIG}: {e}\n"
                 "Tell Chinmay the disk-health skill is not configured (see docs/SETUP.md in the repository).")
    if not cfg.get("host") and not cfg.get("local"):
        sys.exit(f"ERROR: {CONFIG} has no \"host\" entry (user@address of the PC with the drives).")
    return cfg


def fetch(cfg, args):
    if cfg.get("local"):
        cmd = [LOCAL] + args
    else:
        cmd = ["ssh", "-i", KEY, "-o", "IdentitiesOnly=yes", "-o", "BatchMode=yes", "-o", "ConnectTimeout=8",
               "-o", "StrictHostKeyChecking=yes", cfg["host"]] + args
    try:
        p = subprocess.run(cmd, capture_output=True, text=True, timeout=600)
    except subprocess.TimeoutExpired:
        sys.exit("ERROR: the host PC did not finish the disk report within 10 minutes (a drive may be hanging).")
    if p.returncode != 0 and not p.stdout.strip():
        sys.exit("ERROR: could not get the disk report from the host PC: "
                 + (p.stderr.strip().splitlines() or ["no output"])[-1]
                 + "\nTell Chinmay the disk report is unavailable; do not guess.")
    try:
        r = json.loads(p.stdout)
    except ValueError:
        sys.exit("ERROR: the host PC sent an unreadable report: " + p.stdout[:200])
    if "error" in r:
        sys.exit("ERROR: " + r["error"])
    return r


def n(v, unit=""):
    return "unknown" if v is None else f"{v}{unit}"


def match(disk, needle):
    needle = needle.lower()
    hay = " ".join(str(x) for x in [disk["name"], disk.get("label"), disk.get("model"), disk.get("vendor")]
                   + disk.get("mountpoints", [])).lower()
    return needle in hay


def title(d):
    where = "internal" if d["transport"] in ("sata", "nvme") else (d["transport"] or "?").upper()
    return f"{d.get('label') or d['name']} ({d['name']}, {d.get('model') or 'unknown model'}, {d.get('size')}, {where})"


def render(r, only=None, brief=False):
    when = datetime.fromisoformat(r["time"]).strftime("%a %b %d %H:%M")
    disks = [d for d in r["disks"] if not only or match(d, only)]
    if only and not disks:
        print(f"No drive matches '{only}'. Drives present: " + ", ".join(d.get("label") or d["name"] for d in r["disks"]))
        return
    ext = [d for d in disks if d["transport"] == "usb"]
    print(f"Disk report from the host PC at {when}, kernel log window {r['hours']} h. Read-only: nothing was changed.")
    if r.get("actions"):
        print("Actions run on request: " + "; ".join(r["actions"]))

    print("\nVerdict per drive:")
    for d in disks:
        print(f"- {d.get('label') or d['name']}: {d['verdict']}")

    for d in disks:
        print(f"\n{title(d)}")
        sm = d.get("smart") or {}
        if sm.get("skipped"):
            print(f"- SMART: skipped, {sm['skipped']}")
        elif sm.get("unavailable"):
            print(f"- SMART: unavailable ({sm['unavailable']})")
        else:
            health = "PASSED" if sm.get("passed") else ("FAILED" if sm.get("passed") is False else "unknown")
            print(f"- SMART health {health}, temperature {n(sm.get('temperature_c'), ' C')}, "
                  f"power-on {n(sm.get('power_on_hours'), ' h')}, power cycles {n(sm.get('power_cycles'))}")
            a = sm.get("attributes") or {}

            def raw(k):
                return (a.get(k) or {}).get("raw")
            if a:
                por, pc = raw('power_off_retracts'), raw('power_cycles')
                retr = "not reported" if por is None else (f"{por} (unreliable raw value)" if pc and por > pc * 1.5 else f"{por} of {pc} power cycles")
                print(f"- sectors: reallocated {n(raw('reallocated_sectors'))}, pending {n(raw('pending_sectors'))}, "
                      f"offline uncorrectable {n(raw('offline_uncorrectable'))}; interface CRC errors {n(raw('udma_crc_errors'))}; "
                      f"emergency retracts {retr}")
            if sm.get("nvme"):
                nv = sm["nvme"]
                print(f"- NVMe: {n(nv.get('percentage_used'), '%')} worn, media errors {n(nv.get('media_errors'))}, "
                      f"unsafe shutdowns {n(nv.get('unsafe_shutdowns'))}")
            for f in sm.get("flags") or []:
                print(f"- note: {f}")
            if sm.get("self_test_running_pct_left") is not None:
                print(f"- a self-test is running now, {sm['self_test_running_pct_left']}% left")
            for t in (sm.get("self_tests") or [])[:1]:
                print(f"- last self-test: {t.get('type')} -> {t.get('status')} at {n(t.get('at_power_on_hours'), ' h')}")
        u = d.get("usb")
        if u:
            chain = [h for h in u["hub_chain"] if not h["root_hub"]]
            path = " -> ".join([f"{h.get('product') or 'hub'} ({h['devpath']})" for h in reversed(chain)] + ["this drive"]) if chain else "motherboard port, no hub"
            print(f"- USB: {u['speed_mbps'] or '?'} Mbps, driver {u['driver']}, declares {n(u.get('bMaxPower_ma'), ' mA')}, "
                  f"link power management U1 {u.get('lpm_u1') or 'n/a'} / U2 {u.get('lpm_u2') or 'n/a'}")
            print(f"- path: PC -> {path}")
            for h in chain:
                p = h.get("our_port") or {}
                extra = []
                if h.get("power_switching"):
                    extra.append(h["power_switching"].split(" (")[0] + " power switching")
                if h.get("self_powered_claim") is not None:
                    extra.append("claims self-powered" if h["self_powered_claim"] else "BUS-POWERED")
                if p.get("over_current_count"):
                    extra.append(f"{p['over_current_count']} over-current events on its port")
                if extra:
                    print(f"- hub {h['devpath']}: " + ", ".join(extra))
        for p in d.get("partitions", []):
            if not p.get("fstype"):
                continue
            line = f"- {p['name']} {p['fstype']}"
            if p.get("mountpoint"):
                opts = (p.get("mount") or {}).get("options", "")
                writable = p["fstype"] in ("ext4", "ext3", "ext2", "xfs", "btrfs", "f2fs", "ntfs", "ntfs3", "fuseblk", "vfat", "exfat")
                ro = " READ-ONLY" if ("emergency_ro" in opts or (writable and ",ro," in f",{opts},")) else ""
                line += f" mounted at {p['mountpoint']}{ro}, {p.get('used_pct') or '?'} used of {p.get('fs_size') or '?'}"
            else:
                line += " not mounted"
            e4 = p.get("ext4") or {}
            if e4 and not e4.get("unavailable"):
                line += f"; ext4 state '{e4.get('state')}'"
                if e4.get("error_count"):
                    le = e4.get("last_error") or {}
                    line += f", {e4['error_count']} errors, last {le.get('time')} in {le.get('function')}"
            print(line)
        k = r["kernel_log"]["per_device"]
        hits = {}
        for key in [d["name"]] + [p["name"] for p in d.get("partitions", [])]:
            for c, cnt in (k.get(key) or {}).get("counts", {}).items():
                hits[c] = hits.get(c, 0) + cnt
        if hits:
            print(f"- kernel log, last {r['hours']} h: " + ", ".join(f"{v} {c.replace('_', ' ')}" for c, v in sorted(hits.items(), key=lambda x: -x[1])))
            if not brief:
                for s in (k.get(d["name"]) or {}).get("samples", [])[:2]:
                    print(f"    {s}")
        else:
            print(f"- kernel log, last {r['hours']} h: clean")
        if d.get("read_sample"):
            rs = d["read_sample"]
            if isinstance(rs, dict):
                print(f"- read sample: {rs.get('skipped')}")
            else:
                print("- read sample: " + ", ".join(f"{x['offset_pct']}%: " + (x["error"] or f"{x['seconds']} s") for x in rs))
        if d.get("selftest"):
            print(f"- self-test: {'started' if d['selftest']['started'] else 'NOT started'}, {d['selftest']['note']}")
        for why in d.get("verdict_reasons") or []:
            print(f"- why: {why}")

    if r.get("hubs"):
        print("\nUSB hubs:")
        for h in r["hubs"]:
            hub = h["hub"]
            print(f"- {hub.get('product') or 'hub'} {hub['id']} at {hub['devpath']} with {', '.join(h['drives'])}: {h['status']}")
            for why in h["reasons"]:
                print(f"    {why}")
    elif ext:
        print("\nUSB hubs: none in use, every external drive is on a motherboard port.")

    missing = [e for e in r.get("fstab", []) if not e["device_present"]]
    unmounted = [e for e in r.get("fstab", []) if e["device_present"] and not e["mounted"]]
    if missing or unmounted or r.get("failed_units"):
        print("\nConfigured drives (fstab):")
        for e in missing:
            print(f"- {e['target']}: drive NOT CONNECTED (or not enumerating)")
        for e in unmounted:
            print(f"- {e['target']}: present as {e.get('device')} but not mounted")
        for f in r.get("failed_units", []):
            print(f"- failed systemd unit: {f}")
    g = r["kernel_log"].get("global") or {}
    pb = r["kernel_log"].get("previous_boot")
    if g.get("xhci") or g.get("over_current") or g.get("enumeration"):
        print(f"\nHost USB controller: {g.get('xhci', 0)} controller timeouts, {g.get('over_current', 0)} over-current messages, "
              f"{g.get('enumeration', 0)} devices that failed to enumerate")
    if pb:
        print(f"Previous boot for comparison: {pb['usb_resets']} USB resets, {pb['io_errors']} I/O errors, {pb['medium_errors']} medium errors")

    print("\nSuggestions:")
    if not r.get("suggestions"):
        print("- nothing to do; all drives look healthy")
    for s in r["suggestions"]:
        print(f"- {s}")
    print("- Nothing above was changed. Repairs (fsck, remount, moving drives, replacing a hub) are decisions for Chinmay.")
    for e in r.get("errors") or []:
        print(f"- WARNING, part of the report failed: {e}")


def main():
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--hours", type=int, default=24, help="hours of kernel log to summarize (1-720, default 24)")
    ap.add_argument("--drive", help="only this drive (name, label, model or mount path, substring match)")
    ap.add_argument("--selftest", metavar="short[:sdX]",
                    help="start a SMART short self-test (non-destructive, ~2 min). ONLY when Chinmay asked for a test.")
    ap.add_argument("--read-sample", action="store_true",
                    help="time small reads from each disk (spins drives up). ONLY when Chinmay asked for a test.")
    ap.add_argument("--brief", action="store_true", help="skip kernel log sample lines")
    ap.add_argument("--json", action="store_true", help="print the raw report")
    ap.add_argument("ignored", nargs="*", help="ignored: stray words")
    a = ap.parse_args()
    args = ["report", "--hours", str(max(1, min(a.hours, 720)))]
    if a.selftest:
        args += ["--selftest", a.selftest]
    if a.read_sample:
        args += ["--read-sample"]
    cfg = load_config()
    r = fetch(cfg, args)
    if a.json:
        print(json.dumps(r, indent=1))
        return
    render(r, only=a.drive, brief=a.brief)


if __name__ == "__main__":
    main()
