#!/usr/bin/env python3
"""Run native verification on an authenticated private X11 display and bus."""
import argparse
import os
from pathlib import Path
import secrets
import select
import signal
import subprocess
import tempfile


def stop_owned(process, group=False):
    if process is not None and process.poll() is None:
        if group:
            os.killpg(process.pid, signal.SIGTERM)
        else:
            process.terminate()
        try:
            process.wait(timeout=3)
        except subprocess.TimeoutExpired:
            if group:
                os.killpg(process.pid, signal.SIGKILL)
            else:
                process.kill()
            process.wait()


parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--prefix', type=Path, default=Path('/usr'), help='Xvfb/Openbox installation prefix')
parser.add_argument('--log', type=Path, default=Path('/tmp/vtc-x11-probe.log'))
parser.add_argument('binary', type=Path)
parser.add_argument('arguments', nargs=argparse.REMAINDER)
args = parser.parse_args()
prefix = args.prefix.resolve()
binary = args.binary.resolve()
with tempfile.TemporaryDirectory(prefix='vtc-x11-', dir='/tmp') as temporary:
    root = Path(temporary)
    env = dict(os.environ)
    for variable in ['WAYLAND_DISPLAY', 'DBUS_SESSION_BUS_ADDRESS', 'DBUS_STARTER_ADDRESS',
                     'DBUS_STARTER_BUS_TYPE', 'AT_SPI_BUS_ADDRESS', 'IBUS_ADDRESS']:
        env.pop(variable, None)
    for variable in ['HOME', 'XDG_RUNTIME_DIR', 'XDG_CONFIG_HOME', 'XDG_DATA_HOME', 'XDG_CACHE_HOME']:
        directory = root / variable
        directory.mkdir(mode=0o700)
        env[variable] = str(directory)
    env.update(GDK_BACKEND='x11', GSK_RENDERER='cairo', GDK_DEBUG='no-portals',
               GSETTINGS_BACKEND='memory', GIO_USE_VFS='local', VTC_PRIVATE_DISPLAY='1')
    authority = root / 'Xauthority'
    cookie = secrets.token_hex(16)
    xauth = prefix / 'bin/xauth'
    subprocess.run([str(xauth), '-f', str(authority), 'add', ':99', '.', cookie], check=True, capture_output=True)
    env['XAUTHORITY'] = str(authority)
    helper_env = dict(env, LD_LIBRARY_PATH=str(prefix / 'lib/x86_64-linux-gnu'),
                      XDG_DATA_DIRS=f'{prefix}/share:/usr/share')
    read_fd, write_fd = os.pipe()
    server = manager = verifier = None
    try:
        with (root / 'server.log').open('w+') as server_log, (root / 'wm.log').open('w+') as wm_log:
            server = subprocess.Popen([str(prefix / 'bin/Xvfb'), '-displayfd', str(write_fd),
                                       '-auth', str(authority), '-nolisten', 'tcp', '-noreset',
                                       '-screen', '0', '1440x1000x24'],
                                      env=helper_env, pass_fds=(write_fd,), stdout=server_log, stderr=server_log)
            os.close(write_fd)
            write_fd = None
            if not select.select([read_fd], [], [], 30)[0]:
                server_log.seek(0)
                raise RuntimeError(f'Private X server was not ready within 30s (exit={server.poll()}): ' + server_log.read())
            display = os.read(read_fd, 32).decode().strip()
            if not display.isdecimal():
                server_log.seek(0)
                raise RuntimeError('Private X server failed: ' + server_log.read())
            env['DISPLAY'] = helper_env['DISPLAY'] = ':' + display
            subprocess.run([str(xauth), '-f', str(authority), 'add', env['DISPLAY'], '.', cookie],
                           check=True, capture_output=True)
            manager = subprocess.Popen([str(prefix / 'bin/openbox'), '--sm-disable'],
                                       env=helper_env, stdout=wm_log, stderr=wm_log)
            with (root / 'test.log').open('w') as test_log:
                command = [str(binary), *args.arguments]
                verifier = subprocess.Popen(['dbus-run-session', '--', *command], env=env,
                                            stdout=test_log, stderr=subprocess.STDOUT, start_new_session=True)
                try:
                    exit_code = verifier.wait(timeout=120)
                except subprocess.TimeoutExpired:
                    stop_owned(verifier, group=True)
                    exit_code = 124
            output = (root / 'test.log').read_text()
            args.log.write_text(output)
            if exit_code == 0:
                print('\n'.join(line for line in output.splitlines()
                                if any(marker in line for marker in ['RUN', 'PASS', 'WORKSPACE_'])))
            else:
                print(output[-8000:])
            if exit_code:
                server_log.seek(0)
                wm_log.seek(0)
                print(server_log.read()[-2000:])
                print(wm_log.read()[-2000:])
            print('Full X11 probe log:', args.log)
            raise SystemExit(exit_code)
    except Exception as error:
        args.log.write_text('Private X11 verification failed: ' + str(error) + '\n')
        raise
    finally:
        stop_owned(verifier, group=True)
        os.close(read_fd)
        if write_fd is not None:
            os.close(write_fd)
        stop_owned(manager)
        stop_owned(server)
