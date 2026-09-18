"""Offline tests for outcome checkers; data below is a protocol test double."""
import importlib.util
import pathlib
import tempfile
import unittest

SPEC = importlib.util.spec_from_file_location("checks", pathlib.Path(__file__).with_name("checks.py"))
checks = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(checks)


class ChecksTest(unittest.TestCase):
    def test_reminder_requires_new_enabled_correct_schedule_and_message(self):
        tr = {"started_at": "2026-09-17T12:00:00Z", "ended_at": "2026-09-17T12:01:00Z"}
        job = {"id": "sched-test", "type": "reminder", "enabled": True,
               "schedule": "0 9 * * *", "created_at": "2026-09-17T12:00:30Z",
               "reminder_config": {"message": "routing-reminder-7391: water the fern"}}
        self.assertTrue(checks.reminder_ok([job], tr))
        for key, value in [("enabled", False), ("schedule", "0 10 * * *"),
                           ("created_at", "2026-09-17T11:00:00Z"), ("type", "shell")]:
            bad = dict(job, **{key: value})
            self.assertFalse(checks.reminder_ok([bad], tr))
        self.assertFalse(checks.reminder_ok([job, job], tr))
        self.assertFalse(checks.reminder_ok([], tr))

    def test_media_requires_matching_successful_uncached_fetch(self):
        tr = {"routing": {"session_id": "conv-test"}, "tool_trace": []}
        event = {"topic": "tool.execution.complete", "payload": {
            "tool_name": "transcript_fetch", "success": True, "cached": False,
            "conversation_id": "conv-test"}}
        self.assertFalse(checks.media_evidence_ok(tr))
        tr["tool_trace"] = [event]
        self.assertFalse(checks.media_evidence_ok(tr))
        # Distinct session, dispatch conversation, thread and task-step IDs do
        # not establish a producer join. Neither equality nor arbitrary events pass.
        tr["routing"]["session_id"] = "session-test"
        for identity in ("session-test", "conv-test", "conv-test-thread-general-1234", "step-task-test-step-1", "conv-other"):
            event["payload"]["conversation_id"] = identity
            self.assertFalse(checks.media_evidence_ok(tr))
        for key, value in [("success", False), ("cached", True), ("conversation_id", "conv-other")]:
            tr["tool_trace"] = [dict(event, payload=dict(event["payload"], **{key: value}))]
            self.assertFalse(checks.media_evidence_ok(tr))

    def test_url_unit_test_must_execute_and_detect_broken_implementation(self):
        with tempfile.TemporaryDirectory() as root:
            root = pathlib.Path(root)
            (root / "url_value.py").write_text("def video_id(url):\n    return url.split('v=')[1]\n")
            test = root / "test_url_value.py"
            test.write_text("import unittest\nfrom url_value import video_id\nclass URLTest(unittest.TestCase):\n    def test_url(self):\n        self.assertEqual(video_id('https://www.youtube.com/watch?v=BaW_jenozKc'), 'BaW_jenozKc')\n")
            self.assertTrue(checks.url_test_ok(root))
            test.write_text("import unittest\nclass Empty(unittest.TestCase):\n    def test_nothing(self):\n        self.assertTrue(True)\n")
            self.assertFalse(checks.url_test_ok(root))
            test.write_text("# no test\n")
            self.assertFalse(checks.url_test_ok(root))


if __name__ == "__main__":
    unittest.main()
