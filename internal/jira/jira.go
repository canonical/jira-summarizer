package jira

import (
	"bytes"
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

// GetMyAssignedEpics retrieves all opened epics assigned to the current user and its children subtasks.
// Recent changes are attached to it.
// TODO: should retrieve DONE in the last recent changes.
func (jc *Client) GetMyAssignedEpics() (epics []Issue, err error) {
	defer decorate.OnError(&err, "failed to retrieved current user's epics")

	// Use JQL to find all epics assigned to the user that are NOT Done.
	jql := "assignee = currentUser() AND issuetype = Epic AND status != Done"
	encodedJQL := url.QueryEscape(jql)
	path := fmt.Sprintf("/rest/api/2/search?jql=%s", encodedJQL)

	var result struct {
		Issues []Issue
	}
	if err := jiraGet(jc, path, &result); err != nil {
		return nil, err
	}

	issues := make([]Issue, 0, len(result.Issues))
	for _, i := range result.Issues {
		if err := i.refresh(jc); err != nil {
			return nil, err
		}
		issues = append(issues, i)
	}

	return issues, nil
}

// GetIssue retrieves a given issue and children subtasks assigned to it.
func (jc *Client) GetIssue(key string) (issue Issue, err error) {
	defer decorate.OnError(&err, "failed to retrieved issue %s", key)

	path := fmt.Sprintf("/rest/api/2/issue/%s", key)

	var result Issue
	if err := jiraGet(jc, path, &result); err != nil {
		return Issue{}, err
	}

	if err := result.refresh(jc); err != nil {
		return Issue{}, err
	}
	return result, nil
}

// AddComment adds a comment to an issue
func (jc *Client) AddComment(issueKey, commentBody string) (err error) {
	defer decorate.OnError(&err, "failed to psot comment on issue %s", issueKey)

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
		return fmt.Errorf("got network status: %s", resp.Status)
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
