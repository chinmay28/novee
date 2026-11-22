# History

A short log of how Novee was built. Dates are 2026.

## November 1: the VM

- Built a VirtualBox VM (Ubuntu, 4 vCPU, 8 GB RAM, 50 GB disk, bridged network) from an unattended install image, so the assistant has its own address on the LAN and is isolated from the host.
- Installed OpenClaw with the gateway as a systemd user service. Model: Qwen3.5 9B served by Ollama on the host PC's GPU, with a 170k token context.

## November 2: Telegram, search, headlines

- Connected a Telegram bot, restricted to the owner.
- OpenClaw hides its `web_search` tool completely when no search provider is configured. Added the key-free DuckDuckGo plugin.
- Search engines return news websites, not stories, so wrote the `news-headlines` skill on RSS feeds. It later learned to merge the same story from several outlets.
- Noticed Telegram "getUpdates conflict" errors roughly every minute.

## November 5: fresh install and a workspace for a small model

- Reinstalled OpenClaw from scratch and renamed the agent to Novee.
- Found the cause of the Telegram conflict: a second, forgotten OpenClaw instance on another machine was polling the same bot token. A test with the gateway stopped proved it (a new poll always wins, so earlier "it works" probes had been misleading). Rotating the token fixed it.
- Rewrote the workspace files for a 9B model: a short AGENTS.md that lists only the tools the model really sees, plain-text formatting rules for Telegram, and honesty rules.
- Wrote the `linux-sysadmin` skill: one script, read-only except for `manage`.
- Made the local model the default, with a hosted model as fallback. A session had been pinned to an auto-discovered model tag with no configured context size, which silently cut the context to 32k.

## November 5 and 6: guard rails

- Tests showed the small model ignores prompt-only rules for dangerous commands: asked to "deal with" a busy CPU, it killed one of its own workers with a raw `kill`, and once claimed a success that had not happened.
- Fix: an exec allowlist (see `config/exec-approvals.json`). Only skill scripts and read-only commands run freely; anything else needs the owner's approval in Telegram. `manage ... --yes` always needs approval.
- Lesson: allowlist path patterns are matched against the real path of the executable, so `/usr/bin/python3` did not match `python3.14` until the pattern was widened.
- Long sticky chat sessions degrade a small model's tool choices; fresh sessions and "thinking low" by default fixed a LAN scan question that had failed.
- Turned heartbeats off; an empty HEARTBEAT.md alone did not stop them.

## November 18: GPU questions and this repository

- Wrote the `gpu-stats` skill after the question "my GPU fans are spinning, what is using it". The GPU is in the host PC, so the host exposes one read-only report script through an SSH key that can run nothing else.
- Created this repository, moved the workspace into it, and wrote `repo-sync` with a privacy check and the rule that GitHub wins a conflict.
- Novee got a deploy key for this repository and made its first push from the VM.

## November 18, later: virtual machines

- Wrote the `vbox-manage` skill so Novee can do what was done by hand to create it: find and download an ISO, build an unattended Ubuntu install, pick the network, and set up SSH. The host PC exposes one helper over a forced-command SSH key; everything that changes state needs `--yes` and therefore the owner's approval, and the VMs `novee` and `umbrel` are protected from itself.
- Wrote the `disk-health` skill after a long day of chasing four external drives that kept dropping off a USB hub. The host exposes one read-only report (SMART, ext4 state, mounts, hub chain, port over-current counters, kernel log digest) through a second forced-command key. Lessons baked in: a drive that "cannot spin up" on a hub may be perfectly healthy on a motherboard port; a per-port power-off on a ganged hub does not cut power; and `-n standby` cannot be trusted through a USB bridge that cannot report power mode. Anything that changes a disk stays out of the skill on purpose.

## November 20: receipts

- A hosted assistant had done a job Novee could not: sign in to Costco, save three years of receipts, and answer "did I ever buy this". It drove a browser click by click with a large model, which a 9B model cannot do reliably.
- Wrote the `receipts` skill the other way round: a program does the browsing and the model only asks it questions. The owner signs in by hand once in a browser profile kept on the VM; the program then asks the shop's own order service for the data from inside that signed-in page, so no password is ever stored or typed by the agent.
- First skill in Go: small packages with one contract between them (`receipt`, `store`, `search`, `source`, `costco`, `browser`, `render`). A new shop is one more package that implements `source.Source`.
- The search treats receipt names as what they are, abbreviations: "CMND PICT STRP" answers "command picture strips". It separates lines that match every word from lines that match some, and always states the date range it searched, so "not found" is never mistaken for "never bought".

## November 20, later: a browser of its own

- The receipts skill covers one question well, but the hosted assistant could also wander a signed-in site: open the order page, click a receipt, save it. Wrote `web-browse` for that.
- A 9B model cannot work from screenshots or raw HTML, so the tool turns each page into a title, its text and a numbered list of what can be clicked or typed into, and prints that again after every step. The model's whole job is "read, pick one number".
- Each command is a separate short process, so a small keeper process owns the browser between them and closes it after 20 idle minutes.
- The lesson from November 5 applied again: rules in the prompt are not enough. The tool itself stops before a click that looks like buying, sending, deleting or signing out and runs it only with `--yes`, which the exec policy turns into an approval request to the owner. Password and card fields cannot be typed into at all, home-network addresses cannot be opened, and every page is printed under a line saying its text is not an instruction.

## November 20, night: browser only, and a sign-in request

- First real test, "give me the last Costco receipt from 2025", failed. The receipts program had no command for "the latest receipt", so the model invented flags, and each invention became an approval request on the owner's phone. Nothing was stored anyway, because the sign-in needed a terminal on a desktop and the owner was waiting for the assistant to ask him.
- The owner chose the browser for everything, the way the hosted assistant works. `AGENTS.md` now sends every shop and receipt question to `web-browse`, and the receipts skill is switched off on the VM.
- The missing piece was the sign-in. `novee-browse login-request` starts a small page on the VM that shows the browser and passes on taps and typing, and messages the owner the link directly. He signs in from his phone and taps Done; the model never sees the link, the password or the code, and only has to say "I sent you a sign-in link" and wait.
- Costco's sign-in page stays blank in a windowless browser. The tool now gives each browser a private virtual screen (Xvfb), which the page accepts.
- Two lessons from the first run through the agent. OpenClaw removes every process a command leaves behind, even one in its own session, so the browser died between steps; it is now handed to systemd as a transient unit. And the 9B model, told to run a command to request the sign-in, replied "I sent you a link" without running it, so the tool sends the link itself the moment it sees a password field.
- Testing itself broke the sign-in: after a dozen visits without signing in, Costco served the browser profile a sign-in page whose own script crashes and draws nothing. Clearing cookies did not help; a fresh profile did. `open` now replaces the profile when a sign-in page comes up empty, and never reloads a page while the owner holds a live sign-in link for it.
- With the owner signed in, the real orders page taught three more things. A receipt opens in a pop-up, which the page reader ignored, so a snapshot now shows the pop-up alone when one is open and `pdf` prints only it. Ten buttons were all called "View Receipt", so same-named elements now carry the text of the entry they sit in. And Costco names its periods by the year they end in ("2026 December - February" holds December 2025), which is written down in the skill because no small model would guess it.
- And then the model still could not do it. Signed in, with the steps written out, it took forty turns on the orders page without ever clicking the Warehouse tab, typed the word TEXT from the usage line into a command, and gave up. The clicking for that one page now lives in the tool (`costco-receipt last 2025`): the browser, the sign-in and the page are the same, but the model asks one question and gets the receipt, and the tool sends the PDF. Browsing step by step remains for everything else.
- The owner asked for "consolidated receipt PDFs of all calendar years". Novee fetched receipts one at a time, and since each fetch sends its PDF, the chat filled with single receipts until he stopped it twice. `costco-year-pdf` is the answer to that shape of request: a background job walks every period of both tabs, prints each warehouse pop-up and each online order page without the site around it, joins them by calendar year and sends only those files.

## November 20, night: DoorDash

- The receipt commands were extended to DoorDash orders. The sign-in showed two weaknesses of the phone viewer first: the picture was too short to show the e-mail field, and text arrived as one pasted block where the form wanted key presses. The viewer now shows a tall page and types key by key.
- DoorDash's "Download receipt" is nothing but the browser's print dialog, which froze the browser. The tool now notices a button that only prints and saves the printed page itself, which is the receipt the site meant to hand over.
- The order list has no years in its dates. The tool counts them backwards from today as it scrolls and trusts the order's own page for the exact date.
- Asked for a spending breakdown per month, Novee added up the order list in its head: 115 orders where there were 163, and no credits, because an order paid with DoorDash credits shows $0.00 in the list. Asked to read the credits line of each receipt, it started the yearly PDF job instead. Sums are not a job for a 9B model. `doordash-spending` reads every receipt, does the arithmetic in the tool and sends the finished breakdown.
- The owner wanted the credits checked against gift card redemptions, gift cards kept out of the food figures, and the orders categorized. DoorDash has no redemption history at all, so the report accounts for the credits from the other side: what the receipts say was used and given back, the balance now, and the DoorDash gift cards in the year's Costco orders. A store's category comes from its own page.
- With the Costco purchases in hand the report gained the price of the credits: each DoorDash gift card bought at Costco, what a dollar of credit cost, and a "real cost" per month. It was also laid out for a chat, a heading per month with bullet lines under it, in two messages.

## November 21: Uber Eats

- Uber Eats got the DoorDash treatment. To add it, the DoorDash commands were first turned into shop-neutral ones driven by a short description of a shop: how to list its orders, how to read a receipt, what it calls its credits.
- Three things were new. Uber signs in with a phone number and a code, with no password field to recognise the page by. A receipt is a document in a frame inside a pop-up, which the reader did not see and could not print whole. And Uber Cash is not only gift cards (card benefits fill it too), so unlike DoorDash credits it is reported but not priced.
- Amazon was next and is not coming. During the sign-in it answered "Continued access by an unauthorized AI agent violates Amazon's Conditions of Use". The owner decided to leave it out, and the browser now refuses Amazon's addresses outright so that Novee does not wander in anyway.

## November 21, evening: a torrent browser

- The first skill Novee ever had fetched `.torrent` files from one site with a script of its own. It came back as `torrent-browse`, built on the browser: any site the owner names, signed in through the usual link, with the result table read by its shape instead of by site.
- Asked "is it available yet on my tracker" before any site was set, the 9B model picked a well-known public site by itself and searched it. Setting the site is now the one command that needs the owner's approval.
- It also reached for the tool under three different names in three runs. The names it tries now all work: `novee-browse` hands a command it does not know to a companion program, and the usual script path exists.
- First real site, first real bug. The site answered an address it did not know with its front page, the five newest torrents there were taken for search results, and that address was remembered: every search returned the same five titles. A search address is now kept only if its results contain the words searched for, and a remembered one that stops doing so is dropped. The site's own search box, found on its Torrents page, is tried before any guess; a chat box is never typed into.
- Asked to show the browser, Novee said it could not. `novee-browse live` sends the owner the same page he signs in on, to watch and to steer.
- The files never arrived: OpenClaw sends images, documents and archives from disk but not a bare `.torrent` file, so every fetch ended "saved but not sent" and the model fell back on `ls`, `zip` and `tar`, each one an approval request. Files now always travel as a zip, and `send --last K` sends what was already fetched as one archive. Two fetches the model started at the same moment had also read each other's pages; commands now wait for one another.
- "Part 3 is not available" was wrong: it was on the second page of the results. The tool read one page; it now follows the site's "Next" link through every page (ten at most), whatever the link is called after the word Next.
- One part of a series did not exist in the quality asked for, and the model left it out of the zip. The owner's rule: take the best of the next quality down, as long as he is told. `--quality` now falls back that way with a `NOTE:`, and `parts NAME` picks one torrent per part of a series by that rule.
