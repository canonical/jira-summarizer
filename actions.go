package main

import (
	"fmt"
	"iter"
	"strings"

	"github.com/canonical/jira-summarizer/internal/jira"
)

// getTopIssues returns top issues from Jira based on provided keys and grouping strategy.
// It defaults to assigned epics.
func getTopIssues(jc *jira.Client, groupStrategy string, topIssueKeys ...string) iter.Seq2[jira.Issue, error] {
	return func(yield func(jira.Issue, error) bool) {

		topIssuersFunc := jc.GetMyAssignedEpics
		if len(topIssueKeys) > 0 {
			topIssuersFunc = func() iter.Seq2[jira.Issue, error] {
				return jc.GetIssuesByKeys(topIssueKeys...)
			}
		}

		var mergedTopIssues []jira.Issue
		for issue, err := range topIssuersFunc() {
			if err != nil {
				yield(jira.Issue{}, fmt.Errorf("error fetching issues: %v", err))
				return
			}

			switch groupStrategy {
			case "merge":
				mergedTopIssues = append(mergedTopIssues, issue)
			case "children":
				// Return all children of the top issues as its own top issues:
				// The top issues can be objectives, and we want the epic summary.
				for _, child := range issue.Children {
					if more := yield(child, nil); !more {
						return
					}
				}
			default:
				// No grouping, just return the top issues as they are.
				if more := yield(issue, nil); !more {
					return
				}
			}
		}

		if len(mergedTopIssues) == 0 {
			return
		}

		// We didn’t yield any issues yet, so we need to yield the merged top issue as a single item.
		yield(jira.Issue{
			Key:      "virtual",
			Children: mergedTopIssues,
		}, nil)
	}
}

// report generates a formatted string representation of the issue, including its children.
func report(topIssue jira.Issue) string {
	var r strings.Builder
	if topIssue.Embedder() {
		r.WriteString("< This top issue is tracking all children work here")
		if topIssue.IssueType != "" {
			r.WriteString(" and its Title and Description are here only for context")
		}
		r.WriteString(". >\n")
	}

	r.WriteString(topIssue.Format(false))

	return strings.TrimRight(r.String(), "| \n")
}
