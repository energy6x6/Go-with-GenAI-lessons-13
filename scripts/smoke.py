#!/usr/bin/env python3
"""Exercise local gRPC, HTTP, JWT and mTLS; save all artifacts outside the repository."""

import argparse
import json
import os
from pathlib import Path
import signal
import socket
import subprocess
import tempfile
import time
import urllib.error
import urllib.request

ROOT = Path(__file__).resolve().parents[1]


class Demo:
    def __init__(self, evidence):
        self.evidence = evidence
        self.bin = evidence / "bin"
        self.processes = []
        self.files = []
        self.results = []
        self.flags = []

    def run(self, command, expected=0):
        result = subprocess.run(command, cwd=ROOT, capture_output=True, text=True, timeout=30)
        output = result.stdout + result.stderr
        if result.returncode != expected:
            raise AssertionError(f"exit={result.returncode}, expected={expected}: {output}")
        return output

    def client(self, *arguments, expected=0):
        return self.run([str(self.bin / "inventory-client"), "-addr", "127.0.0.1:50051", *self.flags, *arguments], expected)

    def start(self, name, label, *arguments):
        output = (self.evidence / f"{label}.log").open("w")
        self.files.append(output)
        process = subprocess.Popen([str(self.bin / name), *arguments], cwd=ROOT, stdout=output, stderr=output,
                                   env=dict(os.environ, GRPC_GO_LOG_SEVERITY_LEVEL="info"))
        self.processes.append(process)
        return process

    def stop(self):
        for process in reversed(self.processes):
            if process.poll() is None:
                process.terminate()
                try:
                    process.wait(timeout=7)
                except subprocess.TimeoutExpired:
                    process.kill()
                    process.wait(timeout=2)
        for output in self.files:
            output.close()
        self.processes.clear()
        self.files.clear()

    @staticmethod
    def wait_for(operation):
        deadline = time.monotonic() + 15
        while True:
            try:
                return operation()
            except (AssertionError, urllib.error.URLError, TimeoutError):
                if time.monotonic() >= deadline:
                    raise
                time.sleep(0.05)

    @staticmethod
    def http(path, method="GET", body=None):
        request = urllib.request.Request("http://127.0.0.1:8080" + path, method=method,
                                         data=None if body is None else json.dumps(body).encode(),
                                         headers={"Content-Type": "application/json"})
        try:
            with urllib.request.urlopen(request, timeout=5) as response:
                return response.status, json.load(response)
        except urllib.error.HTTPError as error:
            return error.code, json.load(error)

    def check(self, name, operation):
        if operation() is False:
            raise AssertionError(name)
        self.results.append({"scenario": name, "status": "PASS"})
        print("PASS " + name, flush=True)

    def wait_output(self, path, needle):
        def contains():
            if needle not in path.read_text():
                raise AssertionError("missing output: " + needle)
        self.wait_for(contains)

    def live_updates(self):
        process = self.start("inventory-client", "availability-stream", "-addr", "127.0.0.1:50051", "-max", "3", "watch-availability", "book-1")
        path = self.evidence / "availability-stream.log"
        self.wait_output(path, "revision=3")
        self.client("set-status", "copy-1", "LOST")
        self.wait_output(path, "revision=4")
        self.client("register", "book-1", "STREAM-NEW")
        if process.wait(timeout=5) != 0:
            return False
        output = path.read_text()
        return all(value in output for value in ["total=2 available=1 borrowed=1 lost=0 revision=3",
                   "total=2 available=0 borrowed=1 lost=1 revision=4",
                   "total=3 available=1 borrowed=1 lost=1 revision=5", "io.EOF"])

    def cancel_watch(self):
        process = self.start("inventory-client", "availability-cancel", "-addr", "127.0.0.1:50051", "watch-availability", "book-1")
        path = self.evidence / "availability-cancel.log"
        self.wait_output(path, "book=book-1")
        process.send_signal(signal.SIGINT)
        return process.wait(timeout=5) == 0 and "Ctrl+C" in path.read_text()

    def basic(self):
        self.start("inventory-server", "plain-server", "-addr", "127.0.0.1:50051")
        self.wait_for(lambda: self.client("get", "copy-1"))
        self.start("gateway", "plain-gateway", "-http-addr", "127.0.0.1:8080", "-grpc-addr", "127.0.0.1:50051")
        self.wait_for(lambda: self.http("/v1/copies/copy-1"))
        self.check("unary GetCopy", lambda: json.loads(self.client("get", "copy-1"))["status"] == "AVAILABLE")
        self.check("unknown copy -> NotFound", lambda: "code=NotFound" in self.client("get", "missing", expected=1))
        self.check("own unary seeded counts", lambda: json.loads(self.client("availability", "book-1"))["availability"] ==
                   {"book_id": "book-1", "total": 2, "available": 1, "borrowed": 1, "lost": 0, "revision": "3"})
        self.check("own unary empty -> InvalidArgument", lambda: "code=InvalidArgument" in self.client("availability", "", expected=1))
        self.check("own unary unknown -> NotFound", lambda: "code=NotFound" in self.client("availability", "missing", expected=1))
        self.check("own stream status + registration + EOF", self.live_updates)
        self.check("own stream Ctrl+C", self.cancel_watch)
        self.check("own stream deadline", lambda: "code=DeadlineExceeded" in self.client("-stream-timeout", "100ms", "watch-availability", "book-1", expected=1))
        self.check("CLI rejects negative max", lambda: "max must be" in self.client("-max", "-1", "watch-availability", "book-1", expected=1))
        self.check("original server stream EOF", lambda: "io.EOF" in self.client("-max", "1", "watch", "copy-1"))
        self.client("register", "smoke-book", "SMOKE-ONE")
        self.check("duplicate -> AlreadyExists", lambda: "code=AlreadyExists" in self.client("register", "smoke-book", "SMOKE-ONE", expected=1))
        self.check("client stream registers batch", lambda: json.loads(self.client("bulk", "bulk-book", "3"))["registered"] == 3)
        self.check("client stream duplicate batch", lambda: json.loads(self.client("bulk", "bulk-book", "3"))["failed"] == 3)
        self.check("bidi accepts and rejects events", lambda: "applied=true" in (output := self.client("sync")) and "applied=false" in output)
        self.check("REST GetCopy", lambda: self.http("/v1/copies/copy-1")[0] == 200)
        self.check("REST unknown -> 404", lambda: self.http("/v1/copies/missing")[0] == 404)
        self.check("REST invalid status -> 400", lambda: self.http("/v1/copies/copy-1/status", "PATCH", {"status": "STATUS_UNSPECIFIED"})[0] == 400)
        self.check("own REST equals native gRPC", lambda: self.http("/v1/books/book-1/availability")[1] == json.loads(self.client("availability", "book-1")))
        self.check("own REST unknown -> 404", lambda: self.http("/v1/books/missing/availability")[0] == 404)
        self.stop()

    def secure(self):
        certs = self.evidence / "certs"
        self.run([str(self.bin / "gencerts"), "-dir", str(certs)])
        reader = self.run([str(self.bin / "mint-token"), "-role", "reader"]).strip()
        librarian = self.run([str(self.bin / "mint-token"), "-role", "librarian"]).strip()
        expired = self.run([str(self.bin / "mint-token"), "-ttl=-1m"]).strip()
        self.flags = ["-tls", "-cert-dir", str(certs), "-token", reader]
        self.start("inventory-server", "secure-server", "-addr", "127.0.0.1:50051", "-tls", "-auth", "-cert-dir", str(certs))
        self.wait_for(lambda: self.client("get", "copy-1"))
        self.check("mTLS reader unary", lambda: '"total":' in self.client("availability", "book-1"))
        self.check("mTLS reader stream", lambda: "io.EOF" in self.client("-max", "1", "watch-availability", "book-1"))
        self.check("reader cannot register", lambda: "code=PermissionDenied" in self.client("register", "book-1", "DENIED", expected=1))
        self.check("reader cannot stream writes", lambda: "code=PermissionDenied" in self.client("bulk", "book-1", "1", expected=1))
        self.check("missing token unary", lambda: "code=Unauthenticated" in self.client("-token", "", "availability", "book-1", expected=1))
        self.check("missing token stream", lambda: "code=Unauthenticated" in self.client("-token", "", "-max", "1", "watch-availability", "book-1", expected=1))
        self.check("expired token", lambda: "code=Unauthenticated" in self.client("-token", expired, "availability", "book-1", expected=1))
        self.check("librarian registers", lambda: '"id":' in self.client("-token", librarian, "register", "book-1", "SECURE-NEW"))
        self.check("mTLS requires client certificate", lambda: "code=Unavailable" in self.client("-client-cert", "none", "get", "copy-1", expected=1))
        self.check("mTLS rejects foreign CA", lambda: "code=Unavailable" in self.client("-client-cert", "rogue-client", "get", "copy-1", expected=1))
        self.check("foreign CA validation reached", lambda: "unknown authority" in (self.evidence / "secure-server.log").read_text())
        self.stop()
        self.check("tokens absent from server logs", lambda: all(token not in path.read_text() for path in self.evidence.glob("*server.log") for token in [reader, librarian, expired]))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--evidence-dir", type=Path)
    args = parser.parse_args()
    evidence = (args.evidence_dir or Path(tempfile.mkdtemp(prefix="libratrack-smoke-"))).resolve()
    if evidence == ROOT or ROOT in evidence.parents:
        parser.error("evidence directory must be outside the repository")
    evidence.mkdir(parents=True, exist_ok=True)
    for port in (50051, 8080):
        with socket.socket() as probe:
            probe.bind(("127.0.0.1", port))
    demo = Demo(evidence)
    demo.bin.mkdir(exist_ok=True)
    try:
        demo.run(["go", "build", "-o", str(demo.bin) + os.sep, "./cmd/inventory-server", "./cmd/inventory-client", "./cmd/gateway", "./cmd/gencerts", "./cmd/mint-token"])
        demo.basic()
        demo.secure()
    finally:
        demo.stop()
        (evidence / "smoke-results.json").write_text(json.dumps(demo.results, indent=2) + "\n")
    print(f"PASS={len(demo.results)} FAIL=0\nEvidence: {evidence}")


if __name__ == "__main__":
    main()
