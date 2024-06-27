package main

import (
	"context"
	"errors"
	"fmt"
	"html"
	"log"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

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

	news, err := parseNewsFile(f)
	if err != nil {
		log.Fatalf("parsing news file: %v", err)
	}
	log.Printf("Found %d news entries", len(news))

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

	_ = maxPosts
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
			newsEntry := news[i].message
			content := html.UnescapeString(status.Content)
			if postIsNewsEntry(content, newsEntry) {
				log.Printf("Message of latest toot found at index %d with message %q", i, news[i].message)
				return i, nil
			}
		}
	}

	return 0, errors.New("could not find last toot index")
}

func postIsNewsEntry(postContent, newsEntry string) bool {
	p := bluemonday.StripTagsPolicy()
	postContent = p.Sanitize(postContent)
	postContent = html.UnescapeString(postContent)
	return strings.Contains(postContent, newsEntry)
}

func postNextNewsEntries(ctx context.Context, client *mastodon.Client, news []newsEntry, startIdx, maxPosts int) error {
	for i := startIdx; i < startIdx+maxPosts; i++ {
		if i >= len(news) {
			break
		}

		toots := splitIntoToots(news[i].message)

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
	time    string
	message string
}

var spaceRegexp = regexp.MustCompile(`\s+`)

func parseNewsFile(f []byte) ([]newsEntry, error) {
	startStr := "news.entries = ["
	idx := strings.Index(string(f), startStr)
	if idx == -1 {
		return nil, fmt.Errorf("could not find start of news entries")
	}
	f = f[idx+len(startStr):]

	var entries []newsEntry

	var stringStart int
	var firstSingleQuote bool
	var entry newsEntry
	var key, value string
	var state state

	for i, b := range string(f) {
		switch state {
		case unknown:
			switch b {
			case '{':
				state = inBracket
			case ']':
				break
			}
		case inBracket:
			switch {
			case unicode.IsSpace(b):
			case b == '}':
				log.Printf("Parsed news entry: {time: %q, message: %q}\n", entry.time, entry.message)
				entries = append(entries, entry)
				entry = newsEntry{}
				state = unknown
			default:
				stringStart = i
				state = inKey
			}
		case inKey:
			switch {
			case unicode.IsSpace(b):
				key = string(f[stringStart:i])
				state = afterKey
			}
		case afterKey:
			switch {
			case unicode.IsSpace(b):
			case b == '=':
				state = afterEqual
			default:
				return nil, fmt.Errorf("unexpected rune %s after key, expected %q", string(b), "=")
			}
		case afterEqual:
			switch {
			case unicode.IsSpace(b):
			case b == '"':
				firstSingleQuote = false
				state = inStingValue
				stringStart = i
			case b == '\'' && !firstSingleQuote:
				firstSingleQuote = true
			case b == '\'' && firstSingleQuote:
				firstSingleQuote = false
				state = inSingleQuoteStringValue
				stringStart = i
			default:
				state = inNonStringValue
				stringStart = i
			}
		case inStingValue:
			switch {
			case b == '"':
				value = string(f[stringStart+1 : i])
				state = afterStingValue
			default:
			}
		case inSingleQuoteStringValue:
			switch {
			case b == '\'' && !firstSingleQuote:
				firstSingleQuote = true
			case b == '\'' && firstSingleQuote:
				firstSingleQuote = false
				value = string(f[stringStart+1 : i-1])
				state = afterStingValue
			default:
				firstSingleQuote = false
			}
		case inNonStringValue:
			switch b {
			case ';':
				value = string(f[stringStart:i])
				if !strings.Contains(value, "with ") {
					// Some non string value ended.
					state = inBracket
				} else {
					// A with statement ended, still inside a non-string value.
					// Set the the beginning of the string to the next value,
					// so when we check value at the next ';', we don't include
					// the same 'with'.
					stringStart = i + 1
				}
			}
		case afterStingValue:
			switch {
			case unicode.IsSpace(b):
			case b == ';':
				switch key {
				case "time":
					entry.time = value
				case "message":
					m := strings.TrimSpace(value)
					m = spaceRegexp.ReplaceAllString(m, " ")
					entry.message = m
				default:
					log.Printf("Unknown key: %q\n", key)
				}
				key = ""
				value = ""
				state = inBracket
			}
		}
	}

	return entries, nil
}

type state int

const (
	unknown state = iota
	inBracket
	inKey
	afterKey
	afterEqual
	inStingValue
	inSingleQuoteStringValue
	inNonStringValue
	afterStingValue
)
