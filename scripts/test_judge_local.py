"""One-off test harness for scripts/judge-local.py (P2.2 verification).

Spawns a throwaway mock llama-server on a random localhost port, then runs
the judge script against it and asserts the contract:
  - correct endpoint (/v1/chat/completions), temperature==0 in the payload
  - stdin "<rubric>\\n---\\n<answer>" forwarded into the messages
  - stdout single line "<score> <rationale>"
Run: python3 scripts/test_judge_local.py
"""
import json
import subprocess
import sys
import threading
from http.server import BaseHTTPRequestHandler, HTTPServer

COMPLETION = "0.9 the answer matches the rubric criteria\n"
seen = {}


class Handler(BaseHTTPRequestHandler):
    def do_POST(self):
        seen["path"] = self.path
        body = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
        seen["body"] = body
        resp = json.dumps(
            {"choices": [{"message": {"content": COMPLETION}}]}
        ).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(resp)))
        self.end_headers()
        self.wfile.write(resp)

    def log_message(self, *a):
        pass


def main():
    srv = HTTPServer(("127.0.0.1", 0), Handler)
    port = srv.server_address[1]
    threading.Thread(target=srv.serve_forever, daemon=True).start()
    try:
        proc = subprocess.run(
            [sys.executable, "scripts/judge-local.py"],
            input="reply must be all lowercase\n---\nHELLO WORLD\n",
            capture_output=True,
            text=True,
            timeout=20,
            env={"JUDGE_URL": f"http://127.0.0.1:{port}", "PATH": "/usr/bin:/bin"},
        )
    finally:
        srv.shutdown()

    assert proc.returncode == 0, f"script failed: rc={proc.returncode} stderr={proc.stderr}"
    assert seen["path"] == "/v1/chat/completions", seen["path"]
    body = seen["body"]
    assert body["temperature"] == 0, body["temperature"]
    assert "Score strictly per rubric" in body["messages"][0]["content"]
    user = body["messages"][1]["content"]
    assert "reply must be all lowercase" in user and "HELLO WORLD" in user, user

    out = proc.stdout
    assert out.count("\n") == 1 and out.endswith("\n"), f"stdout not one line: {out!r}"
    first, _, rationale = out.strip().partition(" ")
    score = float(first)
    assert score == 0.9, score
    assert rationale == COMPLETION.strip().split(" ", 1)[1], rationale

    # Error path: server responds 500 -> script must exit 1 with stderr detail.
    class Fail(Handler):
        def do_POST(self):
            self.send_response(500)
            self.send_header("Content-Length", "5")
            self.end_headers()
            self.wfile.write(b"boom!")

    srv2 = HTTPServer(("127.0.0.1", 0), Fail)
    port2 = srv2.server_address[1]
    threading.Thread(target=srv2.serve_forever, daemon=True).start()
    try:
        proc2 = subprocess.run(
            [sys.executable, "scripts/judge-local.py"],
            input="r\n---\na\n",
            capture_output=True, text=True, timeout=20,
            env={"JUDGE_URL": f"http://127.0.0.1:{port2}", "PATH": "/usr/bin:/bin"},
        )
    finally:
        srv2.shutdown()
    assert proc2.returncode == 1, proc2.returncode
    assert "HTTP 500" in proc2.stderr, proc2.stderr
    assert proc2.stdout == "", proc2.stdout

    print("judge-local contract: PASS")
    print("stdout:", out.strip())
    print("payload temperature:", body["temperature"], "| model:", body["model"])
    print("HTTP-500 path exits 1 with stderr detail: PASS")


if __name__ == "__main__":
    main()
