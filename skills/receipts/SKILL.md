---
name: receipts
description: Chinmay's shop receipts (Costco now, other shops later) kept in a private archive on this VM. Use for "did I ever buy X", "when did I buy X", "how much did I pay for X", "have I bought item 1234567", "what did I spend at Costco", "how far back do my receipts go", "send me my Costco receipts as a PDF", "download / update my receipts".
metadata:
  {
    "openclaw":
      {
        "emoji": "🧾",
        "os": ["linux"],
        "requires": { "bins": ["novee-receipts"] },
      },
  }
---

# receipts

Every receipt the shop's website offers is stored on this VM as data plus a PDF. One command answers questions about them. Never open a shop website yourself, never ask Chinmay for a password, and never answer a purchase question from memory.

```bash
novee-receipts <command>
```

## Commands

- `search WORDS [--price LO-HI] [--item N] [--from YYYY-MM-DD] [--to YYYY-MM-DD] [--year YYYY] [--store costco]`: did Chinmay buy it, when, for how much. Instant, offline.
- `status`: what is stored: how many receipts, first and last date, net spend, last sync.
- `show ID`: one receipt in full. The ID is in square brackets in a search result.
- `pdf [--year YYYY | --from DATE --to DATE] [--store costco]`: writes one PDF with every receipt in the range and prints its path after `File:`. Send that file to Chinmay with the `message` tool.
- `sync`: fetch new receipts now. Takes up to a few minutes. A weekly timer already does this, so run it only when Chinmay asks for fresh receipts.

## How to search

- Put the product words in plainly: `search 3m command hanging strips`. The script already handles abbreviated receipt names (CMND = command, STRP = strips), partial matches and typos. Do not run many searches with fragments of the same words.
- If Chinmay gives a price or a range, add `--price 12.34` or `--price 10-15`. If he gives an item number, use `--item 1234567`.
- If he gives a product link, the item number is usually not in the link. Search by the product's name.

## How to answer

- Start from the `Verdict:` line and copy dates, prices and item numbers exactly.
- `FOUND`: give the date, the shop, the price, and the coupon and price paid if shown.
- `NOT FOUND` with `Partial matches`: say it was not found under that name, then list the top partial matches as "possibly this", because receipt names are abbreviated. Let Chinmay decide.
- `NOT FOUND` with no partial matches: say no, and say the date range that was searched (it is in the Verdict). Never say "you never bought it"; say "not in the receipts from DATE to DATE".
- `CANNOT ANSWER`: nothing is stored for that range. Say so; do not say "not found".
- Online orders show "line price not reported": give the order total and say the per-item price is not in the data.
- `SIGN-IN NEEDED` (from `sync` or in `status`): the shop session expired. Tell Chinmay he needs to run `novee-receipts login costco` himself from his PC. You cannot do this for him. Stored receipts can still be searched.
- `SYNC PARTIAL` or `SYNC FAILED` or `ERROR`: quote the line. Do not retry more than once.

## What it does not do

It does not buy, change or cancel anything; it only reads the order history. It knows nothing older than the first stored date (Costco's site offers about three years) and nothing from shops that are not listed by `status`.

## Setup (see docs/SETUP.md)

A Go program in this folder, installed as `~/.local/bin/novee-receipts`. Receipts live in `~/.local/share/novee/receipts/`, the browser profile with the sign-in in `~/.local/share/novee/browser/`; neither is in the git repository. `login` is run by Chinmay, by hand, once per shop and again when the session expires.
