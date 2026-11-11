//go:build desktop && linux

package main

/*
#cgo pkg-config: gtk4 webkitgtk-6.0
#include <stdlib.h>
#include <string.h>
#include <gtk/gtk.h>
#include <webkit/webkit.h>

static GtkWidget *vtcFindWebView(GtkWidget *widget) {
    if (WEBKIT_IS_WEB_VIEW(widget)) return widget;
    for (GtkWidget *child = gtk_widget_get_first_child(widget); child; child = gtk_widget_get_next_sibling(child)) {
        GtkWidget *found = vtcFindWebView(child);
        if (found) return found;
    }
    return NULL;
}

static gboolean vtcOwnedURI(const char *uri, const char *origin) {
    if (!uri) return FALSE;
    if (!strcmp(uri, "about:blank")) return TRUE;
    size_t n = strlen(origin);
    return !strncmp(uri, origin, n) && (!uri[n] || uri[n] == '/' || uri[n] == '?' || uri[n] == '#');
}

static gboolean vtcPolicy(WebKitWebView *view, WebKitPolicyDecision *decision,
                          WebKitPolicyDecisionType kind, gpointer origin) {
    const char *uri = NULL;
    if (kind == WEBKIT_POLICY_DECISION_TYPE_NAVIGATION_ACTION ||
        kind == WEBKIT_POLICY_DECISION_TYPE_NEW_WINDOW_ACTION) {
        WebKitNavigationAction *action = webkit_navigation_policy_decision_get_navigation_action(
            WEBKIT_NAVIGATION_POLICY_DECISION(decision));
        uri = webkit_uri_request_get_uri(webkit_navigation_action_get_request(action));
    } else if (kind == WEBKIT_POLICY_DECISION_TYPE_RESPONSE) {
        uri = webkit_uri_response_get_uri(webkit_response_policy_decision_get_response(
            WEBKIT_RESPONSE_POLICY_DECISION(decision)));
    }
    if (uri && !vtcOwnedURI(uri, origin)) {
        int count = GPOINTER_TO_INT(g_object_get_data(G_OBJECT(view), "vtc-blocked"));
        g_object_set_data(G_OBJECT(view), "vtc-blocked", GINT_TO_POINTER(count + 1));
        webkit_policy_decision_ignore(decision);
        return TRUE;
    }
    return FALSE;
}

static int vtcInstallNavigation(void *window, const char *origin) {
    GtkWidget *view = vtcFindWebView(GTK_WIDGET(window));
    if (!view) return 0;
    if (!g_object_get_data(G_OBJECT(view), "vtc-origin")) {
        char *owned = g_strdup(origin);
        g_object_set_data_full(G_OBJECT(view), "vtc-origin", owned, g_free);
        g_signal_connect(view, "decide-policy", G_CALLBACK(vtcPolicy), owned);
    }
    return 1;
}

static int vtcBlockedCount(void *window) {
    GtkWidget *view = vtcFindWebView(GTK_WIDGET(window));
    return view ? GPOINTER_TO_INT(g_object_get_data(G_OBJECT(view), "vtc-blocked")) : -1;
}
*/
import "C"

import "unsafe"

func installNavigationGuard(window unsafe.Pointer, origin string) bool {
	cOrigin := C.CString(origin)
	defer C.free(unsafe.Pointer(cOrigin))
	return C.vtcInstallNavigation(window, cOrigin) != 0
}

func blockedNavigationCount(window unsafe.Pointer) int { return int(C.vtcBlockedCount(window)) }
