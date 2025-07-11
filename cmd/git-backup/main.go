// cmd/git-backup/main.go
package main

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"flag"
	"fmt"
	gitbackup "git-backup"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

var configFilePath = flag.String("config.file", "git-backup.yml", "The path to your config file.")
var targetPath = flag.String("backup.path", "backup", "The target path to the backup folder.")
var failAtEnd = flag.Bool("backup.fail-at-end", false, "Fail at the end of backing up repositories, rather than right away.")
var bareClone = flag.Bool("backup.bare-clone", false, "Make bare clones without checking out the main branch.")
var printVersion = flag.Bool("version", false, "Show the version number and exit.")
var enableInsecure = flag.Bool("insecure", false, "Use this flag to disable verification of SSL/TLS certificates")
var slackWebhook = flag.String("slack.webhook", "", "Slack webhook URL for notifications")

var Version = "dev"
var CommitHash = "n/a"
var BuildTimestamp = "n/a"

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

func main() {
	flag.Parse()
	log.Printf("inscure: %v", *enableInsecure)

	if *printVersion {
		log.Printf("git-backup, version %s (%s-%s)", Version, runtime.GOOS, runtime.GOARCH)
		log.Printf("Built %s (%s)", CommitHash, BuildTimestamp)
		os.Exit(0)
	}

	if *enableInsecure {
		http.DefaultTransport.(*http.Transport).TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}

	// Get Slack webhook URL from environment variable if not provided via flag
	webhookURL := *slackWebhook
	if webhookURL == "" {
		webhookURL = os.Getenv("SLACK_WEBHOOK_URL")
	}

	config := loadConfig()
	sources := config.GetSources()
	if len(sources) == 0 {
		log.Printf("Found a config file at [%s] but detected no sources. Are you sure the file is properly formed?", *configFilePath)
		os.Exit(111)
	}

	// Initialize backup tracking
	backupStart := time.Now()
	repoCount := 0
	errors := 0
	var failedRepos []string

	for _, source := range sources {
		sourceName := source.GetName()
		log.Printf("=== %s ===", sourceName)
		if err := source.Test(); err != nil {
			log.Printf("Failed to verify connection to job [%s]: %s", sourceName, err)
			// Send failure notification and exit
			if webhookURL != "" {
				result := BackupResult{
					RepoCount:   0,
					ErrorCount:  1,
					Duration:    time.Since(backupStart),
					FailedRepos: []string{fmt.Sprintf("Connection failed to %s: %s", sourceName, err)},
					StartTime:   backupStart,
					EndTime:     time.Now(),
					Success:     false,
				}
				SendSlackNotification(webhookURL, result)
			}
			os.Exit(110)
		}
		repos, err := source.ListRepositories()
		if err != nil {
			log.Printf("Communication Error: %s", err)
			// Send failure notification and exit
			if webhookURL != "" {
				result := BackupResult{
					RepoCount:   0,
					ErrorCount:  1,
					Duration:    time.Since(backupStart),
					FailedRepos: []string{fmt.Sprintf("Communication error with %s: %s", sourceName, err)},
					StartTime:   backupStart,
					EndTime:     time.Now(),
					Success:     false,
				}
				SendSlackNotification(webhookURL, result)
			}
			os.Exit(100)
		}
		for _, repo := range repos {
			log.Printf("Discovered %s", repo.FullName)
			targetPath := filepath.Join(*targetPath, sourceName, repo.FullName)
			err := os.MkdirAll(targetPath, os.ModePerm)
			if err != nil {
				log.Printf("Failed to create directory: %s", err)
				errors++
				failedRepos = append(failedRepos, fmt.Sprintf("%s (directory creation failed)", repo.FullName))
				if *failAtEnd == false {
					// Send failure notification and exit
					if webhookURL != "" {
						result := BackupResult{
							RepoCount:   repoCount,
							ErrorCount:  errors,
							Duration:    time.Since(backupStart),
							FailedRepos: failedRepos,
							StartTime:   backupStart,
							EndTime:     time.Now(),
							Success:     false,
						}
						SendSlackNotification(webhookURL, result)
					}
					os.Exit(100)
				}
				continue
			}
			err = repo.CloneInto(targetPath, *bareClone)
			if err != nil {
				errors++
				failedRepos = append(failedRepos, fmt.Sprintf("%s (%s)", repo.FullName, err.Error()))
				log.Printf("Failed to clone: %s", err)
				if *failAtEnd == false {
					// Send failure notification and exit
					if webhookURL != "" {
						result := BackupResult{
							RepoCount:   repoCount,
							ErrorCount:  errors,
							Duration:    time.Since(backupStart),
							FailedRepos: failedRepos,
							StartTime:   backupStart,
							EndTime:     time.Now(),
							Success:     false,
						}
						SendSlackNotification(webhookURL, result)
					}
					os.Exit(100)
				}
			}
			repoCount++
		}
	}

	// Calculate final results
	backupEnd := time.Now()
	duration := backupEnd.Sub(backupStart)
	success := errors == 0

	log.Printf("Backed up %d repositories in %s, encountered %d errors", repoCount, duration, errors)

	// Send Slack notification
	if webhookURL != "" {
		result := BackupResult{
			RepoCount:   repoCount,
			ErrorCount:  errors,
			Duration:    duration,
			FailedRepos: failedRepos,
			StartTime:   backupStart,
			EndTime:     backupEnd,
			Success:     success,
		}

		err := SendSlackNotification(webhookURL, result)
		if err != nil {
			log.Printf("Failed to send Slack notification: %v", err)
		} else {
			log.Printf("Slack notification sent successfully")
		}
	} else {
		log.Printf("No Slack webhook URL configured, skipping notification")
	}

	if errors > 0 {
		os.Exit(100)
	}
}

func loadConfig() gitbackup.Config {
	// try config file in working directory
	config, err := gitbackup.LoadFile(*configFilePath)
	if os.IsNotExist(err) {
		log.Println("No config file found. Exiting...")
		os.Exit(1)
	} else if err != nil {
		log.Printf("Error: %s", err)
		os.Exit(1)
	}
	return config
}
