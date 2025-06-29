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

const (
	postWindow = 90 // days
	dryRun     = false
	hashTags   = "\n#NixOS #Nix #HomeManager"
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

	client := newMastodonClient(&mastodon.Config{
		Server:       mastodonServer,
		ClientID:     mastodonClientID,
		ClientSecret: mastodonClientSecret,
		AccessToken:  mastodonAccessToken,
	}, mastodonClientConfig{
		dryRun:   dryRun,
		maxPosts: maxPosts,
		newsFilter: []func(newsEntry) bool{
			inTimeWindow,
		},
	})

	if err := run(ctx, newsFile.Entries, []postingClient{client}); err != nil {
		log.Fatal(err.Error())
	}
}

type post interface {
	Text() string
}

type postingClient interface {
	NewsFilter() []func(newsEntry) bool
	ListPosts(ctx context.Context) ([]post, error)
	CreatePostChain(ctx context.Context, postChain []string) error
	PlatformName() string
	MaxPosts() int
}

func run(
	ctx context.Context,
	news []newsEntry,
	clients []postingClient,
) error {
	news = transformNewsEntries(news, trimSpace)
	log.Printf("Found %d news entries total", len(news))

	news = filterNewsEntries(news, inTimeWindow)
	log.Printf("Found %d news entries younger than %d days", len(news), postWindow)

	for _, c := range clients {
		log.Printf("Running %s client", c.PlatformName())

		newsForClient := copySlice(news)
		for _, filter := range c.NewsFilter() {
			newsForClient = filterNewsEntries(newsForClient, filter)
		}

		posts, err := c.ListPosts(ctx)
		if err != nil {
			return fmt.Errorf("listing posts: %w", err)
		}
		log.Printf("Found %d %s posts total", len(posts), c.PlatformName())

		postFile, err := os.OpenFile(
			fmt.Sprintf("%s.json", c.PlatformName()),
			os.O_RDWR|os.O_CREATE|os.O_TRUNC,
			0o644,
		)
		if err != nil {
			return fmt.Errorf("opening %s.json: %w", c.PlatformName(), err)
		}
		defer postFile.Close()
		if err := json.NewEncoder(postFile).Encode(posts); err != nil {
			return fmt.Errorf("encoding posts: %w", err)
		}
		log.Printf("Wrote posts file to %s.json", c.PlatformName())

		newsToPost := filterNewsEntries(newsForClient, notYetPosted(posts))
		if len(newsToPost) == 0 {
			log.Println("No unposted news entries found")
			continue
		}
		log.Printf("Found %d unposted news entries", len(newsToPost))

		if err := postNextNewsEntries(ctx, c, newsToPost); err != nil {
			return fmt.Errorf("posting next news entries: %w", err)
		}
	}
	return nil
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

func postNextNewsEntries(ctx context.Context, client postingClient, news []newsEntry) error {
	slices.SortFunc(news, func(a, b newsEntry) int {
		return int(a.Time.UnixNano() - b.Time.UnixNano())
	})

	for i, newsEntry := range news {
		if i >= client.MaxPosts() {
			break
		}

		toots := splitIntoToots(newsEntry.Message)

		log.Printf("Posting news entry %d with %d parts", i, len(toots))
		for j, toot := range toots {
			log.Printf("  %d/%d: %s", j+1, len(toots), toot)
		}

		if err := client.CreatePostChain(ctx, toots); err != nil {
			return fmt.Errorf("posting news entry %d: %w", i, err)
		}
	}

	return nil
}

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

func notYetPosted(posts []post) func(newsEntry) bool {
	return func(n newsEntry) bool {
		for _, post := range posts {
			if strings.Contains(canonicalizePost(post.Text()), canonicalizePost(n.Message)) {
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

func copySlice[T any](s []T) []T {
	if s == nil {
		return nil
	}
	copied := make([]T, len(s))
	copy(copied, s)
	return copied
}
