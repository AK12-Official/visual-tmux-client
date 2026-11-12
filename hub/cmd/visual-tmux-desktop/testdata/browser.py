#!/usr/bin/python3
"""Private test MIME handler: launch a real browser without user profiles."""
import json
import os
from pathlib import Path
import signal
import subprocess
import sys
import tempfile

target = sys.argv[-1]
with Path(__file__).with_name('browser.log').open('a') as output:
    output.write(json.dumps({'invoked': True, 'url': target}) + '\n')
with tempfile.TemporaryDirectory(prefix='browser-', dir=os.environ['VTC_BROWSER_ROOT']) as profile:
    process = subprocess.Popen([
        os.environ['VTC_BROWSER_BINARY'], '--headless', '--no-first-run', '--no-default-browser-check',
        '--disable-background-networking', '--password-store=basic', '--user-data-dir=' + profile, '--timeout=5000', '--dump-dom', target,
    ], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, start_new_session=True)
    with Path(os.environ['VTC_BROWSER_LOG']).open('a') as output:
        output.write(json.dumps({'url': target, 'started': True}) + '\n')
    try:
        process.wait(timeout=8)
    except subprocess.TimeoutExpired:
        os.killpg(process.pid, signal.SIGTERM)  # only this new, owned browser group
        try:
            process.wait(timeout=1)
        except subprocess.TimeoutExpired:
            os.killpg(process.pid, signal.SIGKILL)
            process.wait()
    with Path(os.environ['VTC_BROWSER_LOG']).open('a') as output:
        output.write(json.dumps({'url': target, 'done': True}) + '\n')
