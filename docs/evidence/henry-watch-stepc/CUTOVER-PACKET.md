# C2 cutover packet — Gastown intake adapter (gt-henry-watch)

READ-ONLY. Nothing here is installed, no canary has been sent, and no scheduler has been changed.
Prepared 2026-09-25 by the Gastown Mayor at Henry's request (wmi06n9). Henry owns packet review.
**Authorization to install or activate must come explicitly from Marc, for an exact scope. Henry's
source PASS and this packet grant none.**

## 1. Identity

| | |
|---|---|
| candidate commit | `27945966504e495494331d2e458cb65a41d9e586` (mtccressall/gastown, branch `polecat/mayor/stepc+henry-watch-evidence`) |
| candidate source path | `docs/evidence/henry-watch-stepc/watch_c.py` |
| candidate git blob | `012572013e13b3d5d04032aa1ada854a8b926328` |
| candidate content SHA-256 | `56e5bc351abbeb3f5193750ab56014154bb82bb058e0cd656c3fa4789abc4881` |
| candidate size | 526 lines |
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

### Unknowns, named rather than omitted
- **Henry-side consumers are not in my scope** and I have not enumerated them. If any Henry-side process
  also ACKs on `#dev` as `agent:gas-new`, that is a conflict I cannot see from here.
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
- **Rollback**: restore the backup by atomic rename and restore the cron line. **State-compatible**: the
  installed copy ignores the three new files and reads the same `{ts, ids}` state, so rollback loses no
  cursor and causes no replay. Pending intents and unsent ACKs recorded by the candidate would remain in
  `ledger.json`, unread by the old copy and still there if the candidate is reinstated — that is the one
  asymmetry, and it loses nothing.

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
- **Evidence paths**: `~/gt/.runtime/henry-watch.log` (unfiltered), the three state files, the delivered
  bead ids, and the ACK post ids, published to the evidence branch.

**Refresh before cutover**: this inventory is a snapshot of 2026-09-25T06:1xZ and will be re-run
immediately before any installation, because a new consumer could appear in between.
