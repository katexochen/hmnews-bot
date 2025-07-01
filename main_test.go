package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/mattn/go-mastodon"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRun(t *testing.T) {
	assert := assert.New(t)
	require := require.New(t)

	ctx := context.Background()

	t.Cleanup(func() {
		assert.NoError(os.Remove("stub.json"))
	})

	f, err := os.ReadFile("testdata/news.json")
	require.NoError(err)
	newsFile := newsFile{}
	require.NoError(json.Unmarshal(f, &newsFile))

	f, err = os.ReadFile("testdata/mastodon.json")
	require.NoError(err)
	mastodonPosts := []*mastodon.Status{}
	require.NoError(json.Unmarshal(f, &mastodonPosts))

	client := stubPostingClientFromMastodonPosts(mastodonPosts)
	assert.NoError(run(ctx, newsFile.Entries, []postingClient{client}))
	assert.Len(client.posts, len(mastodonPosts)+2)
	assert.FileExists("stub.json", "stub.json with posts should be created")
}

type stubPostingClient struct {
	posts []post
}

func stubPostingClientFromMastodonPosts(posts []*mastodon.Status) *stubPostingClient {
	stubClient := &stubPostingClient{}
	for _, post := range posts {
		stubClient.posts = append(stubClient.posts, &mastodonPost{post})
	}
	return stubClient
}

func (c *stubPostingClient) CreatePostChain(_ context.Context, postChain []string) error {
	for _, post := range postChain {
		c.posts = append(c.posts, &mastodonPost{&mastodon.Status{Content: post}})
	}
	return nil
}

func (c *stubPostingClient) ListPosts(context.Context) ([]post, error)   { return c.posts, nil }
func (c *stubPostingClient) NewsFilter() map[string]func(newsEntry) bool { return nil }
func (c *stubPostingClient) PlatformName() string                        { return "stub" }
func (c *stubPostingClient) MaxPosts() int                               { return 2 }
func (c *stubPostingClient) MaxPostLen() int                             { return 1000 }

func TestCanonicalizePost(t *testing.T) {
	testCases := []struct {
		post string
		news string
	}{
		{
			post: `<p>Module 'i3status-rust' was updated to support the new configuration format from 0.30.x releases, that introduces many breaking changes. The documentation was updated with examples from 0.30.x to help the transition. See <a href="https://github.com/greshake/i3status-rust/blob/v0.30.0/NEWS.md" target="_blank" rel="nofollow noopener noreferrer"><span class="invisible">https://</span><span class="ellipsis">github.com/greshake/i3status-r</span><span class="invisible">ust/blob/v0.30.0/NEWS.md</span></a> for instructions on how to migrate. Users that don't want to migrate yet can set 'programs.i3status-rust.package' to an older version.<br /><a href="https://techhub.social/tags/NixOS" class="mention hashtag" rel="tag">#<span>NixOS</span></a> <a href="https://techhub.social/tags/Nix" class="mention hashtag" rel="tag">#<span>Nix</span></a> <a href="https://techhub.social/tags/HomeManager" class="mention hashtag" rel="tag">#<span>HomeManager</span></a></p>`,
			news: `Module 'i3status-rust' was updated to support the new configuration format from 0.30.x releases, that introduces many breaking changes. The documentation was updated with examples from 0.30.x to help the transition. See https://github.com/greshake/i3status-rust/blob/v0.30.0/NEWS.md for instructions on how to migrate. Users that don't want to migrate yet can set 'programs.i3status-rust.package' to an older version.`,
		},
		{
			post: `<p>isync/mbsync 1.5.0 has changed several things. isync gained support for using $XDG_CONFIG_HOME, and now places its config file in &#39;$XDG_CONFIG_HOME/isyncrc&#39;. isync changed the configuration options SSLType and SSLVersion to TLSType and TLSVersion respectively. All instances of &#39;accounts.email.accounts.&lt;account-name&gt;.mbsync.extraConfig.account&#39; that use &#39;SSLType&#39; or &#39;SSLVersion&#39; should be replaced with &#39;TLSType&#39; or &#39;TLSVersion&#39;, respectively. TLSType options are unchanged. TLSVersions has a new syntax, requiring a change to the Nix syntax. Old Syntax: SSLVersions = [ &quot;TLSv1.3&quot; &quot;TLSv1.2&quot; ]; New Syntax: TLSVersions = [ &quot;+1.3&quot; &quot;+1.2&quot; &quot;-1.1&quot; ]; NOTE: The minus symbol means to NOT use that particular TLS version.<br /><a href=\"https://techhub.social/tags/NixOS\" class=\"mention hashtag\" rel=\"tag\">#<span>NixOS</span></a> <a href=\"https://techhub.social/tags/Nix\" class=\"mention hashtag\" rel=\"tag\">#<span>Nix</span></a> <a href=\"https://techhub.social/tags/HomeManager\" class=\"mention hashtag\" rel=\"tag\">#<span>HomeManager</span></a></p>`,
			news: `isync/mbsync 1.5.0 has changed several things. isync gained support for using $XDG_CONFIG_HOME, and now places its config file in '$XDG_CONFIG_HOME/isyncrc'. isync changed the configuration options SSLType and SSLVersion to TLSType and TLSVersion respectively. All instances of 'accounts.email.accounts.<account-name>.mbsync.extraConfig.account' that use 'SSLType' or 'SSLVersion' should be replaced with 'TLSType' or 'TLSVersion', respectively. TLSType options are unchanged. TLSVersions has a new syntax, requiring a change to the Nix syntax. Old Syntax: SSLVersions = [ \"TLSv1.3\" \"TLSv1.2\" ]; New Syntax: TLSVersions = [ \"+1.3\" \"+1.2\" \"-1.1\" ]; NOTE: The minus symbol means to NOT use that particular TLS version.`,
		},
	}
	for i, tc := range testCases {
		t.Run(fmt.Sprintf("%d", i), func(t *testing.T) {
			assert.True(t, strings.Contains(canonicalizePost(tc.post), canonicalizePost(tc.news)))
		})
	}
}

func TestSplitIntoToots(t *testing.T) {
	testCases := []struct {
		message    string
		maxPostLen int
		wantToots  int
	}{
		{"a toot", 1000, 1},
		{ // 2020 characters
			`Lorem ipsum dolor sit amet, consetetur sadipscing elitr, sed diam nonumy eirmod tempor invidunt ut labore et dolore magna aliquyam erat, sed diam voluptua. At vero eos et accusam et justo duo dolores et ea rebum. Stet clita kasd gubergren, no sea takimata sanctus est Lorem ipsum dolor sit amet. Lorem ipsum dolor sit amet, consetetur sadipscing elitr, sed diam nonumy eirmod tempor invidunt ut labore et dolore magna aliquyam erat, sed diam voluptua. At vero eos et accusam et justo duo dolores et ea rebum. Stet clita kasd gubergren, no sea takimata sanctus est Lorem ipsum dolor sit amet. Lorem ipsum dolor sit amet, consetetur sadipscing elitr, sed diam nonumy eirmod tempor invidunt ut labore et dolore magna aliquyam erat, sed diam voluptua. At vero eos et accusam et justo duo dolores et ea rebum. Stet clita kasd gubergren, no sea takimata sanctus est Lorem ipsum dolor sit amet.

Duis autem vel eum iriure dolor in hendrerit in vulputate velit esse molestie consequat, vel illum dolore eu feugiat nulla facilisis at vero eros et accumsan et iusto odio dignissim qui blandit praesent luptatum zzril delenit augue duis dolore te feugait nulla fcailisi. Lorem ipsum dolor sit amet, consectetuer adipiscing elit, sed diam nonummy nibh euismod tincidunt ut laoreet dolore magna aliquam erat volutpat.

Ut wisi enim ad minim veniam, quis nostrud exerci tation ullamcorper suscipit lobortis nisl ut aliquip ex ea commodo consequat. Duis autem vel eum iriure dolor in hendrerit in vulputate velit esse molestie consequat, vel illum dolore eu feugiat nulla fcailisis at vero eros et accumsan et iusto odio dignissim qui blandit praesent luptatum zzril delenit augue duis dolore te feugait nulla fcailisi.

Nam liber tempor cum soluta nobis eleifend option congue nihil imperdiet doming id quod mazim placerat facer possim assum. Lorem ipsum dolor sit amet, consectetuer adipiscing elit, sed diam nonummy nibh euismod tincidunt ut laoreet dolore magna aliquam erat volutpat. Ut wisi enim ad minim veniam, quis nostrud`,
			1000,
			3,
		},
		{ // 2000 characters
			`Lorem ipsum dolor sit amet, consetetur sadipscing elitr, sed diam nonumy eirmod tempor invidunt ut labore et dolore magna aliquyam erat, sed diam voluptua. At vero eos et accusam et justo duo dolores et ea rebum. Stet clita kasd gubergren, no sea takimata sanctus est Lorem ipsum dolor sit amet. Lorem ipsum dolor sit amet, consetetur sadipscing elitr, sed diam nonumy eirmod tempor invidunt ut labore et dolore magna aliquyam erat, sed diam voluptua. At vero eos et accusam et justo duo dolores et ea rebum. Stet clita kasd gubergren, no sea takimata sanctus est Lorem ipsum dolor sit amet. Lorem ipsum dolor sit amet, consetetur sadipscing elitr, sed diam nonumy eirmod tempor invidunt ut labore et dolore magna aliquyam erat, sed diam voluptua. At vero eos et accusam et justo duo dolores et ea rebum. Stet clita kasd gubergren, no sea takimata sanctus est Lorem ipsum dolor sit amet.

Duis autem vel eum iriure dolor in hendrerit in vulputate velit esse molestie consequat, vel illum dolore eu feugiat nulla fcailisis at vero eros et accumsan et iusto odio dignissim qui blandit praesent luptatum zzril delenit augue duis dolore te feugait nulla fcailisi. Lorem ipsum dolor sit amet, consectetuer adipiscing elit, sed diam nonummy nibh euismod tincidunt ut laoreet dolore magna aliquam erat volutpat.

Ut wisi enim ad minim veniam, quis nostrud exerci tation ullamcorper suscipit lobortis nisl ut aliquip ex ea commodo consequat. Duis autem vel eum iriure dolor in hendrerit in vulputate velit esse molestie consequat, vel illum dolore eu feugiat nulla facilisis at vero eros et accumsan et iusto odio dignissim qui blandit praesent luptatum zzril delenit augue duis dolore te feugait nulla fcailisi.

Nam liber tempor cum soluta nobis eleifend option congue nihil imperdiet doming id quod mazim placerat facer possim assum. Lorem ipsum dolor sit amet, consectetuer adipiscing elit, sed diam nonummy nibh euismod tincidunt ut laoreet dolore magna aliquam erat volutpat. Ut wisi enim ad minim v`,
			1000,
			3,
		},
		{ // 951 characters
			`Lorem ipsum dolor sit amet, consetetur sadipscing elitr, sed diam nonumy eirmod tempor invidunt ut labore et dolore magna aliquyam erat, sed diam voluptua. At vero eos et accusam et justo duo dolores et ea rebum. Stet clita kasd gubergren, no sea takimata sanctus est Lorem ipsum dolor sit amet. Lorem ipsum dolor sit amet, consetetur sadipscing elitr, sed diam nonumy eirmod tempor invidunt ut labore et dolore magna aliquyam erat, sed diam voluptua. At vero eos et accusam et justo duo dolores et ea rebum. Stet clita kasd gubergren, no sea takimata sanctus est Lorem ipsum dolor sit amet. Lorem ipsum dolor sit amet, consetetur sadipscing elitr, sed diam nonumy eirmod tempor invidunt ut labore et dolore magna aliquyam erat, sed diam voluptua. At vero eos et accusam et justo duo dolores et ea rebum. Stet clita kasd gubergren, no sea takimata sanctus est Lorem ipsum dolor sit amet.

Duis autem vel eum iriure dolor in hendrerit in vulputate vel`,
			1000,
			1,
		},
		{ // 2020 characters
			`Lorem ipsum dolor sit amet, consetetur sadipscing elitr, sed diam nonumy eirmod tempor invidunt ut labore et dolore magna aliquyam erat, sed diam voluptua. At vero eos et accusam et justo duo dolores et ea rebum. Stet clita kasd gubergren, no sea takimata sanctus est Lorem ipsum dolor sit amet. Lorem ipsum dolor sit amet, consetetur sadipscing elitr, sed diam nonumy eirmod tempor invidunt ut labore et dolore magna aliquyam erat, sed diam voluptua. At vero eos et accusam et justo duo dolores et ea rebum. Stet clita kasd gubergren, no sea takimata sanctus est Lorem ipsum dolor sit amet. Lorem ipsum dolor sit amet, consetetur sadipscing elitr, sed diam nonumy eirmod tempor invidunt ut labore et dolore magna aliquyam erat, sed diam voluptua. At vero eos et accusam et justo duo dolores et ea rebum. Stet clita kasd gubergren, no sea takimata sanctus est Lorem ipsum dolor sit amet.

Duis autem vel eum iriure dolor in hendrerit in vulputate velit esse molestie consequat, vel illum dolore eu feugiat nulla facilisis at vero eros et accumsan et iusto odio dignissim qui blandit praesent luptatum zzril delenit augue duis dolore te feugait nulla fcailisi. Lorem ipsum dolor sit amet, consectetuer adipiscing elit, sed diam nonummy nibh euismod tincidunt ut laoreet dolore magna aliquam erat volutpat.

Ut wisi enim ad minim veniam, quis nostrud exerci tation ullamcorper suscipit lobortis nisl ut aliquip ex ea commodo consequat. Duis autem vel eum iriure dolor in hendrerit in vulputate velit esse molestie consequat, vel illum dolore eu feugiat nulla fcailisis at vero eros et accumsan et iusto odio dignissim qui blandit praesent luptatum zzril delenit augue duis dolore te feugait nulla fcailisi.

Nam liber tempor cum soluta nobis eleifend option congue nihil imperdiet doming id quod mazim placerat facer possim assum. Lorem ipsum dolor sit amet, consectetuer adipiscing elit, sed diam nonummy nibh euismod tincidunt ut laoreet dolore magna aliquam erat volutpat. Ut wisi enim ad minim veniam, quis nostrud`,
			300,
			8,
		},
		{ // 2000 characters
			`Lorem ipsum dolor sit amet, consetetur sadipscing elitr, sed diam nonumy eirmod tempor invidunt ut labore et dolore magna aliquyam erat, sed diam voluptua. At vero eos et accusam et justo duo dolores et ea rebum. Stet clita kasd gubergren, no sea takimata sanctus est Lorem ipsum dolor sit amet. Lorem ipsum dolor sit amet, consetetur sadipscing elitr, sed diam nonumy eirmod tempor invidunt ut labore et dolore magna aliquyam erat, sed diam voluptua. At vero eos et accusam et justo duo dolores et ea rebum. Stet clita kasd gubergren, no sea takimata sanctus est Lorem ipsum dolor sit amet. Lorem ipsum dolor sit amet, consetetur sadipscing elitr, sed diam nonumy eirmod tempor invidunt ut labore et dolore magna aliquyam erat, sed diam voluptua. At vero eos et accusam et justo duo dolores et ea rebum. Stet clita kasd gubergren, no sea takimata sanctus est Lorem ipsum dolor sit amet.

Duis autem vel eum iriure dolor in hendrerit in vulputate velit esse molestie consequat, vel illum dolore eu feugiat nulla fcailisis at vero eros et accumsan et iusto odio dignissim qui blandit praesent luptatum zzril delenit augue duis dolore te feugait nulla fcailisi. Lorem ipsum dolor sit amet, consectetuer adipiscing elit, sed diam nonummy nibh euismod tincidunt ut laoreet dolore magna aliquam erat volutpat.

Ut wisi enim ad minim veniam, quis nostrud exerci tation ullamcorper suscipit lobortis nisl ut aliquip ex ea commodo consequat. Duis autem vel eum iriure dolor in hendrerit in vulputate velit esse molestie consequat, vel illum dolore eu feugiat nulla facilisis at vero eros et accumsan et iusto odio dignissim qui blandit praesent luptatum zzril delenit augue duis dolore te feugait nulla fcailisi.

Nam liber tempor cum soluta nobis eleifend option congue nihil imperdiet doming id quod mazim placerat facer possim assum. Lorem ipsum dolor sit amet, consectetuer adipiscing elit, sed diam nonummy nibh euismod tincidunt ut laoreet dolore magna aliquam erat volutpat. Ut wisi enim ad minim v`,
			300,
			7,
		},
		{ // 951 characters
			`Lorem ipsum dolor sit amet, consetetur sadipscing elitr, sed diam nonumy eirmod tempor invidunt ut labore et dolore magna aliquyam erat, sed diam voluptua. At vero eos et accusam et justo duo dolores et ea rebum. Stet clita kasd gubergren, no sea takimata sanctus est Lorem ipsum dolor sit amet. Lorem ipsum dolor sit amet, consetetur sadipscing elitr, sed diam nonumy eirmod tempor invidunt ut labore et dolore magna aliquyam erat, sed diam voluptua. At vero eos et accusam et justo duo dolores et ea rebum. Stet clita kasd gubergren, no sea takimata sanctus est Lorem ipsum dolor sit amet. Lorem ipsum dolor sit amet, consetetur sadipscing elitr, sed diam nonumy eirmod tempor invidunt ut labore et dolore magna aliquyam erat, sed diam voluptua. At vero eos et accusam et justo duo dolores et ea rebum. Stet clita kasd gubergren, no sea takimata sanctus est Lorem ipsum dolor sit amet.

Duis autem vel eum iriure dolor in hendrerit in vulputate vel`,
			300,
			4,
		},
	}

	for i, tc := range testCases {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			assert := assert.New(t)

			toots := splitIntoPosts(tc.message, tc.maxPostLen)
			assert.Len(toots, tc.wantToots)

			for _, toot := range toots {
				fmt.Println(toot)
				assert.LessOrEqual(len(toot), tc.maxPostLen)
			}
			assert.Contains(toots[0], hashTags)
		})
	}
}

func TestParseNewsFile(t *testing.T) {
	assert := assert.New(t)
	require := require.New(t)

	f, err := os.ReadFile("testdata/news.json")
	require.NoError(err)

	news := newsFile{}
	assert.NoError(json.Unmarshal(f, &news))
	assert.Len(news.Entries, 233)
}
