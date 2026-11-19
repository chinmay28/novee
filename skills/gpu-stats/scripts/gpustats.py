#!/usr/bin/env python3
"""gpu-stats: what is using the host PC's GPU, and who asked for it.

The GPU is in the host PC, not in this VM. This script asks the
host for a read-only report over SSH and prints short plain-text bullets.
"""
import argparse, json, os, subprocess, sys
from datetime import datetime

CONFIG = os.path.expanduser(os.environ.get("GPU_STATS_CONFIG", "~/.config/novee/gpu-stats.json"))
KEY = os.path.expanduser("~/.ssh/gpu-stats")


def load_config():
    """Machine-specific settings live outside the repository (see examples/gpu-stats.example.json)."""
    try:
        with open(CONFIG) as f:
            cfg = json.load(f)
    except (OSError, ValueError) as e:
        sys.exit(f"ERROR: cannot read {CONFIG}: {e}\n"
                 "Tell Chinmay the gpu-stats skill is not configured (see docs/SETUP.md in the repository).")
    if not cfg.get("host"):
        sys.exit(f"ERROR: {CONFIG} has no \"host\" entry (user@address of the GPU machine).")
    return cfg


CFG = load_config()
HOST = CFG["host"]
KNOWN = dict(CFG.get("devices") or {})
DESKTOP = {"gnome-shell", "Xwayland", "Xorg", "brave", "firefox", "chrome",
           "chromium", "code", "nautilus", "gjs", "mutter-x11-frames"}


def fetch(hours):
    cmd = ["ssh", "-i", KEY, "-o", "IdentitiesOnly=yes", "-o", "BatchMode=yes",
           "-o", "ConnectTimeout=8", "-o", "StrictHostKeyChecking=yes", HOST, str(hours)]
    try:
        p = subprocess.run(cmd, capture_output=True, text=True, timeout=75)
    except subprocess.TimeoutExpired:
        sys.exit("ERROR: the host PC did not answer within 75 s. It may be off or asleep.")
    if p.returncode != 0:
        sys.exit("ERROR: could not reach the host PC over SSH: "
                 + (p.stderr.strip().splitlines() or ["no output"])[-1]
                 + "\nTell Chinmay the GPU report is unavailable; do not guess numbers.")
    try:
        return json.loads(p.stdout)
    except ValueError:
        sys.exit("ERROR: the host PC sent an unreadable report: " + p.stdout[:200])


def gb(mib):
    return f"{mib / 1024:.1f} GB"


def ago(iso, now):
    s = max(0, int((now - datetime.fromisoformat(iso)).total_seconds()))
    if s < 90:
        return f"{s} s ago"
    if s < 5400:
        return f"{s // 60} min ago"
    if s < 172800:
        return f"{s / 3600:.1f} h ago"
    return f"{s // 86400} days ago"


def clock(iso):
    return datetime.fromisoformat(iso).strftime("%a %H:%M")


def who(ip):
    return f"{KNOWN[ip]}, {ip}" if ip in KNOWN else f"unknown device {ip}"


def main():
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--hours", type=int, default=2,
                    help="hours of Ollama request history to show (1-168, default 2)")
    ap.add_argument("--json", action="store_true", help="print the raw report")
    ap.add_argument("machine", nargs="*", help="ignored: there is only one GPU machine")
    a = ap.parse_args()
    r = fetch(max(1, min(a.hours, 168)))
    if a.json:
        print(json.dumps(r, indent=1))
        return
    now = datetime.fromisoformat(r["time"])
    print(f"GPU report from host PC {r['host']} at {now.strftime('%a %b %d %H:%M %Z')}")

    busy = False
    for g in r.get("gpus") or []:
        busy = busy or (g["util_pct"] or 0) >= 20
        fan = "fan stopped (0%)" if not g["fan_pct"] else f"fan {g['fan_pct']:.0f}%"
        print(f"\nGPU {g['index']}: {g['name']}")
        print(f"- load: {g['util_pct']:.0f}% ({'BUSY' if (g['util_pct'] or 0) >= 20 else 'idle'})")
        print(f"- memory: {gb(g['mem_used_mib'])} of {gb(g['mem_total_mib'])} used "
              f"({100 * g['mem_used_mib'] / g['mem_total_mib']:.0f}%)")
        print(f"- temperature: {g['temp_c']:.0f} C, {fan}, power {g['power_w']:.0f} W of {g['power_limit_w']:.0f} W")

    procs = r.get("processes") or []
    print("\nPrograms using the GPU (largest first):")
    if not procs:
        print("- none")
    for p in procs:
        note = ""
        if p["name"] in ("llama-server", "ollama"):
            note = " <- Ollama running an AI model"
        elif p["name"] in DESKTOP:
            note = " (normal desktop app)"
        print(f"- {p['name']} (pid {p['pid']}, user {p['user']}): {p['mem_mib']} MiB{note}")

    loaded = r.get("ollama_loaded")
    print("\nAI models Ollama has loaded now:")
    if loaded is None:
        print("- unknown (Ollama did not answer)")
    elif not loaded:
        print("- none (nothing loaded, Ollama is not holding GPU memory)")
    else:
        for m in loaded:
            left = ""
            if m.get("expires_at"):
                try:
                    s = int((datetime.fromisoformat(m["expires_at"]) - now).total_seconds())
                    left = f", unloads in {max(0, s) // 60} min {max(0, s) % 60} s if unused"
                except ValueError:
                    pass
            print(f"- {m['name']}: {gb(m['vram_mib'])} on GPU ({m['gpu_pct']}% GPU), "
                  f"context {m['context']}{left}")

    calls = r.get("ollama_calls")
    print(f"\nWho sent requests to Ollama in the last {r['history_hours']} h:")
    if calls is None:
        print("- unknown (could not read the Ollama log)")
    elif not calls:
        print("- nobody")
    else:
        for c in calls:
            ok = c["requests"] - c["failed"]
            if ok == 0:
                res = f"{c['requests']} requests, ALL FAILED (never reached the GPU)"
            elif c["failed"]:
                res = f"{c['requests']} requests, {c['failed']} failed"
            else:
                res = f"{c['requests']} requests"
            print(f"- {who(c['ip'])}: {res}, last {clock(c['last'])} ({ago(c['last'], now)})")

    print("\nVerdict:")
    ai = [p for p in procs if p["name"] in ("llama-server", "ollama")]
    other = [p for p in procs if p["name"] not in DESKTOP and p not in ai and p["mem_mib"] >= 300]
    good = [c for c in (calls or []) if c["requests"] > c["failed"]]
    if ai or loaded:
        names = ", ".join(m["name"] for m in loaded or []) or "a model"
        line = f"- Ollama is the main GPU user ({names} loaded)."
        if good:
            line += f" Most recent caller: {who(good[0]['ip'])}, {ago(good[0]['last'], now)}."
        print(line)
    elif other:
        print("- Main GPU user: " + ", ".join(f"{p['name']} ({p['mem_mib']} MiB)" for p in other) + ".")
    else:
        print("- Nothing heavy is on the GPU right now, only normal desktop apps.")
        if good:
            print(f"- The last AI model use was by {who(good[0]['ip'])}, {ago(good[0]['last'], now)}; "
                  "a model stays loaded for a few minutes after each request, then unloads.")
    if busy:
        print("- The GPU is working right now, so fans spinning is expected.")
    else:
        print("- The GPU is idle right now; fans slow down within a minute or two after work ends.")
    for e in r.get("errors") or []:
        print(f"- WARNING, part of the report failed: {e}")


if __name__ == "__main__":
    main()
