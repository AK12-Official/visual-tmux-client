#!/usr/bin/env python3
"""Run a native fixture with a private IBus daemon, configuration and libpinyin."""
import os
from pathlib import Path
import signal
import subprocess
import sys
import time

wayland = os.environ.get('VTC_PRIVATE_WAYLAND') == '1'
if (os.environ.get('VTC_PRIVATE_DISPLAY') != '1' and not wayland) or not os.environ.get('DBUS_SESSION_BUS_ADDRESS'):
    raise SystemExit('A private display and session bus are required')
env = dict(os.environ, GTK_IM_MODULE='ibus', VTC_PRIVATE_IME='1',
           XDG_SESSION_TYPE='wayland' if wayland else 'x11', XDG_CURRENT_DESKTOP='X-Generic', DESKTOP_SESSION='vtc-private')
for variable in ['GNOME_DESKTOP_SESSION_ID', 'IBUS_USE_PORTAL', 'DBUS_STARTER_ADDRESS', 'DBUS_STARTER_BUS_TYPE']:
    env.pop(variable, None)
env['IBUS_ADDRESS'] = 'unix:path=' + str(Path(env['XDG_RUNTIME_DIR']) / 'ibus-probe')
# libpinyin needs the configuration component: --single/--config=disable would
# leave SetGlobalEngine waiting. Configuration stays under the private HOME/XDG.
arguments = ['ibus-daemon'] + ([] if wayland else ['--xim'])
daemon = subprocess.Popen([*arguments, '--desktop=vtc-private', '--panel=disable',
                           '--emoji-extension=disable', '--address=' + env['IBUS_ADDRESS']],
                          env=env, start_new_session=True)
def terminate(signum, _frame):
    raise SystemExit(128 + signum)

signal.signal(signal.SIGTERM, terminate)
try:
    result = None
    deadline = time.monotonic() + 25
    while time.monotonic() < deadline:
        if daemon.poll() is not None:
            raise RuntimeError('Private IBus daemon exited')
        try:
            result = subprocess.run(['gdbus', 'call', '--address', env['IBUS_ADDRESS'],
                                     '--dest', 'org.freedesktop.IBus', '--object-path', '/org/freedesktop/IBus',
                                     '--method', 'org.freedesktop.IBus.SetGlobalEngine', 'libpinyin'],
                                    env=env, capture_output=True, timeout=5)
            if result.returncode == 0:
                break
        except subprocess.TimeoutExpired:
            pass
        time.sleep(.1)
    else:
        detail = result.stderr.decode(errors='replace') if result else 'No engine response'
        raise RuntimeError('Private libpinyin initialization failed: ' + detail)
    raise SystemExit(subprocess.run(sys.argv[1:], env=env, timeout=85).returncode)
finally:
    if daemon.poll() is None:
        os.killpg(daemon.pid, signal.SIGTERM)  # only this new, owned daemon group
        try:
            daemon.wait(timeout=3)
        except subprocess.TimeoutExpired:
            os.killpg(daemon.pid, signal.SIGKILL)
            daemon.wait()
