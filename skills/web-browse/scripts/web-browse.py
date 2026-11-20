#!/usr/bin/env python3
"""Run novee-browse the way every other skill is run: `python3 .../scripts/web-browse.py COMMAND`.

The browser tool is a Go program (~/.local/bin/novee-browse). A small model
that has learned "a skill is a Python script under scripts/" reaches for this
path, so it exists and passes everything on unchanged.
"""
import os
import shutil
import sys


def main():
    args = sys.argv[1:]
    if args and args[0] == "login":
        print("ERROR: `login` is for Chinmay at his PC. To ask him to sign in, run: login-request")
        return 1
    exe = shutil.which("novee-browse") or os.path.expanduser("~/.local/bin/novee-browse")
    if not os.path.exists(exe):
        print("ERROR: novee-browse is not installed on this machine (see docs/SETUP.md, web-browse).")
        return 1
    os.execv(exe, [exe] + args)


if __name__ == "__main__":
    sys.exit(main())
