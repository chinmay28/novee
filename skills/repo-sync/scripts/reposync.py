#!/usr/bin/env python3
"""repo-sync: keep Novee's workspace in step with its GitHub repository.

The workspace is a git clone. The GitHub repository is the source of truth.

  status   what is different between this workspace and GitHub
  pull     fetch GitHub and apply it here (local edits are kept when they merge)
  push     pull, then publish local edits to GitHub (after a privacy check)
  sync     same as push; this is what the 8-hour timer runs
  check    run only the privacy check on local edits
  log      recent history

Conflict rule: when a local edit and GitHub changed the same lines, GitHub
wins. The local version is saved under the state directory and the owner is
notified. Nothing private may be published: the privacy check blocks a push
when a changed file contains a secret, an email address, or a term from the
local private list.
"""
import argparse, fcntl, fnmatch, json, os, re, shutil, subprocess, sys, time
from datetime import datetime
from pathlib import Path

REPO = Path(os.environ.get("NOVEE_SYNC_REPO") or Path(__file__).resolve().parents[3])
STATE = Path(os.environ.get("NOVEE_SYNC_STATE") or Path.home() / ".local/state/novee-sync")
REMOTE = os.environ.get("NOVEE_SYNC_REMOTE", "origin")
BRANCH = os.environ.get("NOVEE_SYNC_BRANCH", "main")
UPSTREAM = f"{REMOTE}/{BRANCH}"
OPENCLAW_JSON = Path(os.environ.get("NOVEE_OPENCLAW_JSON") or Path.home() / ".openclaw/openclaw.json")
PRIVATE_TERMS = Path(os.environ.get("NOVEE_PRIVATE_TERMS") or Path.home() / ".config/novee/private-terms.txt")
MAX_FILE = 2 * 1024 * 1024

out_lines = []


def say(line=""):
    out_lines.append(line)
    print(line, flush=True)


def git(*args, check=False, timeout=120):
    env = dict(os.environ, GIT_TERMINAL_PROMPT="0", LC_ALL="C")
    env.setdefault("GIT_AUTHOR_NAME", "Novee")
    env.setdefault("GIT_AUTHOR_EMAIL", "novee@localhost")
    env.setdefault("GIT_COMMITTER_NAME", "Novee")
    env.setdefault("GIT_COMMITTER_EMAIL", "novee@localhost")
    try:
        p = subprocess.run(["git", "-C", str(REPO), *args], capture_output=True,
                           text=True, timeout=timeout, env=env)
    except subprocess.TimeoutExpired:
        if check:
            raise SyncError(f"git {args[0]} timed out after {timeout} s")
        return 124, "", "timeout"
    if check and p.returncode != 0:
        raise SyncError(f"git {' '.join(args[:2])} failed: {last_line(p.stderr or p.stdout)}")
    return p.returncode, p.stdout, p.stderr


class SyncError(Exception):
    pass


def last_line(text):
    lines = [l for l in text.strip().splitlines() if l.strip()]
    return lines[-1].strip() if lines else "no output"


def rev(name):
    rc, out, _ = git("rev-parse", "--verify", "-q", name)
    return out.strip() if rc == 0 else None


# ---------------------------------------------------------------- state

def load_state():
    try:
        return json.loads((STATE / "last.json").read_text())
    except (OSError, ValueError):
        return {}


def save_state(**kw):
    st = load_state()
    st.update(kw)
    STATE.mkdir(parents=True, exist_ok=True)
    (STATE / "last.json").write_text(json.dumps(st, indent=1))


def log_line(text):
    STATE.mkdir(parents=True, exist_ok=True)
    with open(STATE / "sync.log", "a") as f:
        f.write(f"{datetime.now().astimezone().isoformat(timespec='seconds')} {text}\n")


# ---------------------------------------------------------------- notify

def owner_target():
    try:
        cfg = json.loads(OPENCLAW_JSON.read_text())
        first = (cfg.get("commands", {}).get("ownerAllowFrom") or [None])[0]
    except (OSError, ValueError):
        return None
    if not first or ":" not in str(first):
        return None
    channel, target = str(first).split(":", 1)
    return channel, target


def notify(text, key=None):
    """Message the owner. With a key, the same text is not sent twice in a row."""
    if key:
        st = load_state()
        if st.get("notified", {}).get(key) == text:
            return
        seen = st.get("notified", {})
        seen[key] = text
        save_state(notified=seen)
    custom = os.environ.get("NOVEE_SYNC_NOTIFY_CMD")
    if custom:
        cmd = [custom, text]
    else:
        tgt = owner_target()
        exe = shutil.which("openclaw") or str(Path.home() / ".npm-global/bin/openclaw")
        if not tgt or not os.path.exists(exe):
            log_line("notify skipped (no owner target or no openclaw binary): " + text[:200])
            return
        cmd = [exe, "message", "send", "--channel", tgt[0], "--target", tgt[1], "-m", text]
    try:
        p = subprocess.run(cmd, capture_output=True, text=True, timeout=60)
        log_line(f"notify rc={p.returncode}: {text[:200]!r}")
    except (OSError, subprocess.TimeoutExpired) as e:
        log_line(f"notify failed ({e}): {text[:200]!r}")


def clear_notice(key):
    st = load_state()
    seen = st.get("notified", {})
    if key in seen:
        del seen[key]
        save_state(notified=seen)


# ---------------------------------------------------------------- privacy

GENERIC = [
    ("Telegram bot token", re.compile(r"\b\d{8,10}:[A-Za-z0-9_-]{35}\b")),
    ("API key (sk-...)", re.compile(r"\bsk-[A-Za-z0-9_-]{20,}")),
    ("GitHub token", re.compile(r"\b(gh[pousr]_[A-Za-z0-9]{30,}|github_pat_[A-Za-z0-9_]{30,})")),
    ("AWS access key", re.compile(r"\bAKIA[0-9A-Z]{16}\b")),
    ("Slack token", re.compile(r"\bxox[baprs]-[A-Za-z0-9-]{10,}")),
    ("private key block", re.compile(r"-----BEGIN [A-Z ]*PRIVATE KEY-----")),
    ("bearer token", re.compile(r"(?i)\bbearer\s+[A-Za-z0-9._-]{24,}")),
]
EMAIL = re.compile(r"[A-Za-z0-9._%+-]+@[A-Za-z0-9-]+(?:\.[A-Za-z0-9-]+)*\.[A-Za-z]{2,}")
EMAIL_OK = re.compile(r"(?i)(@example\.(com|org|net)$|noreply|^git@|@localhost$|^user@|@host\.)")
BAD_NAMES = [".env", ".env.*", "*.pem", "*.key", "id_rsa*", "id_ed25519*", ".credentials*",
             "*credentials*.json", "openclaw.json", "*.sqlite", "telegram-notes*"]
SECRET_KEY = re.compile(r"(?i)(token|secret|password|passwd|apikey|api_key|email)")


def local_private_values():
    """Literal strings and regexes that must never be published, read from this machine."""
    literals, regexes = {}, []

    def walk(o, path=""):
        if isinstance(o, dict):
            for k, v in o.items():
                if "@" in k and EMAIL.search(k):
                    literals[EMAIL.search(k).group(0)] = "an email address from the OpenClaw config"
                walk(v, f"{path}.{k}")
        elif isinstance(o, list):
            for v in o:
                walk(v, path)
        elif isinstance(o, (str, int)) and not isinstance(o, bool):
            s = str(o)
            if SECRET_KEY.search(path) and len(s) >= 12 and not s.startswith("ollama"):
                literals[s] = "a secret from the OpenClaw config"
            elif re.search(r"(?i)allowFrom", path):
                ident = s.split(":")[-1]
                if len(ident) >= 6:
                    literals[ident] = "the owner's chat id"
    try:
        walk(json.loads(OPENCLAW_JSON.read_text()))
    except (OSError, ValueError):
        pass
    try:
        for line in PRIVATE_TERMS.read_text().splitlines():
            line = line.strip()
            if not line or line.startswith("#"):
                continue
            if line.startswith("re:"):
                try:
                    regexes.append((line[3:], re.compile(line[3:], re.I)))
                except re.error:
                    pass
            else:
                literals[line] = "a term from the private list"
    except OSError:
        pass
    return literals, regexes


def privacy_scan(paths):
    """Return a list of findings. A finding never contains the private value itself."""
    literals, regexes = local_private_values()
    lowered = [(k.lower(), why) for k, why in literals.items()]
    findings = []
    for rel in paths:
        f = REPO / rel
        name = os.path.basename(rel)
        if any(fnmatch.fnmatch(name, pat) for pat in BAD_NAMES):
            findings.append(f"{rel}: this kind of file must not be published")
            continue
        if not f.is_file() or f.is_symlink():
            continue
        try:
            raw = f.read_bytes()
        except OSError:
            continue
        if len(raw) > MAX_FILE:
            findings.append(f"{rel}: file is larger than 2 MB")
            continue
        if b"\0" in raw[:8000]:
            continue  # binary (images); nothing to read
        for n, line in enumerate(raw.decode("utf-8", "replace").splitlines(), 1):
            for label, rx in GENERIC:
                if rx.search(line):
                    findings.append(f"{rel} line {n}: looks like a {label}")
            for m in EMAIL.finditer(line):
                if not EMAIL_OK.search(m.group(0)):
                    findings.append(f"{rel} line {n}: contains an email address")
            low = line.lower()
            for lit, why in lowered:
                if lit in low:
                    findings.append(f"{rel} line {n}: contains {why}")
            for src, rx in regexes:
                if rx.search(line):
                    findings.append(f"{rel} line {n}: matches a pattern from the private list")
    seen, uniq = set(), []
    for x in findings:
        if x not in seen:
            seen.add(x)
            uniq.append(x)
    return uniq


# ---------------------------------------------------------------- git steps

def ensure_repo():
    if not (REPO / ".git").exists():
        raise SyncError(f"{REPO} is not a git clone. Setup is described in docs/SETUP.md.")
    rc, out, _ = git("remote", "get-url", REMOTE)
    if rc != 0:
        raise SyncError(f"no git remote named {REMOTE} is configured in {REPO}")
    return out.strip()


def fetch():
    rc, _, err = git("fetch", "--prune", "-q", REMOTE, BRANCH, timeout=90)
    if rc != 0:
        raise SyncError("cannot reach GitHub: " + last_line(err))
    if not rev(UPSTREAM):
        raise SyncError(f"GitHub has no branch {BRANCH}")


def normalize():
    """Be on BRANCH with HEAD at a commit GitHub already has; local work stays as edits."""
    rc, cur, _ = git("symbolic-ref", "-q", "--short", "HEAD")
    if rc != 0 or cur.strip() != BRANCH:
        if rev("HEAD") is None:
            git("checkout", "-q", "-f", "-B", BRANCH, UPSTREAM, check=True)
            return
        git("checkout", "-q", "-B", BRANCH, check=True)
    for marker in ("rebase-merge", "rebase-apply", "MERGE_HEAD"):
        if (REPO / ".git" / marker).exists():
            git("rebase", "--abort")
            git("merge", "--abort")
    rc, base, _ = git("merge-base", "HEAD", UPSTREAM)
    if rc != 0:
        raise SyncError("this workspace and GitHub have unrelated histories; see docs/SETUP.md")
    if base.strip() != rev("HEAD"):
        git("reset", "-q", "--mixed", base.strip(), check=True)  # unpublished commits become edits


def local_changes():
    _, out, _ = git("status", "--porcelain", "-z", "--untracked-files=all", check=True)
    res, parts, i = [], out.split("\0"), 0
    while i < len(parts):
        ent = parts[i]
        i += 1
        if len(ent) < 4:
            continue
        code, path = ent[:2], ent[3:]
        if code[0] in "RC":
            i += 1  # skip the old name
        res.append((code, path))
    return res


def backup_dir():
    d = STATE / "conflicts" / datetime.now().strftime("%Y%m%d-%H%M%S")
    d.mkdir(parents=True, exist_ok=True)
    return d


def save_copy(dest, rel, data):
    target = dest / rel
    target.parent.mkdir(parents=True, exist_ok=True)
    target.write_bytes(data)


def integrate():
    """Apply GitHub's state. Returns (updated_files, conflicts, backup_path)."""
    old = rev("HEAD")
    new = rev(UPSTREAM)
    updated, conflicts, bdir = [], [], None
    if old != new:
        _, out, _ = git("diff", "--name-status", old, new)
        updated = [l.replace("\t", " ") for l in out.strip().splitlines() if l]
    changes = local_changes()
    if old == new:
        return updated, conflicts, bdir
    if not changes:
        git("merge", "-q", "--ff-only", UPSTREAM, check=True)
        return updated, conflicts, bdir

    # New local files that GitHub also added under the same name.
    for code, path in changes:
        if code != "??":
            continue
        theirs = rev(f"{UPSTREAM}:{path}")
        if not theirs:
            continue
        _, mine, _ = git("hash-object", path)
        if mine.strip() != theirs:
            bdir = bdir or backup_dir()
            save_copy(bdir, path, (REPO / path).read_bytes())
            conflicts.append(path)
        (REPO / path).unlink()

    if not local_changes():
        git("merge", "-q", "--ff-only", UPSTREAM, check=True)
        return updated, conflicts, bdir

    git("stash", "push", "-q", "-u", "-m", "novee-sync", check=True)
    try:
        git("merge", "-q", "--ff-only", UPSTREAM, check=True)
    except SyncError:
        git("stash", "pop", "-q")
        raise
    rc, _, err = git("stash", "pop", "-q")
    if rc == 0:
        git("reset", "-q")
        return updated, conflicts, bdir

    bdir = bdir or backup_dir()
    rc2, patch, _ = git("stash", "show", "-p", "--include-untracked", "stash@{0}")
    if rc2 != 0:
        _, patch, _ = git("stash", "show", "-p", "stash@{0}")
    (bdir / "local-changes.patch").write_text(patch)
    _, um, _ = git("diff", "--name-only", "--diff-filter=U", "-z")
    unmerged = [p for p in um.split("\0") if p]
    if unmerged:
        for path in unmerged:
            p = subprocess.run(["git", "-C", str(REPO), "show", f"stash@{{0}}:{path}"],
                               capture_output=True)
            if p.returncode == 0:
                save_copy(bdir, path, p.stdout)
            if rev(f"HEAD:{path}"):
                git("checkout", "-q", "HEAD", "--", path, check=True)
            else:
                git("rm", "-q", "-f", "--cached", "--", path)
                try:
                    (REPO / path).unlink()
                except OSError:
                    pass
            conflicts.append(path)
        git("reset", "-q")
    else:
        # The edits could not be re-applied at all: GitHub wins for everything.
        _, names, _ = git("diff", "--name-only", "-z", "HEAD", "stash@{0}")
        _, extra, _ = git("ls-tree", "-r", "--name-only", "-z", "stash@{0}^3")
        for path in [p for p in (names + extra).split("\0") if p]:
            for src in (f"stash@{{0}}:{path}", f"stash@{{0}}^3:{path}"):
                p = subprocess.run(["git", "-C", str(REPO), "show", src], capture_output=True)
                if p.returncode == 0:
                    save_copy(bdir, path, p.stdout)
                    break
            conflicts.append(path)
        git("reset", "-q", "--hard", "HEAD", check=True)
        git("clean", "-q", "-fd")
    git("stash", "drop", "-q")
    return updated, sorted(set(conflicts)), bdir


def publish(message):
    """Commit and push local edits. Returns (status, detail)."""
    changes = local_changes()
    if not changes:
        return "nothing", []
    paths = [p for _, p in changes]
    findings = privacy_scan([p for c, p in changes if "D" not in c])
    if findings:
        return "blocked", findings
    git("add", "-A", check=True)
    if not message:
        names = ", ".join(paths[:4]) + (f" and {len(paths) - 4} more" if len(paths) > 4 else "")
        message = f"novee: update {names}"
    message = re.sub(r"[\r\n]+", " ", message).strip()[:200]
    git("commit", "-q", "-m", message, check=True)
    rc, _, err = git("push", "-q", REMOTE, f"HEAD:{BRANCH}", timeout=90)
    if rc == 0:
        git("update-ref", f"refs/remotes/{UPSTREAM}", "HEAD")
        return "pushed", paths
    git("reset", "-q", "--mixed", "HEAD~1")  # keep the edits, drop the unpublished commit
    reason = last_line(err)
    if re.search(r"(?i)(rejected|non-fast-forward|fetch first)", err):
        return "raced", [reason]
    if re.search(r"(?i)(permission denied|publickey|authentication|could not read|403|denied|read-only)", err):
        return "noaccess", [reason]
    return "failed", [reason]


# ---------------------------------------------------------------- commands

def show_pull(updated, conflicts, bdir, notify_on):
    if updated:
        say(f"Pulled {len(updated)} changed file(s) from GitHub:")
        for u in updated[:25]:
            say(f"- {u}")
        if len(updated) > 25:
            say(f"- ... and {len(updated) - 25} more")
    else:
        say("Already up to date with GitHub.")
    if conflicts:
        say(f"CONFLICT in {len(conflicts)} file(s). GitHub's version won, the local version was replaced:")
        for c in conflicts:
            say(f"- {c}")
        say(f"The replaced local versions are saved in {bdir}")
        if notify_on:
            notify("Novee repo sync: conflict with GitHub. GitHub's version won for: "
                   + ", ".join(conflicts) + f". The local versions are saved on the VM in {bdir}")
            say("Chinmay was notified.")


def run_sync(do_push, message, auto):
    url = ensure_repo()
    fetch()
    normalize()
    before = rev("HEAD")
    updated, conflicts, bdir = integrate()
    show_pull(updated, conflicts, bdir, notify_on=True)
    result = {"pulled": len(updated), "conflicts": conflicts, "push": "not requested"}
    code = 2 if conflicts else 0
    if do_push:
        status, detail = publish(message)
        if status == "raced":  # GitHub moved while we worked: integrate again, once
            fetch()
            u2, c2, b2 = integrate()
            show_pull(u2, c2, b2, notify_on=True)
            status, detail = publish(message)
        result["push"] = status
        if status == "nothing":
            say("Nothing local to push.")
            clear_notice("blocked"); clear_notice("push")
        elif status == "pushed":
            say(f"Pushed {len(detail)} file(s) to GitHub:")
            for d in detail[:25]:
                say(f"- {d}")
            clear_notice("blocked"); clear_notice("push")
        elif status == "blocked":
            code = 2
            say("PUSH BLOCKED by the privacy check. Nothing was published. Problems:")
            for d in detail[:30]:
                say(f"- {d}")
            say("Remove the private content from these files (or ask Chinmay), then push again.")
            if auto:
                notify("Novee repo sync: push blocked by the privacy check, nothing was published. "
                       + "; ".join(detail[:8]), key="blocked")
        else:
            code = 1
            why = {"noaccess": "GitHub refused the push (no write access for Novee's key)",
                   "raced": "GitHub changed again during the push",
                   "failed": "the push failed"}[status]
            say(f"PUSH FAILED: {why}: {detail[0]}")
            say("The local edits are still here and will be pushed on the next sync.")
            if auto:
                notify(f"Novee repo sync: {why}: {detail[0]}", key="push")
    else:
        pending = local_changes()
        if pending:
            say(f"{len(pending)} local edit(s) are not on GitHub yet (use push to publish):")
            for c, p in pending[:15]:
                say(f"- {p}")
    save_state(time=datetime.now().astimezone().isoformat(timespec="seconds"),
               command="sync" if do_push else "pull", head=rev("HEAD"), **result)
    log_line(f"{'sync' if do_push else 'pull'} {before[:7] if before else '-'}->{rev('HEAD')[:7]} {json.dumps(result)}")
    return code


def cmd_status(_):
    url = ensure_repo()
    say(f"Repository: {url} (branch {BRANCH})")
    online = True
    try:
        fetch()
    except SyncError as e:
        online = False
        say(f"- WARNING: {e}; showing the last known state")
    _, head, _ = git("log", "-1", "--format=%h %s (%ad)", "--date=format:%a %b %d %H:%M")
    say(f"- this workspace is at: {head.strip() or 'no commit yet'}")
    if rev("HEAD") and rev(UPSTREAM):
        _, cnt, _ = git("rev-list", "--left-right", "--count", f"HEAD...{UPSTREAM}")
        ahead, behind = (cnt.split() + ["0", "0"])[:2]
        if behind != "0":
            say(f"- GitHub has {behind} newer commit(s) not pulled yet")
        elif online:
            say("- up to date with GitHub")
        if ahead != "0":
            say(f"- {ahead} local commit(s) not on GitHub")
    ch = local_changes()
    if ch:
        say(f"- {len(ch)} local edit(s) not on GitHub:")
        for c, p in ch[:20]:
            say(f"  - {p} ({'new' if c == '??' else 'deleted' if 'D' in c else 'modified'})")
    else:
        say("- no local edits")
    st = load_state()
    if st.get("time"):
        say(f"- last sync: {st['time']} ({st.get('command')}, pulled {st.get('pulled', 0)} file(s), "
            f"push: {st.get('push')}, conflicts: {len(st.get('conflicts') or [])})")
    else:
        say("- last sync: never")
    try:
        p = subprocess.run(["systemctl", "--user", "show", "novee-sync.timer",
                            "-p", "NextElapseUSecMonotonic", "-p", "ActiveState"],
                           capture_output=True, text=True, timeout=10)
        active = "ActiveState=active" in p.stdout
        nxt = subprocess.run(["systemctl", "--user", "list-timers", "novee-sync.timer", "--no-legend",
                              "--no-pager"], capture_output=True, text=True, timeout=10).stdout.split()
        when = " ".join(nxt[:3]) if active and nxt else ""
        say("- automatic sync every 8 hours: " + (f"on, next run {when}" if active else "OFF (timer not active)"))
    except (OSError, subprocess.TimeoutExpired):
        pass
    return 0


def cmd_check(_):
    ensure_repo()
    ch = [p for c, p in local_changes() if "D" not in c]
    if not ch:
        say("No local edits to check.")
        return 0
    findings = privacy_scan(ch)
    if not findings:
        say(f"Privacy check passed for {len(ch)} edited file(s).")
        return 0
    say("Privacy check FAILED. These must be fixed before a push:")
    for f in findings:
        say(f"- {f}")
    return 2


def cmd_log(a):
    ensure_repo()
    _, out, _ = git("log", f"-n{max(1, min(a.n, 50))}", "--format=- %h %ad %an: %s", "--date=format:%b %d %H:%M")
    say(out.strip() or "No commits yet.")
    return 0


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = ap.add_subparsers(dest="cmd", required=True)
    sub.add_parser("status")
    sub.add_parser("pull")
    for name in ("push", "sync"):
        sp = sub.add_parser(name)
        sp.add_argument("-m", "--message", help="commit message describing the change")
        sp.add_argument("--auto", action="store_true",
                        help="unattended run (the timer): also notify the owner when a push is blocked or fails")
    sub.add_parser("check")
    lp = sub.add_parser("log")
    lp.add_argument("-n", type=int, default=10)
    a = ap.parse_args()

    STATE.mkdir(parents=True, exist_ok=True)
    lock = open(STATE / "lock", "w")
    for _ in range(60):
        try:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
            break
        except OSError:
            time.sleep(1)
    else:
        say("ERROR: another sync is still running. Try again in a minute.")
        return 1
    try:
        if a.cmd == "status":
            return cmd_status(a)
        if a.cmd == "check":
            return cmd_check(a)
        if a.cmd == "log":
            return cmd_log(a)
        if a.cmd == "pull":
            return run_sync(False, None, False)
        return run_sync(True, a.message, a.auto)
    except SyncError as e:
        say(f"ERROR: {e}")
        log_line(f"{a.cmd} error: {e}")
        if getattr(a, "auto", False):
            notify(f"Novee repo sync failed: {e}", key="error")
        return 1


if __name__ == "__main__":
    sys.exit(main())
