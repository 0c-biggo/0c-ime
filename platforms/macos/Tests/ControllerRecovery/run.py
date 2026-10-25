"""Inject one warm IPC timeout without logging request or response payloads."""
import json
import pathlib
import selectors
import socket
import struct
import subprocess
import sys
import tempfile
import threading
import time


def frame(peer):
    def exact(size):
        data = b""
        while len(data) < size:
            chunk = peer.recv(size - len(data))
            if not chunk:
                raise EOFError
            data += chunk
        return data
    header = exact(4)
    size, = struct.unpack("<I", header)
    if not 0 < size <= 1 << 20:
        raise ValueError("frame size")
    return header + exact(size)


def stop_process(process, graceful):
    if process is None or process.poll() is not None:
        return
    if graceful:
        process.terminate()
    else:
        process.kill()
    try:
        process.wait(timeout=10 if graceful else 5)
    except subprocess.TimeoutExpired:
        process.kill()
        process.wait(timeout=5)


with tempfile.TemporaryDirectory(prefix="caps-", dir="/private/tmp") as temporary:
    root = pathlib.Path(temporary)
    (root / "settings.json").write_text(json.dumps({"schema": 1, "selection": "statistical", "punctuation": False,
        "english_autocomplete": False, "mixed_input": False, "mode_memory": "shared", "start_mode": "zhuyin"}))
    target = str(root / "engine.sock")
    proxy = str(root / "proxy.sock")
    cold_proxy = str(root / "cold.sock")
    start_cold = root / "start-cold"
    fault = root / "fault"
    server = subprocess.Popen([sys.argv[1], "-socket", target, "-data", sys.argv[2], "-user", str(root / "user.json")],
                              stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    test = None
    workers = []
    faults = []
    errors = []
    def serve(client):
        try:
            with client, socket.socket(socket.AF_UNIX) as upstream:
                upstream.connect(target)
                while True:
                    request = frame(client)
                    if fault.exists():
                        fault.unlink()
                        faults.append(True)
                        # No payload is recorded or forwarded for this one request.
                        time.sleep(1.4)
                        return
                    upstream.sendall(request)
                    client.sendall(frame(upstream))
        except (EOFError, BrokenPipeError, ConnectionResetError):
            pass
        except Exception as error:
            errors.append(type(error).__name__)
    try:
        deadline = time.monotonic() + 10
        while not pathlib.Path(target).exists():
            if server.poll() is not None or time.monotonic() > deadline:
                raise RuntimeError("isolated engine failed to start")
            time.sleep(0.02)
        with socket.socket(socket.AF_UNIX) as listener, socket.socket(socket.AF_UNIX) as cold_listener, selectors.DefaultSelector() as ready:
            listener.bind(proxy)
            listener.listen()
            ready.register(listener, selectors.EVENT_READ)
            cold_started = False
            test = subprocess.Popen([sys.argv[3], proxy, str(fault), cold_proxy, str(start_cold)])
            deadline = time.monotonic() + 20
            while test.poll() is None:
                if time.monotonic() > deadline:
                    raise RuntimeError("controller test timed out")
                if not cold_started and start_cold.exists():
                    cold_listener.bind(cold_proxy)
                    cold_listener.listen()
                    ready.register(cold_listener, selectors.EVENT_READ)
                    cold_started = True
                for key, _ in ready.select(timeout=0.05):
                    client, _ = key.fileobj.accept()
                    worker = threading.Thread(target=serve, args=(client,), daemon=True)
                    workers.append(worker)
                    worker.start()
            for worker in workers:
                worker.join(timeout=3)
        if len(faults) != 2 or not cold_started or errors or any(worker.is_alive() for worker in workers):
            raise RuntimeError("fault control/cleanup failed")
        result = test.returncode
    finally:
        original_error = sys.exc_info()[1]
        cleanup_error = None
        # Try both children even if one cleanup fails; retain the original test
        # exception instead of replacing it with an unrelated cleanup failure.
        for process, graceful in ((test, False), (server, True)):
            try:
                stop_process(process, graceful)
            except Exception as error:
                cleanup_error = error
                print(f"test process cleanup failed: {type(error).__name__}", file=sys.stderr)
        if cleanup_error is not None and original_error is None:
            raise cleanup_error
    sys.exit(result)
