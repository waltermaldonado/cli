package config

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/cli/cli/api"
	"github.com/cli/cli/auth"
	"gopkg.in/yaml.v3"
)



	// The "GitHub CLI" OAuth app
	oauthClientID = "178c6fc778ccc68e1d6a"
	// This value is safe to be embedded in version control
	oauthClientSecret = "34ddeff2b558a23d38fba8a6de74f086ede1cc0b"
)


func init() {
	if gheHostname := os.Getenv("GITHUB_HOST"); gheHostname != "" {
		oauthHost = gheHostname
	}
}

// TODO: have a conversation about whether this belongs in the "context" package
// FIXME: make testable
func setupConfigFile(filename string) (Config, error) {
	var verboseStream io.Writer
	if strings.Contains(os.Getenv("DEBUG"), "oauth") {
		verboseStream = os.Stderr
	}

	flow := &auth.OAuthFlow{
		Hostname:     oauthHost,
		ClientID:     oauthClientID,
		ClientSecret: oauthClientSecret,
		Scopes:       []string{"repo", "read:org", "gist"},
		WriteSuccessHTML: func(w io.Writer) {
			fmt.Fprintln(w, oauthSuccessPage)
		},
		VerboseStream: verboseStream,
	}

	fmt.Fprintln(os.Stderr, notice)
	fmt.Fprintf(os.Stderr, "Press Enter to open %s in your browser... ", flow.Hostname)
	_ = waitForEnter(os.Stdin)
	token, err := flow.ObtainAccessToken()
	if err != nil {
		return "", "", err
	}

	userLogin, err := getViewer(token)
	if err != nil {
		return "", "", err
	}

	return token, userLogin, nil
}

func AuthFlowComplete() {
	fmt.Fprintln(os.Stderr, "Authentication complete. Press Enter to continue... ")
	_ = waitForEnter(os.Stdin)
}

func getViewer(token string) (string, error) {
	http := api.NewClient(api.AddHeader("Authorization", fmt.Sprintf("token %s", token)))
	return api.CurrentLoginName(http)
}

func waitForEnter(r io.Reader) error {
	scanner := bufio.NewScanner(r)
	scanner.Scan()
	return scanner.Err()
}
