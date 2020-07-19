package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"net/url"
	"regexp"
	"strings"

	"github.com/henvic/httpretty"
	"github.com/shurcooL/graphql"
)

// ClientOption represents an argument to NewClient
type ClientOption = func(http.RoundTripper) http.RoundTripper

// NewHTTPClient initializes an http.Client
func NewHTTPClient(opts ...ClientOption) *http.Client {
	tr := http.DefaultTransport
	for _, opt := range opts {
		tr = opt(tr)
	}
	return &http.Client{Transport: tr}
}

// NewClient initializes a Client
func NewClient(opts ...ClientOption) *Client {
	client := &Client{http: NewHTTPClient(opts...)}
	return client
}

// AddHeader turns a RoundTripper into one that adds a request header
func AddHeader(name, value string) ClientOption {
	return func(tr http.RoundTripper) http.RoundTripper {
		return &funcTripper{roundTrip: func(req *http.Request) (*http.Response, error) {
			// prevent the token from leaking to non-GitHub hosts
			// TODO: GHE support
			if !strings.EqualFold(name, "Authorization") || strings.HasSuffix(req.URL.Hostname(), ".github.com") {
				req.Header.Add(name, value)
			}
			return tr.RoundTrip(req)
		}}
	}
}

// AddHeaderFunc is an AddHeader that gets the string value from a function
func AddHeaderFunc(name string, value func() string) ClientOption {
	return func(tr http.RoundTripper) http.RoundTripper {
		return &funcTripper{roundTrip: func(req *http.Request) (*http.Response, error) {
			// prevent the token from leaking to non-GitHub hosts
			// TODO: GHE support
			if !strings.EqualFold(name, "Authorization") || strings.HasSuffix(req.URL.Hostname(), ".github.com") {
				req.Header.Add(name, value())
			}
			return tr.RoundTrip(req)
		}}
	}
}

// VerboseLog enables request/response logging within a RoundTripper
func VerboseLog(out io.Writer, logTraffic bool, colorize bool) ClientOption {
	logger := &httpretty.Logger{
		Time:           true,
		TLS:            false,
		Colors:         colorize,
		RequestHeader:  logTraffic,
		RequestBody:    logTraffic,
		ResponseHeader: logTraffic,
		ResponseBody:   logTraffic,
		Formatters:     []httpretty.Formatter{&httpretty.JSONFormatter{}},
	}
	logger.SetOutput(out)
	logger.SetBodyFilter(func(h http.Header) (skip bool, err error) {
		return !inspectableMIMEType(h.Get("Content-Type")), nil
	})
	return logger.RoundTripper
}

// ReplaceTripper substitutes the underlying RoundTripper with a custom one
func ReplaceTripper(tr http.RoundTripper) ClientOption {
	return func(http.RoundTripper) http.RoundTripper {
		return tr
	}
}

var issuedScopesWarning bool

const (
	httpOAuthAppID  = "X-Oauth-Client-Id"
	httpOAuthScopes = "X-Oauth-Scopes"
)

// CheckScopes checks whether an OAuth scope is present in a response
func CheckScopes(wantedScope string, cb func(string) error) ClientOption {
	wantedCandidates := []string{wantedScope}
	if strings.HasPrefix(wantedScope, "read:") {
		wantedCandidates = append(wantedCandidates, "admin:"+strings.TrimPrefix(wantedScope, "read:"))
	}

	return func(tr http.RoundTripper) http.RoundTripper {
		return &funcTripper{roundTrip: func(req *http.Request) (*http.Response, error) {
			res, err := tr.RoundTrip(req)
			if err != nil || res.StatusCode > 299 || issuedScopesWarning {
				return res, err
			}

			_, hasHeader := res.Header[httpOAuthAppID]
			if !hasHeader {
				return res, nil
			}

			appID := res.Header.Get(httpOAuthAppID)
			hasScopes := strings.Split(res.Header.Get(httpOAuthScopes), ",")

			hasWanted := false
		outer:
			for _, s := range hasScopes {
				for _, w := range wantedCandidates {
					if w == strings.TrimSpace(s) {
						hasWanted = true
						break outer
					}
				}
			}

			if !hasWanted {
				if err := cb(appID); err != nil {
					return res, err
				}
				issuedScopesWarning = true
			}

			return res, nil
		}}
	}
}

type funcTripper struct {
	roundTrip func(*http.Request) (*http.Response, error)
}

func (tr funcTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return tr.roundTrip(req)
}

// Client facilitates making HTTP requests to the GitHub API
type Client struct {
	http *http.Client
}

type graphQLResponse struct {
	Data   interface{}
	Errors []GraphQLError
}

// GraphQLError is a single error returned in a GraphQL response
type GraphQLError struct {
	Type    string
	Path    []string
	Message string
}

// GraphQLErrorResponse contains errors returned in a GraphQL response
type GraphQLErrorResponse struct {
	Errors []GraphQLError
}

func (gr GraphQLErrorResponse) Error() string {
	errorMessages := make([]string, 0, len(gr.Errors))
	for _, e := range gr.Errors {
		errorMessages = append(errorMessages, e.Message)
	}
	return fmt.Sprintf("GraphQL error: %s", strings.Join(errorMessages, "\n"))
}

// HTTPError is an error returned by a failed API call
type HTTPError struct {
	StatusCode int
	RequestURL *url.URL
	Message    string
}

func (err HTTPError) Error() string {
	if err.Message != "" {
		return fmt.Sprintf("HTTP %d: %s (%s)", err.StatusCode, err.Message, err.RequestURL)
	}
	return fmt.Sprintf("HTTP %d (%s)", err.StatusCode, err.RequestURL)
}

// Returns whether or not scopes are present, appID, and error
func (c Client) HasScopes(wantedScopes ...string) (bool, string, error) {
	url := "https://api.github.com/user"
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return false, "", err
	}

	req.Header.Set("Content-Type", "application/json; charset=utf-8")

	res, err := c.http.Do(req)
	if err != nil {
		return false, "", err
	}
	defer res.Body.Close()

	if res.StatusCode != 200 {
		return false, "", handleHTTPError(res)
	}

	appID := res.Header.Get("X-Oauth-Client-Id")
	hasScopes := strings.Split(res.Header.Get("X-Oauth-Scopes"), ",")

	found := 0
	for _, s := range hasScopes {
		for _, w := range wantedScopes {
			if w == strings.TrimSpace(s) {
				found++
			}
		}
	}

	if found == len(wantedScopes) {
		return true, appID, nil
	}

	return false, appID, nil
}

// GraphQL performs a GraphQL request and parses the response
func (c Client) GraphQL(query string, variables map[string]interface{}, data interface{}) error {
	url := "https://api.github.com/graphql"
	if gheHostname := os.Getenv("GITHUB_HOST"); gheHostname != "" {
		url = fmt.Sprintf("https://%s/api/graphql", gheHostname)
	}

	reqBody, err := json.Marshal(map[string]interface{}{"query": query, "variables": variables})
	if err != nil {
		return err
	}

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(reqBody))
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/json; charset=utf-8")

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	return handleResponse(resp, data)
}

func graphQLClient(h *http.Client) *graphql.Client {
	return graphql.NewClient("https://api.github.com/graphql", h)
}

// REST performs a REST request and parses the response.
func (c Client) REST(method string, p string, body io.Reader, data interface{}) error {
	url := "https://api.github.com/" + p
	if gheHostname := os.Getenv("GITHUB_HOST"); gheHostname != "" {
		url = fmt.Sprintf("https://%s/api/v3/%s", gheHostname, p)
	}

	req, err := http.NewRequest(method, url, body)
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/json; charset=utf-8")

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	success := resp.StatusCode >= 200 && resp.StatusCode < 300
	if !success {
		return handleHTTPError(resp)
	}

	if resp.StatusCode == http.StatusNoContent {
		return nil
	}

	b, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	err = json.Unmarshal(b, &data)
	if err != nil {
		return err
	}

	return nil
}

func handleResponse(resp *http.Response, data interface{}) error {
	success := resp.StatusCode >= 200 && resp.StatusCode < 300

	if !success {
		return handleHTTPError(resp)
	}

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	gr := &graphQLResponse{Data: data}
	err = json.Unmarshal(body, &gr)
	if err != nil {
		return err
	}

	if len(gr.Errors) > 0 {
		return &GraphQLErrorResponse{Errors: gr.Errors}
	}
	return nil
}

func handleHTTPError(resp *http.Response) error {
	httpError := HTTPError{
		StatusCode: resp.StatusCode,
		RequestURL: resp.Request.URL,
	}

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		httpError.Message = err.Error()
		return httpError
	}

	var parsedBody struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &parsedBody); err == nil {
		httpError.Message = parsedBody.Message
	}

	return httpError
}

var jsonTypeRE = regexp.MustCompile(`[/+]json($|;)`)

func inspectableMIMEType(t string) bool {
	return strings.HasPrefix(t, "text/") || jsonTypeRE.MatchString(t)
}
