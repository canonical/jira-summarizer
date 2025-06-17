package main

import (
	"fmt"
	"time"

	"github.com/canonical/jira-summarizer/internal/jira"
)

// collect returns issues from Jira based on provided keys or defaults to assigned epics.
func collect(jc *jira.Client, groupStrategy string, topIssueKeys ...string) ([]jira.Issue, error) {
	var topIssues []jira.Issue
	var err error

	if len(topIssueKeys) == 0 {
		topIssues, err = jc.GetMyAssignedEpics()
	} else {
		topIssues, err = jc.GetIssuesByKeys(topIssueKeys...)
	}
	if err != nil {
		return nil, fmt.Errorf("error fetching issues: %v", err)
	}

	var results []jira.Issue
	switch groupStrategy {
	case "merge":
		results = []jira.Issue{{
			Key:      "virtual",
			Children: topIssues,
		}}
	case "children":
		// Move top issue to all children of top issues (top issues can be objectives for instance and we want epic summary).
		for _, issue := range topIssues {
			results = append(results, issue.Children...)
		}
	default:
		// No grouping, just return the top issues as they are.
		results = topIssues
	}

	return results, nil
}

// filterEvents filters issues based on recent events since the given time.
// It returns only those issues that have events within the specified time frame.
func filterEvents(issues []jira.Issue, sinceTime time.Time) []jira.Issue {
	var relevantIssues []jira.Issue
	for _, i := range issues {
		if !i.KeepRecentEvents(sinceTime) {
			continue
		}
		relevantIssues = append(relevantIssues, i)
	}
	return relevantIssues
}
