package jira

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"iter"
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
func (jc *Client) createRequest(ctx context.Context, method, path, body string) (*http.Request, error) {
	rel, err := url.Parse(path)
	if err != nil {
		return nil, err
	}

	reqURL := jc.baseURL.ResolveReference(rel)

	var bodyReader io.Reader
	if body != "" {
		bodyReader = strings.NewReader(body)
	}

	req, err := http.NewRequestWithContext(ctx, method, reqURL.String(), bodyReader)
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

func jiraGet[T any](ctx context.Context, jc *Client, path string, result *T) (err error) {
	req, err := jc.createRequest(ctx, "GET", path, "")
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
func (jc *Client) GetMyAssignedEpics() iter.Seq2[Issue, error] {
	return func(yield func(Issue, error) bool) {
		// Use JQL to find all epics assigned to the user that are NOT Done.
		jql := "assignee = currentUser() AND issuetype = Epic AND status != Done"
		for issue, err := range jc.getIssuesByJQL(context.Background(), jql) {
			if err != nil {
				yield(Issue{}, fmt.Errorf("failed to retrieved current user's epics: %v", err))
				return
			}
			if more := yield(issue, nil); !more {
				return
			}
		}
	}
}

// GetIssuesByKeys retrieves issues by their keys.
func (jc *Client) GetIssuesByKeys(keys ...string) iter.Seq2[Issue, error] {
	return func(yield func(Issue, error) bool) {

		//defer decorate.OnError(&err, "failed to retrieved issues from given keys: %s", strings.Join(keys, ", "))

		if len(keys) == 0 {
			yield(Issue{}, fmt.Errorf("failed to retrieved issues: no issue keys provided"))
			return
		}

		jql := fmt.Sprintf("key in (%s)", strings.Join(keys, ","))
		for issue, err := range jc.getIssuesByJQL(context.Background(), jql) {
			if err != nil {
				yield(Issue{}, fmt.Errorf("ailed to retrieved issues from given keys (%s): %v", strings.Join(keys, ", "), err))
				return
			}
			if more := yield(issue, nil); !more {
				return
			}
		}
	}
}

func (jc *Client) getIssuesByJQL(ctx context.Context, jql string) iter.Seq2[Issue, error] {
	return func(yield func(Issue, error) bool) {
		var iterErr error
		defer func() {
			if iterErr != nil {
				yield(Issue{}, fmt.Errorf("failed to retrieved issues from JQL: %v", iterErr))
				return
			}
		}()

		if jql == "" {
			iterErr = fmt.Errorf("no JQL query provided")
			return
		}

		encodedJQL := url.QueryEscape(jql)
		path := fmt.Sprintf("/rest/api/2/search?jql=%s", encodedJQL)

		var result struct {
			Issues []jsonIssue
		}
		if err := jiraGet(ctx, jc, path, &result); err != nil {
			iterErr = err
			return
		}

		topIssuesCtx, topIssuesCancel := context.WithCancel(context.Background())
		defer topIssuesCancel()

		var g errgroup.Group
		var issueCh = make(chan Issue)
		for _, jIssue := range result.Issues {
			g.Go(func() error {
				// Each top issue is processed independently of others.
				i, err := newIssueFromJsonIssue(topIssuesCtx, jIssue, jc)
				if err != nil {
					return err
				}

				issueCh <- i
				return nil
			})
		}

		var goroutinesErr error
		go func() {
			goroutinesErr = g.Wait()
			close(issueCh)
		}()

		// Propagating issues or errors.
		for {
			issue, ok := <-issueCh

			// Iterating over all top issues is done
			if !ok {
				iterErr = goroutinesErr
				return
			}

			// Getting one Issue at a time.
			if more := yield(issue, nil); !more {
				// Cancel the other goroutines that may still run.
				topIssuesCancel()
				return
			}
		}
	}
}

// GetIssue retrieves a given issue and children subtasks assigned to it.
func (jc *Client) GetIssue(key string) (issue Issue, err error) {
	defer decorate.OnError(&err, "failed to retrieved issue %s", key)

	path := fmt.Sprintf("/rest/api/2/issue/%s", key)

	ctx := context.Background()

	var jIssue jsonIssue
	if err := jiraGet(ctx, jc, path, &jIssue); err != nil {
		return Issue{}, err
	}

	i, err := newIssueFromJsonIssue(ctx, jIssue, jc)
	if err != nil {
		return Issue{}, err
	}

	return i, nil
}
