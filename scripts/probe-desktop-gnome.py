#!/usr/bin/env python3
"""Verify native file/quit workflows on a private GNOME Wayland desktop.

This runner does not inject keyboard/pointer input. Weston/X11 input verification
remains separate. No existing display, session bus or user settings are used.
"""
import argparse
import os
from pathlib import Path
import signal
import subprocess
import tempfile
import time


def stop_owned(process):
    if process.poll() is None:
        process.terminate()
        try:
            process.wait(timeout=3)
        except subprocess.TimeoutExpired:
            process.kill()
            process.wait()


parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('binary', type=Path, help='compiled desktop-tagged Go test executable')
parser.add_argument('--log', type=Path, default=Path('/tmp/vtc-gnome-workspace.log'))
parser.add_argument('--test-suite', action='store_true')
parser.add_argument('--test-run', default='^TestNativeLinuxWorkspace$')
parser.add_argument('--inside-private-bus', action='store_true', help=argparse.SUPPRESS)
args = parser.parse_args()
binary = args.binary.resolve()
if not args.inside_private_bus:
    with tempfile.TemporaryDirectory(prefix='vtc-gnome-', dir='/tmp') as temporary:
        env = dict(os.environ)
        for variable in ('DISPLAY', 'WAYLAND_DISPLAY', 'DBUS_SESSION_BUS_ADDRESS',
                         'DBUS_STARTER_ADDRESS', 'DBUS_STARTER_BUS_TYPE',
                         'AT_SPI_BUS_ADDRESS', 'IBUS_ADDRESS', 'VTC_PRIVATE_DISPLAY',
                         'VTC_PRIVATE_WAYLAND', 'VTC_PRIVATE_IME'):
            env.pop(variable, None)
        for variable in ('HOME', 'XDG_RUNTIME_DIR', 'XDG_CONFIG_HOME', 'XDG_DATA_HOME', 'XDG_CACHE_HOME'):
            directory = Path(temporary) / variable
            directory.mkdir(mode=0o700)
            env[variable] = str(directory)
        env.update(VTC_PRIVATE_GNOME='1', GSETTINGS_BACKEND='memory', LIBGL_ALWAYS_SOFTWARE='1',
                   XDG_SESSION_TYPE='wayland', XDG_CURRENT_DESKTOP='GNOME', GNOME_SHELL_SESSION_MODE='user',
                   WAYLAND_DISPLAY='vtc-gnome', GDK_BACKEND='wayland', GTK_A11Y='atspi',
                   VTC_PRIVATE_PORTAL='1', GTK_USE_PORTAL='1', GDK_DEBUG='',
                   VTC_GNOME_ACTIVATION_DRIVER=str(Path(__file__).with_name('probe-desktop-activation.py').resolve()),
                   VTC_PORTAL_DRIVER=str(Path(__file__).with_name('probe-desktop-portal.py').resolve()))
        command = ['dbus-run-session', '--', 'python3', str(Path(__file__).resolve()),
                   str(binary), '--inside-private-bus', '--test-run=' + args.test_run]
        if args.test_suite:
            command.append('--test-suite')
        process = subprocess.Popen(command, env=env, stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
                                   text=True, start_new_session=True)
        try:
            output, _ = process.communicate(timeout=110)
        except subprocess.TimeoutExpired:
            os.killpg(process.pid, signal.SIGTERM)
            try:
                output, _ = process.communicate(timeout=5)
            except subprocess.TimeoutExpired:
                os.killpg(process.pid, signal.SIGKILL)
                output, _ = process.communicate()
            args.log.write_text(output)
            raise SystemExit('private GNOME verification timed out: ' + str(args.log))
        args.log.write_text(output)
        print('\n'.join(line for line in output.splitlines()
                        if any(marker in line for marker in ('WORKSPACE_', 'PORTAL_', '--- PASS', '--- FAIL'))))
        if process.returncode:
            print(output[-6000:])
        print('GNOME_PROBE_EXIT', process.returncode, 'full log:', args.log)
        raise SystemExit(process.returncode)

if os.environ.get('VTC_PRIVATE_GNOME') != '1' or not os.environ.get('DBUS_SESSION_BUS_ADDRESS'):
    raise SystemExit('private GNOME bus required')


def interrupted(_number, _frame):
    raise SystemExit(143)


signal.signal(signal.SIGTERM, interrupted)
runtime = Path(os.environ['XDG_RUNTIME_DIR'])
shell_log = runtime / 'gnome-shell.log'
with shell_log.open('w') as output:
    shell = subprocess.Popen(['gnome-shell', '--headless', '--wayland', '--no-x11', '--sm-disable',
                              '--wayland-display=vtc-gnome', '--virtual-monitor=1280x900'],
                             stdout=output, stderr=subprocess.STDOUT)
    try:
        for _ in range(100):
            if shell.poll() is not None:
                raise RuntimeError('private GNOME exited during startup')
            if (runtime / 'vtc-gnome').exists():
                break
            time.sleep(0.1)
        else:
            raise RuntimeError('private GNOME socket not ready')
        # The socket precedes GNOME Shell's services and first frame.
        time.sleep(2)
        tests = ['-test.v'] if args.test_suite else ['-test.run=' + args.test_run, '-test.v']
        result = subprocess.run([str(binary), *tests], capture_output=True, text=True, timeout=85)
        print(result.stdout + result.stderr, flush=True)
        status = result.returncode
        if shell.poll() is not None:
            status = 1
    finally:
        stop_owned(shell)
        print('GNOME SHELL OUTPUT:\n' + shell_log.read_text(), flush=True)
raise SystemExit(status)
