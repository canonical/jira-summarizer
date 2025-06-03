package jira

import (
	"fmt"
	"log/slog"
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
		Status      struct {
			Name            string
			RecentlyChanged bool
		}
	}
	Subtasks []Issue
	Comments []struct {
		Author  string
		Content string
	}
}

const jiraTimeFormat = "2006-01-02T15:04:05.999-0700"

// updateIfStatusRecent checks if the issue's status has changed recently
// and marks it as RecentlyChanged if so.
func (i *Issue) updateIfStatusRecent(jc *Client) (err error) {
	defer decorate.OnError(&err, "failed to check recent status change for issue %s", i.Key)

	path := fmt.Sprintf("/rest/api/2/issue/%s/changelog", i.Key)

	var result struct {
		Values []struct {
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

			i.Fields.Status.RecentlyChanged = true
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
