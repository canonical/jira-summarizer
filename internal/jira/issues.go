package jira

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
