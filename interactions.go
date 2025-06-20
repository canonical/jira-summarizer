package main

import (
	"bufio"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"

	"github.com/canonical/jira-summarizer/internal/jira"
	"github.com/ubuntu/decorate"
)

// printTopSummary delimites and prints the summary of the top issues.
func printTopSummary(edited string) {
	fmt.Println("--------------------------------------------------------------------------------------------------------------")
	fmt.Println(edited)
	fmt.Println()
}

const editableSeparator = "<----- ANY CONTENTS BELOW THIS WILL BE IGNORED ----->"

// editSummaryAndPost opens the editor with the provided issue summary and allows the user to edit it.
// If the user empty the content or does not change it, it will ask if they want to skip posting.
func editSummaryAndPost(jiraClient *jira.Client, issue jira.Issue, summary string) error {
	summary = fmt.Sprintf("\n\n%s\n\n%s", editableSeparator, summary)
	for {
		edited, err := openInEditor(summary)
		if err != nil {
			return err
		}

		// only keep the content before the editable separator.
		edited = strings.TrimSpace(strings.Split(edited, editableSeparator)[0])

		if edited == "" {
			if shouldReedit(edited) {
				continue
			}
			return nil
		}

		if err := issue.AddComment(jiraClient, edited); err != nil {
			return err
		}

		break
	}
	return nil
}

// openInEditor opens the default text editor with the provided content.
// It returns the edited content or an error if it fails to open the editor or read the file.
func openInEditor(content string) (edited string, err error) {
	defer decorate.OnError(&err, "failed to open editor for content editing")

	f, err := os.CreateTemp("", "jira-update-*.md")
	if err != nil {
		return "", err
	}
	defer os.Remove(f.Name())
	defer f.Close()

	if _, err := f.Write([]byte(content)); err != nil {
		return "", err
	}

	cmd := exec.Command("sensible-editor", f.Name())
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return "", err
	}

	// Read the modified content.
	data, err := os.ReadFile(f.Name())
	if err != nil {
		return "", err
	}

	return string(data), nil
}

// shouldReedit checks if the edited content is empty and ask if we should skip posting.
func shouldReedit(content string) bool {
	if content != "" {
		return false
	}

	reader := bufio.NewReader(os.Stdin)
	for {
		fmt.Print("Empty post detected, do you really want to skip posting? [Y/n]: ")
		response, err := reader.ReadString('\n')
		if err != nil {
			slog.Error(fmt.Sprintf("failed to read input: %v", err))
			return true
		}

		response = strings.ToLower(strings.TrimSpace(response))
		switch response {
		case "n", "no":
			return true
		case "y", "yes", "":
			return false
		}
	}
}
