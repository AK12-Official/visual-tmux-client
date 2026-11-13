/* Private Weston 13 verification module. Never installed with the application.
 * Backend ABI signatures follow Weston 13 libweston/backend.h and
 * libweston-internal.h. The socket and compositor are owned by the test runner.
 */
#define _GNU_SOURCE
#include <libweston/libweston.h>
#include <libweston/version.h>
#include <linux/input-event-codes.h>
#include <sys/socket.h>
#include <sys/stat.h>
#include <sys/un.h>
#include <unistd.h>
#include <errno.h>
#include <math.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#if WESTON_VERSION_MAJOR != 13
#error This verification module requires Weston 13
#endif

extern void weston_seat_init(struct weston_seat *, struct weston_compositor *, const char *);
extern void weston_seat_release(struct weston_seat *);
extern int weston_seat_init_pointer(struct weston_seat *);
extern int weston_seat_init_keyboard(struct weston_seat *, struct xkb_keymap *);
extern void notify_key(struct weston_seat *, const struct timespec *, uint32_t,
                       enum wl_keyboard_key_state, enum weston_key_state_update);
extern void notify_button(struct weston_seat *, const struct timespec *, int32_t,
                          enum wl_pointer_button_state);
extern void notify_motion_absolute(struct weston_seat *, const struct timespec *, struct weston_coord_global);
extern void notify_axis(struct weston_seat *, const struct timespec *, struct weston_pointer_axis_event *);
extern void notify_axis_source(struct weston_seat *, uint32_t);
extern void notify_pointer_frame(struct weston_seat *);

struct probe {
    struct weston_compositor *compositor;
    struct weston_seat seat;
    struct wl_listener destroy;
    struct wl_event_source *source;
    struct wl_list clients;
    int fd;
    char path[sizeof(((struct sockaddr_un *)0)->sun_path)];
};

struct connection {
    struct probe *probe;
    struct wl_event_source *source;
    struct wl_list link;
    struct ucred peer;
    int fd;
    size_t used;
    char buffer[256];
};

static void close_connection(struct connection *client) {
    wl_event_source_remove(client->source);
    wl_list_remove(&client->link);
    close(client->fd);
    free(client);
}

static struct weston_view *find_view(struct probe *probe, pid_t pid, unsigned int id) {
    struct weston_view *view;
    wl_list_for_each(view, &probe->compositor->view_list, link) {
        struct wl_resource *resource = view->surface->resource;
        if (!resource || wl_resource_get_id(resource) != id) continue;
        pid_t owner;
        wl_client_get_credentials(wl_resource_get_client(resource), &owner, NULL, NULL);
        if (owner == pid) return view;
    }
    return NULL;
}

static void motion(struct probe *probe, struct weston_view *view,
                   const struct timespec *now, double x, double y) {
    struct weston_coord_surface local = weston_coord_surface(x, y, view->surface);
    struct weston_coord_global global=weston_coord_surface_to_global(view, local);
    notify_motion_absolute(&probe->seat, now, global);
    notify_pointer_frame(&probe->seat);
}

static void key(struct probe *probe, const struct timespec *now, unsigned int code, bool down) {
    notify_key(&probe->seat, now, code, down ? WL_KEYBOARD_KEY_STATE_PRESSED : WL_KEYBOARD_KEY_STATE_RELEASED,
               STATE_UPDATE_AUTOMATIC);
}

static bool command(struct connection *client) {
    long pid;
    unsigned int id;
    char operation[16];
    double x = 0, y = 0, endx = 0, endy = 0;
    int drag = 0;
    int fields = sscanf(client->buffer, "%ld %u %15s %lf %lf %lf %lf %d",
                        &pid, &id, operation, &x, &y, &endx, &endy, &drag);
    if (fields < 3 || pid != client->peer.pid || client->peer.uid != getuid()) return false;
    struct probe *probe = client->probe;
    struct weston_view *view = find_view(probe, (pid_t)pid, id);
    if (!view) return false;
    struct timespec now;
    clock_gettime(CLOCK_MONOTONIC, &now);
    weston_seat_set_keyboard_focus(&probe->seat, view->surface);
    if (!strcmp(operation, "pointer")) {
        if (fields != 8 || !isfinite(x) || !isfinite(y) || !isfinite(endx) || !isfinite(endy) ||
            x < 0 || y < 0 || x > view->surface->width || y > view->surface->height ||
            (drag && (endx < 0 || endy < 0 || endx > view->surface->width || endy > view->surface->height))) return false;
        motion(probe, view, &now, x, y);
        notify_button(&probe->seat, &now, BTN_LEFT, WL_POINTER_BUTTON_STATE_PRESSED);
        notify_pointer_frame(&probe->seat);
        if (drag) motion(probe, view, &now, endx, endy);
        notify_button(&probe->seat, &now, BTN_LEFT, WL_POINTER_BUTTON_STATE_RELEASED);
        notify_pointer_frame(&probe->seat);
    } else if (!strcmp(operation, "copy") || !strcmp(operation, "paste")) {
        bool paste = !strcmp(operation, "paste");
        key(probe, &now, KEY_LEFTCTRL, true);
        if (paste) key(probe, &now, KEY_LEFTSHIFT, true);
        key(probe, &now, paste ? KEY_V : KEY_C, true);
        key(probe, &now, paste ? KEY_V : KEY_C, false);
        if (paste) key(probe, &now, KEY_LEFTSHIFT, false);
        key(probe, &now, KEY_LEFTCTRL, false);
    } else if (!strcmp(operation, "pinyin")) {
        const unsigned int keys[]={KEY_Z,KEY_H,KEY_O,KEY_N,KEY_G,KEY_W,KEY_E,KEY_N,KEY_SPACE};
        for (size_t i=0;i<sizeof(keys)/sizeof(keys[0]);i++) {
            key(probe,&now,keys[i],true);key(probe,&now,keys[i],false);
        }
    } else if (!strcmp(operation, "wheel-up") || !strcmp(operation, "wheel-down") ||
               !strcmp(operation,"finger-up") || !strcmp(operation,"finger-down")) {
        bool finger=!strncmp(operation,"finger-",7);
        int direction = strstr(operation,"down") ? 1 : -1;
        struct weston_pointer_axis_event axis = {
            .axis = WL_POINTER_AXIS_VERTICAL_SCROLL, .value = 60 * direction,
            .has_discrete = !finger, .discrete = finger ? 0 : 5 * direction
        };
        notify_axis_source(&probe->seat, finger ? WL_POINTER_AXIS_SOURCE_FINGER : WL_POINTER_AXIS_SOURCE_WHEEL);
        notify_axis(&probe->seat, &now, &axis);
        notify_pointer_frame(&probe->seat);
        if (finger) { axis.value=0;notify_axis(&probe->seat,&now,&axis);notify_pointer_frame(&probe->seat); }
    } else {
        return false;
    }
    wl_display_flush_clients(probe->compositor->wl_display);
    return true;
}

static int read_command(int fd, uint32_t mask, void *data) {
    struct connection *client = data;
    if (mask & (WL_EVENT_HANGUP | WL_EVENT_ERROR)) { close_connection(client); return 0; }
    ssize_t count = recv(fd, client->buffer + client->used, sizeof(client->buffer) - client->used - 1, 0);
    if (count < 0 && errno == EAGAIN) return 0;
    if (count <= 0) { close_connection(client); return 0; }
    client->used += (size_t)count;
    client->buffer[client->used] = 0;
    if (!strchr(client->buffer, '\n') && client->used < sizeof(client->buffer) - 1) return 0;
    const char *reply = command(client) ? "OK\n" : "ERROR\n";
    (void)send(fd, reply, strlen(reply), MSG_NOSIGNAL);
    close_connection(client);
    return 0;
}

static int accept_client(int fd, uint32_t mask, void *data) {
    (void)mask;
    struct probe *probe = data;
    int accepted = accept4(fd, NULL, NULL, SOCK_NONBLOCK | SOCK_CLOEXEC);
    if (accepted < 0) return 0;
    struct connection *client = calloc(1, sizeof(*client));
    if (!client) { close(accepted); return 0; }
    client->fd = accepted;
    client->probe = probe;
    socklen_t length = sizeof(client->peer);
    if (getsockopt(accepted, SOL_SOCKET, SO_PEERCRED, &client->peer, &length) < 0) {
        close(accepted); free(client); return 0;
    }
    client->source = wl_event_loop_add_fd(wl_display_get_event_loop(probe->compositor->wl_display),
                                          accepted, WL_EVENT_READABLE, read_command, client);
    if (!client->source) { close(accepted); free(client); return 0; }
    wl_list_insert(&probe->clients, &client->link);
    return 0;
}

static void destroy_probe(struct wl_listener *listener, void *data) {
    (void)data;
    struct probe *probe = wl_container_of(listener, probe, destroy);
    struct connection *client, *next;
    wl_list_for_each_safe(client, next, &probe->clients, link) close_connection(client);
    wl_event_source_remove(probe->source);
    close(probe->fd);
    unlink(probe->path);
    weston_seat_release(&probe->seat);
    wl_list_remove(&probe->destroy.link);
    free(probe);
}

__attribute__((visibility("default")))
int wet_module_init(struct weston_compositor *compositor, int *argc, char *argv[]) {
    (void)argc; (void)argv;
    const char *path = getenv("VTC_WAYLAND_INPUT");
    const char *runtime = getenv("XDG_RUNTIME_DIR");
    if (!getenv("VTC_PRIVATE_WAYLAND") || strcmp(getenv("VTC_PRIVATE_WAYLAND"),"1") || !path || !runtime ||
        strncmp(path, runtime, strlen(runtime)) || path[strlen(runtime)] != '/') return -1;
    struct probe *probe = calloc(1, sizeof(*probe));
    if (!probe || strlen(path) >= sizeof(probe->path)) { free(probe); return -1; }
    struct sockaddr_un address = {.sun_family = AF_UNIX};
    strcpy(address.sun_path, path);
    strcpy(probe->path, path);
    probe->fd = socket(AF_UNIX, SOCK_STREAM | SOCK_NONBLOCK | SOCK_CLOEXEC, 0);
    if (probe->fd < 0 || bind(probe->fd, (struct sockaddr *)&address, sizeof(address)) < 0) {
        if (probe->fd >= 0) close(probe->fd);
        free(probe); return -1;
    }
    if (chmod(path, 0600) < 0 || listen(probe->fd, 4) < 0) {
        close(probe->fd); unlink(path); free(probe); return -1;
    }
    probe->compositor = compositor;
    wl_list_init(&probe->clients);
    weston_seat_init(&probe->seat, compositor, "vtc-private-test");
    if (weston_seat_init_pointer(&probe->seat) < 0 || weston_seat_init_keyboard(&probe->seat, NULL) < 0) {
        weston_seat_release(&probe->seat); close(probe->fd); unlink(path); free(probe); return -1;
    }
    probe->source = wl_event_loop_add_fd(wl_display_get_event_loop(compositor->wl_display),
                                        probe->fd, WL_EVENT_READABLE, accept_client, probe);
    if (!probe->source) {
        weston_seat_release(&probe->seat); close(probe->fd); unlink(path); free(probe); return -1;
    }
    weston_compositor_add_destroy_listener_once(compositor, &probe->destroy, destroy_probe);
    return 0;
}
