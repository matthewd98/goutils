package main

// Use 'go run <filepath>' to execute this file

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	jira "github.com/andygrunwald/go-jira"
	"github.com/slack-go/slack"
	"github.com/xanzy/go-gitlab"
)

const (
	slackChannelID             = "C##########"
	gitLabBaseURL              = "https://gitlab.YOURDOMAIN.com/api/v4"
	gitLabSupplyProjectID      = 2520
	maxMergeRequestsToRetrieve = 100
	jiraBaseURL                = "https://jira.YOURDOMAIN.com"
)

// Assumes that MR title has the following format "Title [ISSUE-1234]"
// The first segment is the commit title and the last segment is The JIRA issue enclosed in bracket quotes.
var mrTitleRegex *regexp.Regexp = regexp.MustCompile(`(.*)\s*\[([A-Z0-9]+-[0-9]+)\]`)

func main() {
	gitLabToken, slackToken, jiraToken, isDryRun, err := parseArgs()
	if err != nil {
		outputErrorAndExit(err)
	}

	gitLabClient, err := gitlab.NewClient(gitLabToken, gitlab.WithBaseURL(gitLabBaseURL))
	if err != nil {
		outputErrorAndExit(err)
	}

	tp := jira.BearerAuthTransport{
		Token: jiraToken,
	}
	jiraClient, err := jira.NewClient(tp.Client(), jiraBaseURL)
	if err != nil {
		outputErrorAndExit(err)
	}

	slackClient := slack.New(slackToken)

	mrs, err := getMergeRequests(gitLabClient)
	if err != nil {
		outputErrorAndExit(err)
	}

	staleMRs := getStaleMergeRequests(mrs)

	expiredMRs, err := closeExpiredMergeRequests(mrs, gitLabClient, jiraClient, isDryRun)
	if err != nil {
		outputErrorAndExit(err)
	}

	if len(staleMRs) == 0 && len(expiredMRs) == 0 {
		fmt.Println("no stale or expired merge requests found. Exiting.")
		return
	}

	slackUserIDByGitLabUserID := getSlackUserIDsFromGitLabUserIDs(slackClient, staleMRs, expiredMRs)

	inviteUsersToSlackChannel(slackClient, slackUserIDByGitLabUserID, isDryRun)

	if err := buildAndPostSlackMessage(slackClient, staleMRs, expiredMRs, slackUserIDByGitLabUserID, isDryRun); err != nil {
		outputErrorAndExit(err)
	}
}

func parseArgs() (string, string, string, bool, error) {
	args := os.Args
	if len(args) < 4 {
		return "", "", "", false, fmt.Errorf("missing args. Usage: [gitlabtoken] [slacktoken] [jiratoken] [dryrun]")
	}

	gitLabToken := args[1]
	slackToken := args[2]
	jiraToken := args[3]

	isDryRun := false
	if len(args) == 5 {
		isDryRun, _ = strconv.ParseBool(args[4])
	}
	if isDryRun {
		fmt.Println("dryrun option enabled, GitLab MRs will not be updated and Slack messages will not be posted.")
	}

	return gitLabToken, slackToken, jiraToken, isDryRun, nil
}

// Get merge requests that haven't been updated for over 2 months
func getMergeRequests(client *gitlab.Client) ([]*gitlab.MergeRequest, error) {
	options := &gitlab.ListProjectMergeRequestsOptions{
		State:       gitlab.String("opened"),
		ListOptions: gitlab.ListOptions{PerPage: maxMergeRequestsToRetrieve},
	}

	mergeRequests, resp, err := client.MergeRequests.ListProjectMergeRequests(gitLabSupplyProjectID, options)
	if err != nil {
		return nil, fmt.Errorf("gitlab - http client error: %w", err)
	}
	if resp.StatusCode > 400 {
		return nil, fmt.Errorf("gitlab - invalid request. Status code: %s. Body: %s", resp.Status, extractResponseBody(resp.Response))
	}

	fmt.Printf("MRs found: %d\n", len(mergeRequests))
	fmt.Printf("internal IDs of MRs: %s\n", extractMergeRequestIIDs(mergeRequests))

	return mergeRequests, nil
}

// Get merge requests that haven't been updated for over 2 months
func getStaleMergeRequests(mrs []*gitlab.MergeRequest) []*gitlab.MergeRequest {
	staleMRs := []*gitlab.MergeRequest{}
	twoMonthsAgo := time.Now().AddDate(0, -2, 0)
	for _, mr := range mrs {
		if mr.UpdatedAt.Before(twoMonthsAgo) {
			staleMRs = append(staleMRs, mr)
		}
	}

	fmt.Printf("internal IDs of stale MRs: %s\n", extractMergeRequestIIDs(staleMRs))

	return staleMRs
}

// Close merge requests that haven't been updated for over 3 months or whose associated JIRA issue is closed
func closeExpiredMergeRequests(mrs []*gitlab.MergeRequest, gitlabClient *gitlab.Client, jiraClient *jira.Client, isDryRun bool) ([]*gitlab.MergeRequest, error) {
	expiredMRs := []*gitlab.MergeRequest{}
	threeMonthsAgo := time.Now().AddDate(0, -3, 0)
	for _, mr := range mrs {
		if mr.UpdatedAt.Before(threeMonthsAgo) {
			expiredMRs = append(expiredMRs, mr)
			continue
		}

		jiraIssueID := getJiraIssueID(mr.Title)
		if jiraIssueID == "" {
			fmt.Printf("no JIRA issue ID attached to MR: %d. Skipping status check.\n", mr.IID)
			continue
		}

		issue, resp, err := jiraClient.Issue.Get(jiraIssueID, nil)
		if err != nil {
			if resp != nil && resp.StatusCode == 404 {
				fmt.Printf("JIRA issue ID attached to MR does not exist: %d. Skipping status check.\n", mr.IID)
				continue
			}
			if resp != nil && resp.StatusCode > 400 {
				return nil, fmt.Errorf("jira - invalid request. Status code: %s. Body: %s", resp.Status, extractResponseBody(resp.Response))
			}
			return nil, fmt.Errorf("jira - http client error: %w", err)
		}
		if issue.Fields.Status.Name == "Closed" {
			expiredMRs = append(expiredMRs, mr)
			continue
		}
	}

	fmt.Printf("internal IDs of expired MRs: %s\n", extractMergeRequestIIDs(expiredMRs))

	if isDryRun {
		return expiredMRs, nil
	}

	for _, mr := range expiredMRs {
		updateMergeRequestOptions := &gitlab.UpdateMergeRequestOptions{
			StateEvent: gitlab.String("close"),
		}
		_, resp, err := gitlabClient.MergeRequests.UpdateMergeRequest(gitLabSupplyProjectID, mr.IID, updateMergeRequestOptions)
		if err != nil {
			return nil, fmt.Errorf("gitlab - http client error: %w", err)
		}
		if resp.StatusCode > 400 {
			return nil, fmt.Errorf("gitlab - invalid request. Status code: %s. Body: %s", resp.Status, extractResponseBody(resp.Response))
		}
	}

	return expiredMRs, nil
}

func extractResponseBody(resp *http.Response) string {
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return ""
	}
	return string(body)
}

func extractMergeRequestIIDs(mrs []*gitlab.MergeRequest) string {
	iids := []string{}
	for _, mr := range mrs {
		iidString := strconv.FormatInt(int64(mr.IID), 10)
		iids = append(iids, iidString)
	}

	return strings.Join(iids, ",")
}

func getSlackUserIDsFromGitLabUserIDs(client *slack.Client, staleMRs, expiredMRs []*gitlab.MergeRequest) map[string]string {
	slackUserIDByGitLabUserID := map[string]string{}
	for _, mr := range staleMRs {
		// ASSUMPTION: GitLab usernames are in the format of "firstname.lastname" and they match email usernames "firstname.lastname@YOURDOMAIN.com"
		email := mr.Author.Username + "@YOURDOMAIN.com"
		slackUser, err := client.GetUserByEmail(email)
		if err != nil {
			fmt.Printf("slack - error looking up user id associated to email %s: %v\n", email, err)
			fmt.Printf("continuing...\n")
			continue
		}
		slackUserIDByGitLabUserID[mr.Author.Username] = slackUser.ID
	}
	for _, mr := range expiredMRs {
		// ASSUMPTION: GitLab usernames are in the format of "firstname.lastname" and they match email usernames "firstname.lastname@YOURDOMAIN.com"
		email := mr.Author.Username + "@YOURDOMAIN.com"
		slackUser, err := client.GetUserByEmail(email)
		if err != nil {
			fmt.Printf("slack - error looking up user id associated to email %s: %v\n", email, err)
			fmt.Printf("continuing...\n")
			continue
		}
		slackUserIDByGitLabUserID[mr.Author.Username] = slackUser.ID
	}

	return slackUserIDByGitLabUserID
}

func inviteUsersToSlackChannel(client *slack.Client, slackUserIDByGitLabUserID map[string]string, isDryRun bool) {
	slackUserIDs := []string{}
	for _, slackUserID := range slackUserIDByGitLabUserID {
		slackUserIDs = append(slackUserIDs, slackUserID)
	}

	if isDryRun {
		return
	}

	// Returns 200 status even if some users are already in the channel.
	// If all invited users are already in the channel, a 400 status will be returned.
	_, err := client.InviteUsersToConversation(slackChannelID, slackUserIDs...)
	if err != nil {
		fmt.Printf("slack - error inviting user IDs <%s> to channel <%s>: %v\n", strings.Join(slackUserIDs, ","), slackChannelID, err)
		fmt.Printf("continuing...\n")
	}
}

func buildAndPostSlackMessage(client *slack.Client, staleMRs, expiredMRs []*gitlab.MergeRequest, slackUserIDByGitLabUserID map[string]string, isDryRun bool) error {
	blocks := []slack.Block{}
	if len(staleMRs) > 0 {
		blocks = append(blocks, slack.NewSectionBlock(slack.NewTextBlockObject(slack.MarkdownType, "*Stale MRs (more than 2 months old without any updates):*\nThese MRs will be automatically closed in 1 month if they aren't updated", false, false), nil, nil))
		text := ""
		for _, mr := range staleMRs {
			title, jiraURL := parseMRTitle(mr.Title)
			slackUserID, ok := slackUserIDByGitLabUserID[mr.Author.Username]
			if !ok {
				slackUserID = "Unknown"
			}
			text += fmt.Sprintf(":alarm_clock: <%s|!%d %s> %s - <@%s>\n", mr.WebURL, mr.IID, title, jiraURL, slackUserID)
		}
		blocks = append(blocks, slack.NewSectionBlock(slack.NewTextBlockObject(slack.MarkdownType, text, false, false), nil, nil))
	}
	if len(expiredMRs) > 0 {
		blocks = append(blocks, slack.NewSectionBlock(slack.NewTextBlockObject(slack.MarkdownType, "*MRs that have been closed (due to staleness or the associated JIRA issue being closed):*", false, false), nil, nil))
		text := ""
		for _, mr := range expiredMRs {
			title, jiraURL := parseMRTitle(mr.Title)
			slackUserID, ok := slackUserIDByGitLabUserID[mr.Author.Username]
			if !ok {
				slackUserID = "Unknown"
			}
			text += fmt.Sprintf(":x: <%s|!%d %s> %s - <@%s>\n", mr.WebURL, mr.IID, title, jiraURL, slackUserID)
		}
		blocks = append(blocks, slack.NewSectionBlock(slack.NewTextBlockObject(slack.MarkdownType, text, false, false), nil, nil))
	}

	if isDryRun {
		return nil
	}

	_, _, err := client.PostMessage(slackChannelID, slack.MsgOptionBlocks(blocks...))
	if err != nil {
		return fmt.Errorf("slack - error posting message to channel <%v>: %w", slackChannelID, err)
	}

	return nil
}

// Extracts the title and the JIRA issue
func parseMRTitle(title string) (string, string) {
	matches := mrTitleRegex.FindStringSubmatch(title)
	if len(matches) != 3 {
		return title, ""
	}

	return matches[1], fmt.Sprintf("[<https://jira.YOURDOMAIN.com/browse/%s|%s>]", matches[2], matches[2])
}

// Assumes that MR title has the following format "Title [ISSUE-1234]"
func getJiraIssueID(title string) string {
	matches := mrTitleRegex.FindStringSubmatch(title)
	if len(matches) == 3 {
		return matches[2]
	}
	return ""
}

func outputErrorAndExit(err error) {
	fmt.Println(err)
	os.Exit(1)
}
