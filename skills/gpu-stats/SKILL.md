---
name: gpu-stats
description: Report what is using the GPU of the host PC and who asked for it. Use for "what is using my GPU", "which application is using the GPU", "why are my GPU fans spinning", "why is the GPU loud or hot", GPU temperature, GPU load or utilization, VRAM or GPU memory usage, "is the GPU busy or free", "which model is loaded in Ollama", "who is calling Ollama", "what woke up the GPU".
metadata:
  {
    "openclaw":
      {
        "emoji": "🎮",
        "os": ["linux"],
        "requires": { "bins": ["python3", "ssh"] },
      },
  }
---

# gpu-stats

The GPU is in the host PC, not in this VM. One script asks the host for a read-only report and prints plain-text bullets. Run it with `exec`, read the output, then answer Chinmay.

```bash
python3 {baseDir}/scripts/gpustats.py
```

Run it immediately for any GPU question. Do not ask first, and do not try `nvidia-smi` on this VM (there is no GPU here, it will fail).

## Options

- No options: the answer for "what is using the GPU", "why are the fans spinning", "is the GPU busy", temperature, memory.
- `--hours N`: how far back to list who called Ollama (default 2, max 168). Use `--hours 24` for "what used the GPU today", "what keeps waking the GPU", "who used it last night".
- `--json`: raw data. Only if Chinmay asks for raw output.

## Reading the output

- "GPU" block: load percent (BUSY or idle), memory used, temperature, fan, power.
- "Programs using the GPU": every program holding GPU memory, largest first. `llama-server` is Ollama running an AI model. `gnome-shell`, `Xwayland` and `brave` are the normal desktop and browser and are never the reason for fan noise.
- "AI models Ollama has loaded now": model name, GPU memory, and when it unloads. "none" means Ollama is not holding the GPU.
- "Who sent requests to Ollama": which machine asked for the model. A caller labelled as this VM means your own replies caused it. "ALL FAILED" requests never ran on the GPU.
- "Verdict": the short answer. Start your reply from it.

## Answering

1. First sentence: which application is using the GPU (or that nothing heavy is), taken from the Verdict.
2. Then 2 to 4 bullets: load, memory, temperature and fan, and who called Ollama last and when.
3. Copy numbers, names and times exactly from the output. Never invent or round into different values.
4. If the model that is loaded was called by this VM, say plainly that it is you answering messages.
5. If the script prints `ERROR`, say the GPU report is unavailable and quote the error line. Do not guess.

This skill only reports. It cannot stop programs or unload models. If Chinmay wants the GPU freed, say which program holds it and that it must be stopped on the host PC.

## Setup (already done on Novee's VM)

The script needs two things that are not in the repository: the config file `~/.config/novee/gpu-stats.json` (address of the GPU machine and names for LAN devices, see `examples/gpu-stats.example.json`) and the SSH key `~/.ssh/gpu-stats`, which the GPU machine accepts only for running `host/novee-gpu-report`. Details are in `docs/SETUP.md`.
