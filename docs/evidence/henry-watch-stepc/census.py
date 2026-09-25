#!/usr/bin/env python3
"""Sanitized, reproducible credential-capability census. READ ONLY.

Emits: pane pid, process start identity (kernel starttime ticks + derived UTC),
GT_ROLE, whether LIVEOP_API_KEY is PRESENT, and a BOOLEAN same-as-adapter
comparison. No key value, no hash, no length, no prefix ever leaves this process.
Nothing is restarted, rotated, installed or scheduled.
"""
import datetime, os, subprocess, sys

CLK = os.sysconf("SC_CLK_TCK")
BOOT = None
for line in open("/proc/stat"):
    if line.startswith("btime"):
        BOOT = int(line.split()[1])

def environ(pid):
    try:
        raw = open(f"/proc/{pid}/environ", "rb").read().decode("utf-8", "replace")
    except OSError:
        return None
    out = {}
    for item in raw.split("\0"):
        if "=" in item:
            k, v = item.split("=", 1)
            out[k] = v
    return out

def start_identity(pid):
    """Kernel start time: the half of PID+start identity that makes a pid unambiguous."""
    try:
        stat = open(f"/proc/{pid}/stat").read()
    except OSError:
        return None, None
    fields = stat[stat.rindex(")") + 2:].split()
    ticks = int(fields[19])                      # field 22 overall
    started = BOOT + ticks / CLK
    return ticks, datetime.datetime.fromtimestamp(started, datetime.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")

def ppid(pid):
    try:
        stat = open(f"/proc/{pid}/stat").read()
        return int(stat[stat.rindex(")") + 2:].split()[1])
    except OSError:
        return None

observed = datetime.datetime.now(datetime.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")
print(f"# observed_at_utc={observed} host={os.uname().nodename} uid={os.getuid()}")

# the adapter's own binding, read from the env file the cron job reads
ref = None
try:
    for line in open(os.path.expanduser("~/.config/liveop/e2e-staging.env")):
        if line.startswith("LIVEOP_API_KEY="):
            ref = line.split("=", 1)[1].strip().strip("'\"")
except OSError:
    pass
print(f"# adapter_binding_readable={'yes' if ref else 'no'}  (value never emitted)")

sessions = subprocess.run(["tmux", "-L", "gt-0016fa", "list-panes", "-a", "-F",
                           "#{session_name} #{pane_pid}"], capture_output=True, text=True).stdout.split("\n")
print(f"{'session':22s} {'pane_pid':>9s} {'start_ticks':>12s} {'start_utc':>21s} {'key':>4s} {'same':>5s}  role")
rows = 0
for line in sessions:
    if not line.strip():
        continue
    name, pid = line.split()[0], line.split()[1]
    env = environ(pid)
    if env is None:
        continue
    ticks, utc = start_identity(pid)
    has = "LIVEOP_API_KEY" in env
    same = "n/a"
    if has and ref is not None:
        same = "yes" if env["LIVEOP_API_KEY"] == ref else "NO"     # boolean only
    print(f"{name:22s} {pid:>9s} {str(ticks):>12s} {str(utc):>21s} {str(has):>4s} {same:>5s}  {env.get('GT_ROLE','<unset>')}")
    rows += 1
print(f"# sessions_examined={rows}")

# launcher inheritance: walk each pane's ancestry for the nearest process that also has the key
print("# launcher inheritance probe (nearest ancestor carrying the key):")
seen = {}
for line in sessions:
    if not line.strip():
        continue
    pid = int(line.split()[1])
    p = ppid(pid)
    hops = 0
    while p and p > 1 and hops < 8:
        e = environ(p)
        if e is None:
            break
        if "LIVEOP_API_KEY" in e:
            t, u = start_identity(p)
            seen[p] = (e.get("GT_ROLE", "<unset>"), u, open(f"/proc/{p}/comm").read().strip())
            break
        p = ppid(p); hops += 1
if seen:
    for p, (role, u, comm) in sorted(seen.items()):
        print(f"#   ancestor pid={p} comm={comm} role={role} started={u} HAS key")
else:
    print("#   no ancestor carrying the key is reachable; PROPAGATION ROOT REMAINS UNKNOWN")
