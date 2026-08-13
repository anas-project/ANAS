package runner

// The interactive form.
//
// It is deliberately thin. It calls `capabilities`, offers only what came back
// available, then hands the answers to exactly the path a script would take.
// The rules about what is possible live in one place, and a web layer rendering
// the same JSON reaches the same conclusions without any of them being restated.
//
// Writing a second implementation here would be the obvious shortcut and the
// expensive one: the two copies would agree on the day they were written and
// drift apart on the first change to the privilege rules, at which point the
// interactive path would be offering modes that fail.

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// runBackupInteractive is what `anas backup` with no subcommand does.
func runBackupInteractive(args []string) error {
	if !isTerminal(os.Stdin.Fd()) {
		return usageErrorf("usage: anas backup capabilities|plan|create|list|restore|verify " +
			"(the interactive form needs a terminal)")
	}
	workspace, err := resolveBackupWorkspace("")
	if err != nil {
		return err
	}
	reader := bufio.NewReader(os.Stdin)
	fmt.Printf("workspace: %s\n\n", workspace)

	dest, err := ask(reader, "Back up to which directory?", "")
	if err != nil {
		return err
	}
	if strings.TrimSpace(dest) == "" {
		return usageErrorf("a destination is required")
	}
	absolute, err := absoluteDest(dest)
	if err != nil {
		return err
	}

	caps, err := probeBackupCapabilities(workspace, absolute)
	if err != nil {
		return err
	}
	available := []backupModeReport{}
	for _, mode := range caps.Modes {
		if mode.Available {
			available = append(available, mode)
		}
	}
	if len(available) == 0 {
		// Explaining why is the useful part. "No modes available" on its own
		// leaves the user with nothing to act on, and every one of the reasons
		// has a remedy.
		fmt.Println("\nNo backup mode can run against that destination:")
		for _, mode := range caps.Modes {
			fmt.Printf("  %-10s %s\n", mode.ID, describeBackupReason(mode.Reason))
		}
		return preconditionErrorf(firstUnavailableReason(caps.Modes),
			"no backup mode can run against %s", absolute)
	}

	fmt.Printf("\n%s is %s", absolute, orUnknown(caps.Dest.FSType))
	if caps.Dest.FreeBytes != nil {
		fmt.Printf(" with %s free", formatBytes(*caps.Dest.FreeBytes))
	}
	fmt.Printf("; this backup is about %s\n\n", formatBytes(caps.Estimate.TotalBytes))

	fmt.Println("Available modes:")
	recommended := 0
	for i, mode := range available {
		marker := " "
		if mode.ID == caps.Recommended {
			marker = "*"
			recommended = i
		}
		fmt.Printf(" %s %d) %-10s %s\n", marker, i+1, mode.ID, describeBackupModePurpose(mode.ID))
		for _, note := range mode.Notes {
			fmt.Printf("       %-10s   %s\n", "", describeBackupNote(note))
		}
	}
	choice, err := askIndex(reader, fmt.Sprintf("\nWhich mode? [%d]", recommended+1), recommended, len(available))
	if err != nil {
		return err
	}
	mode := available[choice]

	opts := backupOptions{dest: absolute, mode: mode.ID}
	if mode.Incremental && len(mode.Parents) > 0 {
		yes, err := confirm(fmt.Sprintf("Send incrementally against %s?", mode.Parents[0]), false)
		if err != nil {
			return failuref("stdin_unavailable", "%s", err.Error())
		}
		if yes {
			opts.parent = mode.Parents[0]
		}
	}

	plan, err := buildBackupPlan(workspace, opts)
	if err != nil {
		return err
	}
	fmt.Println()
	printBackupPlan(plan)

	prompt := "\nRun this backup"
	if plan.StopContainers && len(plan.ContainersToStop) > 0 {
		prompt = fmt.Sprintf("\nRun this backup, stopping %d module(s) for about %ds",
			len(plan.ContainersToStop), plan.EstimatedDowntimeSeconds)
	}
	yes, err := confirm(prompt+"?", false)
	if err != nil {
		return failuref("stdin_unavailable", "%s", err.Error())
	}
	if !yes {
		return confirmationErrorf("backup was declined")
	}
	opts.yes = true

	announceWorkspace(workspace)
	unlock, err := acquireRuntimeLock(stateDir(workspace))
	if err != nil {
		return failuref("lock_failed", "%s", err.Error())
	}
	defer unlock()
	cleanStaleBackupTemp(plan.Dest)
	outcome, err := createBackup(workspace, plan, opts)
	if err != nil {
		return err
	}
	fmt.Printf("\nbackup %s written to %s (%s, %s, %ds of downtime)\n",
		outcome.BackupID, outcome.Dest, outcome.Mode,
		formatBytes(outcome.TransferredBytes), outcome.DowntimeSeconds)
	fmt.Printf("check it later with:\n  anas backup verify --to %s\n", outcome.Dest)
	return nil
}

func describeBackupModePurpose(mode string) string {
	switch mode {
	case backupModeSnapshot:
		return "instant, but onto the same disk — no protection against losing it"
	case backupModeSend:
		return "btrfs send into another Btrfs; supports increments"
	case backupModeSendFile:
		return "btrfs send into a file on any filesystem; restores only onto Btrfs"
	case backupModeCopy:
		return "an ordinary copy; restores onto anything"
	}
	return ""
}

func ask(reader *bufio.Reader, prompt, fallback string) (string, error) {
	fmt.Printf("%s ", prompt)
	line, err := reader.ReadString('\n')
	if err != nil {
		return "", failuref("stdin_unavailable", "%s", err.Error())
	}
	answer := strings.TrimSpace(line)
	if answer == "" {
		return fallback, nil
	}
	return answer, nil
}

// askIndex reads a 1-based menu choice and returns a 0-based index. An empty
// answer takes the default, and anything out of range is asked again rather
// than rounded into a neighbouring option — picking the wrong backup mode by
// silent coercion is not a mistake worth being tolerant about.
func askIndex(reader *bufio.Reader, prompt string, fallback, count int) (int, error) {
	for {
		answer, err := ask(reader, prompt, "")
		if err != nil {
			return 0, err
		}
		if answer == "" {
			return fallback, nil
		}
		n, err := strconv.Atoi(answer)
		if err == nil && n >= 1 && n <= count {
			return n - 1, nil
		}
		fmt.Printf("Enter a number between 1 and %d.\n", count)
	}
}
