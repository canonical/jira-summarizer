package jira

import (
	"fmt"
	"log/slog"
	"time"
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
}

// updateIfStatusRecent checks if the issue's status has changed recently
// and marks it as RecentlyChanged if so.
func (i *Issue) updateIfStatusRecent(jc *Client) error {
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
		changeTime, err := time.Parse("2006-01-02T15:04:05.999-0700", changeSet.Created)
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
