package main

import (
	"context"
	"fmt"
	"os"
	"regexp"

	comatproto "github.com/bluesky-social/indigo/api/atproto"
	appbsky "github.com/bluesky-social/indigo/api/bsky"
	lexutil "github.com/bluesky-social/indigo/lex/util"
	xrpc "github.com/bluesky-social/indigo/xrpc"

	"github.com/bluesky-social/indigo/atproto/syntax"
	"github.com/joho/godotenv"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func main() {

	fmt.Println("running")

	err := godotenv.Load()
	if err != nil {
		fmt.Println("Error loading .env file")
	}

	s := server.NewMCPServer(
		"Bluesky MCP Server",
		"1.0.0", // why is the version number a string lmao
	)

	ctx := context.Background()
	did := os.Getenv("DID")
	password := os.Getenv("PASSWORD")
	username, err := syntax.ParseAtIdentifier(did)
	if err != nil {
		fmt.Println("Error loading auth session:", err)
	}
	c, err := loadAuthSession(ctx, username, password)

	postTool := mcp.NewTool("createPost",
		mcp.WithDescription("Make a Bluesky post"),
		mcp.WithString("message",
			mcp.Required(),
			mcp.Description("The text contents of the post. Maximum length is 300 characters. Mentions (@bsky.app), links (https://google.com), and tags (#example) will be automatically detected and added as facets."),
		),
		mcp.WithString("replySubject",
			mcp.Description("Accepts an at-uri. If provided, will create post as a reply top the provided uri."),
		),
		mcp.WithString("repostSubject",
			mcp.Description("Accepts an at-uri. If provided, will quote post the provided uri (must be a post)."),
		),
	)

	s.AddTool(postTool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		m, err := request.RequireString("message")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		if len(m) > 300 {
			return mcp.NewToolResultError("Message exceeds maximum length of 300 characters"), nil
		}
		if len(m) == 0 {
			return mcp.NewToolResultError("Message is empty"), nil
		}

		r, err := createRecord(ctx, c, makePost(ctx, c, m))
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Error creating post: %s", err)), nil
		}

		return mcp.NewToolResultText(fmt.Sprintf("Successfully created post. CID: %s URI: %s", r.Cid, r.Uri)), nil
	})

	repostTool := mcp.NewTool("repost",
		mcp.WithDescription("Repost a Bluesky post"),
		mcp.WithString("repostSubject",
			mcp.Required(),
			mcp.Description("Accepts an at-uri. Must be a post. Will repost the provided uri."),
		),
	)

	s.AddTool(repostTool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		subj, err := request.RequireString("repostSubject")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		r, err := createRecord(ctx, c, makeRepost(c, subj))
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Error creating repost: %s", err)), nil
		}
		return mcp.NewToolResultText(fmt.Sprintf("Successfully created repost. CID: %s URI: %s", r.Cid, r.Uri)), nil
	})

	deletePostTool := mcp.NewTool("deletePost",
		mcp.WithDescription("Delete a Bluesky post"),
		mcp.WithString("uri",
			mcp.Required(),
			mcp.Description("at-uri of post to delete. must be your own post."),
		),
	)

	s.AddTool(deletePostTool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		uri, err := request.RequireString("uri")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		parsed, err := parseURI(uri)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Error parsing URI: %s", err)), nil
		}

		r, err := comatproto.RepoDeleteRecord(ctx, c, &comatproto.RepoDeleteRecord_Input{
			Collection: parsed.collection,
			Repo:       parsed.repo,
			Rkey:       parsed.rkey,
		})
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Error deleting post: %s", err)), nil
		}
		return mcp.NewToolResultText(fmt.Sprintf("Successfully deleted post. Commit CID: %s", r.Commit.Cid)), nil
	})

	notificationTool := mcp.NewTool("readNotifications",
		mcp.WithDescription("Reads notifications"),
		mcp.WithString("cursor",
			mcp.Description("Optional cursor to paginate through notifications. If not provided, will read the latest notifications."),
		),
		mcp.WithNumber("limit",
			mcp.Description("Optional limit on the number of notifications to read. Default is 50."),
		),
	)

	s.AddTool(notificationTool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		cursor := request.GetString("cursor", "")
		limit := request.GetInt("limit", 50)
		r, err := appbsky.NotificationListNotifications(ctx, c, cursor, int64(limit), false, nil, "")
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Error reading notifications: %s", err)), nil
		}

		str := fmt.Sprintf("%d notifications (cursor: %s):\n", len(r.Notifications), *r.Cursor)

		for _, n := range r.Notifications {
			if n.Reason == "like" {
				like := n.Record.Val.(*appbsky.FeedLike)
				uri, err := parseURI(like.Subject.Uri)
				if err != nil {
					fmt.Println("Error parsing URI:", err)
					continue
				}
				likesubject, _ := comatproto.RepoGetRecord(ctx, c, like.Subject.Cid, uri.collection, uri.repo, uri.rkey)
				str += fmt.Sprintf("%s (%s) liked your post (URI %s): %s\n", *n.Author.DisplayName, n.Author.Did, like.Subject.Uri, likesubject.Value.Val.(*appbsky.FeedPost).Text)
			}
			if n.Reason == "mention" {
				str += fmt.Sprintf("%s (%s) mentioned you (URI %s): %s\n", *n.Author.DisplayName, n.Author.Did, n.Uri, n.Record.Val.(*appbsky.FeedPost).Text)
			}
			if n.Reason == "follow" {
				str += fmt.Sprintf("%s (%s) followed you\n", *n.Author.DisplayName, n.Author.Did)
			}
			if n.Reason == "reply" {
				reply := n.Record.Val.(*appbsky.FeedPost)
				uri, err := parseURI(reply.Reply.Parent.Uri)
				if err != nil {
					fmt.Println("Error parsing URI:", err)
					continue
				}
				replysubject, _ := comatproto.RepoGetRecord(ctx, c, reply.Reply.Parent.Cid, uri.collection, uri.repo, uri.rkey)
				str += fmt.Sprintf("%s (%s) replied to your post (URI %s, contents %s) with: %s\n", *n.Author.DisplayName, n.Author.Did, reply.Reply.Parent.Uri, replysubject.Value.Val.(*appbsky.FeedPost).Text, reply.Text)
			}
		}
		fmt.Println(str)
		return mcp.NewToolResultText(str), nil
	})

	readFeedTool := mcp.NewTool("readFeed",
		mcp.WithDescription("Reads a feed."),
		mcp.WithString("feedUri",
			mcp.Description("Optional feed URI to read. If a feed URI is not provided, it will read the home feed (Discover)."),
		),
		mcp.WithString("cursor",
			mcp.Description("Optional cursor to paginate through posts. If not provided, will read the latest posts."),
		),
		mcp.WithNumber("limit",
			mcp.Description("Optional limit on the number of posts to read. Default is 50."),
		),
	)

	s.AddTool(readFeedTool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		feedUri, err := request.RequireString("feedUri")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		cursorParam := request.GetString("cursor", "")
		limit := request.GetInt("limit", 50)
		var posts []*appbsky.FeedDefs_FeedViewPost
		var cursor string

		savedFeeds, err := getSavedFeeds(ctx, c)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Error getting saved feeds: %s", err)), nil
		}

		if feedUri == "" {
			// there's a builtin getTimeline
			r, err := appbsky.FeedGetTimeline(ctx, c, "", cursorParam, int64(limit))
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("Error reading home feed: %s", err)), nil
			}
			posts = r.Feed
			cursor = *r.Cursor
		} else {
			// read a specific feed
			r, err := appbsky.FeedGetFeed(ctx, c, cursorParam, feedUri, int64(limit))
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("Error reading home feed: %s", err)), nil
			}
			posts = r.Feed
			cursor = *r.Cursor
		}

		str := fmt.Sprintf("\"%s\" Feed (cursor: %s):\n", savedFeeds.Items[0].Value, cursor)

		str += generateStringFromPosts(posts)

		return mcp.NewToolResultText(str), nil
	})

	readListFeedTool := mcp.NewTool("readListFeed",
		mcp.WithDescription("Reads a feed."),
		mcp.WithString("listUri",
			mcp.Required(),
			mcp.Description("URI of list to get feed from."),
		),
		mcp.WithString("cursor",
			mcp.Description("Optional cursor to paginate through posts. If not provided, will read the latest posts."),
		),
		mcp.WithNumber("limit",
			mcp.Description("Optional limit on the number of posts to read. Default is 50."),
		),
	)

	s.AddTool(readListFeedTool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		listUri, err := request.RequireString("listUri")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		cursorParam := request.GetString("cursor", "")
		limit := request.GetInt("limit", 50)
		var posts []*appbsky.FeedDefs_FeedViewPost
		var cursor string

		l, err := appbsky.GraphGetList(ctx, c, cursorParam, int64(limit), listUri)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Error getting list: %s", err)), nil
		}
		listName := l.List.Name

		r, err := appbsky.FeedGetListFeed(ctx, c, cursorParam, int64(limit), listUri)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Error reading list feed: %s", err)), nil
		}
		cursor = *r.Cursor
		posts = r.Feed

		str := fmt.Sprintf("Feed generated from list \"%s\" (cursor: %s):\n", listName, cursor)
		str += generateStringFromPosts(posts)

		return mcp.NewToolResultText(str), nil
	})

	readAuthorFeedTool := mcp.NewTool("readAuthorFeed",
		mcp.WithDescription("Reads a feed."),
		mcp.WithString("actor",
			mcp.Required(),
			mcp.Description("AT-identifier of the author to read the feed from. Must be a valid Bluesky DID (e.g., did:plc:... or did:web:...)."),
		),
		mcp.WithString("cursor",
			mcp.Description("Optional cursor to paginate through posts. If not provided, will read the latest posts."),
		),
		mcp.WithNumber("limit",
			mcp.Description("Optional limit on the number of posts to read. Default is 50."),
		),
		mcp.WithString("filter",
			mcp.Description("Optional filter to apply to the feed. ('posts_with_replies', 'posts_no_replies', 'posts_with_media', 'posts_and_author_threads', 'posts_with_video'). Default is 'posts_with_replies'."),
			mcp.Enum("posts_with_replies", "posts_no_replies", "posts_with_media", "posts_and_author_threads", "posts_with_video"),
		),
		mcp.WithBoolean("includePins",
			mcp.Description("Whether or not to include pinned posts."),
		),
	)

	s.AddTool(readAuthorFeedTool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		actor, err := request.RequireString("actor")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		cursorParam := request.GetString("cursor", "")
		limit := request.GetInt("limit", 50)
		filter := request.GetString("filter", "posts_with_replies")
		includePins := request.GetBool("includePins", false)
		var posts []*appbsky.FeedDefs_FeedViewPost
		var cursor string

		a, err := appbsky.ActorGetProfile(ctx, c, actor)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Error getting list: %s", err)), nil
		}
		userName := *a.DisplayName

		r, err := appbsky.FeedGetAuthorFeed(ctx, c, actor, cursorParam, filter, includePins, int64(limit))
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Error reading list feed: %s", err)), nil
		}
		cursor = *r.Cursor
		posts = r.Feed

		str := fmt.Sprintf("Feed generated from posts by actor \"%s\" (cursor: %s):\n", userName, cursor)
		str += generateStringFromPosts(posts)

		return mcp.NewToolResultText(str), nil
	})

	// createRecord(ctx, c, makePost(ctx, c, "test https://pdsls.dev @bsky.app #testtag !"))
}

type URI struct {
	repo       string
	collection string
	rkey       string
}

func parseURI(uri string) (URI, error) {
	parts := regexp.MustCompile(`^at://([^/]+)/([^/]+)/([^/]+)$`).FindStringSubmatch(uri)
	if len(parts) != 4 {
		return URI{}, fmt.Errorf("invalid URI format: %s", uri)
	}

	return URI{
		repo:       parts[1],
		collection: parts[2],
		rkey:       parts[3],
	}, nil
}

func createRecord(ctx context.Context, c *xrpc.Client, rec *comatproto.RepoCreateRecord_Input) (*comatproto.RepoCreateRecord_Output, error) {
	res, err := comatproto.RepoCreateRecord(ctx, c, rec)

	if err != nil {
		return nil, fmt.Errorf("error creating record: %w", err)
	}

	return res, nil
}

func makePost(ctx context.Context, c *xrpc.Client, m string) *comatproto.RepoCreateRecord_Input {
	p := &comatproto.RepoCreateRecord_Input{
		Collection: "app.bsky.feed.post",
		Record: &lexutil.LexiconTypeDecoder{
			Val: &appbsky.FeedPost{
				CreatedAt: syntax.DatetimeNow().String(),
				Text:      m,
				Facets:    getFacetsFromString(ctx, c, m),
			},
		},
		Repo: c.Auth.Did,
	}

	return p
}

func makeRepost(c *xrpc.Client, subj string) *comatproto.RepoCreateRecord_Input {
	uri, err := parseURI(subj)
	if err != nil {
		fmt.Println("Error parsing URI:", err)
		return nil
	}

	r := &comatproto.RepoCreateRecord_Input{
		Collection: "app.bsky.feed.repost",
		Record: &lexutil.LexiconTypeDecoder{
			Val: &appbsky.FeedRepost{
				CreatedAt: syntax.DatetimeNow().String(),
				Subject: &comatproto.RepoStrongRef{
					Cid: uri.rkey,
					Uri: subj,
				},
			},
		},
		Repo: c.Auth.Did,
	}

	return r
}

// need to get mentions, links, and tags
func getFacetsFromString(ctx context.Context, c *xrpc.Client, s string) []*appbsky.RichtextFacet {
	fmt.Println("processing string for facets...")
	lreg, _ := regexp.Compile(`https:\/\/(?:www\.)?[-a-zA-Z0-9@:%._\+~#=]{1,256}\.[a-zA-Z0-9]{2,6}\b(?:[-a-zA-Z0-9@:%_\+.~#?&//=]*)`)
	mreg, _ := regexp.Compile(`@([a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?\.)+[a-zA-Z]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?`)
	treg, _ := regexp.Compile(`#(\S*)`)

	var facets []*appbsky.RichtextFacet

	// links should not be preceded by an @ or #
	for _, indices := range lreg.FindAllStringIndex(s, -1) {
		startIndex := indices[0]
		endIndex := indices[1]
		match := s[startIndex:endIndex]

		fmt.Println(match)

		if startIndex == 0 {
			// cannot be preceded by an @ or #
			facet := &appbsky.RichtextFacet{
				Features: []*appbsky.RichtextFacet_Features_Elem{
					&appbsky.RichtextFacet_Features_Elem{RichtextFacet_Link: &appbsky.RichtextFacet_Link{Uri: match}},
				},
				Index: &appbsky.RichtextFacet_ByteSlice{
					ByteEnd:   int64(endIndex),
					ByteStart: int64(startIndex),
				},
			}
			facets = append(facets, facet)
		}

		preChar := s[startIndex-1 : startIndex]
		if preChar != "@" && preChar != "#" {
			facet := &appbsky.RichtextFacet{
				Features: []*appbsky.RichtextFacet_Features_Elem{
					&appbsky.RichtextFacet_Features_Elem{RichtextFacet_Link: &appbsky.RichtextFacet_Link{Uri: match}},
				},
				Index: &appbsky.RichtextFacet_ByteSlice{
					ByteEnd:   int64(endIndex),
					ByteStart: int64(startIndex),
				},
			}
			facets = append(facets, facet)
		}
	}

	for _, indices := range mreg.FindAllStringIndex(s, -1) {
		startIndex := indices[0]
		endIndex := indices[1]
		match := s[startIndex:endIndex]

		fmt.Println(match)

		r, err := comatproto.IdentityResolveHandle(ctx, c, match[1:]) // skip the @
		if err != nil {
			fmt.Println("Error resolving handle:", err)
			continue
		}

		facet := &appbsky.RichtextFacet{
			Features: []*appbsky.RichtextFacet_Features_Elem{
				&appbsky.RichtextFacet_Features_Elem{RichtextFacet_Mention: &appbsky.RichtextFacet_Mention{Did: r.Did}},
			},
			Index: &appbsky.RichtextFacet_ByteSlice{
				ByteEnd:   int64(endIndex),
				ByteStart: int64(startIndex),
			},
		}
		facets = append(facets, facet)
	}

	for _, indices := range treg.FindAllStringIndex(s, -1) {
		startIndex := indices[0]
		endIndex := indices[1]
		match := s[startIndex:endIndex]

		fmt.Println(match)

		facet := &appbsky.RichtextFacet{
			Features: []*appbsky.RichtextFacet_Features_Elem{
				&appbsky.RichtextFacet_Features_Elem{RichtextFacet_Tag: &appbsky.RichtextFacet_Tag{Tag: match[1:]}},
			},
			Index: &appbsky.RichtextFacet_ByteSlice{
				ByteEnd:   int64(endIndex),
				ByteStart: int64(startIndex),
			},
		}
		facets = append(facets, facet)
	}

	return facets
}

func getSavedFeeds(ctx context.Context, c *xrpc.Client) (*appbsky.ActorDefs_SavedFeedsPrefV2, error) {
	r, err := appbsky.ActorGetPreferences(ctx, c)
	if err != nil {
		fmt.Println("Error getting saved feeds:", err)
		return nil, err
	}

	// r.prefs probably won't be nil
	for _, pref := range r.Preferences {
		if pref.ActorDefs_SavedFeedsPrefV2 != nil {
			return pref.ActorDefs_SavedFeedsPrefV2, nil
		}
	}

	return nil, fmt.Errorf("no saved feeds found") // hopefully never
}

func generateStringFromPosts(posts []*appbsky.FeedDefs_FeedViewPost) string {
	str := ""
	for _, post := range posts {
		if post.Reason.FeedDefs_ReasonPin != nil {
			str += fmt.Sprintf("Pinned post (DID %s) (URI %s) with %d likes, %d quotes, and %d replies: %s\n",
				*post.Post.Author.DisplayName,
				post.Post.Author.Did,
				post.Post.Uri,
				*post.Post.LikeCount,
				*post.Post.QuoteCount,
				*post.Post.ReplyCount,
				post.Post.Record.Val.(*appbsky.FeedPost).Text)
		} else if post.Reason.FeedDefs_ReasonRepost != nil {
			reposter := post.Reason.FeedDefs_ReasonRepost.By
			str += fmt.Sprintf("%s (%s) reposted a post by %s (%s)  with %d likes, %d quotes, %d replies, and a URI of %s: %s\n",
				*reposter.DisplayName,
				reposter.Did,
				*post.Post.Author.DisplayName,
				post.Post.Author.Did,
				*post.Post.LikeCount,
				*post.Post.QuoteCount,
				*post.Post.ReplyCount,
				post.Post.Uri,
				post.Post.Record.Val.(*appbsky.FeedPost).Text)
		} else {
			str += fmt.Sprintf("Post by %s (DID %s) with %d likes, %d quotes, %d replies, and a URI of %s: %s\n",
				*post.Post.Author.DisplayName,
				post.Post.Author.Did,
				*post.Post.LikeCount,
				*post.Post.QuoteCount,
				*post.Post.ReplyCount,
				post.Post.Uri,
				post.Post.Record.Val.(*appbsky.FeedPost).Text)
		}
	}
	return str
}
