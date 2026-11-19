# Setup

How the pieces in this repository are installed on Novee's VM. Replace addresses and names with your own.

## 1. The workspace is the clone

OpenClaw's workspace (`~/.openclaw/workspace`) is a clone of this repository. The `.gitignore` is a whitelist, so the private files that OpenClaw keeps in the same folder (`USER.md`, `TOOLS.md`, `IDENTITY.md`, `memory/`) are never tracked.

```bash
cd ~/.openclaw/workspace
git init -b main 2>/dev/null; git remote add origin https://github.com/chinmay28/novee.git
git fetch origin main && git checkout -f -B main origin/main
cp examples/USER.example.md USER.md          # then edit; same for TOOLS and IDENTITY
```

Reading needs no credentials because the repository is public.

## 2. Push access

Pushing uses a deploy key that is valid for this one repository only.

```bash
ssh-keygen -t ed25519 -N "" -C novee-repo-deploy -f ~/.ssh/novee-repo
git -C ~/.openclaw/workspace remote set-url --push origin git@github.com:chinmay28/novee.git
git -C ~/.openclaw/workspace config core.sshCommand "ssh -i ~/.ssh/novee-repo -o IdentitiesOnly=yes"
```

Add `~/.ssh/novee-repo.pub` on GitHub under Settings, Deploy keys, with "Allow write access". Until then pulls work and pushes report `PUSH FAILED`.

## 3. Privacy list

`repo-sync` blocks secrets and email addresses on its own, and reads tokens and the owner's chat id from `~/.openclaw/openclaw.json` so they can never be pushed. Add your own terms (surname, city, hostnames, LAN prefix) to `~/.config/novee/private-terms.txt`; see `examples/private-terms.example.txt`.

## 4. The 8-hour timer

```bash
mkdir -p ~/.config/systemd/user
cp config/systemd/novee-sync.* ~/.config/systemd/user/
systemctl --user daemon-reload && systemctl --user enable --now novee-sync.timer
loginctl enable-linger "$USER"     # so it runs without a login session
```

Logs: `journalctl --user -u novee-sync` and `~/.local/state/novee-sync/sync.log`. Local versions replaced after a conflict are kept in `~/.local/state/novee-sync/conflicts/`.

## 5. gpu-stats

The GPU is in the host PC, so the host offers one read-only command.

On the host PC:

```bash
install -m 755 skills/gpu-stats/host/novee-gpu-report ~/.local/bin/novee-gpu-report
# one line in ~/.ssh/authorized_keys, with the VM's address and the public key from the VM:
restrict,from="VM_ADDRESS",command="/home/USER/.local/bin/novee-gpu-report" ssh-ed25519 AAAA... novee-gpu-stats
```

On the VM: create the key with `ssh-keygen -t ed25519 -N "" -f ~/.ssh/gpu-stats`, add the host's key to `~/.ssh/known_hosts`, and copy `examples/gpu-stats.example.json` to `~/.config/novee/gpu-stats.json` with the real addresses. The host script is not updated by the sync; reinstall it by hand when it changes.

## 6. Exec approvals

`config/exec-approvals.json` is the policy that lets Novee run skill scripts and read-only commands freely and asks the owner for everything else. Apply it with `openclaw approvals set --file config/exec-approvals.json`. It is not applied automatically by a pull, on purpose: a change to the repository must not be able to widen what the agent may run.

## Tests

```bash
python3 skills/repo-sync/tests/test_reposync.py
```

Runs every sync case (pull, push, merge, conflict, privacy block, failed push) against a throwaway local remote.
