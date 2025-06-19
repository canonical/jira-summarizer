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
	"golang.org/x/sync/errgroup"
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
func (jc *Client) GetMyAssignedEpics() (epics []Issue, err error) {
	defer decorate.OnError(&err, "failed to retrieved current user's epics")

	// Use JQL to find all epics assigned to the user that are NOT Done.
	jql := "assignee = currentUser() AND issuetype = Epic AND status != Done"
	return jc.getIssuesByJQL(jql)
}

// GetIssuesByKeys retrieves issues by their keys.
func (jc *Client) GetIssuesByKeys(keys ...string) (issues []Issue, err error) {
	defer decorate.OnError(&err, "failed to retrieved issues from given keys: %s", strings.Join(keys, ", "))

	if len(keys) == 0 {
		return nil, fmt.Errorf("no issue keys provided")
	}

	jql := fmt.Sprintf("key in (%s)", strings.Join(keys, ","))
	return jc.getIssuesByJQL(jql)
}

func (jc *Client) getIssuesByJQL(jql string) (issues []Issue, err error) {
	defer decorate.OnError(&err, "failed to retrieved issues from JQL")

	if jql == "" {
		return nil, fmt.Errorf("no JQL query provided")
	}

	encodedJQL := url.QueryEscape(jql)
	path := fmt.Sprintf("/rest/api/2/search?jql=%s", encodedJQL)

	var result struct {
		Issues []jsonIssue
	}
	if err := jiraGet(jc, path, &result); err != nil {
		return nil, err
	}

	var g errgroup.Group
	var issueCh = make(chan Issue, len(result.Issues))
	for _, jIssue := range result.Issues {
		g.Go(func() error {
			i, err := newIssueFromJsonIssue(jIssue, jc)
			if err != nil {
				return err
			}

			issueCh <- i
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return nil, err
	}
	close(issueCh)

	issues = make([]Issue, 0, len(result.Issues))
	for i := range issueCh {
		issues = append(issues, i)
	}

	return issues, nil
}

// GetIssue retrieves a given issue and children subtasks assigned to it.
func (jc *Client) GetIssue(key string) (issue Issue, err error) {
	defer decorate.OnError(&err, "failed to retrieved issue %s", key)

	path := fmt.Sprintf("/rest/api/2/issue/%s", key)

	var jIssue jsonIssue
	if err := jiraGet(jc, path, &jIssue); err != nil {
		return Issue{}, err
	}

	i, err := newIssueFromJsonIssue(jIssue, jc)
	if err != nil {
		return Issue{}, err
	}

	return i, nil
}
