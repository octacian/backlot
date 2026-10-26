"""Verify this recipe's actual exported application results, not just collection."""
import hashlib
import json
from pathlib import Path
import runpy
import sys
import zipfile


def verify(bundle, expected):
    root = Path(bundle)
    report = json.loads((root / "report.json").read_text())
    assert report["complete"] and not report["omissions"]
    assert report["result"]["instance_id"] == expected["instance"]
    assert report["result"]["attempt"] == expected["attempt"]
    assert report["result"].get("terminal_exit_code") == expected["terminal_exit"]
    assert report["result"]["cancelled"] == expected.get("cancelled", False)
    for entry in report["evidence"]:
        assert hashlib.sha256((root / entry["path"]).read_bytes()).hexdigest() == entry["sha256"]

    def artifact(name):
        entries = [e for e in report["evidence"] if e["kind"] == "artifact"
                   and e["component"] == "tests" and e["name"] == "reports"
                   and e["path"].endswith("/reports/" + name)]
        assert len(entries) == 1, name
        return root / entries[0]["path"]

    if expected.get("cancelled"):
        holding = json.loads(artifact("holding.json").read_text())
        assert holding["instance"] == expected["instance"] and holding["pid"] > 0
        formats = runpy.run_path(str(Path(__file__).resolve().parents[4] / "examples/ci-scenes/report-formats.py"))
        formats["verify_tree"](artifact("holding.json").parent)
        for entry in report["evidence"]:
            if entry["kind"] == "artifact" and entry["path"].endswith(".json"):
                json.loads((root / entry["path"]).read_text())
        if expected["base_url"]:
            invocation = json.loads(artifact("invocation.json").read_text())
            assert invocation == {"args": expected["args"], "baseURL": expected["base_url"]}
            traces = list(artifact("holding.json").parent.rglob("*.zip"))
            assert traces, "held browser produced no retained trace"
            for trace in traces:
                with zipfile.ZipFile(trace) as archive:
                    assert any(n.endswith(".trace") for n in archive.namelist())
        else:
            logs = [json.loads(line) for line in (root / "logs.ndjson").read_text().splitlines()]
            assert all(r["instance_id"] == expected["instance"] and r["attempt"] == expected["attempt"] for r in logs)
            output = "".join(r["message"] for r in logs if r["component"] == "tests" and r["stream"] == "stdout")
            events = [json.loads(line) for line in output.splitlines()]
            assert any(e.get("Test") == expected["test"] and e["Action"] == "run" for e in events)
        return  # Final framework reports may be absent; present media/archives were checked.

    if expected["base_url"]:
        invocation = json.loads(artifact("invocation.json").read_text())
        assert invocation == {"args": expected["args"], "baseURL": expected["base_url"]}
        browser = json.loads(artifact("playwright.json").read_text())

        def specs(suites):
            return [spec for suite in suites for spec in suite.get("specs", [])] + [
                spec for suite in suites for spec in specs(suite.get("suites", []))]

        selected = specs(browser["suites"])
        assert len(selected) == 1 and selected[0]["title"] == expected["test"]
        tests = selected[0]["tests"]
        assert len(tests) == 1 and len(tests[0]["results"]) == 1
        result = tests[0]["results"][0]
        assert result["status"] == ("failed" if expected["exit"] else "passed")
        stats = browser["stats"]
        assert [stats[k] for k in ["expected", "unexpected", "skipped", "flaky"]] == [
            1 - expected["exit"], expected["exit"], 0, 0]
        assert b"<html" in artifact("html/index.html").read_bytes().lower()
        attachments = {a["name"]: a for a in result["attachments"]}
        assert {"screenshot", "trace"} <= attachments.keys()
        for name in ["screenshot", "trace"]:
            relative = attachments[name]["path"].rsplit("/reports/", 1)[1]
            path = artifact(relative)
            if name == "trace":
                with zipfile.ZipFile(path) as archive:
                    assert any(n.endswith(".trace") for n in archive.namelist())
                    assert any(n.endswith(".network") for n in archive.namelist())
        formats = runpy.run_path(str(Path(__file__).resolve().parents[4] / "examples/ci-scenes/report-formats.py"))
        formats["verify_tree"](artifact("invocation.json").parent)
    else:
        assert json.loads(artifact("result.json").read_text()) == {"exit_code": expected["exit"]}
        logs = [json.loads(line) for line in (root / "logs.ndjson").read_text().splitlines()]
        output = "".join(r["message"] for r in logs if r["component"] == "tests" and r["stream"] == "stdout")
        assert all(r["instance_id"] == expected["instance"] and r["attempt"] == expected["attempt"] for r in logs)
        events = [json.loads(line) for line in output.splitlines()]
        selected = [e for e in events if e.get("Test")]
        assert {e["Test"] for e in selected} == {expected["test"]}
        actions = [e["Action"] for e in selected]
        assert actions.count("run") == 1 and "skip" not in actions
        assert actions.count("fail" if expected["exit"] else "pass") == 1


if __name__ == "__main__":
    verify(sys.argv[1], json.loads(sys.argv[2]))
