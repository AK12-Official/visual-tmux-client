package testutil

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// guardSession is the name of the stand-in session. It is also the marker the
// guard looks for afterwards, so it must be a name no test would create.
const guardSession = "parent-guard"

// guardRoot holds the stand-in's socket. /tmp is deliberate rather than
// os.TempDir(): a unix socket path has to stay under the platform's sun_path
// limit, and $TMPDIR on macOS is a long /var/folders path. It is also the
// directory tmux itself defaults to, so the guard exercises the same one.
const guardRoot = "/tmp"

// socketDirMode keeps the stand-in's socket directory private to the run.
const socketDirMode = 0o700

// probeTimeout bounds every tmux call the guard makes. A tmux server that is
// wedged rather than dead would otherwise block a connect indefinitely, and the
// probes run before m.Run(), so `go test -timeout` cannot interrupt them.
const probeTimeout = 5 * time.Second

// standInSettle is how long to let a stand-in prove it stays up. See
// startStandIn: a session that cannot run its command dies in milliseconds.
const standInSettle = 250 * time.Millisecond

// ParentGuard stands in for the developer's tmux server while a package's tests
// run, so that a test which forgets to isolate itself fails loudly here instead
// of quietly killing the session someone is working in.
//
// Wire it up from every package whose tests can start a tmux server:
//
//	func TestMain(m *testing.M) {
//		os.Exit(testutil.ParentGuard(m))
//	}
//
// The guard cannot make a test isolated -- only the test can do that. What it
// does is remove the silence. It starts a disposable server, points TMUX,
// TMUX_PANE and TMUX_TMPDIR at it exactly as a real session would, and then
// checks what the run did:
//
//   - A test that spawns tmux with a verbatim environment reaches the stand-in,
//     so whatever it does shows up in that server or in its socket directory.
//   - A test that drops TMUX but keeps the inherited TMUX_TMPDIR stays in the
//     stand-in's private directory. tmux discards a TMUX_TMPDIR naming a missing
//     directory in favour of the default one, and the guard creates its own, so
//     that fallback cannot happen by accident here.
//   - A test that drops both reaches the default socket, which is why the guard
//     watches that position as well as the session the run started inside. It
//     compares each one's session list before and after, since a session killed
//     or created there is the incident and leaves the server itself running.
//
// A package with no tmux installed runs its tests unguarded: there is nothing a
// test could reach. If tmux is installed but the guard cannot arm itself, the
// run fails before any test executes, because a guard that failed to arm must
// not look like one that passed -- `go test` hides the output of a passing
// package, so a warning would be invisible.
//
// Two limits worth knowing. Sessions are compared by name, so damage that
// leaves the list alone (`set-option`, `send-keys`) is invisible. And the
// stand-in's session ends by itself when this process does, so an interrupted
// run can leave its private directory behind. Later runs do not sweep other
// directories: a matching name and a failed probe do not prove ownership.
func ParentGuard(m *testing.M) int {
	tmux, err := exec.LookPath("tmux")
	if err != nil {
		return m.Run()
	}

	dir, err := os.MkdirTemp(guardRoot, "vtcguard")
	if err != nil {
		return failToArm(fmt.Sprintf("could not create a stand-in directory under %s (%v)", guardRoot, err))
	}

	socket := filepath.Join(dir, socketDirName(), guardSession)
	// Belt and braces rather than a requirement: every call below passes -S,
	// which already ignores $TMUX. Scrubbing keeps that true if a helper is
	// ever added that names no socket of its own.
	env := withoutEnv(os.Environ(), "TMUX", "TMUX_PANE", "TMUX_TMPDIR")
	env = append(env, "TMUX_TMPDIR="+dir)

	w := watch{tmux: tmux, env: env, standIn: socket, alsoIn: dir}
	defer w.reap()

	if !startStandIn(tmux, env, socket, standInCommand()) {
		return failToArm(fmt.Sprintf("the stand-in server would not start at %s", socket))
	}

	w.before = socketDirEntries(dir)
	w.live = liveSockets()
	for i := range w.live {
		w.live[i].before, w.live[i].wasUp = w.sessions(w.live[i].socket)
	}

	pid := "0"
	if out, code, err := tmuxRun(tmux, env, "-S", socket, "display-message", "-p", "#{pid}"); err == nil && code == 0 {
		pid = strings.TrimSpace(out)
	}
	setProcessEnv("TMUX", socket+","+pid+",0")
	setProcessEnv("TMUX_PANE", "%0")
	setProcessEnv("TMUX_TMPDIR", dir)
	if os.Getenv("VTC_GUARD_VERBOSE") != "" {
		names := make([]string, 0, len(w.live))
		for _, l := range w.live {
			names = append(names, fmt.Sprintf("%q(up=%v)", l.socket, l.wasUp))
		}
		fmt.Fprintf(os.Stderr, "isolation guard active: stand-in %s, watching %s\n",
			socket, strings.Join(names, " "))
	}

	code := m.Run()

	if problem := w.problem(); problem != "" {
		fmt.Fprint(os.Stderr, problem)
		return 1
	}
	return code
}

// live is one socket a fully-scrubbed test could reach, and what it looked like
// when the run started.
type live struct {
	socket string
	before string // its session list then
	wasUp  bool   // and whether it was answering at all
	own    bool   // true for the session this run started inside
}

// watch is everything the guard needs to judge a finished run.
type watch struct {
	tmux    string
	env     []string
	standIn string // the socket of the disposable server
	alsoIn  string // its directory, where a second server would appear
	before  []string
	live    []live
}

// failToArm reports a run that could not be guarded and refuses to run it.
func failToArm(why string) int {
	fmt.Fprintf(os.Stderr, "tmux is installed but the isolation guard could not arm "+
		"(%s), so this test run would be unguarded. Failing instead of running tests "+
		"that could reach a live session.\n", why)
	return 1
}

// standInCommand is what the stand-in's one session runs. It polls this
// process, so the session ends when the test process does and takes the server
// with it -- a run killed outright leaves nothing running for the next hour.
func standInCommand() string {
	return fmt.Sprintf("sh -c 'while kill -0 %d 2>/dev/null; do sleep 2; done'", os.Getpid())
}

// liveSockets is every socket a test could reach with the environment stripped:
// the session this run started inside, when $TMUX names it, and the default
// position, which is where a tmux that has dropped both TMUX and TMUX_TMPDIR
// lands. Watching only the first misses the fallback; watching only the second
// misses the session the run is actually inside.
func liveSockets() []live {
	var out []live
	if value := os.Getenv("TMUX"); value != "" {
		// Socket paths can contain commas; PID and session ID are the last fields.
		socket := value
		for range 2 {
			if i := strings.LastIndex(socket, ","); i >= 0 {
				socket = socket[:i]
			}
		}
		out = append(out, live{socket: socket, own: true})
	}
	return append(out, live{socket: filepath.Join(guardRoot, socketDirName(), "default")})
}

// reap cleans this run's private directory. It keeps the directory only when a
// server was still answering and would not stop -- the one outcome a probe by
// hand could say more about than the failure message. Everything else removes
// it, because nothing left in it can still be serving.
func (w watch) reap() {
	if w.tmux == "" {
		return
	}
	// The stand-in is stopped by name before the directory is listed. It is the
	// one socket cleanup knows is ours by construction, so finding it must not
	// depend on being able to read the directory it lives in -- which is also
	// why the listing below skips it.
	clean := w.standIn == "" || w.kill(w.standIn)
	for _, name := range socketDirEntries(w.alsoIn) {
		if name == guardSession {
			continue
		}
		if !w.kill(filepath.Join(w.alsoIn, socketDirName(), name)) {
			clean = false
		}
	}
	if clean {
		if err := os.RemoveAll(w.alsoIn); err != nil {
			fmt.Fprintf(os.Stderr, "tmux isolation guard: cleanup %s: %v\n", w.alsoIn, err)
		}
	} else {
		fmt.Fprintf(os.Stderr, "tmux isolation guard: preserving %s; a tmux server "+
			"inside it is still answering\n", w.alsoIn)
	}
}

// kill stops whatever is serving on a socket and reports whether the entry is
// settled, which is what lets reap decide whether the directory is worth
// keeping.
//
// It refuses symlinks, including the parent directory, before connecting: a
// name inside our directory must not redirect cleanup to another server. A
// refused path strands nothing, because removing our own directory removes the
// entry itself and never follows it.
func (w watch) kill(socket string) bool {
	parent, err := os.Lstat(filepath.Dir(socket))
	if err != nil || !parent.IsDir() {
		return true
	}
	info, err := os.Lstat(socket)
	if os.IsNotExist(err) {
		return true
	}
	if err != nil || info.Mode()&os.ModeSocket == 0 {
		return true
	}
	if _, code, err := tmuxRun(w.tmux, w.env, "-S", socket, "kill-server"); err == nil && code == 0 {
		return true
	}
	// A stop that failed is a failure only if something is still answering. tmux
	// leaves the socket file behind when a server dies, so a stand-in a test
	// killed would otherwise read as a server that refused to die -- and the
	// directory would be kept for every incident the guard catches.
	_, up := w.sessions(socket)
	return !up
}

// problem reports what the run did to a server it had no business touching, or
// "" when it was clean.
func (w watch) problem() string {
	for _, l := range w.live {
		after, up := w.sessions(l.socket)
		if up == l.wasUp && after == l.before {
			continue
		}
		// The two positions differ in how much the observation is worth. The
		// socket this run started inside is one a test reaches by dropping both
		// TMUX and TMUX_TMPDIR; the default position belongs to nobody. Neither
		// is proof, because a developer working alongside the run can change
		// either one, so the report names the position and leaves the cause open.
		where := "the default position"
		if l.own {
			where = "the socket this run started inside"
		}
		return fmt.Sprintf("\ntmux isolation guard: a watched tmux server changed during "+
			"the run.\n\n%s (%s) held %s before the run and %s after. Check test "+
			"isolation and concurrent user activity; this observation alone cannot "+
			"identify the cause.\nSee \"Tmux safety\" in CONTRIBUTING.md.\n",
			l.socket, where, renderSessions(l.before, l.wasUp), renderSessions(after, up))
	}

	// A second server started inside the guard's own directory. It is contained,
	// but a test put it there and left it running, so say so.
	for _, name := range socketDirEntries(w.alsoIn) {
		if name != guardSession && !contains(w.before, name) {
			return fmt.Sprintf("\ntmux isolation guard: the test run started a tmux "+
				"server of its own at %s.\n\nIt was not the stand-in and the run did not "+
				"clean it up. Tests must target the socket the helpers give them -- see "+
				"\"Tmux safety\" in CONTRIBUTING.md.\n",
				filepath.Join(w.alsoIn, socketDirName(), name))
		}
	}

	after, up := w.standInSessions()
	if !up {
		return "\ntmux isolation guard: the test run killed the stand-in tmux server.\n\n" +
			"The stand-in exists so that this failure happens here instead of in a " +
			"developer's session, where it would have destroyed their work. The test " +
			"spawned tmux without an isolated environment.\nFix the test -- see " +
			"\"Tmux safety\" in CONTRIBUTING.md.\n"
	}
	if after != guardSession {
		return fmt.Sprintf("\ntmux isolation guard: the test run changed the stand-in tmux "+
			"server.\n\nIt should hold exactly one session, %q, but holds %s. The test "+
			"spawned tmux without an isolated environment.\nFix the test -- see "+
			"\"Tmux safety\" in CONTRIBUTING.md.\n", guardSession, renderSessions(after, up))
	}
	return ""
}

// sessions returns the named sessions a server is serving, and whether it
// answered at all. A tmux server that dies leaves its socket file behind, so
// only a connection distinguishes a dead server from a live one -- which is why
// the guard connects instead of stating the file.
func (w watch) sessions(socket string) (string, bool) {
	if socket == "" || w.tmux == "" {
		return "", false
	}
	out, code, err := tmuxRun(w.tmux, w.env, "-S", socket, "list-sessions", "-F", "#{session_name}")
	if err != nil || code != 0 {
		return "", false
	}
	return strings.TrimSuffix(out, "\n"), true
}

// standInSessions is sessions for the stand-in socket.
func (w watch) standInSessions() (string, bool) {
	return w.sessions(w.standIn)
}

// startStandIn brings up the disposable server and reports whether it is really
// serving. Nothing weaker works: the exit status is not evidence, because tmux
// reports a failed `new-session` on stderr and still exits 0, and the socket
// file is not evidence either, because a session whose command cannot be
// executed leaves the file behind with no server behind it. Either mistake
// makes the guard doubt a run that was in fact clean.
func startStandIn(tmux string, env []string, socket, command string) bool {
	if err := os.MkdirAll(filepath.Dir(socket), socketDirMode); err != nil {
		return false
	}
	_, _, _ = tmuxRun(tmux, env, "-S", socket, "-f", "/dev/null", //nolint:errcheck // asked by connecting below
		"new-session", "-d", "-s", guardSession, command)

	w := watch{tmux: tmux, env: env, standIn: socket}
	if serving, up := w.standInSessions(); !up || serving != guardSession {
		return false
	}
	// One check is not enough. A session whose command cannot be executed --
	// tmux runs it through a shell, so a `sh` missing from PATH is enough --
	// dies within milliseconds of starting and takes the server with it. Checked
	// once straight after `new-session`, it is still up, and the guard would then
	// blame a run in which every test passed.
	time.Sleep(standInSettle)
	serving, up := w.standInSessions()
	return up && serving == guardSession
}

// socketDirEntries lists the sockets under a guard directory, so a second
// server started there can be seen and reaped.
func socketDirEntries(dir string) []string {
	entries, err := os.ReadDir(filepath.Join(dir, socketDirName()))
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	sort.Strings(names)
	return names
}

func contains(names []string, want string) bool {
	for _, name := range names {
		if name == want {
			return true
		}
	}
	return false
}

// renderSessions formats a session list for a failure message without letting a
// newline inside it be printed as a literal backslash-n.
func renderSessions(names string, up bool) string {
	if !up {
		return "no server answering"
	}
	if names == "" {
		return "no sessions"
	}
	parts := strings.Split(names, "\n")
	for i, part := range parts {
		parts[i] = fmt.Sprintf("%q", part)
	}
	return strings.Join(parts, ", ")
}

// socketDirName is the directory tmux puts a named socket in under TMUX_TMPDIR.
func socketDirName() string {
	return fmt.Sprintf("tmux-%d", os.Getuid())
}

// setProcessEnv sets a variable for the test process and ignores the error,
// which cannot happen for a key that holds no "=".
func setProcessEnv(key, value string) {
	_ = os.Setenv(key, value) //nolint:errcheck // key holds no "="
}

// tmuxRun runs tmux with an explicit environment and returns its stdout, exit
// code, and any failure to run it at all. A non-zero exit is reported through
// the code, not as an error, because callers distinguish the two.
//
// Every call is bounded twice over: the context kills the process, and
// WaitDelay bounds the wait for its output pipes, which a surviving descendant
// could otherwise hold open indefinitely.
func tmuxRun(bin string, env []string, args ...string) (string, int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = env
	cmd.Stderr = io.Discard
	cmd.WaitDelay = probeTimeout
	var out strings.Builder
	cmd.Stdout = &out

	err := cmd.Run()
	code := 0
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		code, err = exit.ExitCode(), nil
	}
	return out.String(), code, err
}

// withoutEnv drops entries by key. It is a copy of tmux.FilterEnv rather than a
// call to it: this package exists for tests, and the code under test must not
// depend on it.
func withoutEnv(env []string, drop ...string) []string {
	dropSet := make(map[string]struct{}, len(drop))
	for _, name := range drop {
		dropSet[name] = struct{}{}
	}
	kept := make([]string, 0, len(env))
	for _, entry := range env {
		key, _, _ := strings.Cut(entry, "=")
		if _, dropped := dropSet[key]; !dropped {
			kept = append(kept, entry)
		}
	}
	return kept
}
