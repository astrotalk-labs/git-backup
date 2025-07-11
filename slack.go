// slack.go
package git_backup

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// SlackMessage represents the structure of a Slack webhook message
type SlackMessage struct {
	Text        string       `json:"text"`
	Username    string       `json:"username,omitempty"`
	IconEmoji   string       `json:"icon_emoji,omitempty"`
	Channel     string       `json:"channel,omitempty"`
	Attachments []Attachment `json:"attachments,omitempty"`
}

// Attachment represents a Slack message attachment
type Attachment struct {
	Color     string  `json:"color"`
	Title     string  `json:"title"`
	Text      string  `json:"text"`
	Fields    []Field `json:"fields,omitempty"`
	Footer    string  `json:"footer"`
	Timestamp int64   `json:"ts"`
}

// Field represents a field in a Slack attachment
type Field struct {
	Title string `json:"title"`
	Value string `json:"value"`
	Short bool   `json:"short"`
}

// BackupResult holds the results of the backup operation
type BackupResult struct {
	RepoCount   int
	ErrorCount  int
	Duration    time.Duration
	FailedRepos []string
	StartTime   time.Time
	EndTime     time.Time
	Success     bool
}

// SendSlackNotification sends a notification to Slack
func SendSlackNotification(webhookURL string, result BackupResult) error {
	if webhookURL == "" {
		return fmt.Errorf("Slack webhook URL is not configured")
	}

	message := createSlackMessage(result)

	payload, err := json.Marshal(message)
	if err != nil {
		return fmt.Errorf("failed to marshal Slack message: %v", err)
	}

	resp, err := http.Post(webhookURL, "application/json", bytes.NewBuffer(payload))
	if err != nil {
		return fmt.Errorf("failed to send Slack notification: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Slack API returned status code: %d", resp.StatusCode)
	}

	return nil
}

// createSlackMessage creates a formatted Slack message based on backup results
func createSlackMessage(result BackupResult) SlackMessage {
	var color string
	var emoji string
	var title string
	var mainText string

	if result.Success {
		color = "good"
		emoji = ":white_check_mark:"
		title = "Git Backup Completed Successfully"
		mainText = fmt.Sprintf("All %d repositories backed up successfully!", result.RepoCount)
	} else {
		color = "danger"
		emoji = ":x:"
		title = "Git Backup Failed"
		mainText = fmt.Sprintf("Backup completed with %d errors out of %d repositories", result.ErrorCount, result.RepoCount)
	}

	// Create fields for the attachment
	fields := []Field{
		{
			Title: "Total Repositories",
			Value: fmt.Sprintf("%d", result.RepoCount),
			Short: true,
		},
		{
			Title: "Errors",
			Value: fmt.Sprintf("%d", result.ErrorCount),
			Short: true,
		},
		{
			Title: "Duration",
			Value: result.Duration.String(),
			Short: true,
		},
		{
			Title: "Started",
			Value: result.StartTime.Format("2006-01-02 15:04:05 MST"),
			Short: true,
		},
	}

	// Add failed repositories if any
	if result.ErrorCount > 0 && len(result.FailedRepos) > 0 {
		failedReposText := ""
		for _, repo := range result.FailedRepos {
			failedReposText += fmt.Sprintf("• %s\n", repo)
		}
		fields = append(fields, Field{
			Title: "Failed Repositories",
			Value: failedReposText,
			Short: false,
		})
	}

	attachment := Attachment{
		Color:     color,
		Title:     title,
		Text:      mainText,
		Fields:    fields,
		Footer:    "Git Backup Service",
		Timestamp: result.EndTime.Unix(),
	}

	return SlackMessage{
		Text:        fmt.Sprintf("%s %s", emoji, title),
		Username:    "Git Backup Bot",
		IconEmoji:   ":robot_face:",
		Attachments: []Attachment{attachment},
	}
}
