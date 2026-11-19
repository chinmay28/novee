---
name: repo-sync
description: Sync Novee's workspace with its GitHub repository (the source of truth for AGENTS.md, SOUL.md and all skills). Use for "pull the latest from GitHub", "update yourself", "sync your repo", "get the newest skills", "push your changes", "save this to the repo", "commit this skill", "is the repo up to date", "what changed in the repo", "when did you last sync".
metadata:
  {
    "openclaw":
      {
        "emoji": "🔄",
        "os": ["linux"],
        "requires": { "bins": ["python3", "git"] },
      },
  }
---

# repo-sync

This workspace is a git clone of Novee's public GitHub repository. GitHub is the source of truth. One script does all git work; never run `git pull`, `git push`, `git reset` or `git commit` by hand.

```bash
python3 {baseDir}/scripts/reposync.py <command>
```

## Pick the command

- "pull the latest", "update yourself", "get the newest version / skills" → `pull`
- "push your changes", "save this to the repo", "publish the new skill" → `push -m "short description of what changed"`
- "sync" → `push` (it pulls first, then pushes; with nothing to push it only pulls)
- "is the repo up to date", "what is not pushed yet", "when did you last sync", "is the timer running" → `status`
- "what changed recently", "show the history" → `log` (`log -n 20` for more)
- "is it safe to push", "check for private data" → `check`

A systemd timer already runs `sync --auto` every 8 hours, so run these only when asked, or right after you created or changed a skill and Chinmay wants it saved.

## What the output means

- `Pulled N changed file(s)`: GitHub had news and it is applied here. List the files.
- `Already up to date`: nothing new on GitHub.
- `Pushed N file(s)`: local edits are now public on GitHub.
- `Nothing local to push`: there were no local edits.
- `CONFLICT`: a file was edited both here and on GitHub in the same place. GitHub's version won and the local version was saved in the folder the output names. Chinmay is notified automatically; also tell them in your reply which files and where the saved copies are.
- `PUSH BLOCKED by the privacy check`: a changed file contains something private (a token, an email address, an id, a private term). Nothing was published. Report the listed files and lines to Chinmay. Fix it only by removing the private content from that file. Never rename, encode, split or reword private content to get past the check.
- `PUSH FAILED`: GitHub did not accept the push. The edits are kept and retried at the next sync. Quote the reason.
- `ERROR`: quote the line. Do not retry more than once.

## What is published and what is private

The repository is public; anyone can read it.

- Published: `AGENTS.md`, `SOUL.md`, `HEARTBEAT.md`, `README.md`, `HISTORY.md`, and the folders `skills/`, `docs/`, `examples/`, `config/`, `avatars/`.
- Private, never leaves this VM: `USER.md`, `TOOLS.md`, `IDENTITY.md`, `MEMORY.md`, `DREAMS.md`, `memory/`, and everything else.

So: facts about Chinmay, addresses, device names, ids, tokens and passwords belong in the private files. A skill that needs such a value must read it from a file under `~/.config/novee/`, not contain it.

## After a pull

Changed skills and workspace files apply to new chat sessions. If `skills/gpu-stats/host/` changed, tell Chinmay: that script runs on the host PC and must be updated there by hand.
