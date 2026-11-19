#!/usr/bin/env python3
"""End-to-end tests for reposync.py against a throwaway local remote.

Run: python3 skills/repo-sync/tests/test_reposync.py
Nothing outside a temporary directory is touched, and no network is used.
"""
import json, os, subprocess, sys, tempfile
from pathlib import Path

SCRIPT = Path(__file__).resolve().parents[1] / "scripts" / "reposync.py"
GITENV = dict(os.environ, GIT_AUTHOR_NAME="t", GIT_AUTHOR_EMAIL="t@localhost",
              GIT_COMMITTER_NAME="t", GIT_COMMITTER_EMAIL="t@localhost")
fails = []
# Built at run time so this file itself passes the privacy check.
REAL_MAIL = "someone.real" + "@" + "mailbox.test"


def sh(cwd, *cmd):
    p = subprocess.run(cmd, cwd=cwd, capture_output=True, text=True, env=GITENV)
    assert p.returncode == 0, f"{cmd}: {p.stderr}"
    return p.stdout


class World:
    def __init__(self, root):
        self.root = Path(root)
        self.remote = self.root / "remote.git"
        self.ws = self.root / "workspace"      # Novee's clone
        self.other = self.root / "other"       # someone else pushing to GitHub
        self.state = self.root / "state"
        self.notes = self.root / "notify.log"
        sh(root, "git", "init", "-q", "--bare", "-b", "main", str(self.remote))
        sh(root, "git", "clone", "-q", str(self.remote), str(self.other))
        (self.other / "AGENTS.md").write_text("line1\nline2\nline3\nline4\nline5\n")
        (self.other / ".gitignore").write_text("/private/\n")
        self.commit_other("init")
        sh(root, "git", "clone", "-q", str(self.remote), str(self.ws))
        notify = self.root / "notify.sh"
        notify.write_text(f'#!/bin/sh\necho "$1" >> {self.notes}\n')
        notify.chmod(0o755)
        cfg = self.root / "openclaw.json"
        cfg.write_text(json.dumps({"channels": {"telegram": {"botToken": "1234567890:" + "A" * 35}},
                                   "commands": {"ownerAllowFrom": ["telegram:9988776655"]}}))
        terms = self.root / "terms.txt"
        terms.write_text("Secretville\nre:10\\.9\\.8\\.\\d+\n")
        self.env = dict(GITENV, NOVEE_SYNC_REPO=str(self.ws), NOVEE_SYNC_STATE=str(self.state),
                        NOVEE_SYNC_NOTIFY_CMD=str(notify), NOVEE_OPENCLAW_JSON=str(cfg),
                        NOVEE_PRIVATE_TERMS=str(terms))

    def commit_other(self, msg):
        sh(self.other, "git", "add", "-A")
        sh(self.other, "git", "commit", "-q", "-m", msg)
        sh(self.other, "git", "push", "-q", "origin", "HEAD:main")

    def run(self, *args):
        p = subprocess.run([sys.executable, str(SCRIPT), *args], capture_output=True, text=True, env=self.env)
        return p.returncode, p.stdout + p.stderr

    def remote_file(self, path):
        sh(self.other, "git", "pull", "-q", "origin", "main")
        f = self.other / path
        return f.read_text() if f.exists() else None

    def notified(self):
        return self.notes.read_text() if self.notes.exists() else ""


def check(name, cond, detail=""):
    print(("PASS " if cond else "FAIL ") + name)
    if not cond:
        fails.append(name)
        print("     " + detail.replace("\n", "\n     "))


def test(fn):
    with tempfile.TemporaryDirectory() as d:
        fn(World(d))


def t_pull_clean(w):
    (w.other / "SOUL.md").write_text("soul\n")
    w.commit_other("add soul")
    rc, out = w.run("pull")
    check("pull applies a new file from the remote", rc == 0 and (w.ws / "SOUL.md").exists() and "SOUL.md" in out, out)
    rc, out = w.run("pull")
    check("second pull says up to date", rc == 0 and "Already up to date" in out, out)


def t_push(w):
    (w.ws / "skills").mkdir()
    (w.ws / "skills" / "new.md").write_text("a new skill\n")
    (w.ws / "AGENTS.md").write_text("line1\nline2 edited\nline3\nline4\nline5\n")
    rc, out = w.run("push", "-m", "add skill")
    check("push publishes new and modified files", rc == 0 and w.remote_file("skills/new.md") == "a new skill\n"
          and "edited" in w.remote_file("AGENTS.md"), out)
    rc, out = w.run("status")
    check("status is clean after push", "no local edits" in out and "up to date" in out, out)
    rc, out = w.run("push")
    check("push with nothing to do", rc == 0 and "Nothing local to push" in out, out)


def t_merge_no_conflict(w):
    (w.other / "AGENTS.md").write_text("line1 remote\nline2\nline3\nline4\nline5\n")
    w.commit_other("remote edit top")
    (w.ws / "AGENTS.md").write_text("line1\nline2\nline3\nline4\nline5 local\n")
    rc, out = w.run("sync")
    txt = w.remote_file("AGENTS.md")
    check("edits to different lines are merged and pushed", rc == 0 and "line1 remote" in txt and "line5 local" in txt
          and "CONFLICT" not in out, out + str(txt))
    check("no notification without a conflict", w.notified() == "", w.notified())


def t_conflict_repo_wins(w):
    (w.other / "AGENTS.md").write_text("line1\nline2 REMOTE\nline3\nline4\nline5\n")
    w.commit_other("remote edit")
    (w.ws / "AGENTS.md").write_text("line1\nline2 LOCAL\nline3\nline4\nline5\n")
    (w.ws / "notes.md").write_text("unrelated local file\n")
    rc, out = w.run("sync")
    local = (w.ws / "AGENTS.md").read_text()
    check("conflict: the remote version wins locally", "line2 REMOTE" in local and "LOCAL" not in local
          and "<<<<" not in local, out + local)
    check("conflict: reported with exit code 2", rc == 2 and "CONFLICT" in out and "AGENTS.md" in out, out)
    backups = list((w.state / "conflicts").glob("*/AGENTS.md"))
    check("conflict: the local version is backed up", len(backups) == 1 and "line2 LOCAL" in backups[0].read_text(), str(backups))
    check("conflict: the owner is notified", "AGENTS.md" in w.notified() and "conflict" in w.notified(), w.notified())
    check("conflict: unrelated local edits survive and are pushed", w.remote_file("notes.md") == "unrelated local file\n"
          and "line2 REMOTE" in w.remote_file("AGENTS.md"), out)
    rc, out = w.run("status")
    check("conflict: workspace is clean afterwards", "no local edits" in out and "up to date" in out, out)


def t_conflict_on_pull_only(w):
    (w.other / "AGENTS.md").write_text("line1\nline2\nline3 REMOTE\nline4\nline5\n")
    w.commit_other("remote edit")
    (w.ws / "AGENTS.md").write_text("line1\nline2\nline3 LOCAL\nline4\nline5\n")
    rc, out = w.run("pull")
    check("pull-only conflict: remote wins and owner is notified", rc == 2 and "line3 REMOTE" in (w.ws / "AGENTS.md").read_text()
          and "AGENTS.md" in w.notified(), out)


def t_untracked_collision(w):
    (w.other / "TOOLS.md").write_text("remote tools\n")
    w.commit_other("add tools")
    (w.ws / "TOOLS.md").write_text("local tools\n")
    rc, out = w.run("pull")
    check("new local file vs new remote file: remote wins, local backed up",
          (w.ws / "TOOLS.md").read_text() == "remote tools\n" and "CONFLICT" in out
          and any("local tools" in p.read_text() for p in (w.state / "conflicts").glob("*/TOOLS.md")), out)


def t_delete_vs_modify(w):
    sh(w.other, "git", "rm", "-q", "AGENTS.md")
    w.commit_other("remove agents")
    (w.ws / "AGENTS.md").write_text("line1\nline2 LOCAL\nline3\nline4\nline5\n")
    rc, out = w.run("sync")
    check("remote deleted a file that was edited locally: remote wins", not (w.ws / "AGENTS.md").exists()
          and w.remote_file("AGENTS.md") is None and "CONFLICT" in out, out)
    rc, out = w.run("status")
    check("delete/modify: clean afterwards", "no local edits" in out, out)


def t_privacy_block(w):
    cases = {"token.md": "token 1234567890:" + "A" * 35 + "\n",
             "mail.md": "write to " + REAL_MAIL + "\n",
             "id.md": "chat id 9988776655\n",
             "term.md": "lives in secretville\n",
             "ip.md": "server at 10.9.8.7\n",
             "key.md": "-----BEGIN OPENSSH " + "PRIVATE KEY-----\n"}
    for name, body in cases.items():
        (w.ws / name).write_text(body)
        rc, out = w.run("sync", "--auto")
        check(f"privacy check blocks {name}", rc == 2 and "PUSH BLOCKED" in out and name in out
              and w.remote_file(name) is None, out)
        check(f"privacy report for {name} does not repeat the private value", body.split()[-1] not in out or name == "key.md", out)
        (w.ws / name).unlink()
    check("blocked push notifies the owner in --auto mode", "privacy check" in w.notified(), w.notified())
    (w.ws / "ok.md").write_text("mail user@host:path and git@github.com are fine, noreply@example.com too\n")
    rc, out = w.run("sync")
    check("harmless look-alikes pass the privacy check", rc == 0 and w.remote_file("ok.md") is not None, out)
    (w.ws / ".env").write_text("X=1\n")
    rc, out = w.run("check")
    check("check command flags a .env file", rc == 2 and ".env" in out, out)


def t_gitignore_private(w):
    (w.ws / "private").mkdir()
    (w.ws / "private" / "USER.md").write_text(REAL_MAIL + " 9988776655\n")
    (w.ws / "pub.md").write_text("public\n")
    rc, out = w.run("sync")
    check("ignored private files are never pushed and do not block", rc == 0 and w.remote_file("pub.md") == "public\n"
          and w.remote_file("private/USER.md") is None, out)
    (w.other / "AGENTS.md").write_text("changed\n")
    w.commit_other("x")
    w.run("pull")
    check("ignored private files survive a pull", (w.ws / "private" / "USER.md").exists())


def t_push_failure_keeps_edits(w):
    (w.ws / "keep.md").write_text("keep me\n")
    hook = w.remote / "hooks" / "pre-receive"
    hook.write_text("#!/bin/sh\necho 'remote: Permission denied (read-only key)' >&2\nexit 1\n")
    hook.chmod(0o755)
    rc, out = w.run("sync", "--auto")
    check("failed push is reported and edits are kept", rc == 1 and "PUSH FAILED" in out and (w.ws / "keep.md").exists(), out)
    check("failed push notifies once in --auto mode", w.notified().count("Novee repo sync") == 1, w.notified())
    w.run("sync", "--auto")
    check("same failure is not notified twice", w.notified().count("Novee repo sync") == 1, w.notified())
    hook.unlink()
    (w.other / "z.md").write_text("z\n")
    w.commit_other("meanwhile")
    rc, out = w.run("sync", "--auto")
    check("edits are pushed once access works again", rc == 0 and w.remote_file("keep.md") == "keep me\n"
          and (w.ws / "z.md").exists(), out)


def t_local_commits(w):
    (w.ws / "c.md").write_text("committed locally\n")
    sh(w.ws, "git", "add", "-A")
    sh(w.ws, "git", "commit", "-q", "-m", "stray local commit")
    (w.other / "d.md").write_text("d\n")
    w.commit_other("remote d")
    rc, out = w.run("sync")
    check("stray local commits are folded in and pushed", rc == 0 and w.remote_file("c.md") == "committed locally\n"
          and (w.ws / "d.md").exists(), out)


def t_offline(w):
    sh(w.ws, "git", "remote", "set-url", "origin", str(w.root / "gone.git"))
    rc, out = w.run("pull")
    check("unreachable remote gives a clear error", rc == 1 and "ERROR: cannot reach GitHub" in out, out)
    rc, out = w.run("status")
    check("status still works offline", rc == 0 and "WARNING" in out, out)


for t in (t_pull_clean, t_push, t_merge_no_conflict, t_conflict_repo_wins, t_conflict_on_pull_only,
          t_untracked_collision, t_delete_vs_modify, t_privacy_block, t_gitignore_private,
          t_push_failure_keeps_edits, t_local_commits, t_offline):
    test(t)
print(f"\n{'ALL PASSED' if not fails else str(len(fails)) + ' FAILED: ' + ', '.join(fails)}")
sys.exit(1 if fails else 0)
