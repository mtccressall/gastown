# C2 round 3: Henry's C2R2 adjudication findings (ekqqzhw)

Generated 2026-09-23T06:18:10Z. NOT INSTALLED. Live adapter unchanged: sha256 8f386008ac3c300dcf4bfe34820825b5634cf2471ceae0408f26ddb79a01e76c

candidate sha256 578f2f144350459fd3abfbb4d9a6d5474bcfb0178073d45486a335c2c056bea9
suite sha256 8e29591aa02818a382e188207d348a03ef4a31fd7fb56ed512573134fb71c32a — 28 tests

| finding | repair | regression |
|---|---|---|
| (1) 200 poll with errors + data still dispatched | check_poll_envelope rejects any errors key, or data without messages; poll failure returns rc=6, holds the cursor, dispatches nothing | test_poll_envelope_with_errors_is_not_processed (drives the REAL poll via faked urlopen) |
| (1b) ACK 200 with data={} accepted | check_post_envelope now requires the server-assigned id back | test_ack_response_must_carry_the_posted_identity |
| (2) missing recipient + requiresAck=false delivered/ACKed | actionable requires the EXACT protected recipient agent:gas-new; an alias or absent recipient is informational only; receipt ACK requires literal requiresAck true | test_missing_recipient_is_not_actionable, test_receipt_ack_requires_literal_requires_ack_true |
| (3) gapped window recorded a gap but advanced and dispatched 100 | a saturated window past the mark records the gap, logs BLOCKED, returns rc=4 and dispatches nothing; the cursor is held | test_saturated_window_blocks_delivery_and_holds_the_cursor (100 valid second-spaced rows, per your parent's corrected fixture) |
| (4) quarantine capacity failure still marked handled and advanced | quarantine raises QuarantineUnavailable; the tick logs, returns rc=5 and holds the cursor | test_quarantine_write_failure_fails_closed |

Your correction accepted: a fabricated HTTP 500 response object is not normal urllib behaviour, and a real HTTPError already raises. The repair is about APPLICATION-level envelopes, not synthetic 500s.

## RED against the installed adapter
```
ERROR: test_quarantine_records_carry_no_payload
ERROR: test_quarantine_survives_restart_and_is_bounded
ERROR: test_application_error_envelope_does_not_count_as_a_sent_ack
ERROR: test_reconcile_requires_an_exact_id_match
ERROR: test_saturated_window_records_a_visible_gap_and_never_claims_drained
ERROR: test_ack_response_must_carry_the_posted_identity
ERROR: test_missing_recipient_is_not_actionable
ERROR: test_quarantine_write_failure_fails_closed
ERROR: test_corrupt_state_is_not_read_as_first_run
FAIL: test_foreign_recipient_has_no_effect_even_from_henry
FAIL: test_only_allowlisted_kinds_become_actionable
FAIL: test_quarantine_is_never_dispatched_on_a_later_tick
FAIL: test_status_and_ack_do_not_create_work_or_ack_loops
FAIL: test_unknown_identity_is_quarantined_not_delivered
FAIL: test_unsupported_kind_from_a_trusted_sender_is_quarantined
FAIL: test_pending_ack_survives_the_message_leaving_the_poll_window
FAIL: test_poll_envelope_with_errors_is_not_processed
FAIL: test_saturated_window_blocks_delivery_and_holds_the_cursor
FAIL: test_failed_ack_is_not_recorded_as_sent_and_retries
FAIL: test_receipt_ack_is_posted_after_persistence
FAIL: test_restart_replay_does_not_duplicate_the_inbox_write
Ran 28 tests in 0.034s
FAILED (failures=12, errors=9)
```

## GREEN on the candidate
```
Ran 28 tests in 0.029s

OK
```

## Sabotage controls
```
$ skip poll-envelope validation              -> FAILED (failures=1)
$ accept a post with no stored id            -> FAILED (failures=1)
$ ignore the protected recipient rule        -> FAILED (failures=1)
$ ACK regardless of requiresAck              -> FAILED (failures=1)
$ dispatch from a gapped window              -> FAILED (failures=1)
$ proceed when quarantine cannot record      -> FAILED (failures=1)
```
