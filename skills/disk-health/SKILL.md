---
name: disk-health
description: Health of the hard drives, SSDs and USB hubs attached to Chinmay's PC (the host PC, whatever hostname Chinmay uses for it). SMART, bad sectors, filesystem state, mounts, USB hub chain and power, kernel I/O errors, with a verdict per drive and per hub. Read-only. Use for "how are my hard drives", "check the disks", "is a drive failing", "disk health", "SMART status", "are the external drives mounted", "why did the drive go read-only", "is the USB hub starving the drives of power", "hub power problems", "which drive has bad sectors", "what happened to the Elements/Seagate/HP drive", "I/O errors", "run a disk self-test".
metadata:
  {
    "openclaw":
      {
        "emoji": "💽",
        "os": ["linux"],
        "requires": { "bins": ["python3", "ssh"] },
      },
  }
---

# disk-health

The drives are attached to Chinmay's PC, called the host PC here, not to this VM. Chinmay may call that machine by its hostname, "my PC", "the desktop", "the server" or "the host"; every drive question is about it. TOOLS.md has its hostname and the drive names.

This script connects to the host PC by itself (its own key, which can run nothing but the report) and prints the full diagnosis. Run it with `exec`, read the output, then answer.

```bash
python3 {baseDir}/scripts/diskhealth.py
```

Rules:
- Run it immediately, in the first turn, for any drive, disk, SMART, mount, read-only or USB hub question. Do not ask first.
- It is READ-ONLY. It never writes to a disk, runs fsck, formats, unmounts, remounts, rebinds a driver or power-cycles a port. Say so if Chinmay worries about disruption.
- Never run `ssh`, `smartctl`, `fsck`, `mount`, `umount`, `dd`, `mkfs`, `uhubctl` or `hdparm` yourself, on this VM or anywhere else. Never ask for a password or key. Never tell Chinmay to run a command when the script can answer.
- The two test options below spin drives up and add load. Use them ONLY when Chinmay explicitly asks for a test, a self-test, a stress test, or a read speed check. A health question is not a request for a test.
- Repairs are never yours to start. When the report suggests fsck, remounting, moving a drive or replacing a hub, present it as a suggestion and let Chinmay decide. If Chinmay then asks for a repair, say that this skill cannot do it and that it must be done on the host PC by hand.

## Options

- No options: the complete report, kernel log window 24 hours.
- `--hours N`: how far back to read the kernel log (1 to 720). Use `--hours 168` for "this week", `--hours 720` for "this month".
- `--drive NAME`: only one drive, matched against its name, label, model or mount path (for example `--drive Elements`, `--drive sdc`). Verdicts for the others are still computed, just not printed.
- `--brief`: without the kernel log sample lines.
- `--selftest short` or `--selftest short:sdX`: start a SMART short self-test (about 2 minutes, non-destructive, the drive stays usable). The result appears under "last self-test" in a report run a few minutes later. Explicit request only.
- `--read-sample`: read 1 MiB at four places on every awake disk and time it. Shows whether a drive answers at all and how fast. Explicit request only.
- `--json`: raw data, only if Chinmay asks for raw output.

## Reading the output

- "Verdict per drive" is the summary. Statuses: `healthy`; `FAILING media` (bad sectors, failed self-test, medium errors in the log: the disk itself is dying); `link or power trouble (disk itself looks fine)` (USB resets, CRC errors, timeouts, "not ready": cable, hub or power, not the platters); `filesystem forced read-only`; `asleep (not woken)` (spun down and deliberately left alone); `unknown`.
- Each drive block: SMART numbers, the USB path from the PC through every hub to the drive, the hubs' power facts, each partition's mount state and ext4 error count, kernel-log counts for the window, and "why" lines that justify the verdict.
- "USB hubs": one line per external hub with the drives on it and a status. `SHARED HUB PROBLEM likely` means two or more different drives on the same hub show link or power errors, which points at the hub or its power adapter rather than at the drives.
- "Configured drives (fstab)": drives the PC expects but cannot see, or sees but has not mounted.
- "Suggestions": what Chinmay could do. They are suggestions; nothing was done.
- A line starting with `ERROR` means the report could not be produced. Quote it and stop.

## Answering

1. First sentence: the overall picture from "Verdict per drive" (how many drives, which ones are not healthy).
2. Then one bullet per drive that is not healthy, with its verdict and the one or two strongest "why" facts, copied exactly (sector counts, error counts, hub names).
3. If a hub line says SHARED HUB PROBLEM, say so in its own bullet: that is usually the real cause when several drives misbehave together.
4. End with the suggestions that apply, as suggestions. Remind Chinmay that nothing was changed.
5. Copy numbers, device names and labels exactly from the output. Never invent or round. Never claim a drive is fine if its verdict is not `healthy`.
6. Internal drives (SATA, NVMe) are in the report too. If Chinmay asked about external or USB drives only, mention internal ones in one short line at most.

## What this skill will not do

Format, wipe, fsck, remount, unmount, power-cycle, rebind drivers, change fstab, or stop services. Those change data or interrupt what the host PC is doing and are for Chinmay to do by hand after reading the report.

## Setup (already done on Novee's VM)

Two things that are not in the repository: the config file `~/.config/novee/disk-health.json` (user@address of the host PC, see `examples/disk-health.example.json`) and the SSH key `~/.ssh/disk-health`, which the host PC accepts only for running `host/novee-disk-report`. On the host PC itself the script can also be run directly (`novee-disk-report`, or `scripts/diskhealth.py` without a config, which falls back to the local copy). Details in `docs/SETUP.md`.
