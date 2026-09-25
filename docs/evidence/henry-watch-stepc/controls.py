#!/usr/bin/env python3
"""SYNTHETIC controls for census.py. No real /proc, no real binding, no tmux.

Every case builds a fake procfs tree and a fake binding file in a temp dir and
points census.py at them with CENSUS_PROCFS / CENSUS_BINDING / CENSUS_TMUX.
Nothing on this host is read or executed beyond python3 and the census script.
Exit codes are captured from the process, never through a pipe.
"""
import os, shutil, subprocess, sys, tempfile

CLK = os.sysconf("SC_CLK_TCK")


def write_proc(root, pid, ppid, ticks, env, comm="proc"):
    d = os.path.join(root, str(pid)); os.makedirs(d, exist_ok=True)
    # field layout: pid (comm) state ppid ... starttime is field 22 overall
    f = ["0"] * 50
    f[1] = str(ppid)          # field 4 overall
    f[19] = str(ticks)        # field 22 overall
    open(os.path.join(d, "stat"), "w").write(f"{pid} ({comm}) S " + " ".join(f[1:]) + "\n")
    open(os.path.join(d, "environ"), "wb").write(
        "\0".join(f"{k}={v}" for k, v in env.items()).encode())
    open(os.path.join(d, "comm"), "w").write(comm + "\n")


def build(case):
    root = tempfile.mkdtemp(prefix=f"synth-{case}-")
    proc = os.path.join(root, "proc"); os.makedirs(proc)
    open(os.path.join(proc, "stat"), "w").write("btime 1700000000\n")
    binding = os.path.join(root, "binding.env")
    if case != "binding-unreadable":
        open(binding, "w").write("LIVEOP_API_KEY=ADAPTER-KEY\nLIVEOP_API_URL=u\n")
    KEY_OTHER = {"LIVEOP_API_KEY": "OTHER-KEY", "GT_ROLE": "synthetic/role"}
    if case in ("clean", "pane-recycled", "ancestor-recycled", "ancestor-unreadable"):
        write_proc(proc, 100, 200, 5000, KEY_OTHER, "pane")
        if case == "ancestor-unreadable":
            pass                                  # 200 never created -> unreadable ancestor
        elif case == "ancestor-recycled":
            write_proc(proc, 200, 300, 4000, {"GT_ROLE": "mid"}, "mid")
            write_proc(proc, 300, 1, 3000, {"LIVEOP_API_KEY": "OTHER-KEY"}, "server")
        else:
            write_proc(proc, 200, 1, 4000, {"LIVEOP_API_KEY": "OTHER-KEY"}, "tmux: server")
    tmux = os.path.join(root, "tmux.sh")
    if case == "tmux-failure":
        open(tmux, "w").write("#!/bin/sh\nexit 1\n")
    elif case == "empty-enumeration":
        open(tmux, "w").write("#!/bin/sh\nexit 0\n")
    else:
        open(tmux, "w").write("#!/bin/sh\necho 'synthetic 100'\n")
    os.chmod(tmux, 0o755)
    return root, proc, binding, tmux


def run(case, expect, mutate=None):
    root, proc, binding, tmux = build(case)
    if mutate:
        mutate(proc)
    env = {**os.environ, "CENSUS_PROCFS": proc, "CENSUS_BINDING": binding, "CENSUS_TMUX": tmux}
    r = subprocess.run([sys.executable, "census.py"], env=env, capture_output=True, text=True)
    fatal = [l for l in r.stdout.splitlines() if "FATAL" in l or "PROPAGATION REMAINS" in l]
    ok = r.returncode == expect
    print(f"  {case:22s} rc={r.returncode} expect={expect} {'PASS' if ok else 'MISMATCH'}"
          f"  {fatal[-1][2:72] if fatal else ''}")
    shutil.rmtree(root, ignore_errors=True)
    return ok


def recycle_pane(proc):
    """The pane's stat now shows a DIFFERENT start time than its environ sample
    implies, and a new parent: the recycled-PID case."""
    write_proc(proc, 100, 300, 9999, {"LIVEOP_API_KEY": "OTHER-KEY", "GT_ROLE": "synthetic/role"}, "pane")
    write_proc(proc, 300, 1, 3000, {"LIVEOP_API_KEY": "ADAPTER-KEY"}, "attacker")


def function_level_controls():
    """Function-level synthetic controls. FULLY ISOLATED.

    Importing census.py has no side effects (its body is under __main__), all THREE
    overrides are set and restored, and a subprocess guard ASSERTS that no command
    is attempted at all — the leak Henry intercepted was this control falling back
    to the real `tmux -L gt-0016fa list-panes` (c2-census-1b16a787).
    """
    import importlib.machinery, importlib.util, subprocess as sp
    root = tempfile.mkdtemp(prefix="synth-fn-")
    proc = os.path.join(root, "proc"); os.makedirs(proc)
    open(os.path.join(proc, "stat"), "w").write("btime 1700000000\n")
    binding = os.path.join(root, "b.env"); open(binding, "w").write("LIVEOP_API_KEY=K\n")
    forbidden = os.path.join(root, "forbidden.sh")
    open(forbidden, "w").write("#!/bin/sh\ntouch " + os.path.join(root, "INVOKED") + "\nexit 9\n")
    os.chmod(forbidden, 0o755)
    write_proc(proc, 100, 200, 5000, {"LIVEOP_API_KEY": "K"}, "pane")

    saved = {k: os.environ.get(k) for k in ("CENSUS_PROCFS", "CENSUS_BINDING", "CENSUS_TMUX")}
    os.environ["CENSUS_PROCFS"] = proc
    os.environ["CENSUS_BINDING"] = binding
    os.environ["CENSUS_TMUX"] = forbidden          # all THREE set
    attempted = []
    real_run = sp.run
    sp.run = lambda *a, **k: attempted.append(a[0] if a else k.get("args")) or real_run(
        ["true"], capture_output=True, text=True)
    try:
        ld = importlib.machinery.SourceFileLoader("census_fn", "census.py")
        spec = importlib.util.spec_from_loader(ld.name, ld)
        m = importlib.util.module_from_spec(spec)
        ld.exec_module(m)                          # inert: body is under __main__
        results = [
            ("import attempted NO command", attempted == []),
            ("forbidden tmux stub never invoked", not os.path.exists(os.path.join(root, "INVOKED"))),
            ("matching identity -> parent returned", m.ppid_validated(100, 5000) == (200, True)),
            ("MISMATCHED identity -> refused, no parent", m.ppid_validated(100, 4242) == (None, False)),
            ("ppid and ticks from ONE snapshot", m.stat_snapshot(100) == (200, 5000)),
        ]
    finally:
        sp.run = real_run
        for k, v in saved.items():
            if v is None:
                os.environ.pop(k, None)
            else:
                os.environ[k] = v
        shutil.rmtree(root, ignore_errors=True)
    for label, good in results:
        print(f"  {label:44s} {'PASS' if good else 'MISMATCH'}")
    return all(g for _, g in results)


ok = True
print("Synthetic controls (no real /proc, binding or tmux):")
ok &= run("clean", 0)
ok &= run("tmux-failure", 2)
ok &= run("empty-enumeration", 2)
ok &= run("binding-unreadable", 3)
ok &= run("ancestor-unreadable", 5)
print("Function-level synthetic controls for stale-parent validation:")
ok &= function_level_controls()
sys.exit(0 if ok else 1)
