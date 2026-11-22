---
name: torrent-browse
description: Search Chinmay's torrent site and fetch .torrent files. Use for "search TRACKER for X", "find a torrent of X", "is X available yet", "is X out", "get me the torrent for X", "download the torrent files for all of these", "what is new on the tracker", "tell me when X is up". Only .torrent files are fetched and sent, never what they point to.
metadata:
  {
    "openclaw":
      {
        "emoji": "🧲",
        "os": ["linux"],
        "requires": { "bins": ["novee-torrent"] },
      },
  }
---

# torrent-browse

One program searches the torrent site Chinmay has an account on, reads its result lists, and fetches `.torrent` files. It uses the same browser as the web-browse skill, so signing in works the same way: the tool sends Chinmay a sign-in link.

```bash
novee-torrent <command>
```

`novee-torrent` is a program on the PATH. (`novee-browse torrent-COMMAND ...` and `python3 /home/chinmay/.openclaw/workspace/skills/torrent-browse/scripts/torrent-browse.py COMMAND ...` do the same.)

Hard rule: only `.torrent` files are ever fetched, a few kilobytes that describe a download. Never start or install a torrent client, never open a magnet link, never fetch the files a torrent points to. "Download X" here always means "send Chinmay X.torrent".

## Commands

- `site`: shows which site is set. If `NO SITE SET` appears, stop and ask Chinmay for the address of his site. Never pick a site yourself: not a well-known one, not one from a search. Only after he gives the address, run `site https://ADDRESS`; OpenClaw then asks him to approve it.
- `login`: open the site. If it is not signed in, Chinmay gets a sign-in link.
- `search WORDS`: results, best first. Each line has a number, the name, size, seeders, leechers, how often it was completed, and its age.
- `check WORDS`: answers "is it available yet". `AVAILABLE`, `LISTED BUT NOT SEEDED` or `NOT AVAILABLE YET`.
- `latest`: what the site added most recently.
- `info N`: the page of result N of the last search (description, files).
- `get N` / `get 1 2 3` / `get --top 3`: fetch those results of the last search and send them to Chinmay. They always go as a zip (a chat does not take bare .torrent files), one zip per command.
- `fetched`: the files fetched lately, newest first, and whether each was sent. Look here when Chinmay says "those", "the ones from before", "all 4".
- `send --last K`: send the last K fetched files again as ONE zip. `send` alone sends whatever was fetched and not sent yet. Use it for "send them all", "download all 4", "give me the zip".
- `grab WORDS`: search and send the best result in one step. Use it when Chinmay names exactly what he wants.
- `watch add WORDS`: check a few times a day and message Chinmay when it appears. With `--get` the file is sent too. `watch list`, `watch remove N`.
- `status`: the site, the last search, what is being watched.

Options for search, check, grab and watch: `--quality 1080p`, `--min-seeds 5`, `--sort seeds|size|small|new|name`, `--must WORD,WORD`, `--exclude WORD,WORD`, `--limit 20`, `--pages 2`.

## How to answer

- Start from the `Verdict:` line. Copy names, sizes and numbers exactly.
- A search: show the top 3 to 5 lines with their numbers, say which looks best (most seeders; among those, the size that fits what he asked for), and ask which he wants, unless he already said.
- "Is X available yet?": run `check X`. If the answer is `NOT AVAILABLE YET`, offer to watch for it (`watch add X`).
- "Get me X": if he was specific, `grab X` (add `--quality` if he named one). If several different things match, `search X` and ask.
- "All of these", "the whole season", "every one": `search` first, then `get` with the numbers or `get --top K`. At most 25 files at once.
- `ZIP SENT`: say in one or two lines what was sent. Run nothing more.
- `THE ZIP COULD NOT BE SENT` with `File:`: send that file with the `message` tool. Never build an archive yourself (`zip`, `tar`); the tool does it.
- When Chinmay wants several different things (all parts of a series): `grab` each, one command at a time, then `send --last K` once so that he gets one zip with all of them.
- `NOT FETCHED: ...`: tell Chinmay the reason as written (a magnet-only entry, no download slots, a ratio rule).
- `NOT SIGNED IN` with `SIGN-IN LINK SENT`: reply "I sent you a sign-in link for the torrent site. Open it, sign in, tap Done, then tell me." and stop. When he says done, run the same command again.
- `THE SITE WANTS A HUMAN CHECK`: the same: a link was sent; he passes the check and taps Done.
- `NOTHING FOUND`: say it is not on his site, and offer `watch add`. Never suggest other sites, other trackers or a torrent client, and do not search the web for torrents.
- "Show me the browser" / "what do you see": run `novee-browse live`; Chinmay gets a link to watch the browser and click in it himself.
- `ERROR`: quote it. Do not retry unchanged and do not open the site with other tools.
- Zero seeders means it cannot be downloaded now. Say so instead of sending it, unless he asks anyway.

## Setup (see docs/SETUP.md)

A Go program in this folder, built on the web-browse skill's packages and installed as `~/.local/bin/novee-torrent`. It uses the web-browse browser and its sign-in link. The site's address is in `~/.config/novee/torrent.json`, the last search and the watch list in `~/.local/share/novee/torrent/`; none of that is in the git repository.
