import hashlib
import json
from pathlib import Path
import tempfile
import unittest

from verify import verify


class GoReportTests(unittest.TestCase):
    def test_cancelled_go_accepts_missing_final_report_but_requires_held_run(self):
        for mode in ["held", "wrong-instance", "not-running"]:
            with self.subTest(mode=mode), tempfile.TemporaryDirectory() as directory:
                root = Path(directory)
                event = {"Test": "TestHeld", "Action": "run" if mode != "not-running" else "skip"}
                log = {"instance_id": "instance", "attempt": "attempt", "component": "tests",
                       "stream": "stdout", "message": json.dumps(event) + "\n"}
                files = {"logs.ndjson": log, "artifacts/0/reports/holding.json": {
                    "instance": "foreign" if mode == "wrong-instance" else "instance", "pid": 123}}
                report = {"complete": True, "omissions": [], "result": {
                    "instance_id": "instance", "attempt": "attempt", "cancelled": True}, "evidence": []}
                for name, value in files.items():
                    path = root / name
                    path.parent.mkdir(parents=True, exist_ok=True)
                    path.write_text(json.dumps(value) + "\n")
                    report["evidence"].append({"path": name, "kind": "log" if name == "logs.ndjson" else "artifact",
                                               "component": "tests", "name": "reports",
                                               "sha256": hashlib.sha256(path.read_bytes()).hexdigest()})
                (root / "report.json").write_text(json.dumps(report))
                expected = {"instance": "instance", "attempt": "attempt", "terminal_exit": None,
                            "base_url": "", "test": "TestHeld", "cancelled": True}
                if mode == "held":
                    verify(root, expected)
                else:
                    with self.assertRaises(AssertionError):
                        verify(root, expected)

    def test_selected_test_passes_but_skip_missing_or_wrong_exit_rejects(self):
        for mode in ["pass", "skip", "no-tests", "missing-report", "wrong-exit"]:
            with self.subTest(mode=mode), tempfile.TemporaryDirectory() as directory:
                root = Path(directory)
                report = {"complete": True, "omissions": [], "result": {
                    "instance_id": "instance", "attempt": "attempt", "terminal_exit_code": 0, "cancelled": False}, "evidence": []}
                events = [{"Test": "TestDatabase", "Action": "run"},
                          {"Test": "TestDatabase", "Action": "skip" if mode == "skip" else "pass"}]
                if mode == "no-tests":
                    events = []
                output = "".join(json.dumps(event) + "\n" for event in events)
                log = {"instance_id": "instance", "attempt": "attempt", "component": "tests",
                       "stream": "stdout", "message": output}
                for name, value, kind in [("logs.ndjson", log, "log"),
                                          ("artifacts/0/reports/result.json", {"exit_code": int(mode == "wrong-exit")}, "artifact")]:
                    path = root / name
                    path.parent.mkdir(parents=True, exist_ok=True)
                    path.write_text(json.dumps(value) + "\n")
                    report["evidence"].append({"path": name, "kind": kind, "component": "tests",
                                               "name": "reports", "sha256": hashlib.sha256(path.read_bytes()).hexdigest()})
                (root / "report.json").write_text(json.dumps(report))
                if mode == "missing-report":
                    (root / "artifacts/0/reports/result.json").unlink()
                expected = {"instance": "instance", "attempt": "attempt", "terminal_exit": 0,
                            "base_url": "", "test": "TestDatabase", "exit": 0}
                if mode == "pass":
                    verify(root, expected)
                else:
                    with self.assertRaises((AssertionError, FileNotFoundError)):
                        verify(root, expected)


if __name__ == "__main__":
    unittest.main()
