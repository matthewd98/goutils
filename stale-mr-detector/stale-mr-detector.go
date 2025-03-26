package main

// Use 'go run <filepath>' to execute this file

import (
	"errors"
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
	gitLabBaseURL = "https://gitlab.YOURDOMAIN.com/api/v4"
	maxPerPage    = 100
	jiraBaseURL   = "https://jira.YOURDOMAIN.com"
)

var mrTitleRegex = regexp.MustCompile(`(.*)\s*\[([A-Z0-9]+-[0-9]+)\]`)

func main() {
	gitLabToken, gitLabProjectID, slackToken, slackChannelID, jiraToken, isDryRun, err := parseArgs()
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

	// branches

	branches, err := getBranches(gitLabClient, gitLabProjectID)
	if err != nil {
		outputErrorAndExit(err)
	}

	staleBranches := filterStaleNonProtectedBranches(branches)

	err = deleteStaleBranches(staleBranches, gitLabClient, gitLabProjectID, isDryRun)
	if err != nil {
		outputErrorAndExit(err)
	}

	// merge requests

	mrs, err := getMergeRequests(gitLabClient, gitLabProjectID)
	if err != nil {
		outputErrorAndExit(err)
	}

	staleMRs := filterStaleMergeRequests(mrs)

	expiredMRs, err := closeExpiredMergeRequests(mrs, gitLabClient, gitLabProjectID, jiraClient, isDryRun)
	if err != nil {
		outputErrorAndExit(err)
	}

	if len(staleMRs) == 0 && len(expiredMRs) == 0 {
		fmt.Println("no stale or expired merge requests found. Exiting.")
		return
	}

	slackUserIDByGitLabUserID := getSlackUserIDsFromGitLabUserIDs(slackClient, staleMRs, expiredMRs)

	inviteUsersToSlackChannel(slackClient, slackUserIDByGitLabUserID, slackChannelID, isDryRun)

	if err := buildAndPostSlackMessage(slackClient, staleMRs, expiredMRs, slackUserIDByGitLabUserID, slackChannelID, isDryRun); err != nil {
		outputErrorAndExit(err)
	}
}

func parseArgs() (string, string, string, string, string, bool, error) {
	args := os.Args
	if len(args) < 6 {
		return "", "", "", "", "", false, errors.New("missing args. Usage: [gitlab token] [gitlab project id] [slack token] [slack channel id] [jira token] [dryrun]")
	}

	gitLabToken := args[1]
	gitLabProjectID := args[2]
	slackToken := args[3]
	slackChannelID := args[4]
	jiraToken := args[5]

	isDryRun := false
	if len(args) == 7 {
		isDryRun, _ = strconv.ParseBool(args[6])
	}
	if isDryRun {
		fmt.Println("dryrun option enabled, GitLab MRs will not be updated and Slack messages will not be posted.")
	}

	return gitLabToken, gitLabProjectID, slackToken, slackChannelID, jiraToken, isDryRun, nil
}

// Get all branches
func getBranches(client *gitlab.Client, gitLabProjectID string) ([]*gitlab.Branch, error) {
	var branches []*gitlab.Branch

	options := &gitlab.ListBranchesOptions{
		ListOptions: gitlab.ListOptions{PerPage: maxPerPage},
	}

	for {
		branchesInPage, resp, err := client.Branches.ListBranches(gitLabProjectID, options)
		if err != nil {
			return nil, fmt.Errorf("gitlab - http client error: %w", err)
		}
		if resp.StatusCode >= 400 {
			return nil, fmt.Errorf("gitlab - invalid request. Status code: %s. Body: %s", resp.Status, extractResponseBody(resp.Response))
		}

		branches = append(branches, branchesInPage...)

		if resp.NextPage == 0 {
			break
		}

		options.ListOptions.Page = resp.NextPage
	}

	fmt.Printf("Branches found: %d\n", len(branches))
	fmt.Printf("Name of branches: %s\n", extractBranchNames(branches))

	return branches, nil
}

// Filter non-protected branches that haven't been updated for over 6 months
func filterStaleNonProtectedBranches(branches []*gitlab.Branch) []*gitlab.Branch {
	staleBranches := []*gitlab.Branch{}
	sixMonthsAgo := time.Now().AddDate(0, -6, 0)
	for _, b := range branches {
		if !b.Protected && b.Commit.CommittedDate.Before(sixMonthsAgo) {
			staleBranches = append(staleBranches, b)
		}
	}

	fmt.Printf("stale branches found: %d\n", len(staleBranches))
	fmt.Printf("name of stale non-protected branches: %s\n", extractBranchNames(staleBranches))

	return staleBranches
}

func extractBranchNames(branches []*gitlab.Branch) string {
	names := []string{}
	for _, b := range branches {
		names = append(names, b.Name)
	}

	return strings.Join(names, ",")
}

func deleteStaleBranches(staleBranches []*gitlab.Branch, gitlabClient *gitlab.Client, gitLabProjectID string, isDryRun bool) error {
	if isDryRun {
		return nil
	}

	for _, b := range staleBranches {
		resp, err := gitlabClient.Branches.DeleteBranch(gitLabProjectID, b.Name)
		if err != nil {
			return fmt.Errorf("gitlab - http client error: %w", err)
		}
		if resp.StatusCode >= 400 {
			return fmt.Errorf("gitlab - invalid request. Status code: %s. Body: %s", resp.Status, extractResponseBody(resp.Response))
		}
	}

	fmt.Printf("deleted %d stale branches", len(staleBranches))

	return nil
}

// Get all merge requests
func getMergeRequests(client *gitlab.Client, gitLabProjectID string) ([]*gitlab.MergeRequest, error) {
	var mergeRequests []*gitlab.MergeRequest

	options := &gitlab.ListProjectMergeRequestsOptions{
		State:       gitlab.String("opened"),
		ListOptions: gitlab.ListOptions{PerPage: maxPerPage},
	}

	for {
		mergeRequestsInPage, resp, err := client.MergeRequests.ListProjectMergeRequests(gitLabProjectID, options)
		if err != nil {
			return nil, fmt.Errorf("gitlab - http client error: %w", err)
		}
		if resp.StatusCode >= 400 {
			return nil, fmt.Errorf("gitlab - invalid request. Status code: %s. Body: %s", resp.Status, extractResponseBody(resp.Response))
		}

		mergeRequests = append(mergeRequests, mergeRequestsInPage...)

		if resp.NextPage == 0 {
			break
		}

		options.ListOptions.Page = resp.NextPage
	}

	fmt.Printf("MRs found: %d\n", len(mergeRequests))
	fmt.Printf("internal ID of MRs: %s\n", extractMergeRequestIIDs(mergeRequests))

	return mergeRequests, nil
}

// Filter merge requests that haven't been updated for over 2 months
func filterStaleMergeRequests(mrs []*gitlab.MergeRequest) []*gitlab.MergeRequest {
	staleMRs := []*gitlab.MergeRequest{}
	twoMonthsAgo := time.Now().AddDate(0, -2, 0)
	for _, mr := range mrs {
		if mr.UpdatedAt.Before(twoMonthsAgo) {
			staleMRs = append(staleMRs, mr)
		}
	}

	fmt.Printf("stale MRs found: %d\n", len(staleMRs))
	fmt.Printf("internal IDs of stale MRs: %s\n", extractMergeRequestIIDs(staleMRs))

	return staleMRs
}

// Close merge requests that haven't been updated for over 3 months or whose associated JIRA issue is closed
func closeExpiredMergeRequests(mrs []*gitlab.MergeRequest, gitlabClient *gitlab.Client, gitLabProjectID string, jiraClient *jira.Client, isDryRun bool) ([]*gitlab.MergeRequest, error) {
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
			if resp != nil && resp.StatusCode >= 400 {
				return nil, fmt.Errorf("jira - invalid request. Status code: %s. Body: %s", resp.Status, extractResponseBody(resp.Response))
			}
			return nil, fmt.Errorf("jira - http client error: %w", err)
		}
		if issue.Fields.Status.Name == "Closed" {
			expiredMRs = append(expiredMRs, mr)
			continue
		}
	}

	fmt.Printf("expired MRs found: %d\n", len(expiredMRs))
	fmt.Printf("internal ID of expired MRs: %s\n", extractMergeRequestIIDs(expiredMRs))

	if isDryRun {
		return expiredMRs, nil
	}

	for _, mr := range expiredMRs {
		updateMergeRequestOptions := &gitlab.UpdateMergeRequestOptions{
			StateEvent: gitlab.String("close"),
		}
		_, resp, err := gitlabClient.MergeRequests.UpdateMergeRequest(gitLabProjectID, mr.IID, updateMergeRequestOptions)
		if err != nil {
			return nil, fmt.Errorf("gitlab - http client error: %w", err)
		}
		if resp.StatusCode >= 400 {
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

func inviteUsersToSlackChannel(client *slack.Client, slackUserIDByGitLabUserID map[string]string, slackChannelID string, isDryRun bool) {
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

func buildAndPostSlackMessage(client *slack.Client, staleMRs, expiredMRs []*gitlab.MergeRequest, slackUserIDByGitLabUserID map[string]string, slackChannelID string, isDryRun bool) error {
	blocks := []slack.Block{}
	if len(staleMRs) > 0 {
		//nolint:lll
		blocks = append(blocks, slack.NewSectionBlock(slack.NewTextBlockObject(slack.MarkdownType, "*Stale MRs (more than 2 months old without any updates):*\nThese MRs will be automatically closed in 1 month if they aren't updated", false, false), nil, nil))
		text := ""
		for _, mr := range staleMRs {
			title, jiraURL := parseMRTitle(mr.Title)
			slackUserID := getSlackUserID(slackUserIDByGitLabUserID, mr)
			text += fmt.Sprintf(":alarm_clock: <%s|!%d %s> %s - <@%s>\n", mr.WebURL, mr.IID, title, jiraURL, slackUserID)
		}
		blocks = append(blocks, slack.NewSectionBlock(slack.NewTextBlockObject(slack.MarkdownType, text, false, false), nil, nil))
	}
	if len(expiredMRs) > 0 {
		//nolint:lll
		blocks = append(blocks, slack.NewSectionBlock(slack.NewTextBlockObject(slack.MarkdownType, "*MRs that have been closed (due to staleness or the associated JIRA issue being closed):*", false, false), nil, nil))
		text := ""
		for _, mr := range expiredMRs {
			title, jiraURL := parseMRTitle(mr.Title)
			slackUserID := getSlackUserID(slackUserIDByGitLabUserID, mr)
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

func getSlackUserID(slackUserIDByGitLabUserID map[string]string, mr *gitlab.MergeRequest) string {
	slackUserID, ok := slackUserIDByGitLabUserID[mr.Author.Username]
	if !ok {
		slackUserID = "Unknown"
	}
	return slackUserID
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
