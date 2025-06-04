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
		Status struct {
			Name string
			Who  string
			When time.Time
		}
	}
	Children []Issue
	Comments []struct {
		Author  string
		Content string
	}
}

const jiraTimeFormat = "2006-01-02T15:04:05.999-0700"

// refreshState refreshes recent elements of the Issue itself.
func (i *Issue) refreshState(jc *Client) error {
	if err := i.updateIfStatusRecent(jc); err != nil {
		return err
	}
	if err := i.updateRecentComments(jc); err != nil {
		return err
	}
	if err := i.getChildren(jc); err != nil {
		return err
	}

	return nil
}

// updateIfStatusRecent checks if the issue's status has changed recently
// and marks it as RecentlyChanged if so.
func (i *Issue) updateIfStatusRecent(jc *Client) (err error) {
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
		changeTime, err := time.Parse(jiraTimeFormat, changeSet.Created)
		if err != nil {
			slog.Warn(fmt.Sprintf("failed to parse change time %s for issue %s: %v", changeSet.Created, i.Key, err))
			continue
		}
		if changeTime.Before(jc.changesMoreRecentThan) {
			continue
		}

		for _, item := range changeSet.Items {
			if item.Field != "status" {
				continue
			}
			if i.Fields.Status.Name != item.ToString {
				continue
			}

			if changedTime.After(i.Fields.Status.When) {
				i.Fields.Status.Who = changeSet.Author.DisplayName
				i.Fields.Status.When = changedTime
			}

			break outer
		}
	}

	return nil
}

// updateRecentComments checks and attach each recent issue's comment.
func (i *Issue) updateRecentComments(jc *Client) (err error) {
	defer decorate.OnError(&err, "failed to get issue comments for %s", i.Key)

	path := fmt.Sprintf("/rest/api/2/issue/%s/comment", i.Key)

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

		if createdTime.Before(jc.changesMoreRecentThan) {
			continue
		}

		i.Comments = append(i.Comments, struct {
			Author  string
			Content string
		}{
			comment.Author.DisplayName,
			comment.Body,
		})
	}

	return nil
}

// getChildren retrieves all children issues from the given one, recursively.
func (i *Issue) getChildren(jc *Client) (err error) {
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
		if err := child.refreshState(jc); err != nil {
			return err
		}
		if err := child.getChildren(jc); err != nil {
			return err
		}
		i.Children = append(i.Children, child)
	}

	return nil
}
