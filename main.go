package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"log"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/mattn/go-mastodon"
	"github.com/microcosm-cc/bluemonday"
)

func main() {
	ctx := context.Background()

	path := os.Getenv("HMNB_PATH")
	if path == "" {
		log.Fatal("HMNB_PATH not set")
	}
	maxPostsStr := os.Getenv("HMNB_MAX_POSTS")
	if maxPostsStr == "" {
		log.Fatal("HMNB_MAX_POSTS not set")
	}
	maxPosts, err := strconv.Atoi(maxPostsStr)
	if err != nil {
		log.Fatalf("parsing HMNB_MAX_POSTS: %v", err)
	}
	mastodonServer := os.Getenv("HMNB_MASTODON_SERVER")
	if mastodonServer == "" {
		log.Fatal("HMNB_MASTODON_SERVER not set")
	}
	mastodonClientID := os.Getenv("HMNB_MASTODON_CLIENT_ID")
	if mastodonClientID == "" {
		log.Fatal("HMNB_MASTODON_CLIENT_ID not set")
	}
	mastodonClientSecret := os.Getenv("HMNB_MASTODON_CLIENT_SECRET")
	if mastodonClientSecret == "" {
		log.Fatal("HMNB_MASTODON_CLIENT_SECRET not set")
	}
	mastodonAccessToken := os.Getenv("HMNB_MASTODON_ACCESS_TOKEN")
	if mastodonAccessToken == "" {
		log.Fatal("HMNB_MASTODON_ACCESS_TOKEN not set")
	}

	f, err := os.ReadFile(path)
	if err != nil {
		log.Fatalf("reading file at %q: %v", path, err)
	}

	var newsFile newsFile
	if err := json.Unmarshal(f, &newsFile); err != nil {
		log.Fatalf("unmarshalling news file: %v", err)
	}
	news := transformNewsEntries(newsFile.Entries, trimSpace)
	log.Printf("Found %d news entries total", len(news))

	client := mastodon.NewClient(&mastodon.Config{
		Server:       mastodonServer,
		ClientID:     mastodonClientID,
		ClientSecret: mastodonClientSecret,
		AccessToken:  mastodonAccessToken,
	})

	latestIdx, err := findLastTootIdx(ctx, client, news)
	if err != nil {
		log.Fatalf("finding last toot index: %v", err)
	}

	if latestIdx == len(news)-1 {
		log.Println("No new news entries to post")
		return
	}

	if err := postNextNewsEntries(ctx, client, news, latestIdx+1, maxPosts); err != nil {
		log.Fatalf("posting next news entries: %v", err)
	}
}

func findLastTootIdx(ctx context.Context, client *mastodon.Client, news []newsEntry) (int, error) {
	acc, err := client.GetAccountCurrentUser(ctx)
	if err != nil {
		return 0, fmt.Errorf("getting current user: %w", err)
	}

	statuses, err := client.GetAccountStatuses(ctx, acc.ID, nil)
	if err != nil {
		return 0, fmt.Errorf("getting account statuses: %w", err)
	}

	for _, status := range statuses {
		for i := len(news) - 1; i >= 0; i-- {
			content := canonicalizePost(status.Content)
			entry := canonicalizePost(news[i].Message)
			if strings.Contains(content, entry) {
				log.Printf("Message of latest toot found at index %d with message %q", i, content)
				return i, nil
			}
			log.Printf("Message of toot at index %d does not match, toot raw: %q, to-post raw: %q", i, status.Content, news[i].Message)
		}
	}

	return 0, errors.New("could not find last toot index")
}

func canonicalizePost(s string) string {
	p := bluemonday.StrictPolicy()
	s = html.UnescapeString(s)
	if su, err := strconv.Unquote(`"` + s + `"`); err == nil {
		s = su
	}
	s = p.Sanitize(s)
	s = html.UnescapeString(s)
	return s
}

func postNextNewsEntries(ctx context.Context, client *mastodon.Client, news []newsEntry, startIdx, maxPosts int) error {
	for i := startIdx; i < startIdx+maxPosts; i++ {
		if i >= len(news) {
			break
		}

		toots := splitIntoToots(news[i].Message)

		var lastStatusID mastodon.ID
		for j, toot := range toots {
			log.Printf("Posting news entry %d with message %q, part %d/%d", i, toot, j+1, len(toots))
			toot := &mastodon.Toot{Status: toot}
			if lastStatusID != "" {
				toot.InReplyToID = lastStatusID
			}
			status, err := client.PostStatus(ctx, toot)
			if err != nil {
				return fmt.Errorf("posting status: %w", err)
			}
			lastStatusID = status.ID
			time.Sleep(5 * time.Second)
		}
	}

	return nil
}

const hashTags = "\n#NixOS #Nix #HomeManager"

func splitIntoToots(message string) []string {
	if message == "" {
		return nil
	}

	if len(message)+len(hashTags) <= 1000 {
		return []string{message + hashTags}
	}

	var toots []string
	var toot string
	for _, word := range strings.Split(message, " ") {
		if len(toot)+len(word) > 950 {
			toots = append(toots, toot)
			toot = ""
		}
		toot += word + " "
	}
	toots = append(toots, toot)

	for i := range toots {
		toots[i] = fmt.Sprintf("%s [%d/%d]", toots[i], i+1, len(toots))
	}
	toots[len(toots)-1] = toots[len(toots)-1] + hashTags

	return toots
}

type newsEntry struct {
	Time    time.Time `json:"time"`
	Message string    `json:"message"`
}

func (n *newsEntry) UnmarshalJSON(data []byte) error {
	aux := &struct {
		Time    string `json:"time"`
		Message string `json:"message"`
	}{}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	parsedTime, err := time.Parse(time.RFC3339, aux.Time)
	if err != nil {
		log.Println("Error parsing time: %w", err)
	} else {
		n.Time = parsedTime
	}
	n.Message = aux.Message
	return nil
}

type newsFile struct {
	Entries []newsEntry `json:"entries"`
}

var spaceRegexp = regexp.MustCompile(`\s+`)

func trimSpace(n newsEntry) newsEntry {
	m := strings.TrimSpace(n.Message)
	n.Message = spaceRegexp.ReplaceAllString(m, " ")
	return n
}

func transformNewsEntries(news []newsEntry, transform func(newsEntry) newsEntry) []newsEntry {
	for i := range news {
		news[i] = transform(news[i])
	}
	return news
}
