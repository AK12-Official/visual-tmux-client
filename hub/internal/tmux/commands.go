package tmux

// These builders contain no execution or host filesystem access. The caller
// supplies the destination's home and transports each argument literally.
func ListArgs() []string { return []string{"list-sessions", "-F", SessionFormat} }

func HasSessionArgs(name string) []string {
	return []string{"has-session", "-t", ExactTarget(name)}
}

func CreateArgs(name, home string) []string {
	args := []string{"start-server"}
	for _, option := range globalOptions {
		args = append(args, ";", "set-option", option.scope, option.name, option.value)
	}
	return append(args, ";", "new-session", "-d", "-s", name, "-c", home)
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
