package jira

import (
	"fmt"
	"log/slog"
	"net/url"
	"time"

	"github.com/ubuntu/decorate"
)

// Issue represents a Jira issue (epic or subtask)
type Issue struct {
	ID     string
	Key    string
	Fields struct {
		Summary     string
		Description string
		IssueType   struct {
			Name string
		}
		Status struct {
			Name string
			Who  string
			When time.Time
		}
	}
	Children []Issue
	Comments []struct {
		Content string
		Who     string
		When    time.Time
	}
}

const jiraTimeFormat = "2006-01-02T15:04:05.999-0700"

// refresh refreshes recent elements of the Issue itself.
func (i *Issue) refresh(jc *Client) error {
	if err := i.fetchStatusUpdate(jc); err != nil {
		return err
	}
	if err := i.fetchComments(jc); err != nil {
		return err
	}
	if err := i.fetchChildren(jc); err != nil {
		return err
	}

	return nil
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
				Field    string `json:"field"`
				ToString string `json:"toString"`
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
			if i.Fields.Status.Name != item.ToString {
				continue
			}

			if modTime.After(i.Fields.Status.When) {
				i.Fields.Status.Who = changeSet.Author.DisplayName
				i.Fields.Status.When = modTime
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
		Issues []Issue
	}
	if err := jiraGet(jc, path, &children); err != nil {
		return err
	}

	i.Children = make([]Issue, 0, len(children.Issues))
	for _, child := range children.Issues {
		if err := child.refresh(jc); err != nil {
			return err
		}
		if err := child.fetchChildren(jc); err != nil {
			return err
		}
		i.Children = append(i.Children, child)
	}

	return nil
}
