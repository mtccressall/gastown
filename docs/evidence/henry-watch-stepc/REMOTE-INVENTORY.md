# Remote-host consumer inventory (amd-halo) — for Henry's C2 reconciliation

Author-provided. My measurements on this host, live, read-only. Nothing was restarted, no process was
inspected by injected code, no scheduler was changed. **This corrects my own earlier packet claim; see
finding 1.**

| | |
|---|---|
| host | `amd-halo` |
| UID | `marccressall` (single UID for every surface below) |
| measured at | 2026-09-25T09:31Z, with the credential check at 09:3xZ |

## FINDING 1 — I MUST CORRECT MY OWN PACKET. The credential is NOT confined to the adapter.

Section 2 of the cutover packet said: *"one API key, held only by this adapter and by interactive Mayor
commands run by me."* **That is wrong.** Read directly from `/proc/<pid>/environ` for every resident
agent pane:

**`LIVEOP_API_KEY` is present in the environment of ALL 15 resident agent sessions.**

So every agent on this host has the *capability* to poll `#dev` and to post as `agent:gas-new`. That is
the conflict class you asked about, and it is broader than I reported.

**Capability is not observed use, and I am keeping those separate** (this town has a standing rule that an
environment census measures environment, not exposure):
- **Established**: 15 sessions hold the key; the cron adapter holds it by reading the env file.
- **Observed use**: 19 posts as `agent:gas-new` in the current 100-message window, kinds ACK, RESULT,
  REVIEW_REQUEST, STATUS — all consistent with the adapter's receipt ACKs and my own Mayor posts.
- **NOT ESTABLISHED, and not establishable from the channel side**: which *process* made any given post.
  The identity is the shared key, so `agent:gas-new` posts carry no process attribution. I cannot prove
  that no other session has ever posted, only that every post I can see matches traffic I can account for.
- **PROPAGATION ROOT: UNKNOWN.** The key is not in `~/.bashrc` or `~/.profile`. One probe suggested a
  long-running `gt daemon` process carried it (which would explain inheritance by spawned sessions), but
  that PID had exited by my next command, so the reading is unrepeatable and I will not present it as the
  cause. Establishing the root needs a process that is still alive when examined.

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
| tmux session | pane pid | GT_ROLE | created (UTC) |
|---|---|---|---|
| be-witness | 3592198 | beadsrig/witness | 2026-09-12T00:57 |
| gastown-citrine | 1150325 | gastown/polecats/citrine | 2026-09-12T07:57 |
| gastown-refinery | 3592434 | gastown/refinery | 2026-09-12T00:57 |
| gastown-witness | 3592187 | gastown/witness | 2026-09-12T00:57 |
| hq-boot | 1019692 | deacon/boot | 2026-09-16T10:31 |
| hq-deacon | 3020863 | deacon | 2026-09-14T11:25 |
| **hq-mayor** | 612458 | **mayor** | 2026-09-21T14:59 |
| liveop-atom | 3111079 | liveop/polecats/atom | 2026-09-18T22:05 |
| liveop-foundation | 3733790 | liveop/polecats/foundation | 2026-09-21T17:21 |
| liveop-guzzle | 2671802 | liveop/polecats/guzzle | 2026-09-21T19:37 |
| liveop-institute | 3055700 | liveop/polecats/institute | 2026-09-15T21:49 |
| liveop-refinery | 3559011 | liveop/refinery | 2026-09-12T00:56 |
| liveop-synth | 2785101 | liveop/polecats/synth | 2026-09-21T19:41 |
| liveop-witness | 3550527 | liveop/witness | 2026-09-12T00:56 |
| steward-witness | 3592210 | steward/witness | 2026-09-12T00:57 |

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
