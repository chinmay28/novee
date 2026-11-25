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

## 8. receipts (optional, off by default)

Since November 20 Novee answers receipt questions with the browser (section 9) and `AGENTS.md` no longer routes anything here. The skill stays in the repository as an offline archive for whoever wants one; on Novee's VM it is switched off with `openclaw config set skills.entries.receipts.enabled false --strict-json`, so the model is not offered two ways to do the same thing.

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

## 9. web-browse

Same toolchain and browser as receipts.

```bash
cd ~/.openclaw/workspace/skills/web-browse
go test ./... && go build -o ~/.local/bin/novee-browse ./cmd/novee-browse
```

Apply `config/exec-approvals.json` again (section 7). It lets Novee run every `novee-browse` command except `login` (it may run `login-request`, which only sends the owner a link), and anything with `--yes` asks the owner in Telegram first.

The browser needs a screen to draw on, even though nobody looks at it: Costco's sign-in page stays blank in a windowless (headless) browser. Install Xvfb (`sudo apt install xvfb`) and the tool starts a private virtual screen with each browser. Without Xvfb, or with `NOVEE_BROWSE_HEADLESS=1`, it falls back to headless.

Signing in: when a page asks for a password, the tool itself (not the model, which tends to say it did without doing it) starts a sign-in request; `novee-browse login-request` sends a fresh one, and at most three are sent unasked per hour. It starts a small web page on the VM (port 7717, or a free one) that shows the browser and passes on taps and typing, and messages the owner the link with `openclaw message send`. The browser and this page are started as transient systemd user units, because OpenClaw removes every process a command leaves behind. The owner opens it on a phone or PC on the home network, signs in, ticks "keep me signed in" and taps Done. The link carries a random token and dies on Done, when the browser closes, or after 15 minutes. What is typed there goes into the browser and is not logged or shown to the model. It travels over plain HTTP inside the home network, so use it on a network you trust; from elsewhere, reach the VM through a VPN and set `NOVEE_BROWSE_VIEW_HOST` to the name or address the link should use.

Costco receipts have commands of their own (`costco-receipt last 2025`, `costco-receipt MM/DD/YYYY`, `costco-receipts 2025`) that click through the signed-in orders page themselves and send the owner the PDF with `openclaw message send --media`. `costco-year-pdf all|YEAR` makes one PDF per calendar year from every warehouse receipt and online order: it runs as a background job (a transient systemd unit, state in `costco-job.json` next to the session file), joins the PDFs with Ghostscript (`sudo apt install ghostscript`) and sends them itself; other browser commands answer `BUSY` until it is done. The single-receipt commands take 20 to 60 seconds, longer than OpenClaw waits for a command by default (10 seconds) before it lets the model go on without the result, so set `openclaw config set tools.exec.backgroundMs 60000 --strict-json`.

DoorDash has the same three commands (`doordash-receipt`, `doordash-receipts`, `doordash-year-pdf`). Its order history is one endless list with dates that carry no year, so the tool scrolls it, counts the years backwards from today and takes the exact date from each order's own page; the receipt is that page printed with the site's own print layout, which is all its "Download receipt" button does. Only one yearly PDF job runs at a time, whichever shop it is for. `doordash-spending YEAR` is a job of the same kind that reads the amounts instead: per month, what was charged plus the "DoorDash Credits" line of each receipt; it sends the breakdown as a message and a CSV with one line per order. It sorts each store into restaurants or grocery and retail by opening the store's page once (kept in `doordash-stores.json`): DoorDash sends grocery, pharmacy and retail stores to an address with `convenience` in it. Gift cards bought through DoorDash are listed apart. DoorDash has no history of redeemed gift cards, so the credits are accounted for instead: used, given back for refunds, the balance on the payment page, and the DoorDash gift cards among the year's Costco online orders (which needs Costco signed in; without it the report says so). What it reads of an order older than three days is kept in `doordash-orders/` next to the session file (the receipt text and PDF), so later reports and yearly PDFs do not open those pages again.

Uber Eats has the same commands with `ubereats-`. Its receipts are a framed document in a pop-up on the past orders page, which the page reader now includes and `pdf` prints whole; "Uber Cash" under Payments is its stored money. Its sign-in asks for a phone number or e-mail and a code, so a page counts as a sign-in page when its address or title says so and it asks for either, password or not. What a shop needs (its list, its receipt, its word for credits) is one entry in `shops` in `doordash.go`; the receipt, yearly PDF and spending commands are shared.

PG&E statements have `pge-bill`, `pge-bills` and `pge-report`. A statement is fetched as the PDF the billing history builds for "View Bill PDF" (taken at the moment the page makes it, since it is shown in a new tab and not downloaded), kept in `pge-bills/` next to the session file, and turned into text with `pdftotext -layout` (`sudo apt install poppler-utils`); the `pge` package reads that text page by page. PG&E's pages are web components, so the page reader looks inside shadow trees for fields, links and buttons.

Two things protect the sign-in. While a link is live, `open` on that site shows the page as it is instead of loading it again, so the model cannot pull the page away while the owner types. And when a sign-in page comes up empty, `open` deletes that site's browser profile and tries once with a fresh one: Costco's bot protection serves a page that draws nothing to a profile that has visited the sign-in page repeatedly without signing in, and a profile sitting on a sign-in page has no sign-in to lose.

The old way still works from a desktop on the LAN (needs `xauth`); close the window when done:

```bash
ssh -X VM_ADDRESS novee-browse login https://www.costco.com/OrderStatusCmd
```

Each site has its own profile, named after the site (`costco`, `amazon`), under `~/.local/share/novee/browser/`; treat that folder like a password. A profile can be open in one browser at a time.

What the guard does and does not do: it stops before clicks and form submissions whose label looks consequential and before typing into password or payment fields, and it refuses addresses on this machine or the home network. It judges by what the page says about an element, so it is a seat belt, not a guarantee; do not sign the browser in to a site where one wrong click would be costly. While a session is open, the browser's control port listens on 127.0.0.1, so any program running as the same user on the VM could drive it.

Settings: `NOVEE_BROWSE_DIR` (session file, screenshots, saved pages; default `~/.local/share/novee/browse`), `NOVEE_BROWSE_IDLE_MINUTES`, `NOVEE_BROWSE_ALLOW_LAN=1`, `NOVEE_BROWSE_HEADLESS=1`, `NOVEE_BROWSE_VIEW_PORT`, `NOVEE_BROWSE_VIEW_HOST`, `NOVEE_BROWSE_NOTIFY_CMD` (a program that gets the sign-in message as its argument, instead of `openclaw message send`).

## 10. torrent-browse

A second Go program on the web-browse packages; build it after web-browse.

```bash
cd ~/.openclaw/workspace/skills/torrent-browse
go test ./... && go build -o ~/.local/bin/novee-torrent ./cmd/novee-torrent
cp ../../config/systemd/novee-torrent-watch.* ~/.config/systemd/user/
systemctl --user daemon-reload && systemctl --user enable --now novee-torrent-watch.timer
```

Apply `config/exec-approvals.json` again (section 7). It lets Novee run every `novee-torrent` command except setting the site: `novee-torrent site https://ADDRESS` asks the owner in Telegram, because a small model left to itself once picked a site of its own. The owner names the site once; the address is kept in `~/.config/novee/torrent.json` on the VM and is not in the repository.

It searches through the web-browse browser, so signing in is the same link as for any site. It reads a site's result table without being told the site: the rows that hold a link to a `.torrent` file, with the columns named from the header (words, single letters or the titles of little pictures); the first search finds the site's search box or tries the usual addresses and remembers what worked. A site with an unusual layout may need its search address put into `torrent.json` by hand (`"search": "/path?query={q}"`).

Only `.torrent` files are fetched: what a site sends is kept only if it is a small bencoded torrent file, magnet links are not followed, and nothing here runs a torrent client. One file is sent as it is, several as one zip, 25 at most at a time. `watch add` keeps a search and the timer repeats it four times a day until it has a seeded result.

Settings: `NOVEE_TORRENT_DIR` (config and data in one folder, for tests), `NOVEE_TORRENT_NOTIFY_CMD`.

## Tests

```bash
python3 skills/repo-sync/tests/test_reposync.py
python3 skills/vbox-manage/tests/test_novee_vbox.py
(cd skills/receipts && go test ./...)
(cd skills/web-browse && go test ./...)
(cd skills/torrent-browse && go test ./...)
```

The second file checks the host helper's argument handling, the `--yes` and protected-VM guards and the SSH config entries against a fake VirtualBox. The first runs every sync case (pull, push, merge, conflict, privacy block, failed push) against a throwaway local remote. The Go tests cover the receipt parser, the search and the store, and drive a real browser against a fake shop (sign-in missing, sign-in present, a field the shop rejects, expiry); that last test is skipped when no browser is installed. The web-browse tests cover the approval rules and run every command against a fake shop in a real browser, checking that an unapproved "Place order" click reaches nothing, and that a sign-in request sends the link to the owner and not to the model, refuses a wrong token, passes typing on to the page and stops working after Done. They run headless; `NOVEE_BROWSE_TEST_VIRTUAL=1 go test ./...` runs them on a virtual screen.
