package command

import (
	"bytes"
	"encoding/json"
	"io/ioutil"
	"os/exec"
	"regexp"
	"strings"
	"testing"

	"github.com/cli/cli/internal/run"
	"github.com/cli/cli/pkg/httpmock"
	"github.com/cli/cli/test"
	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/assert"
)

func TestIssueStatus(t *testing.T) {
	initBlankContext("", "OWNER/REPO", "master")
	defer stubTerminal(true)()
	http := initFakeHTTP()
	http.StubRepoResponse("OWNER", "REPO")
	http.Register(
		httpmock.GraphQL(`query UserCurrent\b`),
		httpmock.StringResponse(`{"data":{"viewer":{"login":"octocat"}}}`))
	http.Register(
		httpmock.GraphQL(`query IssueStatus\b`),
		httpmock.FileResponse("../test/fixtures/issueStatus.json"))

	output, err := RunCommand("issue status")
	if err != nil {
		t.Errorf("error running command `issue status`: %v", err)
	}

	expectedIssues := []*regexp.Regexp{
		regexp.MustCompile(`(?m)8.*carrots.*about.*ago`),
		regexp.MustCompile(`(?m)9.*squash.*about.*ago`),
		regexp.MustCompile(`(?m)10.*broccoli.*about.*ago`),
		regexp.MustCompile(`(?m)11.*swiss chard.*about.*ago`),
	}

	for _, r := range expectedIssues {
		if !r.MatchString(output.String()) {
			t.Errorf("output did not match regexp /%s/\n> output\n%s\n", r, output)
			return
		}
	}
}

func TestIssueStatus_blankSlate(t *testing.T) {
	initBlankContext("", "OWNER/REPO", "master")
	http := initFakeHTTP()
	http.StubRepoResponse("OWNER", "REPO")
	http.Register(
		httpmock.GraphQL(`query UserCurrent\b`),
		httpmock.StringResponse(`{"data":{"viewer":{"login":"octocat"}}}`))
	http.Register(
		httpmock.GraphQL(`query IssueStatus\b`),
		httpmock.StringResponse(`
		{ "data": { "repository": {
			"hasIssuesEnabled": true,
			"assigned": { "nodes": [] },
			"mentioned": { "nodes": [] },
			"authored": { "nodes": [] }
		} } }`))

	output, err := RunCommand("issue status")
	if err != nil {
		t.Errorf("error running command `issue status`: %v", err)
	}

	expectedOutput := `
Relevant issues in OWNER/REPO

Issues assigned to you
  There are no issues assigned to you

Issues mentioning you
  There are no issues mentioning you

Issues opened by you
  There are no issues opened by you

`
	if output.String() != expectedOutput {
		t.Errorf("expected %q, got %q", expectedOutput, output)
	}
}

func TestIssueStatus_disabledIssues(t *testing.T) {
	initBlankContext("", "OWNER/REPO", "master")
	http := initFakeHTTP()
	http.StubRepoResponse("OWNER", "REPO")
	http.Register(
		httpmock.GraphQL(`query UserCurrent\b`),
		httpmock.StringResponse(`{"data":{"viewer":{"login":"octocat"}}}`))
	http.Register(
		httpmock.GraphQL(`query IssueStatus\b`),
		httpmock.StringResponse(`
		{ "data": { "repository": {
			"hasIssuesEnabled": false
		} } }`))

	_, err := RunCommand("issue status")
	if err == nil || err.Error() != "the 'OWNER/REPO' repository has disabled issues" {
		t.Errorf("error running command `issue status`: %v", err)
	}
}

func TestIssueList_nontty(t *testing.T) {
	initBlankContext("", "OWNER/REPO", "master")
	defer stubTerminal(false)()
	http := initFakeHTTP()
	http.StubRepoResponse("OWNER", "REPO")

	http.Register(
		httpmock.GraphQL(`query IssueList\b`),
		httpmock.FileResponse("../test/fixtures/issueList.json"))

	output, err := RunCommand("issue list")
	if err != nil {
		t.Errorf("error running command `issue list`: %v", err)
	}

	eq(t, output.Stderr(), "")
	test.ExpectLines(t, output.String(),
		`1[\t]+number won[\t]+label[\t]+\d+`,
		`2[\t]+number too[\t]+label[\t]+\d+`,
		`4[\t]+number fore[\t]+label[\t]+\d+`)
}

func TestIssueList_tty(t *testing.T) {
	initBlankContext("", "OWNER/REPO", "master")
	defer stubTerminal(true)()
	http := initFakeHTTP()
	http.StubRepoResponse("OWNER", "REPO")
	http.Register(
		httpmock.GraphQL(`query IssueList\b`),
		httpmock.FileResponse("../test/fixtures/issueList.json"))

	output, err := RunCommand("issue list")
	if err != nil {
		t.Errorf("error running command `issue list`: %v", err)
	}

	eq(t, output.Stderr(), `
Showing 3 of 3 issues in OWNER/REPO

`)

	test.ExpectLines(t, output.String(),
		"number won",
		"number too",
		"number fore")
}

func TestIssueList_tty_withFlags(t *testing.T) {
	defer stubTerminal(true)()
	initBlankContext("", "OWNER/REPO", "master")
	http := initFakeHTTP()
	http.StubRepoResponse("OWNER", "REPO")
	http.Register(
		httpmock.GraphQL(`query IssueList\b`),
		httpmock.GraphQLQuery(`
		{ "data": {	"repository": {
			"hasIssuesEnabled": true,
			"issues": { "nodes": [] }
		} } }`, func(_ string, params map[string]interface{}) {
			assert.Equal(t, "probablyCher", params["assignee"].(string))
			assert.Equal(t, "foo", params["author"].(string))
			assert.Equal(t, "me", params["mention"].(string))
			assert.Equal(t, "1.x", params["milestone"].(string))
			assert.Equal(t, []interface{}{"web", "bug"}, params["labels"].([]interface{}))
			assert.Equal(t, []interface{}{"OPEN"}, params["states"].([]interface{}))
		}))

	output, err := RunCommand("issue list -a probablyCher -l web,bug -s open -A foo --mention me --milestone 1.x")
	if err != nil {
		t.Errorf("error running command `issue list`: %v", err)
	}

	eq(t, output.String(), "")
	eq(t, output.Stderr(), `
No issues match your search in OWNER/REPO

`)
}

func TestIssueList_withInvalidLimitFlag(t *testing.T) {
	initBlankContext("", "OWNER/REPO", "master")
	http := initFakeHTTP()
	http.StubRepoResponse("OWNER", "REPO")

	_, err := RunCommand("issue list --limit=0")

	if err == nil || err.Error() != "invalid limit: 0" {
		t.Errorf("error running command `issue list`: %v", err)
	}
}

func TestIssueList_nullAssigneeLabels(t *testing.T) {
	initBlankContext("", "OWNER/REPO", "master")
	http := initFakeHTTP()
	http.StubRepoResponse("OWNER", "REPO")

	http.StubResponse(200, bytes.NewBufferString(`
	{ "data": {	"repository": {
		"hasIssuesEnabled": true,
		"issues": { "nodes": [] }
	} } }
	`))

	_, err := RunCommand("issue list")
	if err != nil {
		t.Errorf("error running command `issue list`: %v", err)
	}

	bodyBytes, _ := ioutil.ReadAll(http.Requests[1].Body)
	reqBody := struct {
		Variables map[string]interface{}
	}{}
	_ = json.Unmarshal(bodyBytes, &reqBody)

	_, assigneeDeclared := reqBody.Variables["assignee"]
	_, labelsDeclared := reqBody.Variables["labels"]
	eq(t, assigneeDeclared, false)
	eq(t, labelsDeclared, false)
}

func TestIssueList_disabledIssues(t *testing.T) {
	initBlankContext("", "OWNER/REPO", "master")
	http := initFakeHTTP()
	http.StubRepoResponse("OWNER", "REPO")

	http.StubResponse(200, bytes.NewBufferString(`
	{ "data": {	"repository": {
		"hasIssuesEnabled": false
	} } }
	`))

	_, err := RunCommand("issue list")
	if err == nil || err.Error() != "the 'OWNER/REPO' repository has disabled issues" {
		t.Errorf("error running command `issue list`: %v", err)
	}
}

func TestIssueList_web(t *testing.T) {
	initBlankContext("", "OWNER/REPO", "master")
	http := initFakeHTTP()
	http.StubRepoResponse("OWNER", "REPO")

	var seenCmd *exec.Cmd
	restoreCmd := run.SetPrepareCmd(func(cmd *exec.Cmd) run.Runnable {
		seenCmd = cmd
		return &test.OutputStub{}
	})
	defer restoreCmd()

	output, err := RunCommand("issue list --web -a peter -A john -l bug -l docs -L 10 -s all --mention frank --milestone v1.1")
	if err != nil {
		t.Errorf("error running command `issue list` with `--web` flag: %v", err)
	}

	expectedURL := "https://github.com/OWNER/REPO/issues?q=is%3Aissue+assignee%3Apeter+label%3Abug+label%3Adocs+author%3Ajohn+mentions%3Afrank+milestone%3Av1.1"

	eq(t, output.String(), "")
	eq(t, output.Stderr(), "Opening github.com/OWNER/REPO/issues in your browser.\n")

	if seenCmd == nil {
		t.Fatal("expected a command to run")
	}
	url := seenCmd.Args[len(seenCmd.Args)-1]
	eq(t, url, expectedURL)
}

func TestIssueView_web(t *testing.T) {
	initBlankContext("", "OWNER/REPO", "master")
	http := initFakeHTTP()
	http.StubRepoResponse("OWNER", "REPO")

	http.StubResponse(200, bytes.NewBufferString(`
	{ "data": { "repository": { "hasIssuesEnabled": true, "issue": {
		"number": 123,
		"url": "https://github.com/OWNER/REPO/issues/123"
	} } } }
	`))

	var seenCmd *exec.Cmd
	restoreCmd := run.SetPrepareCmd(func(cmd *exec.Cmd) run.Runnable {
		seenCmd = cmd
		return &test.OutputStub{}
	})
	defer restoreCmd()

	output, err := RunCommand("issue view -w 123")
	if err != nil {
		t.Errorf("error running command `issue view`: %v", err)
	}

	eq(t, output.String(), "")
	eq(t, output.Stderr(), "Opening https://github.com/OWNER/REPO/issues/123 in your browser.\n")

	if seenCmd == nil {
		t.Fatal("expected a command to run")
	}
	url := seenCmd.Args[len(seenCmd.Args)-1]
	eq(t, url, "https://github.com/OWNER/REPO/issues/123")
}

func TestIssueView_web_numberArgWithHash(t *testing.T) {
	initBlankContext("", "OWNER/REPO", "master")
	http := initFakeHTTP()
	http.StubRepoResponse("OWNER", "REPO")

	http.StubResponse(200, bytes.NewBufferString(`
	{ "data": { "repository": { "hasIssuesEnabled": true, "issue": {
		"number": 123,
		"url": "https://github.com/OWNER/REPO/issues/123"
	} } } }
	`))

	var seenCmd *exec.Cmd
	restoreCmd := run.SetPrepareCmd(func(cmd *exec.Cmd) run.Runnable {
		seenCmd = cmd
		return &test.OutputStub{}
	})
	defer restoreCmd()

	output, err := RunCommand("issue view -w \"#123\"")
	if err != nil {
		t.Errorf("error running command `issue view`: %v", err)
	}

	eq(t, output.String(), "")
	eq(t, output.Stderr(), "Opening https://github.com/OWNER/REPO/issues/123 in your browser.\n")

	if seenCmd == nil {
		t.Fatal("expected a command to run")
	}
	url := seenCmd.Args[len(seenCmd.Args)-1]
	eq(t, url, "https://github.com/OWNER/REPO/issues/123")
}

func TestIssueView_nontty_Preview(t *testing.T) {
	defer stubTerminal(false)()
	tests := map[string]struct {
		ownerRepo       string
		command         string
		fixture         string
		expectedOutputs []string
	}{
		"Open issue without metadata": {
			ownerRepo: "master",
			command:   "issue view 123",
			fixture:   "../test/fixtures/issueView_preview.json",
			expectedOutputs: []string{
				`title:\tix of coins`,
				`state:\tOPEN`,
				`comments:\t9`,
				`author:\tmarseilles`,
				`assignees:`,
				`\*\*bold story\*\*`,
			},
		},
		"Open issue with metadata": {
			ownerRepo: "master",
			command:   "issue view 123",
			fixture:   "../test/fixtures/issueView_previewWithMetadata.json",
			expectedOutputs: []string{
				`title:\tix of coins`,
				`assignees:\tmarseilles, monaco`,
				`author:\tmarseilles`,
				`state:\tOPEN`,
				`comments:\t9`,
				`labels:\tone, two, three, four, five`,
				`projects:\tProject 1 \(column A\), Project 2 \(column B\), Project 3 \(column C\), Project 4 \(Awaiting triage\)\n`,
				`milestone:\tuluru\n`,
				`\*\*bold story\*\*`,
			},
		},
		"Open issue with empty body": {
			ownerRepo: "master",
			command:   "issue view 123",
			fixture:   "../test/fixtures/issueView_previewWithEmptyBody.json",
			expectedOutputs: []string{
				`title:\tix of coins`,
				`state:\tOPEN`,
				`author:\tmarseilles`,
				`labels:\ttarot`,
			},
		},
		"Closed issue": {
			ownerRepo: "master",
			command:   "issue view 123",
			fixture:   "../test/fixtures/issueView_previewClosedState.json",
			expectedOutputs: []string{
				`title:\tix of coins`,
				`state:\tCLOSED`,
				`\*\*bold story\*\*`,
				`author:\tmarseilles`,
				`labels:\ttarot`,
				`\*\*bold story\*\*`,
			},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			initBlankContext("", "OWNER/REPO", tc.ownerRepo)
			http := initFakeHTTP()
			http.StubRepoResponse("OWNER", "REPO")

			http.Register(httpmock.GraphQL(`query IssueByNumber\b`), httpmock.FileResponse(tc.fixture))

			output, err := RunCommand(tc.command)
			if err != nil {
				t.Errorf("error running command `%v`: %v", tc.command, err)
			}

			eq(t, output.Stderr(), "")

			test.ExpectLines(t, output.String(), tc.expectedOutputs...)
		})
	}
}

func TestIssueView_tty_Preview(t *testing.T) {
	defer stubTerminal(true)()
	tests := map[string]struct {
		ownerRepo       string
		command         string
		fixture         string
		expectedOutputs []string
	}{
		"Open issue without metadata": {
			ownerRepo: "master",
			command:   "issue view 123",
			fixture:   "../test/fixtures/issueView_preview.json",
			expectedOutputs: []string{
				`ix of coins`,
				`Open • marseilles opened about 292 years ago • 9 comments`,
				`bold story`,
				`View this issue on GitHub: https://github.com/OWNER/REPO/issues/123`,
			},
		},
		"Open issue with metadata": {
			ownerRepo: "master",
			command:   "issue view 123",
			fixture:   "../test/fixtures/issueView_previewWithMetadata.json",
			expectedOutputs: []string{
				`ix of coins`,
				`Open • marseilles opened about 292 years ago • 9 comments`,
				`Assignees: marseilles, monaco\n`,
				`Labels: one, two, three, four, five\n`,
				`Projects: Project 1 \(column A\), Project 2 \(column B\), Project 3 \(column C\), Project 4 \(Awaiting triage\)\n`,
				`Milestone: uluru\n`,
				`bold story`,
				`View this issue on GitHub: https://github.com/OWNER/REPO/issues/123`,
			},
		},
		"Open issue with empty body": {
			ownerRepo: "master",
			command:   "issue view 123",
			fixture:   "../test/fixtures/issueView_previewWithEmptyBody.json",
			expectedOutputs: []string{
				`ix of coins`,
				`Open • marseilles opened about 292 years ago • 9 comments`,
				`View this issue on GitHub: https://github.com/OWNER/REPO/issues/123`,
			},
		},
		"Closed issue": {
			ownerRepo: "master",
			command:   "issue view 123",
			fixture:   "../test/fixtures/issueView_previewClosedState.json",
			expectedOutputs: []string{
				`ix of coins`,
				`Closed • marseilles opened about 292 years ago • 9 comments`,
				`bold story`,
				`View this issue on GitHub: https://github.com/OWNER/REPO/issues/123`,
			},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			initBlankContext("", "OWNER/REPO", tc.ownerRepo)
			http := initFakeHTTP()
			http.StubRepoResponse("OWNER", "REPO")
			http.Register(httpmock.GraphQL(`query IssueByNumber\b`), httpmock.FileResponse(tc.fixture))

			output, err := RunCommand(tc.command)
			if err != nil {
				t.Errorf("error running command `%v`: %v", tc.command, err)
			}

			eq(t, output.Stderr(), "")

			test.ExpectLines(t, output.String(), tc.expectedOutputs...)
		})
	}
}

func TestIssueView_web_notFound(t *testing.T) {
	initBlankContext("", "OWNER/REPO", "master")
	http := initFakeHTTP()
	http.StubRepoResponse("OWNER", "REPO")

	http.StubResponse(200, bytes.NewBufferString(`
	{ "errors": [
		{ "message": "Could not resolve to an Issue with the number of 9999." }
	] }
	`))

	var seenCmd *exec.Cmd
	restoreCmd := run.SetPrepareCmd(func(cmd *exec.Cmd) run.Runnable {
		seenCmd = cmd
		return &test.OutputStub{}
	})
	defer restoreCmd()

	_, err := RunCommand("issue view -w 9999")
	if err == nil || err.Error() != "GraphQL error: Could not resolve to an Issue with the number of 9999." {
		t.Errorf("error running command `issue view`: %v", err)
	}

	if seenCmd != nil {
		t.Fatal("did not expect any command to run")
	}
}

func TestIssueView_disabledIssues(t *testing.T) {
	initBlankContext("", "OWNER/REPO", "master")
	http := initFakeHTTP()
	http.StubRepoResponse("OWNER", "REPO")

	http.StubResponse(200, bytes.NewBufferString(`
		{ "data": { "repository": {
			"id": "REPOID",
			"hasIssuesEnabled": false
		} } }
	`))

	_, err := RunCommand(`issue view 6666`)
	if err == nil || err.Error() != "the 'OWNER/REPO' repository has disabled issues" {
		t.Errorf("error running command `issue view`: %v", err)
	}
}

func TestIssueView_web_urlArg(t *testing.T) {
	initBlankContext("", "OWNER/REPO", "master")
	http := initFakeHTTP()

	http.StubResponse(200, bytes.NewBufferString(`
	{ "data": { "repository": { "hasIssuesEnabled": true, "issue": {
		"number": 123,
		"url": "https://github.com/OWNER/REPO/issues/123"
	} } } }
	`))

	var seenCmd *exec.Cmd
	restoreCmd := run.SetPrepareCmd(func(cmd *exec.Cmd) run.Runnable {
		seenCmd = cmd
		return &test.OutputStub{}
	})
	defer restoreCmd()

	output, err := RunCommand("issue view -w https://github.com/OWNER/REPO/issues/123")
	if err != nil {
		t.Errorf("error running command `issue view`: %v", err)
	}

	eq(t, output.String(), "")

	if seenCmd == nil {
		t.Fatal("expected a command to run")
	}
	url := seenCmd.Args[len(seenCmd.Args)-1]
	eq(t, url, "https://github.com/OWNER/REPO/issues/123")
}

func TestIssueCreate_nontty_error(t *testing.T) {
	defer stubTerminal(false)()
	initBlankContext("", "OWNER/REPO", "master")
	http := initFakeHTTP()
	http.StubRepoResponse("OWNER", "REPO")

	http.StubResponse(200, bytes.NewBufferString(`
		{ "data": { "repository": {
			"id": "REPOID",
			"hasIssuesEnabled": true
		} } }
	`))

	_, err := RunCommand(`issue create -t hello`)
	if err == nil {
		t.Fatal("expected error running command `issue create`")
	}

	assert.Equal(t, "must provide --title and --body when not attached to a terminal", err.Error())

}

func TestIssueCreate(t *testing.T) {
	initBlankContext("", "OWNER/REPO", "master")
	http := initFakeHTTP()
	http.StubRepoResponse("OWNER", "REPO")

	http.StubResponse(200, bytes.NewBufferString(`
		{ "data": { "repository": {
			"id": "REPOID",
			"hasIssuesEnabled": true
		} } }
	`))
	http.StubResponse(200, bytes.NewBufferString(`
		{ "data": { "createIssue": { "issue": {
			"URL": "https://github.com/OWNER/REPO/issues/12"
		} } } }
	`))

	output, err := RunCommand(`issue create -t hello -b "cash rules everything around me"`)
	if err != nil {
		t.Errorf("error running command `issue create`: %v", err)
	}

	bodyBytes, _ := ioutil.ReadAll(http.Requests[2].Body)
	reqBody := struct {
		Variables struct {
			Input struct {
				RepositoryID string
				Title        string
				Body         string
			}
		}
	}{}
	_ = json.Unmarshal(bodyBytes, &reqBody)

	eq(t, reqBody.Variables.Input.RepositoryID, "REPOID")
	eq(t, reqBody.Variables.Input.Title, "hello")
	eq(t, reqBody.Variables.Input.Body, "cash rules everything around me")

	eq(t, output.String(), "https://github.com/OWNER/REPO/issues/12\n")
}

func TestIssueCreate_metadata(t *testing.T) {
	initBlankContext("", "OWNER/REPO", "master")
	http := initFakeHTTP()
	defer http.Verify(t)

	http.Register(
		httpmock.GraphQL(`query RepositoryNetwork\b`),
		httpmock.StringResponse(httpmock.RepoNetworkStubResponse("OWNER", "REPO", "master", "WRITE")))
	http.Register(
		httpmock.GraphQL(`query RepositoryInfo\b`),
		httpmock.StringResponse(`
		{ "data": { "repository": {
			"id": "REPOID",
			"hasIssuesEnabled": true,
			"viewerPermission": "WRITE"
		} } }
		`))
	http.Register(
		httpmock.GraphQL(`query RepositoryResolveMetadataIDs\b`),
		httpmock.StringResponse(`
		{ "data": {
			"u000": { "login": "MonaLisa", "id": "MONAID" },
			"repository": {
				"l000": { "name": "bug", "id": "BUGID" },
				"l001": { "name": "TODO", "id": "TODOID" }
			}
		} }
		`))
	http.Register(
		httpmock.GraphQL(`query RepositoryMilestoneList\b`),
		httpmock.StringResponse(`
		{ "data": { "repository": { "milestones": {
			"nodes": [
				{ "title": "GA", "id": "GAID" },
				{ "title": "Big One.oh", "id": "BIGONEID" }
			],
			"pageInfo": { "hasNextPage": false }
		} } } }
		`))
	http.Register(
		httpmock.GraphQL(`query RepositoryProjectList\b`),
		httpmock.StringResponse(`
		{ "data": { "repository": { "projects": {
			"nodes": [
				{ "name": "Cleanup", "id": "CLEANUPID" },
				{ "name": "Roadmap", "id": "ROADMAPID" }
			],
			"pageInfo": { "hasNextPage": false }
		} } } }
		`))
	http.Register(
		httpmock.GraphQL(`query OrganizationProjectList\b`),
		httpmock.StringResponse(`
		{	"data": { "organization": null },
			"errors": [{
				"type": "NOT_FOUND",
				"path": [ "organization" ],
				"message": "Could not resolve to an Organization with the login of 'OWNER'."
			}]
		}
		`))
	http.Register(
		httpmock.GraphQL(`mutation IssueCreate\b`),
		httpmock.GraphQLMutation(`
		{ "data": { "createIssue": { "issue": {
			"URL": "https://github.com/OWNER/REPO/issues/12"
		} } } }
	`, func(inputs map[string]interface{}) {
			eq(t, inputs["title"], "TITLE")
			eq(t, inputs["body"], "BODY")
			eq(t, inputs["assigneeIds"], []interface{}{"MONAID"})
			eq(t, inputs["labelIds"], []interface{}{"BUGID", "TODOID"})
			eq(t, inputs["projectIds"], []interface{}{"ROADMAPID"})
			eq(t, inputs["milestoneId"], "BIGONEID")
			if v, ok := inputs["userIds"]; ok {
				t.Errorf("did not expect userIds: %v", v)
			}
			if v, ok := inputs["teamIds"]; ok {
				t.Errorf("did not expect teamIds: %v", v)
			}
		}))

	output, err := RunCommand(`issue create -t TITLE -b BODY -a monalisa -l bug -l todo -p roadmap -m 'big one.oh'`)
	if err != nil {
		t.Errorf("error running command `issue create`: %v", err)
	}

	eq(t, output.String(), "https://github.com/OWNER/REPO/issues/12\n")
}

func TestIssueCreate_disabledIssues(t *testing.T) {
	initBlankContext("", "OWNER/REPO", "master")
	http := initFakeHTTP()
	http.StubRepoResponse("OWNER", "REPO")

	http.StubResponse(200, bytes.NewBufferString(`
		{ "data": { "repository": {
			"id": "REPOID",
			"hasIssuesEnabled": false
		} } }
	`))

	_, err := RunCommand(`issue create -t heres -b johnny`)
	if err == nil || err.Error() != "the 'OWNER/REPO' repository has disabled issues" {
		t.Errorf("error running command `issue create`: %v", err)
	}
}

func TestIssueCreate_web(t *testing.T) {
	initBlankContext("", "OWNER/REPO", "master")
	http := initFakeHTTP()
	http.StubRepoResponse("OWNER", "REPO")

	var seenCmd *exec.Cmd
	restoreCmd := run.SetPrepareCmd(func(cmd *exec.Cmd) run.Runnable {
		seenCmd = cmd
		return &test.OutputStub{}
	})
	defer restoreCmd()

	output, err := RunCommand(`issue create --web`)
	if err != nil {
		t.Errorf("error running command `issue create`: %v", err)
	}

	if seenCmd == nil {
		t.Fatal("expected a command to run")
	}
	url := seenCmd.Args[len(seenCmd.Args)-1]
	eq(t, url, "https://github.com/OWNER/REPO/issues/new")
	eq(t, output.String(), "Opening github.com/OWNER/REPO/issues/new in your browser.\n")
	eq(t, output.Stderr(), "")
}

func TestIssueCreate_webTitleBody(t *testing.T) {
	initBlankContext("", "OWNER/REPO", "master")
	http := initFakeHTTP()
	http.StubRepoResponse("OWNER", "REPO")

	var seenCmd *exec.Cmd
	restoreCmd := run.SetPrepareCmd(func(cmd *exec.Cmd) run.Runnable {
		seenCmd = cmd
		return &test.OutputStub{}
	})
	defer restoreCmd()

	output, err := RunCommand(`issue create -w -t mytitle -b mybody`)
	if err != nil {
		t.Errorf("error running command `issue create`: %v", err)
	}

	if seenCmd == nil {
		t.Fatal("expected a command to run")
	}
	url := strings.ReplaceAll(seenCmd.Args[len(seenCmd.Args)-1], "^", "")
	eq(t, url, "https://github.com/OWNER/REPO/issues/new?body=mybody&title=mytitle")
	eq(t, output.String(), "Opening github.com/OWNER/REPO/issues/new in your browser.\n")
}

func Test_listHeader(t *testing.T) {
	type args struct {
		repoName        string
		itemName        string
		matchCount      int
		totalMatchCount int
		hasFilters      bool
	}
	tests := []struct {
		name string
		args args
		want string
	}{
		{
			name: "no results",
			args: args{
				repoName:        "REPO",
				itemName:        "table",
				matchCount:      0,
				totalMatchCount: 0,
				hasFilters:      false,
			},
			want: "There are no open tables in REPO",
		},
		{
			name: "no matches after filters",
			args: args{
				repoName:        "REPO",
				itemName:        "Luftballon",
				matchCount:      0,
				totalMatchCount: 0,
				hasFilters:      true,
			},
			want: "No Luftballons match your search in REPO",
		},
		{
			name: "one result",
			args: args{
				repoName:        "REPO",
				itemName:        "genie",
				matchCount:      1,
				totalMatchCount: 23,
				hasFilters:      false,
			},
			want: "Showing 1 of 23 genies in REPO",
		},
		{
			name: "one result after filters",
			args: args{
				repoName:        "REPO",
				itemName:        "tiny cup",
				matchCount:      1,
				totalMatchCount: 23,
				hasFilters:      true,
			},
			want: "Showing 1 of 23 tiny cups in REPO that match your search",
		},
		{
			name: "one result in total",
			args: args{
				repoName:        "REPO",
				itemName:        "chip",
				matchCount:      1,
				totalMatchCount: 1,
				hasFilters:      false,
			},
			want: "Showing 1 of 1 chip in REPO",
		},
		{
			name: "one result in total after filters",
			args: args{
				repoName:        "REPO",
				itemName:        "spicy noodle",
				matchCount:      1,
				totalMatchCount: 1,
				hasFilters:      true,
			},
			want: "Showing 1 of 1 spicy noodle in REPO that matches your search",
		},
		{
			name: "multiple results",
			args: args{
				repoName:        "REPO",
				itemName:        "plant",
				matchCount:      4,
				totalMatchCount: 23,
				hasFilters:      false,
			},
			want: "Showing 4 of 23 plants in REPO",
		},
		{
			name: "multiple results after filters",
			args: args{
				repoName:        "REPO",
				itemName:        "boomerang",
				matchCount:      4,
				totalMatchCount: 23,
				hasFilters:      true,
			},
			want: "Showing 4 of 23 boomerangs in REPO that match your search",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := listHeader(tt.args.repoName, tt.args.itemName, tt.args.matchCount, tt.args.totalMatchCount, tt.args.hasFilters); got != tt.want {
				t.Errorf("listHeader() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIssueStateTitleWithColor(t *testing.T) {
	tests := map[string]struct {
		state string
		want  string
	}{
		"Open state":   {state: "OPEN", want: "Open"},
		"Closed state": {state: "CLOSED", want: "Closed"},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			got := issueStateTitleWithColor(tc.state)
			diff := cmp.Diff(tc.want, got)
			if diff != "" {
				t.Fatalf(diff)
			}
		})
	}
}

func TestIssueClose(t *testing.T) {
	initBlankContext("", "OWNER/REPO", "master")
	http := initFakeHTTP()
	http.StubRepoResponse("OWNER", "REPO")

	http.StubResponse(200, bytes.NewBufferString(`
	{ "data": { "repository": {
		"hasIssuesEnabled": true,
		"issue": { "number": 13, "title": "The title of the issue"}
	} } }
	`))

	http.StubResponse(200, bytes.NewBufferString(`{"id": "THE-ID"}`))

	output, err := RunCommand("issue close 13")
	if err != nil {
		t.Fatalf("error running command `issue close`: %v", err)
	}

	r := regexp.MustCompile(`Closed issue #13 \(The title of the issue\)`)

	if !r.MatchString(output.Stderr()) {
		t.Fatalf("output did not match regexp /%s/\n> output\n%q\n", r, output.Stderr())
	}
}

func TestIssueClose_alreadyClosed(t *testing.T) {
	initBlankContext("", "OWNER/REPO", "master")
	http := initFakeHTTP()
	http.StubRepoResponse("OWNER", "REPO")

	http.StubResponse(200, bytes.NewBufferString(`
	{ "data": { "repository": {
		"hasIssuesEnabled": true,
		"issue": { "number": 13, "title": "The title of the issue", "closed": true}
	} } }
	`))

	http.StubResponse(200, bytes.NewBufferString(`{"id": "THE-ID"}`))

	output, err := RunCommand("issue close 13")
	if err != nil {
		t.Fatalf("error running command `issue close`: %v", err)
	}

	r := regexp.MustCompile(`Issue #13 \(The title of the issue\) is already closed`)

	if !r.MatchString(output.Stderr()) {
		t.Fatalf("output did not match regexp /%s/\n> output\n%q\n", r, output.Stderr())
	}
}

func TestIssueClose_issuesDisabled(t *testing.T) {
	initBlankContext("", "OWNER/REPO", "master")
	http := initFakeHTTP()
	http.StubRepoResponse("OWNER", "REPO")

	http.StubResponse(200, bytes.NewBufferString(`
	{ "data": { "repository": {
		"hasIssuesEnabled": false
	} } }
	`))

	_, err := RunCommand("issue close 13")
	if err == nil || err.Error() != "the 'OWNER/REPO' repository has disabled issues" {
		t.Fatalf("got error: %v", err)
	}
}

func TestIssueReopen(t *testing.T) {
	initBlankContext("", "OWNER/REPO", "master")
	http := initFakeHTTP()
	http.StubRepoResponse("OWNER", "REPO")

	http.StubResponse(200, bytes.NewBufferString(`
	{ "data": { "repository": {
		"hasIssuesEnabled": true,
		"issue": { "number": 2, "closed": true, "title": "The title of the issue"}
	} } }
	`))

	http.StubResponse(200, bytes.NewBufferString(`{"id": "THE-ID"}`))

	output, err := RunCommand("issue reopen 2")
	if err != nil {
		t.Fatalf("error running command `issue reopen`: %v", err)
	}

	r := regexp.MustCompile(`Reopened issue #2 \(The title of the issue\)`)

	if !r.MatchString(output.Stderr()) {
		t.Fatalf("output did not match regexp /%s/\n> output\n%q\n", r, output.Stderr())
	}
}

func TestIssueReopen_alreadyOpen(t *testing.T) {
	initBlankContext("", "OWNER/REPO", "master")
	http := initFakeHTTP()
	http.StubRepoResponse("OWNER", "REPO")

	http.StubResponse(200, bytes.NewBufferString(`
	{ "data": { "repository": {
		"hasIssuesEnabled": true,
		"issue": { "number": 2, "closed": false, "title": "The title of the issue"}
	} } }
	`))

	http.StubResponse(200, bytes.NewBufferString(`{"id": "THE-ID"}`))

	output, err := RunCommand("issue reopen 2")
	if err != nil {
		t.Fatalf("error running command `issue reopen`: %v", err)
	}

	r := regexp.MustCompile(`Issue #2 \(The title of the issue\) is already open`)

	if !r.MatchString(output.Stderr()) {
		t.Fatalf("output did not match regexp /%s/\n> output\n%q\n", r, output.Stderr())
	}
}

func TestIssueReopen_issuesDisabled(t *testing.T) {
	initBlankContext("", "OWNER/REPO", "master")
	http := initFakeHTTP()
	http.StubRepoResponse("OWNER", "REPO")

	http.StubResponse(200, bytes.NewBufferString(`
	{ "data": { "repository": {
		"hasIssuesEnabled": false
	} } }
	`))

	_, err := RunCommand("issue reopen 2")
	if err == nil || err.Error() != "the 'OWNER/REPO' repository has disabled issues" {
		t.Fatalf("got error: %v", err)
	}
}

func Test_listURLWithQuery(t *testing.T) {
	type args struct {
		listURL string
		options filterOptions
	}
	tests := []struct {
		name    string
		args    args
		want    string
		wantErr bool
	}{
		{
			name: "blank",
			args: args{
				listURL: "https://example.com/path?a=b",
				options: filterOptions{
					entity: "issue",
					state:  "open",
				},
			},
			want:    "https://example.com/path?a=b&q=is%3Aissue+is%3Aopen",
			wantErr: false,
		},
		{
			name: "all",
			args: args{
				listURL: "https://example.com/path",
				options: filterOptions{
					entity:     "issue",
					state:      "open",
					assignee:   "bo",
					author:     "ka",
					baseBranch: "trunk",
					mention:    "nu",
				},
			},
			want:    "https://example.com/path?q=is%3Aissue+is%3Aopen+assignee%3Abo+author%3Aka+base%3Atrunk+mentions%3Anu",
			wantErr: false,
		},
		{
			name: "spaces in values",
			args: args{
				listURL: "https://example.com/path",
				options: filterOptions{
					entity:    "pr",
					state:     "open",
					labels:    []string{"docs", "help wanted"},
					milestone: `Codename "What Was Missing"`,
				},
			},
			want:    "https://example.com/path?q=is%3Apr+is%3Aopen+label%3Adocs+label%3A%22help+wanted%22+milestone%3A%22Codename+%5C%22What+Was+Missing%5C%22%22",
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := listURLWithQuery(tt.args.listURL, tt.args.options)
			if (err != nil) != tt.wantErr {
				t.Errorf("listURLWithQuery() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("listURLWithQuery() = %v, want %v", got, tt.want)
			}
		})
	}
}
