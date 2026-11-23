package desktop

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPreferencePersistenceAndValidation(t *testing.T) {
	dir := t.TempDir()
	prefs := OpenPreferences(dir)
	snapshot, warning := prefs.Snapshot()
	if warning != "" {
		t.Fatal(warning)
	}
	snapshot.Values = map[string]string{
		"vtc:locale": "zh-CN", "vtc:font-size": "15", "vtc:sidebar-collapsed": "1", "vtc:recent-host": "local",
		"vtc:order:local": `{"mode":"manual","order":["开发"],"pinned":["开发"]}`,
	}
	if err := prefs.Save(snapshot); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(prefs.Save(snapshot), ErrPreferenceConflict) {
		t.Fatal("lost-update allowed")
	}
	loaded, warning := OpenPreferences(dir).Snapshot()
	if warning != "" || loaded.Revision != 1 || loaded.Values["vtc:locale"] != "zh-CN" {
		t.Fatal("preferences not restored")
	}
	snapshot.Values["vtc:locale"] = "en"
	unchanged, _ := prefs.Snapshot()
	if unchanged.Values["vtc:locale"] != "zh-CN" {
		t.Fatal("caller map mutated committed state")
	}
	path := filepath.Join(dir, "desktop-preferences.json")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("permissions %v", info.Mode())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate a process interrupted before its staging file was renamed.
	interrupted := filepath.Join(dir, ".desktop-preferences-interrupted")
	if err := os.WriteFile(interrupted, []byte("partial"), 0600); err != nil {
		t.Fatal(err)
	}
	if restored, warning := OpenPreferences(dir).Snapshot(); warning != "" || restored.Revision != 1 {
		t.Fatal("interrupted staging write replaced committed preferences")
	}
	for _, key := range []string{"visual-tmux-client-token", "draft", "credential"} {
		loaded.Values[key] = "secret"
		if prefs.Save(loaded) == nil {
			t.Fatal("non-preference data allowed")
		}
		delete(loaded.Values, key)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(data) {
		t.Fatal("rejected save changed durable file")
	}
	loaded.Values["vtc:order:other"] = `{"mode":"manual","order":["same"],"pinned":[]}`
	if err := prefs.Save(loaded); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
}

func TestCorruptAndFailedPreferencesPreserveFile(t *testing.T) {
	contents := []string{
		"broken JSON", `{"version":2,"values":{}}`,
		`{"version":1,"values":{"visual-tmux-client-token":"secret"}}`, strings.Repeat("x", bridgeBodyLimit+1),
	}
	for _, content := range contents {
		dir := t.TempDir()
		path := filepath.Join(dir, "desktop-preferences.json")
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		prefs := OpenPreferences(dir)
		snapshot, warning := prefs.Snapshot()
		if warning == "" || len(snapshot.Values) != 0 {
			t.Fatal("corrupt file not handled")
		}
		data, err := os.ReadFile(path)
		if err != nil || string(data) != content {
			t.Fatal("corrupt file replaced")
		}
	}
	dir := t.TempDir()
	prefs := OpenPreferences(dir)
	// A staging file cannot be created under a regular file (also works as root).
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	prefs.path = filepath.Join(blocker, "desktop-preferences.json")
	snapshot, _ := prefs.Snapshot()
	snapshot.Values["vtc:locale"] = "en"
	if prefs.Save(snapshot) == nil {
		t.Fatal("failed persistence reported success")
	}
	if current, _ := prefs.Snapshot(); current.Revision != 0 {
		t.Fatal("failed write advanced revision")
	}
}

func TestPreferenceEndpointsRequireAuthorization(t *testing.T) {
	prefs := OpenPreferences(t.TempDir())
	bridge := NewBridge("test-bearer")
	if err := bridge.SetOrigin(testOrigin); err != nil {
		t.Fatal(err)
	}
	prefs.Attach(bridge)
	for _, name := range []string{"preferences-read", "preferences-write"} {
		if bridgeRequest(bridge, name, `{}`, testOrigin, "").Code != 401 {
			t.Fatal("preferences public")
		}
	}
	saveJSON := `{"version":1,"revision":0,"values":{"vtc:locale":"en"}}`
	if bridgeRequest(bridge, "preferences-write", saveJSON, testOrigin, "test-bearer").Code != 200 {
		t.Fatal("save failed")
	}
	resp := bridgeRequest(bridge, "preferences-read", `{}`, testOrigin, "test-bearer")
	if resp.Code != 200 || !strings.Contains(resp.Body.String(), `"vtc:locale":"en"`) {
		t.Fatal("read failed")
	}
	staleJSON := `{"version":1,"revision":0,"values":{}}`
	if bridgeRequest(bridge, "preferences-write", staleJSON, testOrigin, "test-bearer").Code != 409 {
		t.Fatal("conflict not reported")
	}
}
