#!/usr/bin/env python3
"""Operate only the GTK portal's chooser on the private verification bus."""
import os
from pathlib import Path
import re
import sys
import time

import gi
gi.require_version('Atspi', '2.0')
from gi.repository import Atspi, Gio, GLib

if os.environ.get('VTC_PRIVATE_PORTAL') != '1' or not any(
        os.environ.get(key) == '1' for key in ('VTC_PRIVATE_DISPLAY', 'VTC_PRIVATE_WAYLAND')):
    raise SystemExit('private portal/display required')
destination = Path(sys.argv[1]).resolve()
if destination.parent != Path(os.environ['VTC_NATIVE_SIGNAL_STATE']).resolve():
    raise SystemExit('destination outside private fixture')
bus = Gio.bus_get_sync(Gio.BusType.SESSION, None)
Atspi.set_timeout(1000, 1000)

def descendants(node):
    yield node
    for index in range(node.get_child_count()):
        child = node.get_child_at_index(index)
        if child is not None:
            yield from descendants(child)

def activate(node):
    action = node.get_action_iface()
    return action is not None and action.get_n_actions() > 0 and action.do_action(0)

cancel = len(sys.argv) > 2 and sys.argv[2] == 'cancel'
filled = cancel
saved = False
deadline = time.monotonic() + 25
while time.monotonic() < deadline:
    try:
        pid = bus.call_sync('org.freedesktop.DBus', '/org/freedesktop/DBus',
                            'org.freedesktop.DBus', 'GetConnectionUnixProcessID',
                            GLib.Variant('(s)', ('org.freedesktop.impl.portal.desktop.gtk',)),
                            GLib.VariantType.new('(u)'), Gio.DBusCallFlags.NONE, 1000, None).unpack()[0]
        desktop = Atspi.get_desktop(0)
        nodes = []
        for index in range(desktop.get_child_count()):
            app = desktop.get_child_at_index(index)
            if app.get_process_id() == pid:
                nodes.extend(descendants(app))
        visible = [n for n in nodes if n.get_state_set().contains(Atspi.StateType.SHOWING)]
        for node in visible:
            if not filled and node.get_role() in (Atspi.Role.TEXT, Atspi.Role.ENTRY):
                editor = node.get_editable_text_iface()
                if editor is not None and editor.set_text_contents(str(destination)):
                    filled = True
                    print('PORTAL_CHECK: private GTK filename entered', flush=True)
                    # Let asynchronous path completion settle before closing
                    # the chooser (GTK3 may own a Wayland completion popup).
                    time.sleep(0.5)
        for node in visible:
            name = re.sub(r'\([A-Za-z]\)', '', (node.get_name() or '').replace('_', '')).strip()
            target = (not saved and name in ('Cancel', '取消')) if cancel else (
                name in ('Replace', '替换') or (not saved and name in ('Save', '保存', 'Save / 保存')))
            if filled and target and activate(node):
                saved = True
                print('PORTAL_CHECK: activated ' + name, flush=True)
                break
        if saved and not visible:
            print('PORTAL_PASS: owned GTK chooser closed', flush=True)
            raise SystemExit(0)
    except GLib.Error:
        pass
    time.sleep(0.1)
raise SystemExit('private GTK portal chooser timed out')
