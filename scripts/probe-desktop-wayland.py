#!/usr/bin/env python3
# Starts only an owned headless compositor and disposable fake-service app.
import argparse
import os
from pathlib import Path
import subprocess
import tempfile
import time

parser = argparse.ArgumentParser(description='Isolated Ubuntu 24.04/Weston 13 WebView probe; no physical input acceptance.')
parser.add_argument('binary', type=Path)
parser.add_argument('--test-workspace', action='store_true', help='run the compiled native workspace test')
parser.add_argument('--weston-prefix', type=Path, default=Path('/usr'))
parser.add_argument('--log', type=Path, default=Path('/tmp/vtc-wayland-result.log'))
args = parser.parse_args()
root = args.weston_prefix.resolve()
binary = args.binary.resolve()
if not binary.is_file():
    parser.error('desktop binary does not exist')
with tempfile.TemporaryDirectory(prefix='vtc-wl-', dir='/tmp') as runtime:
    os.chmod(runtime, 0o700)
    env = dict(os.environ, XDG_RUNTIME_DIR=runtime)
    for variable in ['DISPLAY', 'DBUS_SESSION_BUS_ADDRESS', 'DBUS_STARTER_ADDRESS',
                     'DBUS_STARTER_BUS_TYPE', 'AT_SPI_BUS_ADDRESS', 'IBUS_ADDRESS']:
        env.pop(variable, None)
    for variable, directory in [('HOME', 'home'), ('XDG_CONFIG_HOME', 'config'),
                                ('XDG_DATA_HOME', 'data'), ('XDG_CACHE_HOME', 'cache')]:
        private = Path(runtime) / directory
        private.mkdir(mode=0o700)
        env[variable] = str(private)
    env.update(GSETTINGS_BACKEND='memory', GIO_USE_VFS='local')
    weston_env = dict(env, LD_LIBRARY_PATH=f'{root}/lib/x86_64-linux-gnu:{root}/lib/x86_64-linux-gnu/weston')
    modules = list((root / 'lib/x86_64-linux-gnu/libweston-13').glob('*.so')) + list((root / 'lib/x86_64-linux-gnu/weston').glob('*.so'))
    weston_env['WESTON_MODULE_MAP'] = ';'.join(f'{module.name}={module}' for module in modules)
    log = Path(runtime) / 'weston.log'
    compositor = subprocess.Popen([
        str(root / 'bin/weston'), '--no-config', '--idle-time=0', '--socket=vtc-wayland',
        '--backend=headless',
        '--shell=kiosk',
        '--renderer=pixman', '--width=1280', '--height=900', f'--log={log}',
    ], env=weston_env, stdout=subprocess.DEVNULL, stderr=subprocess.PIPE)
    try:
        for _ in range(50):
            if (Path(runtime) / 'vtc-wayland').exists() or compositor.poll() is not None:
                break
            time.sleep(0.1)
        if not (Path(runtime) / 'vtc-wayland').exists():
            raise RuntimeError(log.read_text() if log.exists() else compositor.stderr.read().decode())
        env.update(WAYLAND_DISPLAY='vtc-wayland', GDK_BACKEND='wayland',
                   GDK_DEBUG='no-portals', GSK_RENDERER='cairo')
        arguments = ['-test.run=^TestNativeLinuxWorkspace$', '-test.v'] if args.test_workspace else ['--spike', '--probe']
        result = subprocess.run(['dbus-run-session', '--', str(binary), *arguments],
                                env=env, capture_output=True, text=True, timeout=100 if args.test_workspace else 40)
        evidence = log.read_text() + '\nAPP OUTPUT:\n' + result.stdout + result.stderr
        args.log.write_text(evidence)
        print('\n'.join(line for line in (result.stdout + result.stderr).splitlines() if line.startswith('spike:') or 'WORKSPACE_' in line))
        if result.returncode:
            print(evidence[-8000:])
        print('HEADLESS_WAYLAND_PROBE_EXIT', result.returncode, 'full log:', args.log)
        raise SystemExit(result.returncode)
    finally:
        if compositor.poll() is None:
            compositor.terminate()
            try:
                compositor.wait(timeout=3)
            except subprocess.TimeoutExpired:
                compositor.kill()
                compositor.wait()
