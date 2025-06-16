package jira

import (
	"fmt"
	"log/slog"
	"net/url"
	"time"

	"github.com/ubuntu/decorate"
	"golang.org/x/sync/errgroup"
)

// Issue represents a Jira issue (epic or subtask)
type Issue struct {
	Key         string
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
	Comments []struct {
		Content string
		Who     string
		When    time.Time
	}
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
func newIssueFromJsonIssue(j jsonIssue, jc *Client) (Issue, error) {
	// converted the created time to time.Time
	createdTime, err := time.Parse(jiraTimeFormat, j.Fields.Created)
	if err != nil {
		return Issue{}, fmt.Errorf("failed to parse created time %s for issue %s: %w", j.Fields.Created, j.Key, err)
	}

	i := Issue{
		Key:         j.Key,
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

	var g errgroup.Group
	g.Go(func() error {
		return i.fetchStatusUpdate(jc)
	})
	g.Go(func() error {
		return i.fetchComments(jc)
	})
	g.Go(func() error {
		return i.fetchChildren(jc)
	})

	if err := g.Wait(); err != nil {
		return Issue{}, fmt.Errorf("failed to fetch additional issue data for %s: %w", j.Key, err)
	}

	return i, nil
}

// fetchStatusUpdate marks last recent status change for the issue.
func (i *Issue) fetchStatusUpdate(jc *Client) (err error) {
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

	if err := jiraGet(jc, path, &result); err != nil {
		return nil
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
func (i *Issue) fetchComments(jc *Client) (err error) {
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
	if err := jiraGet(jc, path, &result); err != nil {
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
func (i *Issue) fetchChildren(jc *Client) (err error) {
	defer decorate.OnError(&err, "failed to gather children of %s", i.Key)

	jql := fmt.Sprintf("parent = %s", i.Key)
	encodedJQL := url.QueryEscape(jql)
	path := fmt.Sprintf("/rest/api/2/search?jql=%s", encodedJQL)

	var children struct {
		Issues []jsonIssue
	}
	if err := jiraGet(jc, path, &children); err != nil {
		return err
	}

	i.Children = make([]Issue, 0, len(children.Issues))
	for _, childJson := range children.Issues {
		child, err := newIssueFromJsonIssue(childJson, jc)
		if err != nil {
			return err
		}
		i.Children = append(i.Children, child)
	}

	return nil
}
