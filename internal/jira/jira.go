package jira

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/ubuntu/decorate"
)

// Client handles API communication
type Client struct {
	username string
	token    string
	baseURL  *url.URL
	client   *http.Client
}

// NewClient creates a new Jira client
func NewClient(baseURL, user, token string) (*Client, error) {
	base, err := url.Parse(baseURL)
	if err != nil {
		return nil, err
	}

	return &Client{
		username: user,
		token:    token,
		baseURL:  base,
		client:   &http.Client{},
	}, nil
}

// Issue represents a Jira issue (epic or subtask)
type Issue struct {
	ID     string
	Key    string
	Fields struct {
		Summary     string
		Description string
		Status      struct {
			Name string
		}
		Updated  string
		Subtasks []struct {
			ID     string
			Key    string
			Fields struct {
				Summary string
			}
		}
	}
}

// ChangelogItem represents a change to an issue
type ChangelogItem struct {
	Field      string `json:"field"`
	FromString string `json:"fromString"`
	ToString   string `json:"toString"`
}

// ChangelogEntry represents an entry in the changelog
type ChangelogEntry struct {
	Author struct {
		DisplayName string `json:"displayName"`
	} `json:"author"`
	Created string          `json:"created"`
	Items   []ChangelogItem `json:"items"`
}

// IssueHistory contains the changelog for an issue
type IssueHistory struct {
	Values []ChangelogEntry `json:"values"`
}

// Comment represents a Comment on an issue
type Comment struct {
	Author struct {
		DisplayName string `json:"displayName"`
	} `json:"author"`
	Created string `json:"created"`
	Body    string `json:"body"`
}

// CommentList contains all comments for an issue
type CommentList struct {
	Comments []Comment `json:"comments"`
}

// EpicUpdateInfo contains info about changes to an epic's subtasks
type EpicUpdateInfo struct {
	EpicKey        string
	EpicSummary    string
	SubtaskUpdates []SubtaskUpdate
}

// SubtaskUpdate contains info about changes to a subtask
type SubtaskUpdate struct {
	Key           string
	Summary       string
	Description   string
	Status        string
	StatusChanges []string
	Comments      []CommentInfo
}

// CommentInfo contains simplified comment information
type CommentInfo struct {
	Author  string
	Created string
	Body    string
}

// createRequest builds a new authenticated HTTP request
func (jc *Client) createRequest(method, path, body string) (*http.Request, error) {
	rel, err := url.Parse(path)
	if err != nil {
		return nil, err
	}

	reqURL := jc.baseURL.ResolveReference(rel)

	var bodyReader io.Reader
	if body != "" {
		bodyReader = strings.NewReader(body)
	}

	req, err := http.NewRequest(method, reqURL.String(), bodyReader)
	if err != nil {
		return nil, err
	}

	// Add Basic Authentication
	auth := jc.username + ":" + jc.token
	encodedAuth := base64.StdEncoding.EncodeToString([]byte(auth))
	req.Header.Add("Authorization", "Basic "+encodedAuth)
	req.Header.Add("Content-Type", "application/json")

	return req, nil
}

func jiraGet[T any](jc *Client, path string, result *T) (err error) {
	req, err := jc.createRequest("GET", path, "")
	if err != nil {
		return err
	}

	resp, err := jc.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("got network status: %s", resp.Status)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	if err := json.Unmarshal(body, result); err != nil {
		return err
	}

	return nil
}

// GetMyAssignedEpics retrieves all opened epics assigned to the current user.
func (jc *Client) GetMyAssignedEpics() (issues []Issue, err error) {
	defer decorate.OnError(&err, "failed to retrieved current user's epics")

	// Use JQL to find all epics assigned to the user that are NOT Done.
	jql := "assignee = currentUser() AND issuetype = Epic AND status != Done"
	encodedJQL := url.QueryEscape(jql)
	path := fmt.Sprintf("/rest/api/2/search?jql=%s&expand=subtasks", encodedJQL)

	var result struct {
		Issues []Issue `json:"issues"`
	}
	if err := jiraGet(jc, path, &result); err != nil {
		return nil, err
	}

	return result.Issues, nil
}

// GetSubtasks retrieves all subtasks for an epic
func (jc *Client) GetSubtasks(epicKey string) (issues []Issue, err error) {
	defer decorate.OnError(&err, "failed to retrieved subtasks of %s", epicKey)

	// JQL to find all subtasks of the epic
	jql := fmt.Sprintf("parent = %s OR \"Epic Link\" = %s", epicKey, epicKey)
	encodedJQL := url.QueryEscape(jql)
	path := fmt.Sprintf("/rest/api/2/search?jql=%s", encodedJQL)

	var result struct {
		Issues []Issue `json:"issues"`
	}
	if err := jiraGet(jc, path, &result); err != nil {
		return nil, err
	}

	return result.Issues, nil
}

// GetIssueChangelog retrieves the changelog for an issue
func (jc *Client) GetIssueChangelog(issueKey string) (IssueHistory, error) {
	path := fmt.Sprintf("/rest/api/2/issue/%s/changelog", issueKey)

	req, err := jc.createRequest("GET", path, "")
	if err != nil {
		return IssueHistory{}, err
	}

	resp, err := jc.client.Do(req)
	if err != nil {
		return IssueHistory{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return IssueHistory{}, fmt.Errorf("failed to retrieve changelog: %s", resp.Status)
	}

	path := fmt.Sprintf("/rest/api/2/issue/%s/changelog", issueKey)

	var result IssueHistory
	if err := jiraGet(jc, path, &result); err != nil {
		return IssueHistory{}, err
	}

	return result, nil
}

// GetIssueComments retrieves comments for an issue
func (jc *Client) GetIssueComments(issueKey string) (CommentList, error) {
	path := fmt.Sprintf("/rest/api/2/issue/%s/comment", issueKey)

	req, err := jc.createRequest("GET", path, "")
	if err != nil {
		return CommentList{}, err
	}

	resp, err := jc.client.Do(req)
	if err != nil {
		return CommentList{}, err
	}
	defer resp.Body.Close()

	path := fmt.Sprintf("/rest/api/2/issue/%s/comment", issueKey)

	var result CommentList
	if err := jiraGet(jc, path, &result); err != nil {
		return CommentList{}, err
	}

	return result, nil
}

// GetIssueDetails retrieves full details for an issue
func (jc *Client) GetIssueDetails(issueKey string) (Issue, error) {
	path := fmt.Sprintf("/rest/api/2/issue/%s", issueKey)

	req, err := jc.createRequest("GET", path, "")
	if err != nil {
		return Issue{}, err
	}

	path := fmt.Sprintf("/rest/api/2/issue/%s", issueKey)

	var result Issue
	if err := jiraGet(jc, path, &result); err != nil {
		return Issue{}, err
	}

	return result, nil
}

// AddComment adds a comment to an issue
func (jc *Client) AddComment(issueKey, commentBody string) error {
	path := fmt.Sprintf("/rest/api/2/issue/%s/comment", issueKey)

	commentJSON := fmt.Sprintf(`{"body": %s}`, formatJSONString(commentBody))

	req, err := jc.createRequest("POST", path, commentJSON)
	if err != nil {
		return err
	}

	resp, err := jc.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to add comment: %s - %s", resp.Status, string(bodyBytes))
	}

	return nil
}

// formatJSONString properly escapes a string for use in JSON
func formatJSONString(s string) string {
	bytes, err := json.Marshal(s)
	if err != nil {
		// Fallback if marshaling fails
		return fmt.Sprintf("\"%s\"", strings.Replace(s, "\"", "\\\"", -1))
	}
	return string(bytes)
}
