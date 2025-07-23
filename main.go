package main

import (
	"errors"
	"fmt"
	"log"
	"log/slog"
	"os"
	"slices"
	"strings"

	_ "embed"

	"github.com/canonical/jira-summarizer/internal/jira"
	"github.com/canonical/jira-summarizer/internal/sinceflag"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func newViperConfig(name string) (*viper.Viper, error) {
	vip := viper.New()
	vip.SetEnvPrefix(strings.ReplaceAll(name, "-", "_"))
	vip.SetEnvKeyReplacer(strings.NewReplacer(".", "_")) // So Jira.Username → PULSE_SUMMARIZER_JIRA_USERNAME
	vip.AutomaticEnv()
	vip.SetConfigName(name)
	vip.AddConfigPath(".")

	if err := vip.ReadInConfig(); err != nil {
		var e viper.ConfigFileNotFoundError
		if errors.As(err, &e) {
			slog.Info("No configuration file. We will only use the defaults, env variables or flags.")
			return vip, nil
		}
		return nil, fmt.Errorf("invalid configuration file: %v", err)
	}

	return vip, nil
}

//go:embed jira-summarizer.example.yaml
var configExample string

var validGroupOptions = []string{"top", "merge", "children"}

func main() {
	// Remove date and time from log output to keep it clean.
	log.SetFlags(log.Flags() &^ (log.Ldate | log.Ltime))

	name := "jira-summarizer"

	vip, err := newViperConfig(name)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading configuration: %v\n", err)
		os.Exit(2)
	}

	rootCmd := cobra.Command{
		Use:           fmt.Sprintf("%s [JIRA_TICKET…]", name),
		Short:         fmt.Sprintf("%s posts update frequently", name),
		Long:          "Summarize the high level tickets based on recent activity on its children. If no Jira ticket is provided, all active assigned epics are considered.",
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRoot(vip, args)
		},
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			cmd.SilenceUsage = true

			if vip.Get("jira.username") == "" || vip.Get("jira.api_token") == nil {
				return fmt.Errorf(`missing configuration. Please set:
  * PULSE_SUMMARIZER_JIRA_USERNAME (your email)")
  * PULSE_SUMMARIZER_IRA_API_TOKEN (API token from your Atlassian account)")
You can also store them permanently in a configuration file named %s.yaml with:

%v`, name, configExample)
			}

			// Ensure group is one of the valid options.
			if !slices.Contains(validGroupOptions, vip.GetString("group")) {
				return fmt.Errorf("invalid group value: %q. Valid options are: %s", vip.GetString("group"), strings.Join(validGroupOptions, ", "))
			}

			// Fallback to summary only when using the merge strategy.
			if vip.GetString("group") == "merge" {
				if !vip.GetBool("no-post") {
					slog.Info("merge grouping strategy in a virtual top ticket can’t be posted on Jira. Only doing a summary.")
					vip.Set("no-post", true)
				}
			}

			return nil
		},
	}

	rootCmd.Flags().String("jira-username", "", "jira username to use to connect to")
	err = vip.BindPFlag("jira.username", rootCmd.Flags().Lookup("jira-username"))
	if err != nil {
		log.Fatalf("program error: unable to bind flag jira-username: %v", err)
	}

	rootCmd.Flags().Bool("no-post", false, "do not offer posting the summary to the grouping jira tickets")
	err = vip.BindPFlag("no-post", rootCmd.Flags().Lookup("no-post"))
	if err != nil {
		log.Fatalf("program error: unable to bind flag no-post: %v", err)
	}

	var since sinceflag.SinceValue
	if err := since.Set("2w"); err != nil {
		log.Fatalf("program error: invalid default value for --since: %v", err)
	}
	rootCmd.Flags().VarP(&since, "since", "s", "Start time or relative duration (e.g. '2004-10-20', '6mo', '1w', '5d')")
	if err = vip.BindPFlag("since", rootCmd.Flags().Lookup("since")); err != nil {
		log.Fatalf("program error: unable to bind flag 'since': %v", err)
	}

	var group string
	rootCmd.Flags().StringVarP(&group, "group", "g", "top", fmt.Sprintf("Grouping behavior: %s", strings.Join(validGroupOptions, ", ")))
	if err = rootCmd.RegisterFlagCompletionFunc("group", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return []string{"top", "merge", "children"}, cobra.ShellCompDirectiveNoFileComp
	}); err != nil {
		log.Fatalf("program error: register shell completion failed: %v", err)
	}
	if err = vip.BindPFlag("group", rootCmd.Flags().Lookup("group")); err != nil {
		log.Fatalf("program error: unable to bind flag 'group': %v", err)
	}

	if err := rootCmd.Execute(); err != nil {
		slog.Error(err.Error())
		os.Exit(1)
	}
}

// run executes the main logic of the command.
func runRoot(vip *viper.Viper, args []string) error {
	jiraClient, err := jira.NewClient("https://warthogs.atlassian.net", vip.GetString("jira.username"), vip.GetString("jira.api_token"))
	if err != nil {
		return fmt.Errorf("invalid jira Client: %v", err)
	}

	sinceTime, err := sinceflag.ParseSince(vip.GetString("since"))
	if err != nil {
		return fmt.Errorf("invalid --since value: %w", err)
	}

	for issue, err := range getTopIssues(jiraClient, vip.GetString("group"), args...) {
		if err != nil {
			return err
		}

		if issue.Embedder() {
			// Don't show comments on top issues which are embedder, as they can be generated from children work.
			issue.Comments = nil
		}

		if !issue.KeptRecentEvents(sinceTime) {
			continue
		}

		summary := report(issue)

		switch {
		case vip.GetBool("no-post"):
			printTopSummary(summary)
		default:
			if err := editSummaryAndPost(jiraClient, issue, summary); err != nil {
				return fmt.Errorf("error posting new summary: %v", err)
			}

		}

	}

	return nil
}
