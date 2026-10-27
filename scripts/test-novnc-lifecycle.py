#!/usr/bin/env python3
"""Run inside a disposable full container; replaces its CLI with a synthetic waiter."""
import os
from pathlib import Path
import signal
import socket
import subprocess
import time
import tempfile
import urllib.request

wrapper = os.environ.get("BOOTSTRAP_WRAPPER", "/usr/local/bin/serial-sync-with-novnc")
cli = Path("/usr/local/bin/serial-sync")
cli.rename("/tmp/serial-sync-real")
cli.write_text('''#!/bin/sh
if [ "$1" = exit ]; then exit 23; fi
if [ "$1" = mfa ]; then exec "$BROWSER_FIXTURE_BINARY" -test.v -test.run TestChromiumSyntheticHumanMFA; fi
exec sleep 300
''')
cli.chmod(0o755)


def helpers():
    found = {}
    for path in Path("/proc").glob("[0-9]*/comm"):
        try:
            name = path.read_text().strip()
            arguments = (path.parent / "cmdline").read_bytes().split(b"\0")
            if any(argument.rsplit(b"/", 1)[-1] == b"websockify" for argument in arguments):
                name = "websockify"
            if name in {"Xvfb", "fluxbox", "x11vnc", "websockify", "sleep", "chrome", "chromium", "chrome_crashpad"}:
                found[int(path.parent.name)] = name
        except (FileNotFoundError, ProcessLookupError):
            pass
    return found


def wait_for(check, message):
    end = time.monotonic() + 10
    while time.monotonic() < end:
        value = check()
        if value:
            return value
        time.sleep(0.05)
    raise AssertionError(message)


def websocket_connection():
    connection = socket.create_connection(("127.0.0.1", 8080), timeout=0.3)
    try:
        connection.sendall(
            b"GET /websockify HTTP/1.1\r\nHost: 127.0.0.1:8080\r\n"
            b"Upgrade: websocket\r\nConnection: Upgrade\r\n"
            b"Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n"
            b"Sec-WebSocket-Version: 13\r\nSec-WebSocket-Protocol: binary\r\n\r\n"
        )
        response = b""
        while b"\r\n\r\n" not in response:
            chunk = connection.recv(4096)
            if not chunk:
                raise OSError("incomplete websocket/RFB handshake")
            response += chunk
        headers, payload = response.split(b"\r\n\r\n", 1)
        if b" 101 " not in headers.split(b"\r\n", 1)[0]:
            raise OSError("incomplete websocket/RFB handshake")
        while len(payload) < 14:
            chunk = connection.recv(4096)
            if not chunk:
                raise OSError("incomplete websocket/RFB handshake")
            payload += chunk
        if not (payload[0] == 0x82 and payload[1] == 12 and payload[2:6] == b"RFB "):
            raise OSError("invalid websocket/RFB greeting")
        return connection
    except OSError:
        connection.close()
        raise


def websocket_ready():
    try:
        return websocket_connection()
    except OSError:
        return False


try:
    baseline = helpers()
    for mode in ("exit", "cancel", "clean_restart", "mfa"):
        output = tempfile.TemporaryFile(mode="w+")
        process = subprocess.Popen([wrapper, mode], stdout=output, stderr=subprocess.STDOUT)
        viewer = None
        if mode != "exit":
            def ready():
                try:
                    return urllib.request.urlopen("http://127.0.0.1:8080/vnc.html", timeout=0.2).status == 200
                except OSError:
                    return False
            wait_for(ready, "noVNC HTTP surface never became ready")
            viewer = wait_for(websocket_ready, "noVNC websocket never forwarded the RFB greeting")
            print("noVNC websocket forwarded RFB greeting; viewer remains connected", flush=True)
            print(f"{mode} owned services: {helpers()}", flush=True)
            if mode != "mfa":
                process.send_signal(signal.SIGTERM)
        try:
            code = process.wait(timeout=35 if mode == "mfa" else 10)
        finally:
            output.seek(0)
            transcript = output.read()
            print(transcript, end="", flush=True)
            output.close()
        if mode == "mfa":
            assert "--- PASS: TestChromiumSyntheticHumanMFA" in transcript, transcript
            assert "borrowed display retained one Xvfb" in transcript, transcript
        assert code == {"exit": 23, "cancel": 143, "clean_restart": 143, "mfa": 0}[mode], (mode, code)
        wait_for(lambda: helpers() == baseline, f"{mode} leaked processes: {helpers()}")
        if viewer is not None:
            viewer.close()
        print(f"{mode}: exit={code}; owned services cleaned", flush=True)
finally:
    for pid in helpers():
        try:
            os.kill(pid, signal.SIGKILL)
        except ProcessLookupError:
            pass
    cli.unlink()
    Path("/tmp/serial-sync-real").rename(cli)
