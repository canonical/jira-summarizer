package jira

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/ubuntu/decorate"
	"golang.org/x/sync/errgroup"
)

// Issue represents a Jira issue (epic or subtask)
type Issue struct {
	Key         string
	URL         string
	Summary     string
	Description string
	Created     time.Time
	IssueType   string
	Status      struct {
		Name string
		Who  string
		When time.Time
	}
	Children []Issue
	Comments []Comment
}

// Comment represent a comment on a Jira issue.
type Comment struct {
	Content string
	Who     string
	When    time.Time
}

// Embedder returns if top issues is only used only as embedder:
// - it’s either a virtual issue (no key)
// - or it has children
func (i Issue) Embedder() bool {
	if i.Key == "" || len(i.Children) > 0 {
		return true
	}

	return false
}

// AddComment adds a comment to an issue.
func (i *Issue) AddComment(jc *Client, commentBody string) (err error) {
	defer decorate.OnError(&err, "failed to add comment on issue %s", i.Key)

	path := fmt.Sprintf("/rest/api/2/issue/%s/comment", i.Key)

	d := fmt.Sprintf(`{"body": %s}`, formatJSONString(commentBody))
	req, err := jc.createRequest(context.Background(), "POST", path, d)
	if err != nil {
		return err
	}

	resp, err := jc.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("got network status: %s", resp.Status)
	}

	/* Let’s not refresh the issue after adding a comment for now, as it can be expensive
	and they are not reused during the program execution for now.
	if *i, err = i.fetchComments(jc); err != nil {
		return fmt.Errorf("failed to refresh issue after adding comment: %v", err)
	}*/

	return nil
}

// KeptRecentEvents filters issues to only include those with recent changes.
// Those can be recent comments or status changes.
// It will signal if any changed happened on that issue or any of its children.
func (i *Issue) KeptRecentEvents(sinceTime time.Time) (hasChanged bool) {
	var hasChanges bool

	if i.Created.After(sinceTime) {
		hasChanges = true
	}

	if i.Status.When.After(sinceTime) {
		hasChanges = true
	} else {
		i.Status.Name = ""
	}

	var recentComments []Comment
	for _, comment := range i.Comments {
		if comment.When.Before(sinceTime) {
			continue
		}
		hasChanges = true
		recentComments = append(recentComments, comment)
	}
	i.Comments = recentComments

	var children []Issue
	for _, child := range i.Children {
		if !child.KeptRecentEvents(sinceTime) {
			continue
		}
		hasChanges = true
		children = append(children, child)
	}
	i.Children = children

	return hasChanges
}

// Format returns a formatted representation of the issue and its children.
func (i Issue) Format(indent bool) string {
	var sb strings.Builder

	sb.WriteString(i.String())

	for index, child := range i.Children {
		if index == 0 {
			sb.WriteString("\nChildren tasks:\n|\n")
		}

		sb.WriteString("|- Task: " + child.Key + "\n")
		sb.WriteString(child.Format(true))
		sb.WriteString("\n")
	}

	prefix := "|  "
	if !indent {
		prefix = ""
	}
	return prefix + strings.ReplaceAll(sb.String(), "\n", "\n"+prefix)
}

const timeFormat = "02/01/2006 15:04"

// String returns a string representation of the issue.
func (i Issue) String() string {
	var sb strings.Builder

	// Virtual tasks, only used for issues.
	if i.IssueType == "" {
		if len(i.Children) > 0 {
			sb.WriteString(fmt.Sprintf("Number of modified direct children tasks: %d\n", len(i.Children)))
		}
		return sb.String()
	}

	sb.WriteString(fmt.Sprintf(`Title: %s
Link: %s
Created on: %s
`, i.Summary, i.URL, i.Created.Format(timeFormat)))

	if i.Status.Name != "" {
		sb.WriteString(fmt.Sprintf("Status changed to %s on %s by %s\n", i.Status.Name, i.Status.When.Format(timeFormat), i.Status.Who))
	}

	sb.WriteString(fmt.Sprintf("Description: %s\n", strings.ReplaceAll(strings.TrimSpace(i.Description), "\n", "\n  ")))

	if len(i.Comments) > 0 {
		sb.WriteString("Comments:\n")
		for _, comment := range i.Comments {
			sb.WriteString(fmt.Sprintf("  - %s (%s): %s\n", comment.Who, comment.When.Format(timeFormat), strings.ReplaceAll(strings.TrimSpace(comment.Content), "\n", "\n      ")))
		}
	}

	if len(i.Children) > 0 {
		sb.WriteString(fmt.Sprintf("Number of modified direct children tasks: %d\n", len(i.Children)))
	}

	return sb.String()
}

// jsonIssue is a JSON representation of a Jira issue.
type jsonIssue struct {
	Key    string
	Fields struct {
		Summary     string
		Description string
		Created     string
		IssueType   struct {
			Name string
		}
		Status struct {
			Name string
		}
	}
}

const jiraTimeFormat = "2006-01-02T15:04:05.999-0700"

// newIssueFromJsonIssue creates a new Issue from the json issue representation
// and initializes it with additional properties of that issue.
func newIssueFromJsonIssue(ctx context.Context, j jsonIssue, jc *Client) (Issue, error) {
	// converted the created time to time.Time
	createdTime, err := time.Parse(jiraTimeFormat, j.Fields.Created)
	if err != nil {
		return Issue{}, fmt.Errorf("failed to parse created time %s for issue %s: %w", j.Fields.Created, j.Key, err)
	}

	i := Issue{
		Key:         j.Key,
		URL:         fmt.Sprintf("%s/browse/%s", jc.baseURL, j.Key),
		Summary:     j.Fields.Summary,
		Description: j.Fields.Description,
		Created:     createdTime,
		IssueType:   j.Fields.IssueType.Name,
		Status: struct {
			Name string
			Who  string
			When time.Time
		}{
			Name: j.Fields.Status.Name,
		},
	}

	// Shared context for fetching additional data. First error on an issue cancel all other requests, including children.
	// If fetchChildren returns an errors, it will also cancel the global context as .Wait() will return the first error.
	g, ctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		return i.fetchStatusUpdate(ctx, jc)
	})
	g.Go(func() error {
		return i.fetchComments(ctx, jc)
	})
	g.Go(func() error {
		return i.fetchChildren(ctx, jc)
	})

	if err := g.Wait(); err != nil {
		return Issue{}, fmt.Errorf("failed to fetch additional issue data for %s: %w", j.Key, err)
	}

	return i, nil
}

// fetchStatusUpdate marks last recent status change for the issue.
func (i *Issue) fetchStatusUpdate(ctx context.Context, jc *Client) (err error) {
	defer decorate.OnError(&err, "failed to check recent status change for issue %s", i.Key)

	path := fmt.Sprintf("/rest/api/2/issue/%s/changelog", i.Key)

	var result struct {
		Values []struct {
			Author struct {
				DisplayName string
			}
			Created string
			Items   []struct {
				Field    string
				ToString string
			}
		}
	}

	if err := jiraGet(ctx, jc, path, &result); err != nil {
		return err
	}

outer:
	for _, changeSet := range result.Values {
		modTime, err := time.Parse(jiraTimeFormat, changeSet.Created)
		if err != nil {
			slog.Warn(fmt.Sprintf("failed to parse change time %s for issue %s: %v", changeSet.Created, i.Key, err))
			continue
		}

		for _, item := range changeSet.Items {
			if item.Field != "status" {
				continue
			}
			if i.Status.Name != item.ToString {
				continue
			}

			if modTime.After(i.Status.When) {
				i.Status.Who = changeSet.Author.DisplayName
				i.Status.When = modTime
			}

			break outer
		}
	}

	return nil
}

// fetchComments attaches all comments to the issue in ascending order.
func (i *Issue) fetchComments(ctx context.Context, jc *Client) (err error) {
	defer decorate.OnError(&err, "failed to get issue comments for %s", i.Key)

	// get all Jira comments for the issue in ascending creation order.
	path := fmt.Sprintf("/rest/api/2/issue/%s/comment?orderBy=created", i.Key)

	var result struct {
		Comments []struct {
			Author struct {
				DisplayName string
			}
			Created string
			Body    string
		}
	}
	if err := jiraGet(ctx, jc, path, &result); err != nil {
		return err
	}

	for _, comment := range result.Comments {
		createdTime, err := time.Parse(jiraTimeFormat, comment.Created)
		if err != nil {
			slog.Warn(fmt.Sprintf("failed to parse comment time %s for issue %s: %v", comment.Created, i.Key, err))
			continue
		}

		i.Comments = append(i.Comments, struct {
			Content string
			Who     string
			When    time.Time
		}{
			comment.Body,
			comment.Author.DisplayName,
			createdTime,
		})
	}

	return nil
}

// fetchChildren retrieves all children issues from the given one, recursively.
func (i *Issue) fetchChildren(ctx context.Context, jc *Client) (err error) {
	defer decorate.OnError(&err, "failed to gather children of %s", i.Key)

	jql := fmt.Sprintf("parent = %s", i.Key)
	encodedJQL := url.QueryEscape(jql)
	path := fmt.Sprintf("/rest/api/2/search?jql=%s", encodedJQL)

	var children struct {
		Issues []jsonIssue
	}
	if err := jiraGet(ctx, jc, path, &children); err != nil {
		return err
	}

	i.Children = make([]Issue, 0, len(children.Issues))
	for _, childJson := range children.Issues {
		child, err := newIssueFromJsonIssue(ctx, childJson, jc)
		if err != nil {
			return err
		}
		i.Children = append(i.Children, child)
	}

	return nil
}

// formatJSONString properly escapes a string for use in JSON
func formatJSONString(s string) string {
	bytes, err := json.Marshal(s)
	if err != nil {
		// Fallback if marshaling fails
		return fmt.Sprintf("\"%s\"", strings.ReplaceAll(s, "\"", "\\\""))
	}
	return string(bytes)
}
