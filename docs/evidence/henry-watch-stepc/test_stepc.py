"""Step C RED suite for the Henry->Mayor intake adapter (v2 section 5C).

Every test drives the module's own main() with the network, the mail command and
the local model replaced. Nothing here touches the live state, the live log, the
live channel or the installed script: HOME is redirected to a temp dir.

Cases required by v2 5C: restart replay, duplicate request, failed ACK, corrupt
state, disk-write failure, foreign recipient, malformed metadata, ACK loop.
"""
import importlib.machinery
import importlib.util
import json
import os
import shutil
import tempfile
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
TARGET = os.environ.get("STEPC_TARGET", os.path.join(HERE, "watch_c.py"))


def load(home):
    """Load the adapter with HOME redirected, so RUNTIME/STATE/LOCK are temp."""
    os.environ["HOME"] = home
    ld = importlib.machinery.SourceFileLoader("watch_c_" + os.path.basename(home), TARGET)
    spec = importlib.util.spec_from_loader(ld.name, ld)
    m = importlib.util.module_from_spec(spec)
    ld.exec_module(m)
    m.summarize = lambda text: "SUMMARY"
    m.load_env = lambda: {"LIVEOP_API_URL": "http://test.invalid", "LIVEOP_API_KEY": "k"}
    return m


def msg(mid, ts, auth="agent:henry", recipient=None, kind="review-handoff", content="body", meta=True):
    md = {"authenticatedAgentId": auth, "kind": kind}
    if recipient:
        md["recipient"] = recipient
    return {"id": mid, "timestamp": ts, "sender": "henry", "content": content,
            "replyTo": None, "isDeleted": False, "metadata": md if meta else None}


class Harness(unittest.TestCase):
    def setUp(self):
        self.home = tempfile.mkdtemp(prefix="stepc-home-")
        os.makedirs(os.path.join(self.home, "gt", ".runtime"), exist_ok=True)
        self.real_home = os.environ.get("HOME")
        self.m = load(self.home)
        self.mail = []          # subjects handed to `gt mail send`
        self.posts = []         # what the adapter posted back to the channel
        self.window = []
        self.fail_mail = False
        self.fail_post = False
        self.fail_state_write = False

        def fake_run(cmd):
            if cmd[:3] == ["gt", "mail", "send"]:
                if self.fail_mail:
                    raise RuntimeError("mail send failed")
                self.mail.append(cmd[4])
            return ""

        self.m.run = fake_run
        self.m.poll = lambda env: list(self.window)
        # post_ack may not exist yet (that is the point of the RED suite)
        if hasattr(self.m, "post_ack"):
            real = self.m.post_ack

            def fake_post(env, message):
                if self.fail_post:
                    raise RuntimeError("post failed")
                self.posts.append(message["id"])
            self.m.post_ack = fake_post
        if self.fail_state_write:
            pass

    def tearDown(self):
        if self.real_home:
            os.environ["HOME"] = self.real_home
        shutil.rmtree(self.home, ignore_errors=True)

    def seed(self, ts="2026-09-23T00:00:00.000Z", ids=()):
        self.m.write_state(ts, list(ids))

    def run_tick(self):
        return self.m.main()


class TestIntake(Harness):
    # ---- controls that must already pass -------------------------------
    def test_foreign_recipient_creates_no_work(self):
        self.seed()
        self.window = [msg("m1", "2026-09-23T01:00:00.000Z", auth="agent:ven", recipient="casey")]
        self.run_tick()
        self.assertEqual(self.mail, [], "a message addressed to another agent must not be delivered")

    def test_malformed_metadata_does_not_crash_and_creates_no_work(self):
        self.seed()
        self.window = [
            msg("m1", "2026-09-23T01:00:00.000Z", meta=False),
            {"id": "m2", "timestamp": "2026-09-23T01:00:01.000Z", "sender": "x",
             "content": "c", "metadata": "{not json"},
            {"id": "m3", "timestamp": "2026-09-23T01:00:02.000Z", "sender": "x",
             "content": "c", "metadata": ["list", "not", "object"]},
        ]
        self.run_tick()
        self.assertEqual(self.mail, [], "malformed metadata must be quarantined, not delivered")

    def test_own_post_is_never_ingested_ack_loop(self):
        self.seed()
        self.window = [msg("m1", "2026-09-23T01:00:00.000Z", auth="agent:gas-new",
                           recipient="gastown", kind="ACK")]
        self.run_tick()
        self.assertEqual(self.mail, [], "our own ACK must not create work (ACK loop)")

    def test_idle_tick_makes_no_model_call(self):
        """v2 qualification: no LLM inference when there is no work."""
        calls = []
        self.m.summarize = lambda t: calls.append(t) or "S"
        self.seed(ts="2026-09-23T09:00:00.000Z")
        self.window = [msg("old", "2026-09-23T08:00:00.000Z")]   # older than the mark
        self.run_tick()
        self.assertEqual(calls, [], "an idle tick must not call the local model")
        self.assertEqual(self.mail, [])

    # ---- the RED set ---------------------------------------------------
    def test_receipt_ack_is_posted_after_persistence(self):
        self.seed()
        self.window = [msg("m1", "2026-09-23T01:00:00.000Z")]
        self.run_tick()
        self.assertEqual(len(self.mail), 1, "message should be persisted to the inbox")
        self.assertTrue(hasattr(self.m, "post_ack"), "adapter must have a receipt-ACK path")
        self.assertEqual(self.posts, ["m1"], "a durable receipt ACK must be posted for m1")

    def test_failed_ack_is_not_recorded_as_sent_and_retries(self):
        self.seed()
        self.window = [msg("m1", "2026-09-23T01:00:00.000Z")]
        self.fail_post = True
        self.run_tick()
        self.fail_post = False
        self.posts.clear()
        self.run_tick()   # second natural tick
        self.assertEqual(self.posts, ["m1"], "a failed ACK must be retried on the next tick")
        self.assertEqual(len(self.mail), 1, "retrying the ACK must not re-deliver the message")

    def test_restart_replay_does_not_duplicate_the_inbox_write(self):
        self.seed()
        self.window = [msg("m1", "2026-09-23T01:00:00.000Z")]
        self.fail_state_write = True
        orig = self.m.write_state

        def boom(ts, ids):
            raise OSError("disk full")
        self.m.write_state = boom
        try:
            self.run_tick()      # crashes after the mail is sent
        except Exception:
            pass
        self.m.write_state = orig
        n_after_crash = len(self.mail)
        self.run_tick()          # restart: same message still in the window
        self.assertEqual(len(self.mail), n_after_crash,
                         "a crash between the inbox write and the state write must not "
                         "produce a second inbox write for the same message id")

    def test_duplicate_delivery_of_same_id_is_idempotent(self):
        self.seed()
        self.window = [msg("m1", "2026-09-23T01:00:00.000Z")]
        self.run_tick()
        self.window = [msg("m1", "2026-09-23T01:00:00.000Z"),
                       msg("m2", "2026-09-23T01:00:00.000Z")]  # identical timestamp
        self.run_tick()
        self.assertEqual(len(self.mail), 2, "m1 must not be delivered twice; m2 must be delivered once")

    def test_corrupt_state_is_not_read_as_first_run(self):
        open(self.m.STATE, "w").write("{ this is not json")
        self.window = [msg("m1", "2026-09-23T01:00:00.000Z")]
        rc = self.run_tick()
        self.assertEqual(self.mail, [], "corrupt state must stop the tick, not replay history")
        self.assertNotEqual(rc, 0, "a corrupt state file must fail loudly")

    def test_disk_write_failure_does_not_ack(self):
        self.seed()
        self.window = [msg("m1", "2026-09-23T01:00:00.000Z")]
        self.m.write_state = lambda ts, ids: (_ for _ in ()).throw(OSError("read-only fs"))
        try:
            self.run_tick()
        except Exception:
            pass
        self.assertEqual(self.posts, [], "no ACK may be posted when durable state could not be written")


if __name__ == "__main__":
    unittest.main(verbosity=2)
