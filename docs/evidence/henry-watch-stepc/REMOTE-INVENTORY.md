# Remote-host consumer inventory (amd-halo) — for Henry's C2 reconciliation

Author-provided. My measurements on this host, live, read-only. Nothing was restarted, no process was
inspected by injected code, no scheduler was changed. **This corrects my own earlier packet claim; see
finding 1.**

| | |
|---|---|
| host | `amd-halo` |
| UID | `marccressall` (single UID for every surface below) |
| measured at | **2026-09-25T09:35:59Z** exactly (census.py emits its own observation time; the earlier `09:3xZ` is replaced) |

## FINDING 1 — CORRECTED TWICE. Read all three versions; the third is the measured one.

**v1 (my cutover packet, WRONG):** "one API key, held only by this adapter and by interactive Mayor
commands run by me."

**v2 (my first correction, OVERSTATED):** every agent session holds the key, so every agent "has the
capability to post as `agent:gas-new`". Presence was right; the capability conclusion was not measured.

**v3, MEASURED 2026-09-25T09:35:59Z, boolean-only comparison, no value or hash emitted:**

| fact | status |
|---|---|
| all 15 resident sessions have `LIVEOP_API_KEY` in their environment | ESTABLISHED |
| that value **differs from the adapter's binding** in all 15 (`same=NO`, 15/15) | ESTABLISHED |
| the sessions carry **only** `LIVEOP_API_KEY`, and none of `LIVEOP_API_URL`, `LIVEOP_AGENT_ID`, `LIVEOP_AGENT_INBOX_CHANNEL` | ESTABLISHED |
| whether the sessions' key resolves to `agent:gas-new` | **UNKNOWN** — deciding it means USING another credential to see what identity the server stamps, which I have not done and am not authorised to do |
| propagation root | **FOUND**: the tmux server process (pid 3539834, comm `tmux: server`, started 2026-09-12T00:56:09Z) carries a key; every pane inherits it. Server key == Mayor session key, and both differ from the adapter's binding |

So Henry's caution was exactly right: **environment presence alone establishes neither an equal
credential value nor effective permissions.** Presence is 15/15; equality with the adapter's binding is
0/15. My v2 conclusion is withdrawn.

**What this does and does not mean.** The sessions cannot post using the adapter's binding, because they
do not have it. Whether their own key grants the same *identity* is unknown, and an older or rotated key
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

## Explicit unknowns

1. **Propagation root of the credential** — unknown, as above. Consequence: I cannot say whether a new
   session will inherit it, so the capability set may grow without anyone acting.
2. **Process attribution of `agent:gas-new` posts** — impossible from the channel. Mitigation available if
   you want it: a distinct identity for the adapter, so its posts are separable from the Mayor's. That is a
   change to request, not something I will do unilaterally.
3. **Whether any non-Mayor session has ever used the key** — not established, and not disprovable with
   current evidence.
4. **Henry-side consumers** — outside my scope, yours to enumerate.

**Under the packet's own rule, unknowns 1 and 2 block cutover** until you and Marc decide they are
acceptable or they are closed. I am not proposing to install over them.
