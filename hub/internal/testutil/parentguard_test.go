package testutil

import (
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLiveSocketsPreservesCommasInPath(t *testing.T) {
	t.Setenv("TMUX", "/tmp/a,b/socket,4242,0")
	if got := liveSockets()[0].socket; got != "/tmp/a,b/socket" {
		t.Fatalf("socket path changed: %q", got)
	}
}

func TestSessionsPreservesWhitespace(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "fake-tmux")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nprintf 'project  alpha\\n'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	w := watch{tmux: bin, env: withoutEnv(os.Environ(), "TMUX", "TMUX_PANE", "TMUX_TMPDIR")}
	got, up := w.sessions("/unused")
	if !up || got != "project  alpha" {
		t.Fatalf("session names lost whitespace: %q (up=%v)", got, up)
	}
}

// guarding returns a guard-shaped directory holding one unix socket, and the
// path of that socket. A socket file with nothing behind it is the state tmux
// leaves behind when a server dies, which is what these cases turn on.
func guarding(t *testing.T) (string, string) {
	t.Helper()
	dir := SocketDir(t)
	socketDir := filepath.Join(dir, socketDirName())
	if err := os.Mkdir(socketDir, 0o700); err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(socketDir, "s")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := listener.Close(); err != nil {
			t.Error(err)
		}
	})
	return dir, socket
}

// stubTmux writes a script standing in for the tmux binary, so the two halves of
// a stop -- whether anything answers, and whether it goes away -- can be chosen
// separately. reap's decision rests on the difference.
func stubTmux(t *testing.T, body string) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "fake-tmux")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"+body), 0o700); err != nil {
		t.Fatal(err)
	}
	return bin
}

// answersWithoutStopping is a tmux whose server keeps answering and refuses to
// die. Nothing else reproduces the one outcome cleanup must not mistake for a
// dead socket.
const answersWithoutStopping = `for arg in "$@"; do
	if [ "$arg" = kill-server ]; then exit 1; fi
done
printf 'stuck\n'
exit 0
`

// deadSocket is a tmux that can neither answer nor stop anything, standing in
// for the socket file tmux leaves behind after a server dies.
const deadSocket = "exit 1\n"

func TestReapPreservesALiveServerThatWouldNotStop(t *testing.T) {
	dir, socket := guarding(t)
	w := watch{tmux: stubTmux(t, answersWithoutStopping), alsoIn: dir}
	w.reap()
	if _, err := os.Stat(socket); err != nil {
		t.Fatalf("cleanup removed a socket a live server still answers on: %v", err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("cleanup removed the directory of a server that would not stop: %v", err)
	}
}

// The directory used to be kept whenever a stop failed, and a killed server
// leaves its socket file behind -- so every incident the guard caught left a
// directory in /tmp for good, now that no run sweeps another's.
func TestReapRemovesTheDirectoryOfASocketWithNothingBehindIt(t *testing.T) {
	dir, _ := guarding(t)
	w := watch{tmux: stubTmux(t, deadSocket), alsoIn: dir}
	w.reap()
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("a socket with nothing behind it kept its directory: %v", err)
	}
}

// The same case against a real tmux: a test that kills the stand-in is the
// incident the guard exists to catch, and cleanup must not read the socket file
// it leaves as a server that refused to die.
func TestReapRemovesTheDirectoryOfADeadStandIn(t *testing.T) {
	w := newWatch(t)
	// What a test that names no socket does to the stand-in, which is the whole
	// point of the guard.
	_, _, _ = tmuxRun(w.tmux, w.env, "-S", w.standIn, "kill-server") //nolint:errcheck // the incident, reproduced

	w.reap()
	if _, err := os.Stat(w.alsoIn); !os.IsNotExist(err) {
		t.Fatalf("the directory survived a stand-in a test had already killed: %v", err)
	}
}

// The stand-in is the one socket cleanup knows it started, so it must be
// stopped even when the directory it lives in cannot be listed -- otherwise a
// test that removed that directory would leave the server running with its
// socket unlinked, which is a server nobody can reach or stop.
func TestReapStopsTheStandInWithoutListingTheDirectory(t *testing.T) {
	socket, env := otherServer(t, "stand-in")
	bin, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("tmux not available")
	}
	w := watch{tmux: bin, env: env, standIn: socket, alsoIn: filepath.Join(SocketDir(t), "absent")}

	w.reap()
	if _, up := w.sessions(socket); up {
		t.Fatal("the stand-in survived cleanup that could not list its directory")
	}
}

// kill refuses a symlink and reports the entry settled, so reap goes on to
// remove the directory the link lives in. That is only safe because removing a
// directory does not follow the links inside it -- if it did, a name inside the
// guard's directory could aim cleanup at a real server's socket directory.
func TestReapDoesNotFollowASymlinkedSocketDirectory(t *testing.T) {
	target := SocketDir(t)
	if err := os.WriteFile(filepath.Join(target, "keep"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	dir := SocketDir(t)
	if err := os.Symlink(target, filepath.Join(dir, socketDirName())); err != nil {
		t.Fatal(err)
	}
	bin, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("tmux not available")
	}

	w := watch{tmux: bin, env: withoutEnv(os.Environ(), "TMUX", "TMUX_PANE", "TMUX_TMPDIR"), alsoIn: dir}
	w.reap()
	if _, err := os.Stat(filepath.Join(target, "keep")); err != nil {
		t.Fatalf("cleanup reached through a symlinked socket directory: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("cleanup left its own directory behind: %v", err)
	}
}

func TestKillDoesNotActOnASymlink(t *testing.T) {
	for _, parentLink := range []bool{false, true} {
		dir := SocketDir(t)
		target := filepath.Join(dir, "target")
		if err := os.Mkdir(target, 0o700); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(dir, "link")
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
		socket := link
		if parentLink {
			socket = filepath.Join(link, "socket")
		}
		// A nonexistent command would also fail, so use an executable that leaves
		// evidence if cleanup ever gets past the path checks.
		marker := filepath.Join(dir, "invoked")
		bin := stubTmux(t, "touch \"$MARKER\"\n")
		w := watch{tmux: bin, env: append(withoutEnv(os.Environ(), "TMUX", "TMUX_PANE", "TMUX_TMPDIR"),
			"MARKER="+marker)}
		settled := w.kill(socket)
		if _, err := os.Stat(marker); !os.IsNotExist(err) {
			t.Fatalf("cleanup executed on a symlink: %v", err)
		}
		// Nothing was stranded: removing our own directory removes the link
		// itself and never follows it, so cleanup may proceed.
		if !settled {
			t.Fatal("a refused path was treated as a server that would not stop")
		}
	}
}

// ParentGuard can only fail a run if the watch actually notices, and a guard
// that has stopped noticing looks exactly like a guard that passes. These cases
// pin the noticing down. They are the reason a green `make test` means anything.

// newWatch brings up a stand-in the way ParentGuard does and returns a watch
// ready to judge a finished run.
func newWatch(t *testing.T) watch {
	t.Helper()
	bin, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("tmux not available")
	}
	dir := SocketDir(t)
	socket := filepath.Join(dir, socketDirName(), guardSession)
	env := withoutEnv(os.Environ(), "TMUX", "TMUX_PANE", "TMUX_TMPDIR")
	env = append(env, "TMUX_TMPDIR="+dir)

	if !startStandIn(bin, env, socket, standInCommand()) {
		t.Fatal("the stand-in did not start, so the guard would be checking nothing")
	}
	t.Cleanup(func() {
		_, _, _ = tmuxRun(bin, env, "-S", socket, "kill-server") //nolint:errcheck // throwaway server
	})
	return watch{
		tmux: bin, env: env, standIn: socket, alsoIn: dir,
		before: socketDirEntries(dir),
	}
}

// otherServer starts a second disposable server, standing in for a server a
// test might reach: the session the run is inside, or the default position.
func otherServer(t *testing.T, sessions ...string) (string, []string) {
	t.Helper()
	bin, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("tmux not available")
	}
	dir := SocketDir(t)
	socket := filepath.Join(dir, socketDirName(), "lived")
	env := withoutEnv(os.Environ(), "TMUX", "TMUX_PANE", "TMUX_TMPDIR")
	env = append(env, "TMUX_TMPDIR="+dir)
	// tmux will not create the parent of an -S path, and reports the failure on
	// stderr while still exiting 0 -- the trap startStandIn exists to avoid.
	if err := os.MkdirAll(filepath.Dir(socket), socketDirMode); err != nil {
		t.Fatalf("create the socket directory: %v", err)
	}
	for _, name := range sessions {
		if _, code, err := tmuxRun(bin, env, "-S", socket, "-f", "/dev/null",
			"new-session", "-d", "-s", name, "sleep 300"); err != nil || code != 0 {
			t.Fatalf("could not start %s: code=%d err=%v", name, code, err)
		}
	}
	if _, up := (watch{tmux: bin, env: env, standIn: socket}).standInSessions(); !up {
		t.Fatal("the second server did not come up, so the case would test nothing")
	}
	t.Cleanup(func() {
		_, _, _ = tmuxRun(bin, env, "-S", socket, "kill-server") //nolint:errcheck // throwaway server
	})
	return socket, env
}

// watching returns a watch that treats socket as the server it should not touch.
func (w watch) watching(t *testing.T, socket string) watch {
	t.Helper()
	before, up := w.sessions(socket)
	w.live = []live{{socket: socket, before: before, wasUp: up, own: true}}
	return w
}

func TestProblemIsQuietWhenNothingWasTouched(t *testing.T) {
	w := newWatch(t)
	if problem := w.problem(); problem != "" {
		t.Fatalf("an untouched run must pass, got %q", problem)
	}
}

func TestProblemReportsAStandInThatWasKilled(t *testing.T) {
	w := newWatch(t)
	// What a test that names no socket does to it, which is the whole point.
	_, _, _ = tmuxRun(w.tmux, w.env, "-S", w.standIn, "kill-server") //nolint:errcheck // throwaway server

	problem := w.problem()
	if problem == "" {
		t.Fatal("a killed stand-in was not reported: the guard is vacuous")
	}
	if !strings.Contains(problem, "stand-in") {
		t.Errorf("the report should say what went wrong, got %q", problem)
	}
}

// A tmux server that dies leaves its socket file behind -- which is why tmux
// distinguishes "no server running on <path>" from "error connecting ... (No
// such file or directory)". A file-existence check therefore reports a killed
// server, and a stand-in whose session could not run, as healthy.
func TestProblemNoticesTheLiveServerDied(t *testing.T) {
	w := newWatch(t)
	liveSocket, liveEnv := otherServer(t, "keepme")
	w = w.watching(t, liveSocket)

	_, _, _ = tmuxRun(w.tmux, liveEnv, "-S", liveSocket, "kill-server") //nolint:errcheck // throwaway server

	if _, err := os.Stat(liveSocket); err != nil {
		t.Fatalf("this tmux removed the socket when the server died (%v), so the "+
			"distinction this check rests on does not hold here", err)
	}
	if problem := w.problem(); !strings.Contains(problem, "no server answering") {
		t.Fatalf("a live server that died was not reported correctly, got %q", problem)
	}
}

// The narrower failure a liveness-only check cannot see: the server keeps
// running, so nothing looks wrong, but a test killed a session inside it. That
// is the shape of the original incident.
func TestProblemNoticesASessionKilledOnTheLiveServer(t *testing.T) {
	w := newWatch(t)
	liveSocket, liveEnv := otherServer(t, "keepme", "precious-work")
	w = w.watching(t, liveSocket)

	_, _, _ = tmuxRun(w.tmux, liveEnv, "-S", liveSocket, //nolint:errcheck // the damage under test
		"kill-session", "-t", "precious-work")

	if _, stillUp := w.sessions(liveSocket); !stillUp {
		t.Fatal("the server should still be answering, which is the point of this case")
	}
	problem := w.problem()
	if problem == "" {
		t.Fatal("a session killed on the live server was not reported: the guard is vacuous")
	}
	if !strings.Contains(problem, "precious-work") {
		t.Errorf("the report should name the sessions it saw, got %q", problem)
	}
	// Which position changed decides how much the observation is worth, so the
	// report has to say: this one is the session the run started inside, not the
	// default position, which belongs to nobody.
	if !strings.Contains(problem, "this run started inside") {
		t.Errorf("the report should name the position it watched, got %q", problem)
	}
}

// A server that appears where there was none is the other half: a test that
// drops both TMUX and TMUX_TMPDIR lands on the default socket and can start one
// there. Comparing only "was it up, is it up" misses it.
func TestProblemNoticesAServerThatAppeared(t *testing.T) {
	w := newWatch(t)
	liveSocket, _ := otherServer(t, "intruder")
	// As if nothing had been there when the run began.
	w.live = []live{{socket: liveSocket, before: "", wasUp: false}}

	problem := w.problem()
	if problem == "" {
		t.Fatal("a server that appeared where there was none was not reported")
	}
	if !strings.Contains(problem, "intruder") {
		t.Errorf("the report should name what it found, got %q", problem)
	}
	// This entry carries no own flag, which is the default position a stripped
	// tmux falls back to -- the one an unrelated server can appear on.
	if !strings.Contains(problem, "the default position") {
		t.Errorf("the report should say which position it watched, got %q", problem)
	}
}

func TestProblemNoticesASecondServerInTheGuardDirectory(t *testing.T) {
	w := newWatch(t)
	extra := filepath.Join(w.alsoIn, socketDirName(), "another")
	if _, code, err := tmuxRun(w.tmux, w.env, "-S", extra, "-f", "/dev/null",
		"new-session", "-d", "-s", "stray", "sleep 300"); err != nil || code != 0 {
		t.Fatalf("could not start the stray server: code=%d err=%v", code, err)
	}
	t.Cleanup(func() {
		_, _, _ = tmuxRun(w.tmux, w.env, "-S", extra, "kill-server") //nolint:errcheck // throwaway server
	})

	problem := w.problem()
	if problem == "" {
		t.Fatal("a second server started in the guard's directory was not reported")
	}
	if !strings.Contains(problem, "server of its own") {
		t.Errorf("the report should say a second server appeared, got %q", problem)
	}
}

// A socket file with nothing behind it must not be mistaken for a working
// server. This is how a stand-in whose session could not run fooled an earlier
// version into blaming a run in which every test passed.
func TestASocketFileIsNotAServer(t *testing.T) {
	bin, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("tmux not available")
	}
	dir := SocketDir(t)
	socket := filepath.Join(dir, socketDirName(), "stale")
	env := withoutEnv(os.Environ(), "TMUX", "TMUX_PANE", "TMUX_TMPDIR")
	env = append(env, "TMUX_TMPDIR="+dir)
	if err := os.MkdirAll(filepath.Dir(socket), socketDirMode); err != nil {
		t.Fatalf("create the socket directory: %v", err)
	}

	// A session whose command cannot be executed closes at once, leaving the
	// socket file behind with no server to answer on it.
	_, _, _ = tmuxRun(bin, env, "-S", socket, "-f", "/dev/null", //nolint:errcheck // its aftermath is the point
		"new-session", "-d", "-s", "stale", "/nonexistent-binary-for-this-test")

	if _, err := os.Stat(socket); err != nil {
		t.Fatalf("this tmux did not leave a socket behind (%v), so the case does not arise", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		_, up := (watch{tmux: bin, env: env, standIn: socket}).standInSessions()
		if !up {
			return // a socket file with no server behind it is not read as one
		}
		if time.Now().After(deadline) {
			t.Fatal("the socket file still has a server behind it, so the case does not arise")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// startStandIn must refuse a stand-in whose session cannot run, because the
// socket file it leaves behind looks healthy.
func TestStartStandInRejectsASessionThatCannotRun(t *testing.T) {
	bin, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("tmux not available")
	}
	dir := SocketDir(t)
	socket := filepath.Join(dir, socketDirName(), guardSession)
	env := withoutEnv(os.Environ(), "TMUX", "TMUX_PANE", "TMUX_TMPDIR")
	env = append(env, "TMUX_TMPDIR="+dir)

	if startStandIn(bin, env, socket, "/nonexistent-binary-for-this-test") {
		t.Fatal("a stand-in whose session cannot run was reported as serving")
	}
}

// The settle is what catches a session that is still up when it is first asked
// and gone a moment later -- the race that made an earlier version blame a run
// in which every test passed. A session told to live for a fraction of the
// settle reproduces that window deterministically: the first check sees it, the
// second does not.
func TestStartStandInRejectsASessionThatOutlivesItsFirstCheck(t *testing.T) {
	bin, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("tmux not available")
	}
	dir := SocketDir(t)
	socket := filepath.Join(dir, socketDirName(), guardSession)
	env := withoutEnv(os.Environ(), "TMUX", "TMUX_PANE", "TMUX_TMPDIR")
	env = append(env, "TMUX_TMPDIR="+dir)

	command := "sh -c 'sleep 0.1'"
	if startStandIn(bin, env, socket, command) {
		t.Fatal("a stand-in whose session died during the settle was reported as serving")
	}
}

// The watched positions are the whole reason the layer works: the session the
// run is inside, and the default socket a fully stripped tmux falls back to.
func TestLiveSocketsCoversBothPositions(t *testing.T) {
	fallback := filepath.Join(guardRoot, socketDirName(), "default")

	t.Setenv("TMUX", "/tmp/somewhere/tmux-501/inside,4242,0")
	got := liveSockets()
	if len(got) != 2 {
		t.Fatalf("want the session's socket and the default position, got %d entries", len(got))
	}
	if got[0].socket != "/tmp/somewhere/tmux-501/inside" || !got[0].own {
		t.Errorf("the first watched socket should be the one $TMUX names, got %+v", got[0])
	}
	if got[1].socket != fallback || got[1].own {
		t.Errorf("the second should be the default position, got %+v", got[1])
	}

	// Claude Code's Bash environment strips TMUX, which must not leave the
	// layer watching nothing at all.
	t.Setenv("TMUX", "")
	got = liveSockets()
	if len(got) != 1 || got[0].socket != fallback {
		t.Fatalf("with no $TMUX the default position must still be watched, got %+v", got)
	}
}
