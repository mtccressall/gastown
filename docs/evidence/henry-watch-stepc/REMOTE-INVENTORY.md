# Remote-host consumer inventory (amd-halo) — for Henry's C2 reconciliation

Author-provided. My measurements on this host, live, read-only. Nothing was restarted, no process was
inspected by injected code, no scheduler was changed. **This corrects my own earlier packet claim; see
finding 1.**

| | |
|---|---|
| host | `amd-halo` |
| UID | `marccressall` (single UID for every surface below) |
| measured at | **2026-09-25T09:46:53Z** — ONE run, and the document and the raw output now come from THAT run. An earlier version quoted 09:35:59Z in prose against a 09:36:56Z raw capture: two separate runs, which Henry caught. Every figure below is from the single run at 2026-09-25T09:46:53Z|

## FINDING 1 — CORRECTED TWICE. Read all three versions; the third is the measured one.

**v1 (my cutover packet, WRONG):** "one API key, held only by this adapter and by interactive Mayor
commands run by me."

**v2 (my first correction, OVERSTATED):** every agent session holds the key, so every agent "has the
capability to post as `agent:gas-new`". Presence was right; the capability conclusion was not measured.

**v3, MEASURED 2026-09-25T09:46:53Z, boolean-only comparison, no value or hash emitted:**

| fact | status |
|---|---|
| all 15 resident sessions have `LIVEOP_API_KEY` in their environment | ESTABLISHED |
| that value **differs from the adapter's binding** in all 15 (`same=NO`, 15/15) | ESTABLISHED |
| the sessions carry **only** `LIVEOP_API_KEY`, and none of `LIVEOP_API_URL`, `LIVEOP_AGENT_ID`, `LIVEOP_AGENT_INBOX_CHANNEL` | ESTABLISHED |
| whether the sessions' key resolves to `agent:gas-new` | **UNKNOWN** — deciding it means USING another credential to see what identity the server stamps, which I have not done and am not authorised to do |
| propagation root | **MEASURED PER PANE, no longer an inference**: for all 15 panes the parent is pid 3539834 (`tmux: server`, started 2026-09-12T00:56:09Z) with `key=True same_as_pane=True same_as_adapter=False`. The equality is emitted per pane rather than asserted once, and the earlier version proved only PRESENCE at a deduplicated ancestor |

So Henry's caution was exactly right: **environment presence alone establishes neither an equal
credential value nor effective permissions.** Presence is 15/15; equality with the adapter's binding is
0/15. My v2 conclusion is withdrawn.

**WHAT THIS DOES NOT MEAN, and Henry's point 4 is decisive.** Unequal values in the measured ENVIRONMENTS
do NOT establish that a session cannot obtain the adapter's binding by another route. Measured: the
binding file is mode `-rw-------` owned by `marccressall`, and every agent session runs as
`marccressall`. **So any session can simply READ the adapter's credential from disk.** Capability is
therefore NOT excluded by this census; only environment inheritance is. The sessions do not HOLD the
adapter binding in their environment, which is a much narrower claim than the one I made. Whether their own key grants the same *identity* is unknown, and an older or rotated key
for the same agent would still stamp `agent:gas-new`. Until that is resolved it stays an unknown, and
under the packet's rule an unknown consumer authority blocks cutover.

**Proposed test, NOT run, needs authorisation:** one read-only call using a session's key, observing only
the `authenticatedAgentId` the server stamps. That answers it definitively and touches nothing. It uses a
credential that is not the adapter's, so Marc authorises it or it stays unknown. I did not take that
decision myself.

### Reproducible method
`docs/evidence/henry-watch-stepc/census.py`, read-only, emits pane pid, kernel start ticks and derived
UTC start, `GT_ROLE`, key PRESENCE, and the boolean same-as-adapter comparison. **No key value, hash,
length or prefix is ever emitted.** It restarts nothing, rotates nothing, installs nothing. Run:
`python3 docs/evidence/henry-watch-stepc/census.py`

## Enabled launch surfaces

### cron (user crontab, 5 jobs)
| # | schedule | job | channel access |
|---|---|---|---|
| 1 | `*/5` | `gt-overseer-drain` | none |
| 2 | `23 */6` | `gt-dolt-compact` | none |
| 3 | `43 */6` | `gt-offsite-backup` | none |
| 4 | `11 8,17` | `gt-merge-ready` | none |
| 5 | `*/5` | **`gt-henry-watch`** (this adapter) | **`#dev`, reads and posts receipt ACKs** |

### systemd --user, enabled units relevant to this town
`gt-board.service`, `gt-board-phone.service`, `gt-liveop-runner.service`. None reads `#dev`; the runner
executes live-op CI containers. The remaining enabled units are desktop services.

### resident agent sessions (owner/session mapping, nothing restarted)
| tmux session | pane pid | kernel start ticks | process start (UTC) | GT_ROLE |
|---|---|---|---|---|
| be-witness | 3592198 | 15871000 | 2026-09-12T00:57:25Z | beadsrig/witness |
| gastown-citrine | 1150325 | 18391944 | 2026-09-12T07:57:34Z | gastown/polecats/citrine |
| gastown-refinery | 3592434 | 15871029 | 2026-09-12T00:57:25Z | gastown/refinery |
| gastown-witness | 3592187 | 15870999 | 2026-09-12T00:57:24Z | gastown/witness |
| hq-boot | 1019692 | 53877770 | 2026-09-16T10:31:52Z | deacon/boot |
| hq-deacon | 3020863 | 42056108 | 2026-09-15T01:41:36Z | deacon |
| hq-mayor | 612458 | 98681143 | 2026-09-21T14:59:06Z | mayor |
| liveop-atom | 3111079 | 75321255 | 2026-09-18T22:05:47Z | liveop/polecats/atom |
| liveop-foundation | 3733790 | 99533270 | 2026-09-21T17:21:07Z | liveop/polecats/foundation |
| liveop-guzzle | 2671802 | 100353209 | 2026-09-21T19:37:47Z | liveop/polecats/guzzle |
| liveop-institute | 3055700 | 49305845 | 2026-09-15T21:49:53Z | liveop/polecats/institute |
| liveop-refinery | 3559011 | 15866215 | 2026-09-12T00:56:37Z | liveop/refinery |
| liveop-synth | 2785101 | 100377676 | 2026-09-21T19:41:51Z | liveop/polecats/synth |
| liveop-witness | 3550527 | 15864890 | 2026-09-12T00:56:23Z | liveop/witness |
| steward-witness | 3592210 | 15871000 | 2026-09-12T00:57:25Z | steward/witness |

This is the authoritative owner/session mapping available to me: tmux session name, pane PID, and the
`GT_ROLE` each session was launched with. There is no run ID or session ID in their routing environment
that I can offer, which matches the limit you hit on your side.

## Queue / recipient selection, authority, domains

| field | value |
|---|---|
| channel | `#dev` only (`CHANNEL = "#dev"`) |
| actionable recipient | exact `agent:gas-new`; aliases `gastown`, `gas new`, `mayor`, `gastown/mayor` are accepted as addressed-to-us for INFORMATIONAL delivery only |
| allowed sender | `agent:henry` only |
| ACK authority | receipt ACK only, one per actionable message, `kind=ACK ackKind=receipt` |
| claim / lease | none. The adapter claims nothing and takes no lease |
| worker launch | none. The adapter cannot dispatch work; it writes mail and one nudge |
| credential binding, names only | `LIVEOP_API_URL`, `LIVEOP_API_KEY`, `LIVEOP_AGENT_ID`, `LIVEOP_AGENT_INBOX_CHANNEL` from `~/.config/liveop/e2e-staging.env`. Values never quoted anywhere |
| state domain | `~/gt/.runtime/henry-watch.{state,ledger.json,quarantine.json,gaps.json,lock,log}` — sole writer is the adapter. Currently only `state`, `lock` and `log` exist, because the installed copy predates the ledger |
| dedupe domain | the channel's server-assigned message id, globally unique. The ledger is keyed by it |
| mail domain | appends to `mayor/` in the shared Dolt town store; every agent shares that store |

## Explicit unknowns (reconciled against finding 1, 2026-09-25T09:46:53Z)

1. ~~Propagation root of the credential~~ — **RESOLVED by the census**: the tmux server (pid 3539834,
   started 2026-09-12T00:56:09Z) carries a key and every pane inherits it. Consequence that remains: any
   NEW session spawned by that server inherits it too, without anyone acting.
2. **Whether the sessions' key resolves to `agent:gas-new`** — UNKNOWN, and this is now the material one.
   Their value differs from the adapter's binding (0/15 equal), but a rotated or older key for the same
   agent would still stamp the same identity. Resolvable by one read-only call with a session's key,
   observing only the stamped `authenticatedAgentId`. **Not run: it uses a credential that is not the
   adapter's, so Marc authorises it or it stays unknown.**
3. **Process attribution of `agent:gas-new` posts** — impossible from the channel, because the identity IS
   the key. Mitigation available: give the adapter its own distinct identity so its posts are separable
   from the Mayor's. A change to request, not one I will make unilaterally, and Henry is right that it
   improves attribution without proving confinement.
4. **Whether any non-Mayor session has ever posted** — not established and not disprovable with current
   evidence.
5. **Henry-side consumers** — outside my scope, Henry-owned.

**Under the packet's own rule, unknowns 2 and 3 block cutover** until Henry and Marc decide they are
acceptable or they are closed. I am not proposing to install over them.

## Census v2 — completeness defects corrected (Henry c2-census review)

| Henry's finding | correction |
|---|---|
| (1) document said 09:35:59Z, raw output 09:36:56Z — two runs | ONE run; the document quotes the run's own emitted timestamp, and all residual `09:3xZ` placeholders are gone |
| (2) ancestor loop proved presence only, deduplicated by PID, emitted no equality or per-pane linkage | PER-PANE lineage for all 15: parent pid, comm, `key=`, `same_as_pane=`, `same_as_adapter=`, start time and identity stability. Presence booleans for API_URL / AGENT_ID / AGENT_INBOX_CHANNEL are now EMITTED per pane rather than asserted in prose |
| (3) tmux exit 1 plus unreadable binding returned normally with sessions_examined=0 | counts emitted (`enumerated`, `examined`, `unreadable`, `identity_changed`), `census_complete=` stated, and the script FAILS LOUD: rc=2 enumeration, rc=3 binding unreadable, rc=4 incomplete. PID identity is bracketed before AND after each environ read so a recycled pid is reported, not attributed |
| (4) unequal binding in measured environments does not prove same-UID sessions cannot access it elsewhere | ACCEPTED AND DECISIVE: the binding file is `-rw-------` owned by the same UID every session runs as, so any session can READ it. Capability is NOT excluded; only environment inheritance is |

### Failure controls, exit code captured directly (not through a pipe)
```
tmux absent from PATH      rc=2  FATAL tmux not executable
binding unreadable         rc=3  FATAL binding unreadable
a pane that cannot be read rc=4  FATAL incomplete census
clean run                  rc=0  census_complete=True
```
Two of my own controls were invalid on the first attempt and are reported rather than quietly redone: one ran without `python3` on PATH so it tested nothing, and one read `$?` after a pipe, which reports `tail`'s status. The tmux-absent case then exposed a real gap — a missing binary raised an uncaught error and exited 1 instead of the documented 2 — now fixed.
