#!/usr/bin/env python3
"""Launch an owned second fixture using GTK's desktop startup notification."""
import os
import subprocess
from pathlib import Path
import sys

import gi
gi.require_version('Gtk', '4.0')
from gi.repository import Gdk, Gio, GLib, Gtk

if os.environ.get('VTC_PRIVATE_GNOME') != '1':
    raise SystemExit('private GNOME display required')
Gtk.init()
bus = Gio.bus_get_sync(Gio.BusType.SESSION, None)
destination = 'org.gnome.Mutter.RemoteDesktop'
session = bus.call_sync(destination, '/org/gnome/Mutter/RemoteDesktop', destination, 'CreateSession',
                        None, GLib.VariantType.new('(o)'), Gio.DBusCallFlags.NONE, 3000, None).unpack()[0]
interface = destination + '.Session'


def call(method, arguments=None):
    return bus.call_sync(destination, session, interface, method, arguments, None,
                         Gio.DBusCallFlags.NONE, 3000, None)


call('Start')
# Dismiss GNOME's initial overview on this new private desktop.
for state in (True, False):
    call('NotifyKeyboardKeycode', GLib.Variant('(ub)', (1, state)))
window = Gtk.Window(title='Private desktop activation fixture')
button = Gtk.Button(label='Launch owned desktop fixture')
window.set_child(button)
window.set_default_size(480, 200)
window.present()
loop = GLib.MainLoop()
child = None


def launch(_button):
    global child
    if child is not None:
        return
    context = Gdk.Display.get_default().get_app_launch_context()
    info = Gio.AppInfo.create_from_commandline(sys.argv[1], 'Private desktop fixture', Gio.AppInfoCreateFlags.NONE)
    token = context.get_startup_notify_id(info, [])
    if not token:
        raise SystemExit('desktop activation token unavailable')
    os.environ['XDG_ACTIVATION_TOKEN'] = token
    # Keep the launching surface alive while the recipient consumes the token,
    # as a real desktop launcher does; exec would destroy its Wayland client.
    child = subprocess.Popen(sys.argv[1:])
    GLib.timeout_add(50, poll)


def poll():
    if child.poll() is None:
        return True
    if not (Path(os.environ['VTC_NATIVE_SIGNAL_STATE']) / 'focus-ack').exists():
        return True
    loop.quit()
    return False


def press():
    button.grab_focus()
    for state in (True, False):
        call('NotifyKeyboardKeycode', GLib.Variant('(ub)', (57, state)))
    return False


button.connect('clicked', launch)
GLib.timeout_add(700, press)
GLib.timeout_add_seconds(5, loop.quit)
loop.run()
call('Stop')
if child is None:
    raise SystemExit('private desktop launch did not activate')
try:
    result = child.wait(timeout=1)
except subprocess.TimeoutExpired:
    child.kill()
    child.wait()
    raise SystemExit('private second launch timed out')
if not (Path(os.environ['VTC_NATIVE_SIGNAL_STATE']) / 'focus-ack').exists():
    raise SystemExit('recipient did not focus while launcher remained alive')
raise SystemExit(result)
