package main

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"log"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mattn/go-mastodon"
	"github.com/microcosm-cc/bluemonday"
)

const postWindow = 90 // days

const dryRun = true

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

	client := mastodon.NewClient(&mastodon.Config{
		Server:       mastodonServer,
		ClientID:     mastodonClientID,
		ClientSecret: mastodonClientSecret,
		AccessToken:  mastodonAccessToken,
	})
	mastodonPosts, err := getToots(ctx, client)
	if err != nil {
		log.Fatalf("getting toots: %v", err)
	}
	mastodonPostsFile, err := os.OpenFile("mastodon.json", os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		log.Fatalf("opening mastodon.json: %v", err)
	}
	defer mastodonPostsFile.Close()
	if err := json.NewEncoder(mastodonPostsFile).Encode(mastodonPosts); err != nil {
		log.Fatalf("encoding mastodon posts: %v", err)
	}

	if err := run(ctx, newsFile.Entries, mastodonPosts, client, maxPosts); err != nil {
		log.Fatalf(err.Error())
	}
}

func run(
	ctx context.Context,
	news []newsEntry,
	mastodonPosts []*mastodon.Status,
	mastodonClient mastodonClient,
	maxPosts int,
) error {
	news = transformNewsEntries(news, trimSpace)
	log.Printf("Found %d news entries total", len(news))

	news = filterNewsEntries(news, inTimeWindow)
	log.Printf("Found %d news entries younger than %d days", len(news), postWindow)

	log.Printf("Found %d existing posts younger than %d days", len(mastodonPosts), postWindow)

	newToPost := filterNewsEntries(news, notYetPosted(mastodonPosts))
	if len(newToPost) == 0 {
		log.Println("No unposted news entries found")
		return nil
	}
	log.Printf("Found %d unposted news entries", len(newToPost))

	if err := postNextNewsEntries(ctx, mastodonClient, newToPost, maxPosts); err != nil {
		log.Fatalf("posting next news entries: %v", err)
	}
	return nil
}

func getToots(ctx context.Context, client *mastodon.Client) ([]*mastodon.Status, error) {
	var allStatuses []*mastodon.Status

	acc, err := client.GetAccountCurrentUser(ctx)
	if err != nil {
		return nil, fmt.Errorf("getting current user: %w", err)
	}

	var pg mastodon.Pagination
	for {
		statuses, err := client.GetAccountStatuses(ctx, acc.ID, &pg)
		if err != nil {
			return nil, fmt.Errorf("getting account statuses: %w", err)
		}
		if len(statuses) == 0 {
			break
		}
		allStatuses = append(allStatuses, statuses...)
		pg.MaxID = statuses[len(statuses)-1].ID
	}

	return allStatuses, nil
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

type mastodonClient interface {
	PostStatus(ctx context.Context, status *mastodon.Toot) (*mastodon.Status, error)
}

func postNextNewsEntries(ctx context.Context, client mastodonClient, news []newsEntry, maxPosts int) error {
	slices.SortFunc(news, func(a, b newsEntry) int {
		return int(a.Time.UnixNano() - b.Time.UnixNano())
	})

	for i, newsEntry := range news {
		if i >= maxPosts {
			break
		}

		toots := splitIntoToots(newsEntry.Message)

		var lastStatusID mastodon.ID
		for j, toot := range toots {
			log.Printf("Posting news entry %d with message %q, part %d/%d", i, toot, j+1, len(toots))
			toot := &mastodon.Toot{
				Status:      toot,
				InReplyToID: lastStatusID,
			}
			if dryRun {
				continue
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
		log.Println("Warn: parsing time: %w", err)
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

func inTimeWindow(n newsEntry) bool {
	return n.Time.After(time.Now().AddDate(0, 0, -postWindow))
}

func notYetPosted(posts []*mastodon.Status) func(newsEntry) bool {
	return func(n newsEntry) bool {
		for _, post := range posts {
			if strings.Contains(canonicalizePost(post.Content), canonicalizePost(n.Message)) {
				return false
			}
		}
		return true
	}
}

func filterNewsEntries(news []newsEntry, filter func(newsEntry) bool) []newsEntry {
	var filtered []newsEntry
	for _, entry := range news {
		if filter(entry) {
			filtered = append(filtered, entry)
		}
	}
	return filtered
}
