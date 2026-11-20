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

## 6. vbox-manage

VirtualBox runs on the host PC, so the host exposes one helper that only this skill can call.

On the host PC:

```bash
install -m 755 skills/vbox-manage/host/novee-vbox ~/.local/bin/novee-vbox
# one line in ~/.ssh/authorized_keys, with the VM's address and the public key from the VM:
restrict,from="VM_ADDRESS",command="/home/USER/.local/bin/novee-vbox" ssh-ed25519 AAAA... novee-vbox-manage
# optional: VMs the helper must never stop or delete (default: novee, umbrel)
printf 'novee\numbrel\n' > ~/.config/novee/vbox-protected.txt
```

The helper needs `VBoxManage`, `xorriso` (unattended Ubuntu installs) and `openssl`. ISOs it downloads go to `~/VirtualBox VMs/iso/`. New VMs get the host's `~/.ssh/id_ed25519.pub` and the VM's `~/.ssh/novee-vms.pub` preinstalled, and `ssh-setup` adds a `Host NAME` entry to the host's `~/.ssh/config`.

On the VM: `ssh-keygen -t ed25519 -N "" -f ~/.ssh/vbox-manage`, add the host to `~/.ssh/known_hosts`, and copy `examples/vbox-manage.example.json` to `~/.config/novee/vbox-manage.json`. The script creates `~/.ssh/novee-vms` on first use for logging in to the VMs it creates.

## 7. Exec approvals

`config/exec-approvals.json` is the policy that lets Novee run skill scripts and read-only commands freely and asks the owner for everything else. Apply it with `openclaw approvals set --file config/exec-approvals.json`. It is not applied automatically by a pull, on purpose: a change to the repository must not be able to widen what the agent may run.

## 7. disk-health

Same pattern as gpu-stats: the drives are on the host PC, which offers one read-only report command.

On the host PC:

```bash
install -m 755 skills/disk-health/host/novee-disk-report ~/.local/bin/novee-disk-report
# one line in ~/.ssh/authorized_keys, with the VM's address and the public key from the VM:
restrict,from="VM_ADDRESS",command="/home/USER/.local/bin/novee-disk-report" ssh-ed25519 AAAA... novee-disk-health
```

The report runs `smartctl`, `tune2fs -l` and `lsusb -v` through `sudo -n`, so the host user needs passwordless sudo for those (or for everything, as on Novee's host). Without it the report still works but shows SMART and ext4 details as unavailable. The script accepts only `--hours N`, `--selftest short[:sdX]` and `--read-sample`; anything else is rejected, so the key cannot be used for anything but the report.

On the VM: `ssh-keygen -t ed25519 -N "" -f ~/.ssh/disk-health`, make sure the host's key is in `~/.ssh/known_hosts`, and copy `examples/disk-health.example.json` to `~/.config/novee/disk-health.json` with the real address. On the host PC itself `scripts/diskhealth.py` runs without a config and uses the local `~/.local/bin/novee-disk-report`.

## 8. receipts

A Go program, built on the VM. It needs Go 1.23 or newer and a Chrome or Chromium it can start.

```bash
sudo apt install golang-go xauth          # xauth is for the one-time sign-in window
# a browser: Google Chrome's .deb is the simplest. Ubuntu's chromium is a snap, which may refuse a
# profile in a hidden folder; if so set NOVEE_BROWSER_DIR=$HOME/novee-browser for login and sync.
cd ~/.openclaw/workspace/skills/receipts
go test ./... && go build -o ~/.local/bin/novee-receipts ./cmd/novee-receipts
```

Rebuild by hand after a pull that changes the skill; like the exec approvals, a pull never changes what runs. Then apply `config/exec-approvals.json` again (section 7) so Novee may run `novee-receipts status|search|show|pdf|sync`.

Sign in once, by hand, from a desktop on the LAN. The window is the VM's browser shown on your screen; the password goes from your keyboard to the shop and is not stored by Novee.

```bash
ssh -X VM_ADDRESS novee-receipts login costco     # sign in, tick "keep me signed in", wait for "Signed in."
ssh VM_ADDRESS novee-receipts sync costco         # first run fetches about three years
```

Weekly fetch:

```bash
cp config/systemd/novee-receipts.* ~/.config/systemd/user/
systemctl --user daemon-reload && systemctl --user enable --now novee-receipts.timer
```

When the shop session expires the timer run reports `SIGN-IN NEEDED`, Novee messages the owner once, and `login` has to be repeated. If the shop serves the unattended (headless) browser a blank page while the window from `login` works, run the sync with a virtual screen instead: `xvfb-run novee-receipts sync costco --headed`, and put the same in the unit's `ExecStart`.

Data: `~/.local/share/novee/receipts/` (one JSON and one PDF per receipt, `state.json`, `exports/`) and `~/.local/share/novee/browser/` (the signed-in profile; treat it like a password). Both are outside the repository. Settings: `NOVEE_RECEIPTS_DIR`, `NOVEE_BROWSER_DIR`, `NOVEE_CHROMIUM` (browser binary), `NOVEE_COSTCO_WAREHOUSE` (home warehouse number, used for the online order list).

Costco has no public API. The connector uses the same order service as the "Orders & Purchases" page, with query shapes taken from public community scripts. If Costco changes it, `sync` says what was rejected; without a browser, a reply saved from the page can be loaded with `novee-receipts import costco FILE`.

## Tests

```bash
python3 skills/repo-sync/tests/test_reposync.py
python3 skills/vbox-manage/tests/test_novee_vbox.py
(cd skills/receipts && go test ./...)
```

The second file checks the host helper's argument handling, the `--yes` and protected-VM guards and the SSH config entries against a fake VirtualBox. The first runs every sync case (pull, push, merge, conflict, privacy block, failed push) against a throwaway local remote. The Go tests cover the receipt parser, the search and the store, and drive a real browser against a fake shop (sign-in missing, sign-in present, a field the shop rejects, expiry); that last test is skipped when no browser is installed.
