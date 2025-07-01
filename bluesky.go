package main

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode"

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
	newsFilter map[string]func(newsEntry) bool
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
			Facets: append(
				hashtagFacetsFromString(post),
				linkFacetsFromString(post)...,
			),
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

		parentURI, parentCID = out.Uri, out.Cid
		if i == 0 {
			rootURI, rootCID = out.Uri, out.Cid
		}
		time.Sleep(2 * time.Second)
	}

	return nil
}

func (c *blueskyClient) NewsFilter() map[string]func(newsEntry) bool {
	return c.newsFilter
}

func (c *blueskyClient) PlatformName() string {
	return "bluesky"
}

func (c *blueskyClient) MaxPosts() int {
	return c.maxPosts
}

func (c *blueskyClient) MaxPostLen() int {
	return 300
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

func hashtagFacetsFromString(s string) []*bsky.RichtextFacet {
	newFacet := func(s string, start, end int) *bsky.RichtextFacet {
		return &bsky.RichtextFacet{
			Index: &bsky.RichtextFacet_ByteSlice{
				ByteStart: int64(start),
				ByteEnd:   int64(end),
			},
			Features: []*bsky.RichtextFacet_Features_Elem{
				{
					RichtextFacet_Tag: &bsky.RichtextFacet_Tag{
						Tag: s[start+1 : end],
					},
				},
			},
		}
	}

	var facets []*bsky.RichtextFacet
	start := -1
	for i, r := range s {
		switch {
		case r == '#':
			start = i
		case unicode.IsSpace(r):
			if start < 0 {
				continue
			}
			if i-start <= 1 { // At least one character after #
				start = -1
				continue
			}
			facets = append(facets, newFacet(s, start, i))
		case i == len(s)-1 && start != -1: // End of string
			facets = append(facets, newFacet(s, start, i+1))
		}
	}
	return facets
}

func linkFacetsFromString(s string) []*bsky.RichtextFacet {
	facets := []*bsky.RichtextFacet{}

	for _, w := range strings.Fields(s) {
		if !strings.HasPrefix(w, "https://") {
			continue
		}
		facets = append(facets, &bsky.RichtextFacet{
			Index: &bsky.RichtextFacet_ByteSlice{
				ByteStart: int64(strings.Index(s, w)),
				ByteEnd:   int64(strings.Index(s, w) + len(w)),
			},
			Features: []*bsky.RichtextFacet_Features_Elem{
				{
					RichtextFacet_Link: &bsky.RichtextFacet_Link{
						Uri: w,
					},
				},
			},
		})
	}

	return facets
}
