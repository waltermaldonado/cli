package command

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"runtime/debug"
	"strings"

	"github.com/MakeNowJust/heredoc"
	"github.com/cli/cli/api"
	"github.com/cli/cli/context"
	"github.com/cli/cli/internal/config"
	"github.com/cli/cli/internal/ghrepo"
	"github.com/cli/cli/internal/run"
	apiCmd "github.com/cli/cli/pkg/cmd/api"
	"github.com/cli/cli/pkg/cmdutil"
	"github.com/cli/cli/pkg/iostreams"
	"github.com/cli/cli/utils"
	"github.com/google/shlex"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// TODO these are sprinkled across command, context, config, and ghrepo
var defaultHostname = "github.com"

// Version is dynamically set by the toolchain or overridden by the Makefile.
var Version = "DEV"

// BuildDate is dynamically set at build time in the Makefile.
var BuildDate = "" // YYYY-MM-DD

var versionOutput = ""

func init() {
	if gheHostname := os.Getenv("GITHUB_HOST"); gheHostname != "" {
		defaultHostname = gheHostname
	}

	if Version == "DEV" {
		if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "(devel)" {
			Version = info.Main.Version
		}
	}
	Version = strings.TrimPrefix(Version, "v")
	if BuildDate == "" {
		RootCmd.Version = Version
	} else {
		RootCmd.Version = fmt.Sprintf("%s (%s)", Version, BuildDate)
	}
	versionOutput = fmt.Sprintf("gh version %s\n%s\n", RootCmd.Version, changelogURL(Version))
	RootCmd.AddCommand(versionCmd)
	RootCmd.SetVersionTemplate(versionOutput)

	RootCmd.PersistentFlags().Bool("help", false, "Show help for command")
	RootCmd.Flags().Bool("version", false, "Show gh version")
	// TODO:
	// RootCmd.PersistentFlags().BoolP("verbose", "V", false, "enable verbose output")

	RootCmd.SetHelpFunc(rootHelpFunc)
	RootCmd.SetUsageFunc(rootUsageFunc)

	RootCmd.SetFlagErrorFunc(func(cmd *cobra.Command, err error) error {
		if err == pflag.ErrHelp {
			return err
		}
		return &cmdutil.FlagError{Err: err}
	})

	// TODO: iron out how a factory incorporates context
	cmdFactory := &cmdutil.Factory{
		IOStreams: iostreams.System(),
		HttpClient: func() (*http.Client, error) {
			token := os.Getenv("GITHUB_TOKEN")
			if len(token) == 0 {
				// TODO: decouple from `context`
				ctx := context.New()
				var err error
				// TODO: pass IOStreams to this so that the auth flow knows if it's interactive or not
				token, err = ctx.AuthToken()
				if err != nil {
					return nil, err
				}
			}
			return httpClient(token), nil
		},
		BaseRepo: func() (ghrepo.Interface, error) {
			// TODO: decouple from `context`
			ctx := context.New()
			return ctx.BaseRepo()
		},
	}
	RootCmd.AddCommand(apiCmd.NewCmdApi(cmdFactory, nil))
}

// RootCmd is the entry point of command-line execution
var RootCmd = &cobra.Command{
	Use:   "gh <command> <subcommand> [flags]",
	Short: "GitHub CLI",
	Long:  `Work seamlessly with GitHub from the command line.`,

	SilenceErrors: true,
	SilenceUsage:  true,
	Example: heredoc.Doc(`
	$ gh issue create
	$ gh repo clone cli/cli
	$ gh pr checkout 321
	`),
	Annotations: map[string]string{
		"help:feedback": heredoc.Doc(`
			Fill out our feedback form https://forms.gle/umxd3h31c7aMQFKG7
			Open an issue using “gh issue create -R cli/cli”
		`),
		"help:environment": heredoc.Doc(`
			GITHUB_TOKEN: an authentication token for API requests. Setting this avoids being
			prompted to authenticate and overrides any previously stored credentials.

			GH_REPO: specify the GitHub repository in "OWNER/REPO" format for commands that
			otherwise operate on a local repository.

			GH_EDITOR, GIT_EDITOR, VISUAL, EDITOR (in order of precedence): the editor tool to use
			for authoring text.

			BROWSER: the web browser to use for opening links.

			DEBUG: set to any value to enable verbose output to standard error. Include values "api"
			or "oauth" to print detailed information about HTTP requests or authentication flow.

			NO_COLOR: avoid printing ANSI escape sequences for color output.
		`),
	},
}

var versionCmd = &cobra.Command{
	Use:    "version",
	Hidden: true,
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Print(versionOutput)
	},
}

// overridden in tests
var initContext = func() context.Context {
	ctx := context.New()
	if repo := os.Getenv("GH_REPO"); repo != "" {
		ctx.SetBaseRepo(repo)
	}
	if token := os.Getenv("GITHUB_TOKEN"); token != "" {
		ctx.SetAuthToken(token)
	}
	return ctx
}

// BasicClient returns an API client that borrows from but does not depend on
// user configuration
func BasicClient() (*api.Client, error) {
	var opts []api.ClientOption
	if verbose := os.Getenv("DEBUG"); verbose != "" {
		opts = append(opts, apiVerboseLog())
	}
	opts = append(opts, api.AddHeader("User-Agent", fmt.Sprintf("GitHub CLI %s", Version)))

	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		if c, err := config.ParseDefaultConfig(); err == nil {
			token, _ = c.Get(defaultHostname, "oauth_token")
		}
	}
	if token != "" {
		opts = append(opts, api.AddHeader("Authorization", fmt.Sprintf("token %s", token)))
	}
	return api.NewClient(opts...), nil
}

func contextForCommand(cmd *cobra.Command) context.Context {
	ctx := initContext()
	if repo, err := cmd.Flags().GetString("repo"); err == nil && repo != "" {
		ctx.SetBaseRepo(repo)
	}
	return ctx
}

// for cmdutil-powered commands
func httpClient(token string) *http.Client {
	var opts []api.ClientOption
	if verbose := os.Getenv("DEBUG"); verbose != "" {
		opts = append(opts, apiVerboseLog())
	}
	opts = append(opts,
		api.AddHeader("Authorization", fmt.Sprintf("token %s", token)),
		api.AddHeader("User-Agent", fmt.Sprintf("GitHub CLI %s", Version)),
	)
	return api.NewHTTPClient(opts...)
}

// overridden in tests
var apiClientForContext = func(ctx context.Context) (*api.Client, error) {
	token, err := ctx.AuthToken()
	if err != nil {
		return nil, err
	}

	var opts []api.ClientOption
	if verbose := os.Getenv("DEBUG"); verbose != "" {
		opts = append(opts, apiVerboseLog())
	}

	getAuthValue := func() string {
		return fmt.Sprintf("token %s", token)
	}

	tokenFromEnv := func() bool {
		return os.Getenv("GITHUB_TOKEN") == token
	}

	checkScopesFunc := func(appID string) error {
		if config.IsGitHubApp(appID) && !tokenFromEnv() && utils.IsTerminal(os.Stdin) && utils.IsTerminal(os.Stderr) {
			cfg, err := ctx.Config()
			if err != nil {
				return err
			}
			newToken, err := config.AuthFlowWithConfig(cfg, defaultHostname, "Notice: additional authorization required")
			if err != nil {
				return err
			}
			// update configuration in memory
			token = newToken
		} else {
			fmt.Fprintln(os.Stderr, "Warning: gh now requires the `read:org` OAuth scope.")
			fmt.Fprintln(os.Stderr, "Visit https://github.com/settings/tokens and edit your token to enable `read:org`")
			if tokenFromEnv() {
				fmt.Fprintln(os.Stderr, "or generate a new token for the GITHUB_TOKEN environment variable")
			} else {
				fmt.Fprintln(os.Stderr, "or generate a new token and paste it via `gh config set -h github.com oauth_token MYTOKEN`")
			}
		}
		return nil
	}

	opts = append(opts,
		api.CheckScopes("read:org", checkScopesFunc),
		api.AddHeaderFunc("Authorization", getAuthValue),
		api.AddHeader("User-Agent", fmt.Sprintf("GitHub CLI %s", Version)),
		// antiope-preview: Checks
		api.AddHeader("Accept", "application/vnd.github.antiope-preview+json"),
	)

	return api.NewClient(opts...), nil
}

var ensureScopes = func(ctx context.Context, client *api.Client, wantedScopes ...string) (*api.Client, error) {
	hasScopes, appID, err := client.HasScopes(wantedScopes...)
	if err != nil {
		return client, err
	}

	if hasScopes {
		return client, nil
	}

	tokenFromEnv := len(os.Getenv("GITHUB_TOKEN")) > 0

	if config.IsGitHubApp(appID) && !tokenFromEnv && utils.IsTerminal(os.Stdin) && utils.IsTerminal(os.Stderr) {
		cfg, err := ctx.Config()
		if err != nil {
			return nil, err
		}
		_, err = config.AuthFlowWithConfig(cfg, defaultHostname, "Notice: additional authorization required")
		if err != nil {
			return nil, err
		}

		reloadedClient, err := apiClientForContext(ctx)
		if err != nil {
			return client, err
		}
		return reloadedClient, nil
	} else {
		fmt.Fprintf(os.Stderr, "Warning: gh now requires %s OAuth scopes.\n", wantedScopes)
		fmt.Fprintf(os.Stderr, "Visit https://github.com/settings/tokens and edit your token to enable %s\n", wantedScopes)
		if tokenFromEnv {
			fmt.Fprintln(os.Stderr, "or generate a new token for the GITHUB_TOKEN environment variable")
		} else {
			fmt.Fprintln(os.Stderr, "or generate a new token and paste it via `gh config set -h github.com oauth_token MYTOKEN`")
		}
		return client, errors.New("Unable to reauthenticate")
	}

}

func apiVerboseLog() api.ClientOption {
	logTraffic := strings.Contains(os.Getenv("DEBUG"), "api")
	colorize := utils.IsTerminal(os.Stderr)
	return api.VerboseLog(utils.NewColorable(os.Stderr), logTraffic, colorize)
}

func colorableOut(cmd *cobra.Command) io.Writer {
	out := cmd.OutOrStdout()
	if outFile, isFile := out.(*os.File); isFile {
		return utils.NewColorable(outFile)
	}
	return out
}

func colorableErr(cmd *cobra.Command) io.Writer {
	err := cmd.ErrOrStderr()
	if outFile, isFile := err.(*os.File); isFile {
		return utils.NewColorable(outFile)
	}
	return err
}

func changelogURL(version string) string {
	path := "https://github.com/cli/cli"
	r := regexp.MustCompile(`^v?\d+\.\d+\.\d+(-[\w.]+)?$`)
	if !r.MatchString(version) {
		return fmt.Sprintf("%s/releases/latest", path)
	}

	url := fmt.Sprintf("%s/releases/tag/v%s", path, strings.TrimPrefix(version, "v"))
	return url
}

func determineBaseRepo(apiClient *api.Client, cmd *cobra.Command, ctx context.Context) (ghrepo.Interface, error) {
	repo, _ := cmd.Flags().GetString("repo")
	if repo != "" {
		baseRepo, err := ghrepo.FromFullName(repo)
		if err != nil {
			return nil, fmt.Errorf("argument error: %w", err)
		}
		return baseRepo, nil
	}

	remotes, err := ctx.Remotes()
	if err != nil {
		return nil, err
	}

	repoContext, err := context.ResolveRemotesToRepos(remotes, apiClient, "")
	if err != nil {
		return nil, err
	}

	baseRepo, err := repoContext.BaseRepo()
	if err != nil {
		return nil, err
	}

	return baseRepo, nil
}

func formatRemoteURL(cmd *cobra.Command, repo ghrepo.Interface) string {
	ctx := contextForCommand(cmd)

	var protocol string
	cfg, err := ctx.Config()
	if err != nil {
		fmt.Fprintf(colorableErr(cmd), "%s failed to load config: %s. using defaults\n", utils.Yellow("!"), err)
	} else {
		protocol, _ = cfg.Get(repo.RepoHost(), "git_protocol")
	}

	if protocol == "ssh" {
		return fmt.Sprintf("git@%s:%s/%s.git", repo.RepoHost(), repo.RepoOwner(), repo.RepoName())
	}

	return fmt.Sprintf("https://%s/%s/%s.git", repo.RepoHost(), repo.RepoOwner(), repo.RepoName())
}

func determineEditor(cmd *cobra.Command) (string, error) {
	editorCommand := os.Getenv("GH_EDITOR")
	if editorCommand == "" {
		ctx := contextForCommand(cmd)
		cfg, err := ctx.Config()
		if err != nil {
			return "", fmt.Errorf("could not read config: %w", err)
		}
		editorCommand, _ = cfg.Get(defaultHostname, "editor")
	}

	return editorCommand, nil
}

func ExecuteShellAlias(args []string) error {
	externalCmd := exec.Command(args[0], args[1:]...)
	externalCmd.Stderr = os.Stderr
	externalCmd.Stdout = os.Stdout
	externalCmd.Stdin = os.Stdin
	preparedCmd := run.PrepareCmd(externalCmd)

	return preparedCmd.Run()
}

var findSh = func() (string, error) {
	shPath, err := exec.LookPath("sh")
	if err == nil {
		return shPath, nil
	}

	if runtime.GOOS == "windows" {
		winNotFoundErr := errors.New("unable to locate sh to execute the shell alias with. The sh.exe interpreter is typically distributed with Git for Windows.")
		// We can try and find a sh executable in a Git for Windows install
		gitPath, err := exec.LookPath("git")
		if err != nil {
			return "", winNotFoundErr
		}

		shPath = filepath.Join(filepath.Dir(gitPath), "..", "bin", "sh.exe")
		_, err = os.Stat(shPath)
		if err != nil {
			return "", winNotFoundErr
		}

		return shPath, nil
	}

	return "", errors.New("unable to locate sh to execute shell alias with")
}

// ExpandAlias processes argv to see if it should be rewritten according to a user's aliases. The
// second return value indicates whether the alias should be executed in a new shell process instead
// of running gh itself.
func ExpandAlias(args []string) (expanded []string, isShell bool, err error) {
	err = nil
	isShell = false
	expanded = []string{}

	if len(args) < 2 {
		// the command is lacking a subcommand
		return
	}

	ctx := initContext()
	cfg, err := ctx.Config()
	if err != nil {
		return
	}
	aliases, err := cfg.Aliases()
	if err != nil {
		return
	}

	expansion, ok := aliases.Get(args[1])
	if ok {
		if strings.HasPrefix(expansion, "!") {
			isShell = true
			shPath, shErr := findSh()
			if shErr != nil {
				err = shErr
				return
			}

			expanded = []string{shPath, "-c", expansion[1:]}

			if len(args[2:]) > 0 {
				expanded = append(expanded, "--")
				expanded = append(expanded, args[2:]...)
			}

			return
		}

		extraArgs := []string{}
		for i, a := range args[2:] {
			if !strings.Contains(expansion, "$") {
				extraArgs = append(extraArgs, a)
			} else {
				expansion = strings.ReplaceAll(expansion, fmt.Sprintf("$%d", i+1), a)
			}
		}
		lingeringRE := regexp.MustCompile(`\$\d`)
		if lingeringRE.MatchString(expansion) {
			err = fmt.Errorf("not enough arguments for alias: %s", expansion)
			return
		}

		var newArgs []string
		newArgs, err = shlex.Split(expansion)
		if err != nil {
			return
		}

		expanded = append(newArgs, extraArgs...)
		return
	}

	expanded = args[1:]
	return
}

func connectedToTerminal(cmd *cobra.Command) bool {
	return utils.IsTerminal(cmd.InOrStdin()) && utils.IsTerminal(cmd.OutOrStdout())
}
