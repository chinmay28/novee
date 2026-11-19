#!/usr/bin/env python3
"""Tests for the host helper's argument handling and guards. No VirtualBox needed:
VBoxManage calls are replaced by a fake that knows two VMs (novee, umbrel)."""
import json, os, runpy, subprocess, sys, tempfile, unittest
from pathlib import Path

HERE = Path(__file__).resolve().parent
HELPER = HERE.parent / "host" / "novee-vbox"


def load_helper(tmp):
    os.environ["HOME"] = tmp
    mod = runpy.run_path(str(HELPER), run_name="helper")
    fake = {"novee": {"VMState": "running", "cpus": "4", "memory": "8192", "nic1": "bridged", "bridgeadapter1": "wlo1",
                      "macaddress1": "080027000001", "CfgFile": f"{tmp}/VirtualBox VMs/novee/novee.vbox", "ostype": "Ubuntu (64-bit)"},
            "umbrel": {"VMState": "running", "cpus": "24", "memory": "32768", "nic1": "bridged", "bridgeadapter1": "wlo1",
                       "macaddress1": "080027000002", "CfgFile": f"{tmp}/VirtualBox VMs/umbrel/umbrel.vbox", "ostype": "Debian (64-bit)"},
            "worker": {"VMState": "running", "cpus": "2", "memory": "4096", "nic1": "nat", "macaddress1": "080027000004",
                       "CfgFile": f"{tmp}/VirtualBox VMs/worker/worker.vbox", "ostype": "Ubuntu (64-bit)"},
            "lab": {"VMState": "poweroff", "cpus": "2", "memory": "2048", "nic1": "bridged", "bridgeadapter1": "enp0",
                    "macaddress1": "080027000003", "CfgFile": f"{tmp}/VirtualBox VMs/lab/lab.vbox", "ostype": "Ubuntu (64-bit)"}}
    calls = []

    def vbm(*args, **kw):
        calls.append(args)
        if args[0] == "list" and args[1] == "vms":
            return "".join(f'"{n}" {{uuid-{n}}}\n' for n in fake)
        if args[0] == "showvminfo":
            return "".join(f'{k}="{v}"\n' for k, v in fake[args[1]].items())
        if args[0] == "snapshot":
            return ""
        if args[0] == "controlvm" and args[2] == "poweroff":
            fake[args[1]]["VMState"] = "poweroff"
        return ""
    g = mod["main"].__globals__          # run_path returns a copy; patch the namespace the functions use
    g["vbm"] = vbm
    g["run"] = lambda cmd, **kw: ""      # getent / ip neigh: no answers
    g["VM_ROOT"] = Path(tmp) / "VirtualBox VMs"
    g["ISO_DIRS"] = [Path(tmp) / "iso"]
    g["PROTECTED_FILE"] = Path(tmp) / "nonexistent-protected.txt"
    g["SSH_CONFIG"] = Path(tmp) / ".ssh" / "config"
    return g, calls


class HelperTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.mkdtemp()
        self.mod, self.calls = load_helper(self.tmp)
        self.Args, self.Fail = self.mod["Args"], self.mod["Fail"]

    def dispatch(self, line):
        """Run the helper's dispatcher on one command line and return the JSON it prints."""
        import io, contextlib, shlex
        os.environ["SSH_ORIGINAL_COMMAND"] = line
        buf = io.StringIO()
        with contextlib.redirect_stdout(buf):
            try:
                self.mod["main"]()
            except SystemExit:
                pass
        return json.loads(buf.getvalue())

    def test_args_parsing(self):
        a = self.Args(["create", "lab2", "--cpus", "2", "--ssh-key", "k1", "--ssh-key", "k2", "--unattended", "--yes"])
        self.assertEqual(a.cmd, "create"); self.assertEqual(a.get_name(), "lab2")
        self.assertEqual(a.opt("cpus"), "2"); self.assertEqual(a.opts("ssh-key"), ["k1", "k2"])
        self.assertTrue(a.flag("unattended")); self.assertTrue(a.yes)

    def test_bad_names_rejected(self):
        for bad in ["", "1abc", "a b", "x;rm", "../etc", "a" * 40]:
            with self.assertRaises(self.Fail):
                self.Args(["info", bad]).get_name()

    def test_list_and_info(self):
        d = self.dispatch("list")
        self.assertTrue(d["ok"]); self.assertEqual([v["name"] for v in d["vms"]], ["novee", "umbrel", "worker", "lab"])
        self.assertTrue(d["vms"][0]["protected"]); self.assertFalse(d["vms"][3]["protected"])
        d = self.dispatch("info lab")
        self.assertEqual(d["state"], "poweroff"); self.assertEqual(d["cpus"], 2)

    def test_mutations_need_yes(self):
        for line in ["start lab", "stop lab", "delete lab", "modify lab --cpus 1", "create x --yes".replace(" --yes", ""),
                     "iso download https://example.org/x.iso", "snapshot lab take s1"]:
            d = self.dispatch(line)
            self.assertFalse(d["ok"], line); self.assertIn("--yes", d["error"], line)

    def test_protected_vms(self):
        for line in ["stop novee --yes", "poweroff umbrel --yes", "delete novee --yes", "modify umbrel --cpus 2 --yes",
                     "snapshot novee restore s --yes"]:
            d = self.dispatch(line)
            self.assertFalse(d["ok"], line); self.assertIn("protected", d["error"], line)
        # start is allowed on anything (harmless) and reports already running
        d = self.dispatch("start novee --yes")
        self.assertTrue(d["ok"]); self.assertEqual(d["result"], "already running")

    def test_delete_requires_poweroff_and_unknown_vm(self):
        d = self.dispatch("delete nosuch --yes")
        self.assertFalse(d["ok"]); self.assertIn("no VM named", d["error"])
        d = self.dispatch("delete lab --yes")           # powered off, unprotected: allowed
        self.assertTrue(d["ok"]); self.assertIn(("unregistervm", "lab", "--delete-all"), [c[:3] for c in self.calls])
        d = self.dispatch("poweroff worker --yes")
        self.assertTrue(d["ok"]); self.assertIn(("controlvm", "worker", "poweroff"), [c[:3] for c in self.calls])
        d = self.dispatch("delete worker --yes")        # was running until a moment ago: the fake now says poweroff
        self.assertTrue(d["ok"])

    def test_unknown_and_injection(self):
        for line in ["id", "list; cat /etc/passwd", "iso rm", "$(whoami)"]:
            d = self.dispatch(line)
            self.assertFalse(d["ok"], line)

    def test_create_validation(self):
        for line, msg in [("create novee --yes", "already exists"), ("create new1 --cpus 999 --yes", "cpus"),
                          ("create new1 --ram 1 --yes", "ram"), ("create new1 --net wifi --yes", "net"),
                          ("create new1 --iso nothere.iso --yes", "not cached"),
                          ("create new1 --unattended --yes", "live-server")]:
            d = self.dispatch(line)
            self.assertFalse(d["ok"], line); self.assertIn(msg, d["error"], line)

    def test_ssh_config_entries(self):
        add, entries, remove = self.mod["add_ssh_config"], self.mod["ssh_config_entries"], self.mod["remove_ssh_config"]
        cfg = Path(self.tmp) / ".ssh" / "config"
        cfg.parent.mkdir(parents=True); cfg.write_text("Host keepme\n  HostName 1.2.3.4\n")
        add("lab", "10.0.0.5", "chinmay"); add("lab", "10.0.0.6", "chinmay")
        self.assertEqual(entries(), ["lab"]); self.assertEqual(cfg.read_text().count("Host lab"), 1)
        self.assertIn("10.0.0.6", cfg.read_text()); self.assertNotIn("10.0.0.5", cfg.read_text())
        remove("lab")
        self.assertEqual(entries(), []); self.assertIn("Host keepme", cfg.read_text())


if __name__ == "__main__":
    unittest.main(verbosity=1)
