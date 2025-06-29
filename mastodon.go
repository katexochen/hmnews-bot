package main

import (
	"context"
	"fmt"
	"time"

	"github.com/mattn/go-mastodon"
)

type mastodonClient struct {
	client *mastodon.Client
	mastodonClientConfig
}

type mastodonClientConfig struct {
	dryRun     bool
	maxPosts   int
	maxPostLen int
	newsFilter []func(newsEntry) bool
}

func newMastodonClient(mConfig *mastodon.Config, config mastodonClientConfig) *mastodonClient {
	return &mastodonClient{
		client:               mastodon.NewClient(mConfig),
		mastodonClientConfig: config,
	}
}

func (c *mastodonClient) ListPosts(ctx context.Context) ([]post, error) {
	acc, err := c.client.GetAccountCurrentUser(ctx)
	if err != nil {
		return nil, fmt.Errorf("getting current user: %w", err)
	}
	var allStatuses []*mastodon.Status
	var pg mastodon.Pagination
	for {
		statuses, err := c.client.GetAccountStatuses(ctx, acc.ID, &pg)
		if err != nil {
			return nil, fmt.Errorf("getting account statuses: %w", err)
		}
		allStatuses = append(allStatuses, statuses...)
		if pg.MaxID == "" || len(statuses) == 0 {
			break
		}
		pg.MinID = ""
	}

	var allPosts []post
	for _, status := range allStatuses {
		allPosts = append(allPosts, &mastodonPost{status})
	}

	return allPosts, nil
}

func (c *mastodonClient) CreatePostChain(ctx context.Context, postChain []string) error {
	if c.dryRun {
		return nil
	}
	var lastStatusID mastodon.ID
	for _, post := range postChain {
		status, err := c.client.PostStatus(ctx, &mastodon.Toot{
			Status:      post,
			InReplyToID: lastStatusID,
		})
		if err != nil {
			return fmt.Errorf("posting status: %w", err)
		}
		lastStatusID = status.ID
		time.Sleep(2 * time.Second)

	}
	return nil
}

func (c *mastodonClient) NewsFilter() []func(newsEntry) bool {
	return c.newsFilter
}

func (c *mastodonClient) PlatformName() string {
	return "mastodon"
}

func (c *mastodonClient) MaxPosts() int {
	return c.maxPosts
}

func (c *mastodonClient) MaxPostLen() int {
	return c.maxPostLen
}

type mastodonPost struct {
	*mastodon.Status
}

func (p *mastodonPost) Text() string {
	if p == nil {
		return ""
	}
	return p.Content
}
