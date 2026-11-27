package tmux

import (
	"maps"
	"slices"
)

// These builders contain no execution or host filesystem access. The caller
// supplies the destination's home and transports each argument literally.
func ListArgs() []string { return []string{"list-sessions", "-F", SessionFormat} }

func HasSessionArgs(name string) []string {
	return []string{"has-session", "-t", ExactTarget(name)}
}

// CreateArgs builds the queue that starts the server when needed, installs the
// global options and creates a detached session. environment holds variables
// for that session only; tmux copies them into each window the session opens.
func CreateArgs(name, home string, environment map[string]string) []string {
	args := []string{"start-server"}
	for _, option := range globalOptions {
		args = append(args, ";", "set-option", option.scope, option.name, option.value)
	}
	newSession := []string{"new-session", "-d", "-s", name, "-c", home}
	for _, key := range slices.Sorted(maps.Keys(environment)) {
		newSession = append(newSession, "-e", key+"="+environment[key])
	}
	return append(append(args, ";"), newSession...)
}

func RenameArgs(oldName, newName string) []string {
	return []string{"rename-session", "-t", ExactTarget(oldName), "--", newName}
}

func KillArgs(name string) []string {
	return []string{"kill-session", "-t", ExactTarget(name)}
}

func AttachArgs(name string) []string {
	return []string{"attach-session", "-t", ExactTarget(name)}
}

func ListPanesArgs() []string { return []string{"list-panes", "-a", "-F", PaneFormat} }

func WorkingDirectoryArgs(name string) []string {
	return []string{"display-message", "-p", "-t", paneTargetOf(name), "#{pane_current_path}"}
}
