#!/usr/bin/env python3
"""Isolated mocked controls for census.py failure paths. Read-only, no live calls."""
import importlib.machinery, importlib.util, io, contextlib, sys

def run(patch, label, expect):
    ld = importlib.machinery.SourceFileLoader("c_" + label.replace(" ", "_"), "census.py")
    spec = importlib.util.spec_from_loader(ld.name, ld)
    m = importlib.util.module_from_spec(spec)
    buf = io.StringIO(); rc = 0
    try:
        with contextlib.redirect_stdout(buf):
            patch(m, ld)
    except SystemExit as e:
        rc = e.code or 0
    out = buf.getvalue()
    flag = "PASS" if rc == expect else "MISMATCH"
    tail = [l for l in out.splitlines() if l.startswith("# FATAL") or "PROPAGATION REMAINS" in l]
    print(f"  {label:34s} rc={rc} expect={expect} {flag}  {tail[-1][:70] if tail else ''}")
    return rc == expect

def clean(m, ld): ld.exec_module(m)

def ancestor_unreadable(m, ld):
    import builtins
    real_open = builtins.open
    # make every ancestor environ read fail, panes unaffected
    ld.exec_module_orig = ld.exec_module
    def patched(mod):
        real_ppid = None
        def hook():
            nonlocal real_ppid
            real_ppid = mod.ppid
            mod.ppid = lambda pid: 999999999      # a pid that cannot exist
        mod.__dict__["_hook"] = hook
        ld.exec_module_orig(mod)
    # simplest: run the module with ppid stubbed by pre-seeding the namespace
    src = open("census.py").read().replace("def ppid(pid):", "def ppid(pid):\n    return 999999999\n\ndef _unused_ppid(pid):")
    exec(compile(src, "census_ancestor_unreadable", "exec"), {"__name__": "__main__"})

def identity_changed(m, ld):
    # start_ticks returns a different value on each call -> brackets never match
    src = open("census.py").read().replace(
        "def start_ticks(pid):",
        "_tick_call = [0]\ndef start_ticks(pid):\n    _tick_call[0] += 1\n    return _tick_call[0]\n\ndef _unused_start_ticks(pid):")
    exec(compile(src, "census_identity_changed", "exec"), {"__name__": "__main__"})

ok = True
print("Isolated mocked controls:")
ok &= run(clean, "clean run", 0)
ok &= run(ancestor_unreadable, "ancestor unreadable", 5)
ok &= run(identity_changed, "identity changed under us", 4)
sys.exit(0 if ok else 1)
