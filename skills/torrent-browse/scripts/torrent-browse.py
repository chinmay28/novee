#!/usr/bin/env python3
"""Run novee-torrent the way other skills are run: `python3 .../scripts/torrent-browse.py COMMAND`.

The tool is a Go program (~/.local/bin/novee-torrent). A small model that has
learned "a skill is a Python script under scripts/" reaches for this path, so
it exists and passes everything on, except setting the site: that goes through
the program itself, where the exec policy asks the owner to approve it.
"""
import os
import shutil
import sys


def main():
    args = sys.argv[1:]
    if args and args[0] == "site" and len(args) > 1:
        print("ERROR: set the site with the program itself, so that Chinmay can approve it: novee-torrent site https://ADDRESS")
        return 1
    exe = shutil.which("novee-torrent") or os.path.expanduser("~/.local/bin/novee-torrent")
    if not os.path.exists(exe):
        print("ERROR: novee-torrent is not installed on this machine (see docs/SETUP.md, torrent-browse).")
        return 1
    os.execv(exe, [exe] + args)


if __name__ == "__main__":
    sys.exit(main())
