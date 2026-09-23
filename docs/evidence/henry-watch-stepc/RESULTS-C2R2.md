# C2 round 2: Henry's independent step C findings, as rejection regressions

Generated 2026-09-23T05:59:20Z. NOT INSTALLED. Live adapter unchanged: sha256 8f386008ac3c300dcf4bfe34820825b5634cf2471ceae0408f26ddb79a01e76c

candidate sha256 f6ef16ad5ab15ad746492608f79a0fd039d7354858ef68926f69b59df53c0891
suite     sha256 d213eef6fc60b1816cc4923b1236bc752d466cd847b66208e5d37c6f6102183e  — 22 tests (10 step C, 8 C2 allowlist/quarantine, 4 Henry findings)

## Henry's four findings (a209a305) and their regressions

| finding | disposition | regression |
|---|---|---|
| (1) foreign-recipient chatter delivered/ACKed | FIXED in C2 by targeting-first classification | test_foreign_recipient_has_no_effect_even_from_henry |
| (2) HTTP 500 / application errors marked ACK success | FIXED: check_post_envelope rejects errors/error keys and envelopes with neither data nor operation; urlopen still raises on 5xx | test_application_error_envelope_does_not_count_as_a_sent_ack (drives the REAL post_ack via a faked urlopen) |
| (3) failed ACK lost when the source leaves the poll window | FIXED: the ACK payload is persisted in the ledger and retries iterate the LEDGER, not the window | test_pending_ack_survives_the_message_leaving_the_poll_window |
| (4) substring inbox match falsely reconciles | FIXED: exact whole-line 'message id: <id>' match over parsed JSON rows; an unparseable listing raises instead of reconciling | test_reconcile_requires_an_exact_id_match |
| (5) saturated polls must preserve the cursor and record a bounded visible gap | FIXED: record_gap writes a durable bounded record and the log says coverage is INCOMPLETE; the watermark is never advanced past an unread row | test_saturated_window_records_a_visible_gap_and_never_claims_drained |

## RED against the installed adapter
```
ERROR: test_quarantine_records_carry_no_payload
ERROR: test_quarantine_survives_restart_and_is_bounded
ERROR: test_application_error_envelope_does_not_count_as_a_sent_ack
ERROR: test_reconcile_requires_an_exact_id_match
ERROR: test_saturated_window_records_a_visible_gap_and_never_claims_drained
ERROR: test_corrupt_state_is_not_read_as_first_run
FAIL: test_foreign_recipient_has_no_effect_even_from_henry
FAIL: test_only_allowlisted_kinds_become_actionable
FAIL: test_quarantine_is_never_dispatched_on_a_later_tick
FAIL: test_status_and_ack_do_not_create_work_or_ack_loops
FAIL: test_unknown_identity_is_quarantined_not_delivered
FAIL: test_unsupported_kind_from_a_trusted_sender_is_quarantined
FAIL: test_pending_ack_survives_the_message_leaving_the_poll_window
FAIL: test_failed_ack_is_not_recorded_as_sent_and_retries
FAIL: test_receipt_ack_is_posted_after_persistence
FAIL: test_restart_replay_does_not_duplicate_the_inbox_write
Ran 22 tests in 0.030s
FAILED (failures=10, errors=6)
```

## GREEN on the candidate
```
Ran 22 tests in 0.046s

OK
```

## Sabotage controls for the four repairs
```
$ validator accepts every envelope           -> FAILED (failures=1)
$ retry ACKs from the poll window only       -> FAILED (failures=1)
$ reconcile on a substring match             -> FAILED (failures=1)
$ do not record the saturation gap           -> FAILED (failures=1)
```
