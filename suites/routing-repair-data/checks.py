"""Strict suite-local outcome probes; never read newest/global run artifacts.

MEEPT_ROUTING_RUN_DIR must name this invocation's fresh absolute --out path.
MEEPT_ROUTING_JOBS_FILE must name the scratch daemon's jobs.json (no fallback).
"""
import datetime
import json
import os
import pathlib
import re
import shutil
import subprocess
import sys
import tempfile


def stamp(value):
    return datetime.datetime.fromisoformat(value.replace("Z", "+00:00"))


def reminder_ok(jobs, transcript):
    start, end = stamp(transcript["started_at"]), stamp(transcript["ended_at"])
    matches = []
    for job in jobs:
        if (job.get("type") == "reminder" and job.get("enabled") is True
                and job.get("schedule") in ("0 9 * * *", "0 0 9 * * *")
                and job.get("id", "").startswith("sched-")
                and job.get("reminder_config", {}).get("message") == "routing-reminder-7391: water the fern"
                and start <= stamp(job["created_at"]) <= end):
            matches.append(job)
    return len(matches) == 1


def media_evidence_ok(transcript):
    # recordDispatch stores the handler conversation ID as session_id. Tool
    # events instead use the executing loop's accounting conversation, possibly
    # a routed thread or step-task ID. The wire has no validated join from that
    # loop to this turn, nor the fetched URL/content for transcript_fetch.
    # Equality can happen for direct execution, but is not ingestion proof.
    return False


def url_test_ok(root):
    root = pathlib.Path(root)
    text = (root / "test_url_value.py").read_text()
    if "https://www.youtube.com/watch?v=BaW_jenozKc" not in text:
        return False
    # Execute only these two copied files, not repo-wide tests; require real
    # tests passing and the same tests failing with an always-wrong mutant.
    # The checker never alters the attempt's files.
    with tempfile.TemporaryDirectory(prefix="routing-unit-check-") as tmp:
        tmp = pathlib.Path(tmp)
        for name in ("url_value.py", "test_url_value.py"):
            shutil.copyfile(root / name, tmp / name)
        def run():
            return subprocess.run([sys.executable, "-B", "-m", "unittest", "-v", "test_url_value"],
                                  cwd=tmp, capture_output=True, text=True, timeout=30)
        good = run()
        if good.returncode != 0 or not re.search(r"Ran [1-9][0-9]* tests?", good.stderr):
            return False
        (tmp / "url_value.py").write_text("def video_id(url):\n    return 'WRONG_VIDEO_ID'\n")
        bad = run()
        return bad.returncode != 0 and "FAIL:" in bad.stderr


def current_transcript(case):
    out = pathlib.Path(os.environ["MEEPT_ROUTING_RUN_DIR"])
    if not out.is_absolute():
        raise ValueError("MEEPT_ROUTING_RUN_DIR must be absolute")
    # Worktree basename is the same name the runner uses for its transcript.
    name = pathlib.Path.cwd().name
    prefix = f"routing-repair-{case}-a"
    if not name.startswith(prefix) or not name[len(prefix):].isdigit():
        raise ValueError("checker worktree does not match case/attempt")
    tr = json.loads((out / "transcripts" / (name + ".json")).read_text())
    if tr.get("suite") != "routing-repair" or tr.get("task_id") != case or tr.get("attempt") != int(name[len(prefix):]):
        raise ValueError("transcript identity mismatch")
    return tr


def main():
    mode = sys.argv[1]
    if mode == "url-test":
        passed = url_test_ok(pathlib.Path.cwd())
    elif mode == "reminder":
        jobs_path = pathlib.Path(os.environ["MEEPT_ROUTING_JOBS_FILE"])
        if not jobs_path.is_absolute():
            raise ValueError("MEEPT_ROUTING_JOBS_FILE must be absolute")
        passed = reminder_ok(json.loads(jobs_path.read_text())["jobs"], current_transcript("timed-reminder-control"))
    elif mode == "media":
        print("media: unsupported producer evidence: missing turn/step/thread and requested-URL/content association")
        return 1
    elif mode == "git-status":
        actual = subprocess.check_output(["git", "status", "--porcelain", "--untracked-files=all"], text=True)
        expected = sorted(line for line in actual.splitlines() if not line.endswith(" routing-status.txt"))
        saved = sorted(pathlib.Path("routing-status.txt").read_text().splitlines())
        passed = bool(expected) and saved == expected and any(line.endswith(" routing-git-note.txt") for line in saved)
    else:
        raise ValueError("unknown checker mode")
    print(f"{mode}: {'pass' if passed else 'required evidence absent or wrong'}")
    return 0 if passed else 1


if __name__ == "__main__":
    try:
        sys.exit(main())
    except (KeyError, ValueError, OSError, subprocess.SubprocessError) as exc:
        print(f"outcome unavailable: {exc}")
        sys.exit(1)
