#!/usr/bin/env python3
"""Sanitized, reproducible credential-capability census. READ ONLY, FAILS LOUD.

Emits per pane: pid, kernel start identity (bracketed before AND after the
environ read, so PID reuse is detected), GT_ROLE, presence booleans for each
LIVEOP_* name, and a BOOLEAN comparison against the adapter binding. Then a
PER-PANE lineage with a boolean key-equality at each hop.

NEVER emitted: any key value, hash, length or prefix.
NEVER done: restart, rotation, install, scheduler change, or any call using a
credential other than the adapter's own binding.

Exit codes: 0 complete census; 2 enumeration failed; 3 binding unreadable;
4 incomplete (examined < enumerated, or any pane unreadable, or any identity
changed under us). A vacuous run CANNOT exit 0 (Henry, c2-census review).
"""
import datetime, os, subprocess, sys

CLK = os.sysconf("SC_CLK_TCK")
BOOT = next(int(l.split()[1]) for l in open("/proc/stat") if l.startswith("btime"))
NAMES = ["LIVEOP_API_KEY", "LIVEOP_API_URL", "LIVEOP_AGENT_ID", "LIVEOP_AGENT_INBOX_CHANNEL"]
OBSERVED = datetime.datetime.now(datetime.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")


def start_ticks(pid):
    try:
        s = open(f"/proc/{pid}/stat").read()
        return int(s[s.rindex(")") + 2:].split()[19])
    except OSError:
        return None


def start_utc(ticks):
    if ticks is None:
        return None
    t = BOOT + ticks / CLK
    return datetime.datetime.fromtimestamp(t, datetime.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")


def ppid(pid):
    try:
        s = open(f"/proc/{pid}/stat").read()
        return int(s[s.rindex(")") + 2:].split()[1])
    except OSError:
        return None


def read_env_bracketed(pid):
    """Returns (env, identity_stable). Brackets the read with the start time so a
    PID recycled mid-read is reported rather than silently attributed."""
    before = start_ticks(pid)
    try:
        raw = open(f"/proc/{pid}/environ", "rb").read().decode("utf-8", "replace")
    except OSError:
        return None, before == start_ticks(pid)
    after = start_ticks(pid)
    env = dict(i.split("=", 1) for i in raw.split("\0") if "=" in i)
    return env, (before is not None and before == after)


print(f"# census_version=2  observed_at_utc={OBSERVED}  host={os.uname().nodename}  uid={os.getuid()}")

ref = None
try:
    for line in open(os.path.expanduser("~/.config/liveop/e2e-staging.env")):
        if line.startswith("LIVEOP_API_KEY="):
            ref = line.split("=", 1)[1].strip().strip("'\"")
except OSError as e:
    print(f"# FATAL binding unreadable: {e.__class__.__name__}")
    sys.exit(3)
if not ref:
    print("# FATAL binding present but carries no LIVEOP_API_KEY")
    sys.exit(3)
print("# adapter_binding_readable=yes (value never emitted)")

try:
    proc = subprocess.run(["tmux", "-L", "gt-0016fa", "list-panes", "-a", "-F",
                           "#{session_name} #{pane_pid}"], capture_output=True, text=True)
except OSError as e:
    # tmux absent entirely: an enumeration failure, not an empty town
    print(f"# FATAL tmux not executable: {e.__class__.__name__}: {e}")
    sys.exit(2)
if proc.returncode != 0:
    print(f"# FATAL tmux enumeration failed rc={proc.returncode}: {proc.stderr.strip()[:120]}")
    sys.exit(2)
panes = [l.split() for l in proc.stdout.splitlines() if l.strip()]
enumerated = len(panes)
if enumerated == 0:
    print("# FATAL enumerated=0; an empty census is not a clean census")
    sys.exit(2)

examined = unreadable = identity_changed = 0
rows = []
print(f"# columns: session pane_pid start_ticks start_utc identity_stable "
      f"{' '.join(n.replace('LIVEOP_','') for n in NAMES)} same_as_adapter role")
for name, pid in panes:
    env, stable = read_env_bracketed(pid)
    if env is None:
        unreadable += 1
        print(f"{name} {pid} - - {stable} UNREADABLE")
        continue
    if not stable:
        identity_changed += 1
    examined += 1
    presence = " ".join(str(n in env) for n in NAMES)
    same = "n/a"
    if "LIVEOP_API_KEY" in env:
        same = "yes" if env["LIVEOP_API_KEY"] == ref else "NO"
    t = start_ticks(pid)
    rows.append((name, pid, env))
    print(f"{name} {pid} {t} {start_utc(t)} {stable} {presence} {same} {env.get('GT_ROLE','<unset>')}")

print(f"# enumerated={enumerated} examined={examined} unreadable={unreadable} "
      f"identity_changed={identity_changed}")

print("# PER-PANE LINEAGE: pane -> ancestors, with boolean key-equality at each hop")
for name, pid, env in rows:
    chain, p, hops = [], ppid(int(pid)), 0
    while p and p > 1 and hops < 8:
        aenv, astable = read_env_bracketed(p)
        if aenv is None:
            chain.append(f"{p}:unreadable")
            break
        has = "LIVEOP_API_KEY" in aenv
        eq_pane = (has and "LIVEOP_API_KEY" in env
                   and aenv["LIVEOP_API_KEY"] == env["LIVEOP_API_KEY"])
        eq_ref = has and aenv["LIVEOP_API_KEY"] == ref
        comm = open(f"/proc/{p}/comm").read().strip() if os.path.exists(f"/proc/{p}/comm") else "?"
        chain.append(f"{p}({comm}) key={has} same_as_pane={eq_pane} same_as_adapter={eq_ref} "
                     f"started={start_utc(start_ticks(p))} stable={astable}")
        if has:
            break
        p = ppid(p); hops += 1
    print(f"#   {name} {pid} -> " + " | ".join(chain) if chain else f"#   {name} {pid} -> no ancestor reachable")

incomplete = unreadable or identity_changed or examined != enumerated
print(f"# census_complete={not incomplete}")
if incomplete:
    print("# FATAL incomplete census: refusing to report a clean result")
    sys.exit(4)
sys.exit(0)
