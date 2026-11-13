#!/usr/bin/env python3
# Starts only an owned headless compositor and disposable fake-service app.
import argparse
import os
from pathlib import Path
import subprocess
import shlex
import tempfile
import time

parser = argparse.ArgumentParser(description='Isolated Weston 13 WebView verification; input devices are virtual.')
parser.add_argument('binary', type=Path)
parser.add_argument('--portal', action='store_true', help='exercise the GTK desktop-portal chooser')
parser.add_argument('--ime', action='store_true', help='run private IBus/libpinyin with the virtual seat')
parser.add_argument('--test-input', action='store_true', help='build and load a private Weston 13 input seat')
parser.add_argument('--test-suite', action='store_true', help='run all tests in the native test binary')
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
    portal_config = Path(env['XDG_CONFIG_HOME']) / 'xdg-desktop-portal'
    portal_config.mkdir(mode=0o700)
    (portal_config / 'portals.conf').write_text('[preferred]\ndefault=gtk\n')
    env['XDG_CURRENT_DESKTOP'] = 'X-Generic'
    weston_env = dict(env, LD_LIBRARY_PATH=f'{root}/lib/x86_64-linux-gnu:{root}/lib/x86_64-linux-gnu/weston')
    modules = list((root / 'lib/x86_64-linux-gnu/libweston-13').glob('*.so')) + list((root / 'lib/x86_64-linux-gnu/weston').glob('*.so'))
    weston_env['WESTON_MODULE_MAP'] = ';'.join(f'{module.name}={module}' for module in modules)
    configuration = Path(runtime) / 'weston.ini'
    configuration.write_text('[shell]\nclient=' + str(root / 'libexec/weston-desktop-shell') + '\npanel-position=none\n')
    log = Path(runtime) / 'weston.log'
    extra_arguments=[]
    if args.test_input:
        plugin=Path(runtime)/'vtc-input.so'
        source=Path(__file__).with_name('testdata')/'weston-input.c'
        flags=shlex.split(subprocess.check_output(['pkg-config','--cflags','--libs','wayland-server','pixman-1','xkbcommon'],text=True))
        subprocess.run(['gcc','-shared','-fPIC','-Wall','-Wextra','-Werror','-I'+str(root/'include/libweston-13'),
                        str(source),'-o',str(plugin),*flags],check=True)
        env.update(VTC_PRIVATE_WAYLAND='1',VTC_WAYLAND_INPUT=str(Path(runtime)/'input.sock'))
        weston_env.update(VTC_PRIVATE_WAYLAND='1',VTC_WAYLAND_INPUT=env['VTC_WAYLAND_INPUT'])
        extra_arguments=['--modules='+str(plugin)]
    compositor_command = [
        str(root / 'bin/weston'), '--config=' + str(configuration), '--idle-time=0', '--socket=vtc-wayland',
        '--backend=headless',
        '--shell=desktop',
        '--renderer=pixman', '--width=1280', '--height=900', f'--log={log}',*extra_arguments,
    ]
    compositor = subprocess.Popen(compositor_command, env=weston_env,
                                  stdout=subprocess.DEVNULL, stderr=subprocess.PIPE)
    try:
        for _ in range(50):
            if (Path(runtime) / 'vtc-wayland').exists() or compositor.poll() is not None:
                break
            time.sleep(0.1)
        if not (Path(runtime) / 'vtc-wayland').exists():
            raise RuntimeError(log.read_text() if log.exists() else compositor.stderr.read().decode())
        env.update(WAYLAND_DISPLAY='vtc-wayland', GDK_BACKEND='wayland',
                   GDK_DEBUG='no-portals', GSK_RENDERER='cairo')
        if args.portal:
            env.update(VTC_PRIVATE_PORTAL='1', GTK_USE_PORTAL='1', GDK_DEBUG='', GTK_A11Y='atspi',
                       VTC_PORTAL_DRIVER=str(Path(__file__).with_name('probe-desktop-portal.py').resolve()))
        arguments = ['--spike','--probe']
        if args.test_workspace: arguments=['-test.run=^TestNativeLinuxWorkspace$','-test.v']
        if args.test_suite: arguments=['-test.v']
        command=[str(binary),*arguments]
        if args.ime:
            if not args.test_input: raise RuntimeError('--ime requires --test-input')
            command=['python3',str(Path(__file__).with_name('probe-desktop-ime.py')),*command]
        result = subprocess.run(['dbus-run-session', '--',*command],
                                env=env, capture_output=True, text=True, timeout=100 if args.test_workspace or args.test_suite else 40)
        evidence = log.read_text() + '\nAPP OUTPUT:\n' + result.stdout + result.stderr
        if compositor.poll() is not None:
            diagnostics = compositor.stderr
            evidence += '\nCOMPOSITOR EXIT: ' + str(compositor.returncode) + '\n' + diagnostics.read().decode()
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
