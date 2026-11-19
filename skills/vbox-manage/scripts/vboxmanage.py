#!/usr/bin/env python3
"""vbox-manage: create and manage VirtualBox virtual machines on the host PC from this VM.

The VMs run on the host PC, not here. This script sends one command to the host's
`novee-vbox` helper over SSH (a key that can run nothing else) and prints plain text.
Anything that changes state needs --yes, which OpenClaw turns into an approval request.
"""
import argparse, base64, json, os, re, shlex, subprocess, sys, time
from pathlib import Path

CONFIG = Path(os.path.expanduser(os.environ.get("VBOX_MANAGE_CONFIG", "~/.config/novee/vbox-manage.json")))
KEY = Path.home() / ".ssh" / "vbox-manage"
VM_KEY = Path.home() / ".ssh" / "novee-vms"          # key this VM uses to log in to VMs it creates
SSH_CONFIG = Path.home() / ".ssh" / "config"
MEDIA = Path.home() / ".openclaw" / "media" / "vbox"


def load_config():
    try:
        cfg = json.loads(CONFIG.read_text())
    except (OSError, ValueError) as e:
        sys.exit(f"ERROR: cannot read {CONFIG}: {e}\nTell Chinmay the vbox-manage skill is not configured (docs/SETUP.md).")
    if not cfg.get("host"):
        sys.exit(f"ERROR: {CONFIG} has no \"host\" entry (user@address of the host PC).")
    return cfg


def call(argv, timeout=900):
    """Run one helper command on the host; return its JSON (exit on failure)."""
    cfg = load_config()
    cmd = ["ssh", "-i", str(KEY), "-o", "IdentitiesOnly=yes", "-o", "BatchMode=yes", "-o", "ConnectTimeout=10",
           "-o", "StrictHostKeyChecking=yes", cfg["host"], shlex.join(argv)]
    try:
        p = subprocess.run(cmd, capture_output=True, text=True, timeout=timeout)
    except subprocess.TimeoutExpired:
        sys.exit(f"ERROR: the host PC did not answer within {timeout} s.")
    out = p.stdout.strip()
    if not out:
        sys.exit("ERROR: could not reach the host PC over SSH: " + (p.stderr.strip().splitlines() or ["no output"])[-1])
    try:
        d = json.loads(out)
    except ValueError:
        sys.exit("ERROR: unreadable answer from the host: " + out[:300])
    if not d.get("ok"):
        sys.exit("ERROR: " + str(d.get("error")))
    return d


def gb(mb):
    return f"{mb / 1024:.0f} GB" if mb and mb % 1024 == 0 else f"{mb} MB"


def vm_line(v):
    bits = [f"{v['state']}", f"{v['cpus']} CPU", gb(v["memory_mb"]), v["network"]]
    if v.get("ip"):
        bits.append(f"IP {v['ip']}")
    if v.get("phase") and v["phase"] != "running":
        bits.append(f"install: {v['phase']}")
    if v.get("protected"):
        bits.append("protected")
    return f"- {v['name']}: " + ", ".join(bits)


# ---------------- commands ----------------
def do_host(a):
    d = call(["host"])
    print(f"Host PC: VirtualBox {d['virtualbox']}, {d['host_cpus']} CPUs, {gb(d['host_memory_mb'])} RAM "
          f"({gb(d['host_memory_available_mb'])} available now), {d['vm_folder_free_gb']} GB free for VMs")
    print(f"- VMs: {d['vms_total']} total, {d['vms_running']} running, using {d['cpus_assigned_to_running_vms']} CPUs and "
          f"{gb(d['memory_assigned_to_running_vms_mb'])} RAM")
    print(f"- Network for new VMs: bridged on {d['default_bridge']} (gets its own LAN IP). Interfaces: "
          + ", ".join(f"{i['name']} {i['ip']}" for i in d["bridged_interfaces"]))
    print("- Protected VMs (never stopped or deleted by this skill): " + ", ".join(d["protected_vms"]))
    print("- Cached ISOs: " + (", ".join(i["name"] for i in d["isos"]) or "none") + f"  (folder {d['vm_folder']}/iso)")


def do_list(a):
    d = call(["list"])
    if not d["vms"]:
        print("No virtual machines on the host PC."); return
    print(f"{len(d['vms'])} virtual machine(s) on the host PC:")
    for v in d["vms"]:
        print(vm_line(v))


def do_info(a):
    d = call(["info", a.name])
    print(vm_line(d))
    print(f"- OS type {d['ostype']}, firmware {d['firmware']}, MAC {d['mac']}, folder {d['folder']}")
    print(f"- Disks: " + ", ".join(x for x in d["disks"] if x != "emptydrive"))
    sn = d["snapshots"]
    print(f"- Snapshots: " + (", ".join(sn["snapshots"]) + (f" (current: {sn['current']})" if sn["current"] else "") if sn["snapshots"] else "none"))
    if d.get("user"):
        print(f"- Login user: {d['user']}; password is in CREDENTIALS.txt in the VM folder on the host PC")
    if d.get("ssh"):
        print(f"- SSH: {d['ssh']}" + (f"  (from this VM: ssh {a.name})" if ssh_entry(a.name) else ""))
    if d.get("install"):
        i = d["install"]
        print(f"- Install: phase {i.get('phase')}, unattended {i.get('unattended')}, ISO {i.get('iso')}")


def do_ip(a):
    d = call(["ip", a.name])
    print(f"{a.name}: " + (f"IP {d['ip']} (also {d['mdns']})" if d["ip"] else "no IP known (not running, or no mDNS answer yet)"))


def do_iso(a):
    if a.sub == "list":
        d = call(["iso", "list"])
        if not d["isos"]:
            print("No ISO images cached on the host PC. Use `iso search ubuntu` then `iso download URL --yes`."); return
        print("Cached ISO images on the host PC:")
        for i in d["isos"]:
            print(f"- {i['name']}: {i['size_gb']} GB" + (", downloading" if i["downloading"] else "") + (", checksum verified" if i["verified"] else ""))
    elif a.sub == "search":
        argv = ["iso", "search", a.distro or "ubuntu"] + ([a.version] if a.version else [])
        d = call(argv, timeout=180)
        if not d["results"]:
            print(f"No {d['distro']} ISOs found" + (f" for version {a.version}" if a.version else "")); return
        print(f"{d['distro']} ISO images available (newest last):")
        for r in d["results"]:
            tags = [r["kind"]]
            if r.get("lts"): tags.append("LTS")
            if r["unattended_supported"]: tags.append("unattended install supported")
            if r["cached"]: tags.append("already cached")
            print(f"- {r['name']} ({', '.join(tags)}): {r['url']}")
        print(d["note"])
    elif a.sub == "download":
        if not a.url:
            sys.exit("ERROR: iso download needs the URL from `iso search`")
        d = call(["iso", "download", a.url] + (["--name", a.name] if a.name else []) + (["--yes"] if a.yes else []))
        print(f"Download {d['status']}: {d['name']}" + (f", {d['total_gb']} GB" if d.get("total_gb") else "") + f" -> {d['path']}")
        if d.get("hint"): print(d["hint"])
    elif a.sub == "status":
        d = call(["iso", "status"])
        if isinstance(d["downloads"], str):
            print("No ISO downloads in progress."); return
        for i in d["downloads"]:
            pct = f" {i['percent']}%" if i.get("percent") is not None else ""
            print(f"- {i['name']}: {i['status']}{pct}, {i['downloaded_gb']} of {i['total_gb'] or '?'} GB" + (f" ({i['error']})" if i.get("error") else ""))
    elif a.sub == "verify":
        d = call(["iso", "verify", a.name_or_url], timeout=1200)
        print(f"{d['name']}: checksum " + ("OK, verified" if d["verified"] else f"MISMATCH (expected {d['expected'][:12]}..., got {d['actual'][:12]}...)"))


def ensure_vm_key():
    if not VM_KEY.exists():
        subprocess.run(["ssh-keygen", "-t", "ed25519", "-N", "", "-q", "-C", "novee-vms", "-f", str(VM_KEY)], check=True)
    return VM_KEY.with_suffix(".pub").read_text().strip()


def ssh_entry(name):
    return SSH_CONFIG.exists() and re.search(rf"^Host {re.escape(name)}\s*# managed by vbox-manage", SSH_CONFIG.read_text(), re.M)


def write_ssh_entry(name, ip, user, port=None):
    SSH_CONFIG.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
    txt = SSH_CONFIG.read_text() if SSH_CONFIG.exists() else ""
    txt = re.sub(rf"\nHost {re.escape(name)}\s*# managed by vbox-manage\n(?:[ \t]+.*\n)*", "\n", txt)
    txt += f"\nHost {name}  # managed by vbox-manage\n  HostName {ip}\n  User {user}\n  IdentityFile {VM_KEY}\n  IdentitiesOnly yes\n  StrictHostKeyChecking accept-new\n"
    if port and str(port) != "22":
        txt += f"  Port {port}\n"
    SSH_CONFIG.write_text(txt); SSH_CONFIG.chmod(0o600)


def remove_ssh_entry(name):
    if SSH_CONFIG.exists():
        txt = re.sub(rf"\nHost {re.escape(name)}\s*# managed by vbox-manage\n(?:[ \t]+.*\n)*", "\n", SSH_CONFIG.read_text())
        SSH_CONFIG.write_text(txt)


def test_ssh(name):
    p = subprocess.run(["ssh", "-o", "BatchMode=yes", "-o", "ConnectTimeout=8", name, "hostname && uptime -p"], capture_output=True, text=True, timeout=30)
    return p.returncode == 0, (p.stdout.strip() or p.stderr.strip().splitlines()[-1:] or [""])[0] if p.returncode else p.stdout.strip()


def do_ssh_setup(a):
    pub = ensure_vm_key()
    argv = ["ssh-setup", a.name, "--key", pub] + (["--user", a.user] if a.user else []) + (["--password", a.password] if a.password else [])
    d = call(argv, timeout=120)
    write_ssh_entry(a.name, d["ip"], d["user"], d.get("port"))
    ok, out = test_ssh(a.name)
    print(f"{a.name}: {d['user']}@{d['ip']}" + (f" port {d['port']}" if str(d.get('port')) != "22" else ""))
    print(f"- Keys present on the VM: {', '.join(d['keys_present']) or 'already set'}; host PC can log in: {'yes' if d['key_login'] else 'no'}")
    print(f"- From this VM: `ssh {a.name}` " + (f"works ({out})" if ok else f"FAILED: {out}"))
    print(f"- {d['host_ssh_config']}")


def do_create(a):
    pub = ensure_vm_key()
    argv = ["create", a.name, "--cpus", str(a.cpus), "--ram", str(a.ram), "--disk", str(a.disk), "--net", a.net, "--ssh-key", pub]
    if a.bridge: argv += ["--bridge", a.bridge]
    if a.iso: argv += ["--iso", a.iso]
    if a.user: argv += ["--user", a.user]
    if a.password: argv += ["--password", a.password]
    if a.ostype: argv += ["--ostype", a.ostype]
    if a.unattended: argv.append("--unattended")
    if a.start: argv.append("--start")
    if a.yes: argv.append("--yes")
    d = call(argv, timeout=1200)
    v = d["created"]
    print(f"Created VM {v['name']}: {v['cpus']} CPU, {gb(v['memory_mb'])} RAM, {a.disk} GB disk, {v['network']}, user {d['user']}"
          + (f", {d['ssh_keys_installed']} SSH key(s) preinstalled" if d["unattended"] else ""))
    print(f"- Password saved on the host PC in {d['password_file']}")
    print(f"- Next: {d['next']}")
    if d["unattended"] and a.wait:
        wait_install(a.name, a.wait)


def wait_install(name, minutes):
    print(f"Waiting up to {minutes} min for the unattended install ...", flush=True)
    t0 = time.time(); last = None
    while time.time() - t0 < minutes * 60:
        d = call(["install-status", name], timeout=60)
        if d["phase"] != last:
            print(f"- {time.strftime('%H:%M')} phase: {d['phase']} (VM {d['vm_state']})", flush=True); last = d["phase"]
        if d.get("done"):
            print(f"- {name} is installed and running at {d['ip']}")
            class A: pass
            a = A(); a.name = name; a.user = d.get("user"); a.password = None
            do_ssh_setup(a); return
        if d["phase"] in ("install-timeout", "install-aborted"):
            sys.exit(f"ERROR: install {d['phase']}; check `log {name}` and `screenshot {name}`")
        time.sleep(20)
    sys.exit(f"ERROR: install still not finished after {minutes} min; check `install-status {name}` later")


def do_install_status(a):
    if a.wait:
        wait_install(a.name, a.wait); return
    d = call(["install-status", a.name])
    print(f"{a.name}: phase {d['phase'] or 'not an unattended install'}, VM {d['vm_state']}" + (f", IP {d['ip']}" if d.get("ip") else "")
          + (", DONE" if d.get("done") else ""))


def do_power(a):
    d = call([a.cmd, a.name] + (["--yes"] if a.yes else []), timeout=180)
    print(f"{a.name}: {d['result']}" + (f", state now {d['state']}" if d.get("state") else ""))


def do_delete(a):
    d = call(["delete", a.name] + (["--yes"] if a.yes else []), timeout=600)
    remove_ssh_entry(a.name)
    print(f"{a.name}: {d['result']}")


def do_modify(a):
    argv = ["modify", a.name]
    for k in ("cpus", "ram", "net", "bridge"):
        if getattr(a, k): argv += [f"--{k}", str(getattr(a, k))]
    if a.yes: argv.append("--yes")
    d = call(argv)
    print(f"{a.name}: changed " + ", ".join(f"{k}={v}" for k, v in d["changed"].items()))
    print(vm_line(d["now"]))


def do_snapshot(a):
    argv = ["snapshot", a.name, a.sub] + ([a.snap] if a.snap else []) + (["--yes"] if a.yes else [])
    d = call(argv, timeout=900)
    if d.get("result"): print(f"{a.name}: {d['result']}")
    print(f"- Snapshots of {a.name}: " + (", ".join(d["snapshots"]) + (f" (current: {d['current']})" if d.get("current") else "") if d["snapshots"] else "none"))


def do_screenshot(a):
    d = call(["screenshot", a.name], timeout=60)
    MEDIA.mkdir(parents=True, exist_ok=True)
    path = MEDIA / f"{a.name}-{time.strftime('%Y%m%d-%H%M%S')}.png"
    path.write_bytes(base64.b64decode(d["png_base64"]))
    print(f"Console screenshot of {a.name} saved: {path} ({d['bytes']} bytes). Send it with the message tool (--media) if Chinmay wants to see it.")


def do_log(a):
    d = call(["log", a.name, "--lines", str(a.lines)])
    print("\n".join(d["lines"]))


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = ap.add_subparsers(dest="cmd", required=True)
    sub.add_parser("host", help="host PC resources, interfaces, cached ISOs").set_defaults(fn=do_host)
    sub.add_parser("list", help="all VMs").set_defaults(fn=do_list)
    p = sub.add_parser("info", help="details of one VM"); p.add_argument("name"); p.set_defaults(fn=do_info)
    p = sub.add_parser("ip", help="IP address of a VM"); p.add_argument("name"); p.set_defaults(fn=do_ip)
    p = sub.add_parser("iso", help="list / search / download / status / verify ISO images")
    p.add_argument("sub", choices=["list", "search", "download", "status", "verify"])
    p.add_argument("distro", nargs="?", help="search: ubuntu|debian|alpine;  verify: ISO name;  download: URL")
    p.add_argument("version", nargs="?", help="search: version prefix like 26.04")
    p.add_argument("--name"); p.add_argument("--yes", action="store_true"); p.set_defaults(fn=do_iso)
    p = sub.add_parser("create", help="create a VM (add --unattended with an Ubuntu server ISO for a hands-off install)")
    p.add_argument("name"); p.add_argument("--cpus", type=int, default=2); p.add_argument("--ram", type=int, default=2048, help="MB")
    p.add_argument("--disk", type=int, default=20, help="GB"); p.add_argument("--net", choices=["bridged", "nat"], default="bridged")
    p.add_argument("--bridge"); p.add_argument("--iso", help="cached ISO name (see `iso list`)"); p.add_argument("--user")
    p.add_argument("--password", help="default: random, saved on the host PC"); p.add_argument("--ostype")
    p.add_argument("--unattended", action="store_true"); p.add_argument("--start", action="store_true")
    p.add_argument("--wait", type=int, nargs="?", const=25, help="minutes to wait for an unattended install (default 25)")
    p.add_argument("--yes", action="store_true"); p.set_defaults(fn=do_create)
    p = sub.add_parser("install-status", help="progress of an unattended install"); p.add_argument("name")
    p.add_argument("--wait", type=int, nargs="?", const=25); p.set_defaults(fn=do_install_status)
    for c in ("start", "stop", "poweroff", "reset", "pause", "resume"):
        p = sub.add_parser(c, help=f"{c} a VM"); p.add_argument("name"); p.add_argument("--yes", action="store_true"); p.set_defaults(fn=do_power)
    p = sub.add_parser("delete", help="delete a VM and its disks"); p.add_argument("name"); p.add_argument("--yes", action="store_true"); p.set_defaults(fn=do_delete)
    p = sub.add_parser("modify", help="change CPUs, RAM or network (VM must be off)"); p.add_argument("name")
    p.add_argument("--cpus", type=int); p.add_argument("--ram", type=int); p.add_argument("--net", choices=["bridged", "nat"]); p.add_argument("--bridge")
    p.add_argument("--yes", action="store_true"); p.set_defaults(fn=do_modify)
    p = sub.add_parser("snapshot", help="list / take / restore / delete snapshots"); p.add_argument("name")
    p.add_argument("sub", choices=["list", "take", "restore", "delete"]); p.add_argument("snap", nargs="?"); p.add_argument("--yes", action="store_true"); p.set_defaults(fn=do_snapshot)
    p = sub.add_parser("screenshot", help="save a PNG of the VM console"); p.add_argument("name"); p.set_defaults(fn=do_screenshot)
    p = sub.add_parser("log", help="tail of the VM's VBox.log"); p.add_argument("name"); p.add_argument("--lines", type=int, default=40); p.set_defaults(fn=do_log)
    p = sub.add_parser("ssh-setup", help="make `ssh NAME` work from this VM and from the host PC"); p.add_argument("name")
    p.add_argument("--user"); p.add_argument("--password"); p.set_defaults(fn=do_ssh_setup)
    a = ap.parse_args()
    if a.cmd == "iso":
        a.url = a.distro; a.name_or_url = a.distro
    a.fn(a)


if __name__ == "__main__":
    main()
