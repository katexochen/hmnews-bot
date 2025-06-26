package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBluesky(t *testing.T) {
	require := require.New(t)
	ctx := context.Background()

	credsFile, err := os.ReadFile(".creds")
	if errors.Is(err, os.ErrNotExist) {
		t.Skip("Credentials file .creds not found, skipping test")
	}
	require.NoError(err, "reading credentials file")
	var creds map[string]map[string]string
	require.NoError(json.Unmarshal(credsFile, &creds), "unmarshalling credentials")

	client, err := newBlueskyClient(ctx, blueskyClientConfig{
		handle:   creds["bluesky"]["handle"],
		appkey:   creds["bluesky"]["appkey"],
		dryRun:   false,
		maxPosts: 100,
	})
	require.NoError(err, "creating Bluesky client")

	err = client.CreatePostChain(ctx, []string{
		"Hello, Bluesky! This is a test post.",
		"This is the second part of the post chain.",
		"And this is the third part of the post chain.",
	})
	require.NoError(err, "creating post")

	feed, err := client.ListPosts(ctx)
	require.NoError(err, "getting author feed")

	require.NotNil(feed, "feed should not be nil")
	PrintPosts(feed)
}

func PrintPosts(posts []post) {
	for _, post := range posts {
		fmt.Printf("%s\n", post.Text())
		fmt.Println("---")
	}
}
