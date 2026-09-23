# C2 evidence: actionable allowlist + visible quarantine (Henry 2650a920)

Generated 2026-09-23T05:47:38Z. NOT INSTALLED. Live adapter unchanged at sha256 8f386008ac3c300dcf4bfe34820825b5634cf2471ceae0408f26ddb79a01e76c.

candidate watch_c.py sha256 0088e687fe481b56f775815ab8d0e71fdbb866aa74cd6acd25297e37e6f3b064
suite     test_stepc.py sha256 52a0bc8d75480fc6ddb1bff5183c7a82f600b3cf06956469174d5cb45510afb2  (18 tests: 10 step C + 8 C2)

## RED: the 18-test suite against the INSTALLED adapter
```
ERROR: test_quarantine_records_carry_no_payload
ERROR: test_quarantine_survives_restart_and_is_bounded
ERROR: test_corrupt_state_is_not_read_as_first_run
FAIL: test_foreign_recipient_has_no_effect_even_from_henry
FAIL: test_only_allowlisted_kinds_become_actionable
FAIL: test_quarantine_is_never_dispatched_on_a_later_tick
FAIL: test_status_and_ack_do_not_create_work_or_ack_loops
FAIL: test_unknown_identity_is_quarantined_not_delivered
FAIL: test_unsupported_kind_from_a_trusted_sender_is_quarantined
FAIL: test_failed_ack_is_not_recorded_as_sent_and_retries
FAIL: test_receipt_ack_is_posted_after_persistence
FAIL: test_restart_replay_does_not_duplicate_the_inbox_write
Ran 18 tests in 0.024s
FAILED (failures=9, errors=3)
```

## GREEN: same suite against the candidate
```
----------------------------------------------------------------------
Ran 18 tests in 0.022s

OK
```

## Sabotage controls (C2 guards)
```
$ remove the kind allowlist                    -> FAILED (failures=1)
$ trust any authenticated sender               -> FAILED (failures=3)
$ skip the quarantine record                   -> FAILED (failures=3)
$ store the payload in quarantine              -> FAILED (failures=1)
$ ignore recipient targeting                   -> FAILED (failures=1)
$ drop the [FYI] marking                       -> FAILED (failures=1)
```

NOT CAUGHT, disclosed: removing the ledger mark on a quarantined id does NOT fail the suite, because classification already refuses it on every later tick. That mark is an audit trail, not the guard. The guard is demonstrated by the trust-any-sender and allowlist rows above.
