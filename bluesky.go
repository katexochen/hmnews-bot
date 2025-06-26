package main

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/bluesky-social/indigo/api/atproto"
	"github.com/bluesky-social/indigo/api/bsky"
	lexutil "github.com/bluesky-social/indigo/lex/util"
	"github.com/bluesky-social/indigo/xrpc"
)

const (
	apiEntryway = "https://bsky.social"
	apiPublic   = "https://public.api.bsky.app"
)

type blueskyClient struct {
	xrpcClient *xrpc.Client
	did        string
	blueskyClientConfig
}

type blueskyClientConfig struct {
	handle     string
	appkey     string
	dryRun     bool
	maxPosts   int
	maxPostLen int
	newsFilter []func(newsEntry) bool
}

func newBlueskyClient(ctx context.Context, conf blueskyClientConfig) (*blueskyClient, error) {
	client := &blueskyClient{
		xrpcClient: &xrpc.Client{
			Client: &http.Client{},
			Host:   string(apiEntryway),
		},
		blueskyClientConfig: conf,
	}

	did, err := client.resolveHandle(ctx, client.handle)
	if err != nil {
		return nil, fmt.Errorf("resolving handle %q: %w", client.handle, err)
	}
	client.did = did

	session, err := atproto.ServerCreateSession(ctx, client.xrpcClient, &atproto.ServerCreateSession_Input{
		Identifier: client.handle,
		Password:   client.appkey,
	})
	if err != nil {
		return nil, fmt.Errorf("creating authenticated session: %w", err)
	}
	client.xrpcClient.Auth = &xrpc.AuthInfo{AccessJwt: session.AccessJwt}

	return client, nil
}

func (c *blueskyClient) resolveHandle(ctx context.Context, handle string) (string, error) {
	handle = strings.TrimPrefix(handle, "@")
	output, err := atproto.IdentityResolveHandle(ctx, c.xrpcClient, handle)
	if err != nil {
		return "", fmt.Errorf("ResolveHandle error: %v", err)
	}
	return output.Did, nil
}

func (c *blueskyClient) ListPosts(ctx context.Context) ([]post, error) {
	feed, err := bsky.FeedGetAuthorFeed(ctx, c.xrpcClient, c.did, "", "", false, int64(20))
	if err != nil {
		return nil, fmt.Errorf("getting author feed: %w", err)
	}
	if feed == nil {
		return nil, fmt.Errorf("feed is nil")
	}
	if feed.Feed == nil {
		return nil, fmt.Errorf("feed.Feed is nil")
	}
	posts := make([]*bsky.FeedPost, 0, len(feed.Feed))
	for _, entry := range feed.Feed {
		post, ok := entry.Post.Record.Val.(*bsky.FeedPost)
		if !ok {
			return nil, fmt.Errorf("unexpected record type in feed post")
		}
		posts = append(posts, post)
	}

	var allPosts []post
	for _, post := range posts {
		allPosts = append(allPosts, &blueskyPost{post})
	}

	return allPosts, nil
}

func (c *blueskyClient) CreatePostChain(ctx context.Context, postChain []string) error {
	if c.dryRun {
		return nil
	}

	var parentURI, parentCID, rootURI, rootCID string
	for i, post := range postChain {
		post := &bsky.FeedPost{
			Text:      post,
			CreatedAt: time.Now().UTC().Format(time.RFC3339),
			Langs:     []string{"en"},
		}

		if i > 0 {
			post.Reply = &bsky.FeedPost_ReplyRef{
				Parent: &atproto.RepoStrongRef{
					Uri: parentURI,
					Cid: parentCID,
				},
				Root: &atproto.RepoStrongRef{
					Uri: rootURI,
					Cid: rootCID,
				},
			}
		}

		in := &atproto.RepoCreateRecord_Input{
			Repo:       c.did,
			Collection: "app.bsky.feed.post",
			Record:     &lexutil.LexiconTypeDecoder{Val: post},
		}
		out, err := atproto.RepoCreateRecord(ctx, c.xrpcClient, in)
		if err != nil {
			return fmt.Errorf("failed to create post %d: %w", i, err)
		}

		parentURI = out.Uri
		parentCID = out.Cid
		if i == 0 {
			rootURI = out.Uri
			rootCID = out.Cid
		}
		time.Sleep(2 * time.Second)
	}

	return nil
}

func (c *blueskyClient) NewsFilter() []func(newsEntry) bool {
	return c.newsFilter
}

func (c *blueskyClient) PlatformName() string {
	return "bluesky"
}

func (c *blueskyClient) MaxPosts() int {
	return c.maxPosts
}

func (c *blueskyClient) MaxPostLen() int {
	return c.maxPostLen
}

type blueskyPost struct {
	*bsky.FeedPost
}

func (p *blueskyPost) Text() string {
	if p == nil {
		return ""
	}
	return p.FeedPost.Text
}
