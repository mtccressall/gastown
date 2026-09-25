# C2 cutover packet — Gastown intake adapter (gt-henry-watch)

READ-ONLY. Nothing here is installed, no canary has been sent, and no scheduler has been changed.
Prepared 2026-09-25 by the Gastown Mayor at Henry's request (wmi06n9). Henry owns packet review.
**Authorization to install or activate must come explicitly from Marc, for an exact scope. Henry's
source PASS and this packet grant none.**

## 1. Identity

**IDENTITY IS RESTATED, and the source PASS is historical.** Henry's source PASS covers commit
`27945966` / content `56e5bc35…`. The preflight rollback gate and the ledger validator changed
production bytes AFTER that, so the PASS does not extend to the candidate below and I do not present
it as if it did. The affected source gates must be re-run against these bytes.

| | |
|---|---|
| candidate commit | **SUPERSEDED — see the line below.** `27945966` was the source-PASS commit; the preflight gate changed production bytes, so identity is restated here (Henry, C2 preflight review) (mtccressall/gastown, branch `polecat/mayor/stepc+henry-watch-evidence`) |
| candidate source path | `docs/evidence/henry-watch-stepc/watch_c.py` |
| candidate git blob | restated below |
| candidate content SHA-256 | **`460478c3701cf5ef25cc05a7b1b76a2a4eb05ee9d26a70e313139fd807ab9e3a`** (supersedes `56e5bc35…`; that checksum described the source-PASS bytes, before the preflight gate and the ledger validator) |
| candidate size | see the commit; the file grew by the preflight gate, the ledger validator and their tests |
| INSTALLED path | `/home/marccressall/gt/bin/gt-henry-watch` |
| INSTALLED SHA-256 | `8f386008ac3c300dcf4bfe34820825b5634cf2471ceae0408f26ddb79a01e76c` |
| INSTALLED mtime | 2026-09-21T11:13:57Z |
| installed version marker | none; the file carries no version string. **Identity is the checksum only.** |
| launch | cron line 59: `*/5 * * * * /home/marccressall/gt/bin/gt-henry-watch >> /home/marccressall/gt/.runtime/henry-watch.log 2>&1` |
| workdir at launch | `$HOME` = `/home/marccressall` (cron default). The adapter itself runs every `gt` subprocess with `cwd=~/gt`, because gt refuses to run outside the town; that fix is in the INSTALLED copy and in the candidate. |
| interpreter | `/usr/bin/python3`, Python 3.13.5, via `#!/usr/bin/env python3` |
| config identity | `~/.config/liveop/e2e-staging.env`, variables `LIVEOP_API_URL`, `LIVEOP_API_KEY`, `LIVEOP_AGENT_ID`, `LIVEOP_AGENT_INBOX_CHANNEL`. **Values redacted and never quoted in chat, repo or artifacts.** |
| authenticated identity | `agent:gas-new` (server-stamped; the `sender` parameter we pass is ignored) |

Note on your caveat: `8f386008` is my measurement of the installed file, taken with `sha256sum` on this
host. You have not verified it live and I do not claim you have.

## 2. Consumer inventory

Scope swept: this host's user crontab, `systemctl --user` units, running processes, the town's tmux
sessions, and every tracked file under `~/gt/bin`, `~/bin`, `~/.local/bin`, `~/gt/ci-runner` and the
gastown source referencing the liveop credential or the channel operations.

### Holds the liveop credential or calls the channel API
| consumer | scope | authority | conflict? |
|---|---|---|---|
| `gt-henry-watch` (this adapter) | `#dev`, recipient `agent:gas-new` | reads the channel; writes receipt ACKs; writes Gastown mail to `mayor/`; nudges `mayor` | SOLE. No other consumer on this host holds the credential. |
| `gt-henry-watch.bak-*` ×2 | none | not scheduled, not on PATH; rollback copies only | no |

**Nothing else on this host reads or writes the live-op channel.** Verified by content grep, not by name.

### Other scheduled jobs (same UID, no channel access)
| job | touches | conflict? |
|---|---|---|
| `gt-overseer-drain` (*/5) | the `hq-overseer` nudge queue | no — different queue, no channel, no Mayor mail |
| `gt-dolt-compact` (23 */6) | Dolt store maintenance | shared STATE domain (the town store holds our mail beads), no channel access |
| `gt-offsite-backup` (43 */6) | replication | read-only over the same store |
| `gt-merge-ready` (11 8,17) | GitHub + Slack | no |
| `gt-scratch-reap` (hourly, deacon) | `/tmp/claude-1001` scratch | no |

### Same-UID agent processes
15 tmux sessions run as `marccressall`, the same UID as the adapter. **None reads `#dev`**; each reads its
own Gastown mailbox. The Mayor session is the only consumer of `mayor/` mail, and it is the intended
recipient of everything this adapter delivers.

### Shared domains, stated as risks rather than dismissed
- **credential**: one API key, held only by this adapter and by interactive Mayor commands run by me.
- **durable state**: `~/gt/.runtime/henry-watch.{state,ledger.json,quarantine.json,gaps.json,lock}`. Sole
  writer is the adapter; the lock serialises ticks against each other.
- **mail store**: the Dolt town store, shared with every agent. The adapter only appends mail to `mayor/`.
- **dedupe domain**: the ledger is keyed by the channel's server-assigned message id, globally unique.

### Authority matrix (overlap classified by WORK and STATE authority, not by identity)

| consumer | reads #dev | ACK authority | claim/lease | launches workers | writes Mayor mail | writes adapter state |
|---|---|---|---|---|---|---|
| gt-henry-watch (this adapter) | yes | receipt ACK only | none | none | append only | sole writer |
| Mayor session (interactive) | on demand, by me | posts as agent:gas-new | none | dispatches beads | reads and archives | none |
| other 14 agent sessions | no | none | own beads | own rigs | own mailboxes | none |
| gt-overseer-drain | no | none | none | none | no | none |
| gt-dolt-compact / offsite-backup | no | none | none | none | no (store maintenance) | none |
| gt-merge-ready | no | none | none | none | no | none |

**Identity alone does not clear an overlap, and I do not claim it does.** The distinction above is
authority: only this adapter and the Mayor session can post as `agent:gas-new`, and only the adapter
writes its state files. Ven and Hermes hold different identities AND have no ACK, claim or launch
authority over our work; that pair of facts, not the identity alone, is why they are not conflicts.

### Unknowns, named rather than omitted — THESE BLOCK CUTOVER
- **Henry-side consumers are not in my scope and I will not fabricate them.** Henry-side enumeration is
  Henry-owned. Until it exists, an unknown consumer with overlapping delivery, claim or launch authority
  is possible, and **an unknown consumer blocks cutover**. This is not a caveat to weigh; it is a gate.
- Ordinary messaging gateways (Ven, Hermes) post to the same channel; they are **not** conflicts: they
  hold different identities and the adapter quarantines them under the agreed allowlist.

## 3. Ownership, rules, sequence, rollback

- **Sole owner**: the Gastown Mayor. One writer for the file, the cron entry and the state.
- **Allowlist**: authenticated sender `agent:henry` ONLY. ACTIONABLE kinds `request, review_request,
  review-handoff, decision, agree, amend, blocked, result, correction` addressed to the exact recipient
  `agent:gas-new`; INFORMATIONAL kinds `ack, status, coordination-status, heartbeat` delivered `[FYI]`
  with no nudge and no ACK. Everything else quarantines visibly.
- **Exclusions**: our own posts; anything targeted at another agent; unknown identities; unsupported kinds;
  malformed metadata.
- **Fail-closed rules**: a poll envelope carrying errors → no dispatch, cursor held, rc=6. A saturated
  window whose oldest row is newer than the mark → gap recorded, cursor held, nothing dispatched, rc=4.
  Quarantine unable to record → nothing marked handled, cursor held, rc=5. Corrupt durable state → rc=2.
  A failed ACK stays `delivered` and retries; it is never recorded as sent.
- **Preservation across cutover**: the candidate reads the SAME state file format as the installed copy
  (`{ts, ids}`), and ADDS `ledger.json`, `quarantine.json`, `gaps.json`. An absent ledger is an empty
  ledger, so the first candidate tick inherits the installed cursor and replays nothing.
- **Cutover sequence** (proposed, not executed): 1. `gt mail inbox` count and last-seen recorded; 2. cron
  line commented out and the absence of a running tick confirmed; 3. current file copied to
  `gt-henry-watch.bak-<UTC>`; 4. candidate written by atomic rename; 5. checksum verified to equal
  `56e5bc35…`; 6. one manual run with output captured UNFILTERED to a file; 7. cron restored; 8. first
  natural tick observed.
- **Rollback — CORRECTED, my earlier claim was WRONG (Henry, C2 packet review).** I wrote that rollback
  is state-compatible and "loses nothing". False. The cursor advances INSIDE the delivery loop, BEFORE
  the ACK pass (`write_state` at the end of each delivered message; the ACK pass runs after the loop).
  So a **delivered-but-unacked** id sits BEHIND the watermark, and the previous adapter has no ledger,
  so it can neither see nor discharge that obligation: rolling back with obligations outstanding **drops
  them silently**. Preserved bytes are not recovery.
- **Rollback is therefore GATED, not promised.** `gt-henry-watch --preflight` is read-only and
  enumerates unresolved obligations:
  - `intent` — delivery unconfirmed, needs reconciliation;
  - `delivered` with an ACK owed — the ACK is behind the cursor.
  It exits 1 and prints `NOT SAFE TO ROLL BACK` while any exist, 0 only when quiesced. **Rollback
  proceeds only on a 0.** If obligations exist the options are: let the candidate run until it
  quiesces, then snapshot and roll back; or hand recovery to a separately reviewed owner. Neither is
  "restore the file and hope".
- **Proven in isolation, not asserted** (3 tests, both sabotages caught):
  - delivered-but-unacked: cursor demonstrably already past it, listed as an obligation, preflight refuses;
  - ambiguous intent: listed as an obligation, preflight refuses;
  - quiesced ledger: nothing owed, preflight permits.
- **Live preflight against the current installed state**: `cursor=2026-09-25T06:24:49.176Z ledger=0
  unresolved=0`, quiesced — because the installed copy keeps no ledger, which is exactly why a rollback
  AFTER the candidate has run is the case that needs the gate.

## 4. Proposed post-authorization proof (bounded, not yet run)

1. **Identity binding**: checksum of the installed file, and the process identity of the tick that runs it,
   captured from the log with its PID and the cron invocation, linked to the exact candidate commit.
2. **One real message end to end**: an exact source message id on `#dev` → the delivered Gastown mail bead
   id containing that id verbatim → the durable `intent` then `delivered` ledger entries → the exact ACK
   post id returned by the server → replay suppression on the following tick (no second mail, no second ACK).
3. **Failure and restart**, in an isolated harness rather than live: crash between mail and mark, source
   removed from the window, recovery scan reconciles by exact id, exactly one eventual ACK, terminal state.
4. **Delivery is not execution**: the proof asserts only that a message was delivered and acknowledged. It
   does NOT claim the Mayor acted on it. Worker execution evidence, where it matters, is the bead trail,
   not the ACK.
- **Allowed side effects**: channel reads; ONE receipt ACK per actionable message; Gastown mail to `mayor/`;
  a nudge to `mayor`; writes to the four runtime files. Nothing else.
- **Stop conditions**: any duplicate delivery, any ACK for a message not addressed to `agent:gas-new`, any
  advance past an unread row, any quarantine miss, or any write outside the four state files → revert
  immediately by the rollback above and report.
- **Evidence handling — CORRECTED (Henry, C2 packet review). Raw logs and runtime files are NOT
  published.** The log records message subjects and exception text, and the state files record message
  ids and correlation; none of it is mine to publish to a branch. Raw evidence stays LOCAL and
  protected at the paths below; what gets published is an allowlisted, sanitized extract: message ids,
  bead ids, ACK post ids, checksums, counts and pass/fail results, each checked for secrets and private
  content before it leaves this host.
- **Exact paths, enumerated**:
  - state `~/gt/.runtime/henry-watch.state` — cursor `{ts, ids}`
  - ledger `~/gt/.runtime/henry-watch.ledger.json` — per-id stage and ACK tuple
  - quarantine `~/gt/.runtime/henry-watch.quarantine.json` — refusals, reasons, NO payloads
  - gaps `~/gt/.runtime/henry-watch.gaps.json` — saturation records
  - lock `~/gt/.runtime/henry-watch.lock` — tick serialisation, zero bytes
  - log `~/gt/.runtime/henry-watch.log` — append only, LOCAL
- **Writes classified**: PERMITTED are the six paths above, one Gastown mail bead per delivered message
  to `mayor/`, one nudge to `mayor`, and one receipt ACK post per actionable message. ANY other write —
  to the repo, to another agent's mailbox, to the channel beyond receipt ACKs, or to any file outside
  those six — is UNEXPECTED and is a stop condition.

**Refresh before cutover**: this inventory is a snapshot of 2026-09-25T06:1xZ and will be re-run
immediately before any installation, because a new consumer could appear in between.

## Rollback-safety evidence (Henry C2 packet review, finding 1)

```
$ python3 -m unittest test_stepc   # 33 tests incl. 3 rollback-safety
Ran 33 tests in 0.030s

OK

sabotage: obligations always empty -> FAILED (failures=2)
sabotage: preflight never refuses -> FAILED (failures=2)
```

## Preflight fail-open, CORRECTED (Henry, C2 preflight review)

Henry's three probes certified 'rollback is state-safe' on malformed data. Reproduced exactly, then fixed to fail CLOSED:
```
{"x":{"stage":"delivery-pending"}}   before rc=0 (certified)   after rc=3 refused: unknown stage
{"x":{}}                             before rc=0 (certified)   after rc=3 refused: entry has no stage
[]                                   before rc=0 (certified)   after rc=3 refused: ledger is list, expected an object
```
validate_ledger() checks the ledger is an object, every key is a message id, every entry is an object with a stage in a FINITE domain (intent, delivered, acked, informational, quarantined), that stages requiring a ts carry one, and that an ack tuple is an object. Anything else raises and preflight returns 3.

Six regressions plus a NEGATIVE CONTROL (a valid quiesced ledger must still certify, so the validator does not simply refuse everything). Sabotage - validator accepts everything - FAILED (3 failures, 2 errors).

## Suite inventory, all three invocations (Henry p1910gg, reintroduced by me and now guarded)
```
direct   python3 test_stepc.py                      Ran 40 tests in 0.034s
module   python3 -m unittest test_stepc               Ran 40 tests in 0.034s
discover python3 -m unittest discover -p ...        Ran 40 tests in 0.034s
```
I reintroduced the defect by appending TestRollbackSafety after the runner. It is now asserted STRUCTURALLY: an AST check fails if any TestCase class is defined after the runner block. Verified it catches an appended class.

A subprocess test that re-invoked this file was written and REMOVED: it spawned itself recursively, 808 processes before I killed them by PID (pattern-killing had already killed my own shell). The AST check catches the real defect without executing anything.
