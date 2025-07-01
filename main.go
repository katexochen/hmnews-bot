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
	dryRunStr := os.Getenv("HMNB_DRY_RUN")
	dryRun := false
	if dryRunStr != "" {
		if dryRun, err = strconv.ParseBool(dryRunStr); err != nil {
			log.Fatalf("parsing HMNB_DRY_RUN: %v", err)
		}
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
	blueskyHandle := os.Getenv("HMNB_BLUESKY_HANDLE")
	if blueskyHandle == "" {
		log.Fatal("HMNB_BLUESKY_HANDLE not set")
	}
	blueskyAppPassword := os.Getenv("HMNB_BLUESKY_APP_PASSWORD")
	if blueskyAppPassword == "" {
		log.Fatal("HMNB_BLUESKY_APP_PASSWORD not set")
	}

	f, err := os.ReadFile(path)
	if err != nil {
		log.Fatalf("reading file at %q: %v", path, err)
	}
	var newsFile newsFile
	if err := json.Unmarshal(f, &newsFile); err != nil {
		log.Fatalf("unmarshaling news file: %v", err)
	}

	mastodonC := newMastodonClient(&mastodon.Config{
		Server:       mastodonServer,
		ClientID:     mastodonClientID,
		ClientSecret: mastodonClientSecret,
		AccessToken:  mastodonAccessToken,
	}, mastodonClientConfig{
		dryRun:   dryRun,
		maxPosts: maxPosts,
		newsFilter: map[string]func(newsEntry) bool{
			"not older than 90d": inTimeWindow,
		},
	})

	bluesskyC, err := newBlueskyClient(ctx, blueskyClientConfig{
		handle:   blueskyHandle,
		appkey:   blueskyAppPassword,
		dryRun:   dryRun,
		maxPosts: maxPosts,
		newsFilter: map[string]func(newsEntry) bool{
			"not older than 90d": inTimeWindow,
		},
	})
	if err != nil {
		log.Fatalf("creating Bluesky client: %v", err)
	}

	if err := run(ctx, newsFile.Entries, []postingClient{mastodonC, bluesskyC}); err != nil {
		log.Fatal(err.Error())
	}
}

type post interface {
	Text() string
}

type postingClient interface {
	NewsFilter() map[string]func(newsEntry) bool
	ListPosts(ctx context.Context) ([]post, error)
	CreatePostChain(ctx context.Context, postChain []string) error
	PlatformName() string
	MaxPosts() int
	MaxPostLen() int
}

func run(
	ctx context.Context,
	news []newsEntry,
	clients []postingClient,
) error {
	news = transformNewsEntries(news, trimSpace)
	log.Printf("Found %d news entries total", len(news))

	for _, c := range clients {
		log.Printf("Running %s client", c.PlatformName())

		newsForClient := copySlice(news)
		for name, filter := range c.NewsFilter() {
			newsForClient = filterNewsEntries(newsForClient, filter)
			log.Printf("%d news entries left after filter %q", len(newsForClient), name)
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

		posts := splitIntoPosts(newsEntry.Message, client.MaxPostLen())

		log.Printf("Posting news entry %d with %d parts", i, len(posts))
		for j, post := range posts {
			log.Printf("  %d/%d: %s", j+1, len(posts), post)
		}

		if err := client.CreatePostChain(ctx, posts); err != nil {
			return fmt.Errorf("posting news entry %d: %w", i, err)
		}
	}

	return nil
}

func splitIntoPosts(message string, maxPostLen int) []string {
	if message == "" {
		return nil
	}

	if len(message)+len(hashTags) <= maxPostLen {
		return []string{message + hashTags}
	}

	var posts []string
	var post string
	for _, word := range strings.Split(message, " ") {
		var lenHashtags int
		if len(posts) == 0 {
			// First post get hash tags, so additional space needed.
			lenHashtags = len(hashTags)
		}
		if len(post)+len(word)+1 > (maxPostLen - lenHashtags - 5) { // 5 for "[n/n]", 1 for space
			posts = append(posts, post)
			post = ""
		}
		post += word + " "
	}
	posts = append(posts, post)

	for i := range posts {
		posts[i] = fmt.Sprintf("%s[%d/%d]", posts[i], i+1, len(posts))
	}
	posts[0] = posts[0] + hashTags

	return posts
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
