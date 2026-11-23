#!/usr/bin/python3
"""Private test MIME handler: launch a real browser without user profiles."""
import json
import os
from pathlib import Path
import signal
import subprocess
import sys
import tempfile
import time

target = sys.argv[-1]
with Path(__file__).with_name('browser.log').open('a') as output:
    output.write(json.dumps({'invoked': True, 'url': target}) + '\n')
with tempfile.TemporaryDirectory(prefix='browser-', dir=os.environ['VTC_BROWSER_ROOT']) as profile:
    started = time.monotonic()
    timed_out = False
    errors = tempfile.TemporaryFile()
    process = subprocess.Popen([
        os.environ['VTC_BROWSER_BINARY'], '--headless', '--no-first-run', '--no-default-browser-check',
        '--disable-background-networking', '--password-store=basic', '--user-data-dir=' + profile, '--timeout=10000', '--dump-dom', target,
    ], stdout=subprocess.DEVNULL, stderr=errors, start_new_session=True)
    with Path(os.environ['VTC_BROWSER_LOG']).open('a') as output:
        output.write(json.dumps({'url': target, 'started': True}) + '\n')
    try:
        process.wait(timeout=20)
    except subprocess.TimeoutExpired:
        timed_out = True
        os.killpg(process.pid, signal.SIGTERM)  # only this new, owned browser group
        try:
            process.wait(timeout=2)
        except subprocess.TimeoutExpired:
            os.killpg(process.pid, signal.SIGKILL)
            process.wait()
    errors.seek(0)
    diagnostic = errors.read(4000).decode(errors='replace')
    errors.close()
    with Path(os.environ['VTC_BROWSER_LOG']).open('a') as output:
        output.write(json.dumps({'url': target, 'done': True, 'exitCode': process.returncode,
                                 'timedOut': timed_out, 'seconds': round(time.monotonic() - started, 2),
                                 'diagnostic': diagnostic if process.returncode else ''}) + '\n')
