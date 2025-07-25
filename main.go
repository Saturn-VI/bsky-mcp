package main

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"

	comatproto "github.com/bluesky-social/indigo/api/atproto"
	appbsky "github.com/bluesky-social/indigo/api/bsky"
	lexutil "github.com/bluesky-social/indigo/lex/util"
	xrpc "github.com/bluesky-social/indigo/xrpc"

	"github.com/bluesky-social/indigo/atproto/syntax"
	"github.com/joho/godotenv"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

var Version string = "1.0.0"

func main() {

	fmt.Println("running")

	err := godotenv.Load()
	if err != nil {
		fmt.Println("Error loading .env file")
	}

	s := server.NewMCPServer(
		"Bluesky MCP Server",
		Version, // why is the version number a string lmao
	)

	ctx := context.Background()
	did := os.Getenv("ATPROTO_DID")
	password := os.Getenv("ATPROTO_APP_PASSWORD")
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

		repost, err := makeRepost(c, subj)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Error creating repost: %s", err)), nil
		}
		r, err := createRecord(ctx, c, repost)
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

	likePostTool := mcp.NewTool("likePost",
		mcp.WithDescription("Like a Bluesky post"),
		mcp.WithString("uri",
			mcp.Required(),
			mcp.Description("at-uri of post to like. must be a post."),
		),
	)

	s.AddTool(likePostTool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		uri, err := request.RequireString("uri")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		parsed, err := parseURI(uri)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Error parsing URI: %s", err)), nil
		}

		likeTarget, err := comatproto.RepoGetRecord(ctx, c, "", parsed.collection, parsed.repo, parsed.rkey)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Error getting post to like: %s", err)), nil
		}

		like := &appbsky.FeedLike{
			CreatedAt: syntax.DatetimeNow().String(),
			Subject: &comatproto.RepoStrongRef{
				Cid: *likeTarget.Cid,
				Uri: likeTarget.Uri,
			},
		}
		r, err := comatproto.RepoCreateRecord(ctx, c, &comatproto.RepoCreateRecord_Input{
			Collection: like.LexiconTypeID,
			Record: &lexutil.LexiconTypeDecoder{
				Val: like,
			},
			Repo: c.Auth.Did,
		})
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Error liking post: %s", err)), nil
		}

		return mcp.NewToolResultText(fmt.Sprintf("Successfully liked post. Commit CID: %s", r.Commit.Cid)), nil
	})

	unlikePostTool := mcp.NewTool("unlikePost",
		mcp.WithDescription("Unlike a Bluesky post"),
		mcp.WithString("uri",
			mcp.Required(),
			mcp.Description("at-uri of post to unlike. must be a post."),
		),
	)

	s.AddTool(unlikePostTool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		uri, err := request.RequireString("uri")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		posts, err := appbsky.FeedGetPosts(ctx, c, []string{uri})
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Error getting post: %s", err)), nil
		}
		if len(posts.Posts) == 0 {
			return mcp.NewToolResultError(fmt.Sprintf("Post not found: %s", uri)), nil
		}

		post := posts.Posts[0]
		if post.Viewer.Like == nil {
			return mcp.NewToolResultError(fmt.Sprintf("Post not liked: %s", uri)), nil
		}
		like := post.Viewer.Like

		parsed, err := parseURI(*like)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Error parsing like URI: %s", err)), nil
		}
		r, err := comatproto.RepoDeleteRecord(ctx, c, &comatproto.RepoDeleteRecord_Input{
			Collection: parsed.collection,
			Repo:       parsed.repo,
			Rkey:       parsed.rkey,
		})
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Error deleting like: %s", err)), nil
		}
		return mcp.NewToolResultText(fmt.Sprintf("Successfully unliked post. Commit CID: %s", r.Commit.Cid)), nil
	})

	followUserTool := mcp.NewTool("followUser",
		mcp.WithDescription("Follow a Bluesky user"),
		mcp.WithString("did",
			mcp.Required(),
			mcp.Description("DID of the user to follow. Must be a valid Bluesky DID (e.g., did:plc:... or did:web:...)."),
		),
	)

	s.AddTool(followUserTool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		did, err := request.RequireString("did")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		follow := &appbsky.GraphFollow{
			CreatedAt: syntax.DatetimeNow().String(),
			Subject:   did,
		}
		r, err := comatproto.RepoCreateRecord(ctx, c, &comatproto.RepoCreateRecord_Input{
			Collection: follow.LexiconTypeID,
			Record: &lexutil.LexiconTypeDecoder{
				Val: follow,
			},
			Repo: c.Auth.Did,
		})
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Error following user: %s", err)), nil
		}

		return mcp.NewToolResultText(fmt.Sprintf("Successfully followed user. Commit CID: %s", r.Commit.Cid)), nil
	})

	unfollowUserTool := mcp.NewTool("unfollowUser",
		mcp.WithDescription("Unfollow a Bluesky user"),
		mcp.WithString("did",
			mcp.Required(),
			mcp.Description("DID of the user to unfollow. Must be a valid Bluesky DID (e.g., did:plc:... or did:web:...)."),
		),
	)

	s.AddTool(unfollowUserTool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		did, err := request.RequireString("did")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		user, err := appbsky.ActorGetProfile(ctx, c, did)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Error getting user profile: %s", err)), nil
		}
		if user.Viewer.Following == nil {
			return mcp.NewToolResultError(fmt.Sprintf("You are not following user: %s", did)), nil
		}
		follow := user.Viewer.Following

		parsed, err := parseURI(*follow)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Error parsing follow URI: %s", err)), nil
		}
		r, err := comatproto.RepoDeleteRecord(ctx, c, &comatproto.RepoDeleteRecord_Input{
			Collection: parsed.collection,
			Repo:       parsed.repo,
			Rkey:       parsed.rkey,
		})
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Error deleting follow: %s", err)), nil
		}
		return mcp.NewToolResultText(fmt.Sprintf("Successfully unfollowed user. Commit CID: %s", r.Commit.Cid)), nil
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
				likesubject, err := comatproto.RepoGetRecord(ctx, c, like.Subject.Cid, uri.collection, uri.repo, uri.rkey)
				if err != nil {
					fmt.Println("Error getting like subject:", err)
					continue
				}
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
				replysubject, err := comatproto.RepoGetRecord(ctx, c, reply.Reply.Parent.Cid, uri.collection, uri.repo, uri.rkey)
				if err != nil {
					fmt.Println("Error getting reply subject:", err)
					continue
				}
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
		var cursor string = ""

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
			if r.Cursor != nil {
				cursor = *r.Cursor
			}
		} else {
			// read a specific feed
			r, err := appbsky.FeedGetFeed(ctx, c, cursorParam, feedUri, int64(limit))
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("Error reading home feed: %s", err)), nil
			}
			posts = r.Feed
			if r.Cursor != nil {
				cursor = *r.Cursor
			}
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
		var cursor string = ""

		l, err := appbsky.GraphGetList(ctx, c, cursorParam, int64(limit), listUri)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Error getting list: %s", err)), nil
		}
		listName := l.List.Name

		r, err := appbsky.FeedGetListFeed(ctx, c, cursorParam, int64(limit), listUri)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Error reading list feed: %s", err)), nil
		}
		if r.Cursor != nil {
			cursor = *r.Cursor
		}
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
		var cursor string = ""

		a, err := appbsky.ActorGetProfile(ctx, c, actor)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Error getting list: %s", err)), nil
		}
		userName := *a.DisplayName

		r, err := appbsky.FeedGetAuthorFeed(ctx, c, actor, cursorParam, filter, includePins, int64(limit))
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Error reading list feed: %s", err)), nil
		}
		if r.Cursor != nil {
			cursor = *r.Cursor
		}
		posts = r.Feed

		str := fmt.Sprintf("Feed generated from posts by actor \"%s\" (cursor: %s):\n", userName, cursor)
		str += generateStringFromPosts(posts)

		return mcp.NewToolResultText(str), nil
	})

	readLikedPostsTool := mcp.NewTool("readLikedPosts",
		mcp.WithDescription("Reads the liked posts of the logged in user."),
		mcp.WithString("cursor",
			mcp.Description("Optional cursor to paginate through posts. If not provided, will read the latest posts."),
		),
		mcp.WithNumber("limit",
			mcp.Description("Optional limit on the number of posts to read. Default is 50."),
		),
	)

	s.AddTool(readLikedPostsTool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		cursorParam := request.GetString("cursor", "")
		limit := request.GetInt("limit", 50)
		r, err := appbsky.FeedGetActorLikes(ctx, c, c.Auth.Did, cursorParam, int64(limit))
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Error reading liked posts: %s", err)), nil
		}
		if len(r.Feed) == 0 {
			return mcp.NewToolResultText("No liked posts found."), nil
		}
		str := fmt.Sprintf("Liked posts (cursor: %s):\n", *r.Cursor)
		str += generateStringFromPosts(r.Feed)

		return mcp.NewToolResultText(str), nil
	})

	readProfileTool := mcp.NewTool("readProfile",
		mcp.WithDescription("Reads a Bluesky profile."),
		mcp.WithString("actor",
			mcp.Required(),
			mcp.Description("AT-identifier of the actor to read the profile from. Must be a valid Bluesky DID (e.g., did:plc:... or did:web:...)."),
		),
	)

	s.AddTool(readProfileTool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		actor, err := request.RequireString("actor")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		profile, err := appbsky.ActorGetProfile(ctx, c, actor)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Error getting profile: %s", err)), nil
		}

		pronounsdiyDID := "did:plc:wkoofae5uytcm7bjncmev6n6"
		labels, err := comatproto.LabelQueryLabels(ctx, c, "", 100, []string{pronounsdiyDID}, []string{actor})

		verified := "No"
		if profile.Verification.TrustedVerifierStatus == "valid" || profile.Verification.VerifiedStatus == "valid" {
			verified = "Yes"
		}

		str := fmt.Sprintf("Profile of %s (%s):\n", *profile.DisplayName, profile.Did)
		str += fmt.Sprintf("Handle: %s\n", profile.Handle)
		str += fmt.Sprintf("Verified: %s", verified)
		str += fmt.Sprintf("Bio: %s\n", *profile.Description)
		str += fmt.Sprintf("Followers: %d\n", *profile.FollowersCount)
		str += fmt.Sprintf("Following: %d\n", *profile.FollowsCount)
		str += fmt.Sprintf("Posts: %d\n", *profile.PostsCount)

		for _, label := range labels.Labels {
			if label.Src == pronounsdiyDID {
				str += fmt.Sprintf("Pronouns: %s\n", label.Val)
			}
		}

		return mcp.NewToolResultText(str), nil
	})

	listSavedFeedsTool := mcp.NewTool("listSavedFeeds",
		mcp.WithDescription("Lists saved feeds."),
	)

	s.AddTool(listSavedFeedsTool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		savedFeeds, err := getSavedFeeds(ctx, c)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Error getting saved feeds: %s", err)), nil
		}

		str := "Saved Feeds:\n"
		for _, item := range savedFeeds.Items {
			feedGen, err := appbsky.FeedGetFeedGenerator(ctx, c, item.Value)
			if err != nil {
				fmt.Printf("Error getting feed generator: %s\n", err.Error())
				continue
			}
			isOnline := "Currently online"
			if !feedGen.IsOnline {
				isOnline = "Currently offline"
			}
			str += fmt.Sprintf("%s URI: %s, (%s, %d likes) — %s", feedGen.View.DisplayName, feedGen.View.Uri, isOnline, *feedGen.View.LikeCount, *feedGen.View.Description)
		}

		return mcp.NewToolResultText(str), nil
	})

	getFollowersTool := mcp.NewTool("getFollowers",
		mcp.WithDescription("Gets followers of a Bluesky actor."),
		mcp.WithString("actor",
			mcp.Required(),
			mcp.Description("AT-identifier of the actor to get followers from. Must be a valid Bluesky DID (e.g., did:plc:... or did:web:...)."),
		),
		mcp.WithString("cursor",
			mcp.Description("Optional cursor to paginate through posts. If not provided, will read the latest posts."),
		),
		mcp.WithNumber("limit",
			mcp.Description("Optional limit on the number of posts to read. Default is 50."),
		),
	)

	s.AddTool(getFollowersTool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		actor, err := request.RequireString("actor")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		cursorParam := request.GetString("cursor", "")
		limit := request.GetInt("limit", 50)

		followers, err := appbsky.GraphGetFollowers(ctx, c, actor, cursorParam, int64(limit))
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Error getting followers: %s", err)), nil
		}

		str := fmt.Sprintf("Followers of %s (%s):\n", *followers.Subject.DisplayName, followers.Subject.Did)
		for _, follower := range followers.Followers {
			str += fmt.Sprintf("%s (%s) — %s\n", *follower.DisplayName, follower.Did, *follower.Description)
		}

		return mcp.NewToolResultText(str), nil
	})

	getFollowingTool := mcp.NewTool("getFollowing",
		mcp.WithDescription("Gets those who a Bluesky actor follows."),
		mcp.WithString("actor",
			mcp.Required(),
			mcp.Description("AT-identifier of the actor to get followers from. Must be a valid Bluesky DID (e.g., did:plc:... or did:web:...)."),
		),
		mcp.WithString("cursor",
			mcp.Description("Optional cursor to paginate through posts. If not provided, will read the latest posts."),
		),
		mcp.WithNumber("limit",
			mcp.Description("Optional limit on the number of posts to read. Default is 50."),
		),
	)

	s.AddTool(getFollowingTool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		actor, err := request.RequireString("actor")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		cursorParam := request.GetString("cursor", "")
		limit := request.GetInt("limit", 50)

		following, err := appbsky.GraphGetFollows(ctx, c, actor, cursorParam, int64(limit))
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Error getting followers: %s", err)), nil
		}

		str := fmt.Sprintf("Followers of %s (%s):\n", *following.Subject.DisplayName, following.Subject.Did)
		for _, follow := range following.Follows {
			str += fmt.Sprintf("%s (%s) — %s\n", *follow.DisplayName, follow.Did, *follow.Description)
		}

		return mcp.NewToolResultText(str), nil
	})

	getTrendingTool := mcp.NewTool("getTrending",
		mcp.WithDescription("Gets top trending categories, number of posts per category, time started, and status."),
		mcp.WithNumber("limit",
			mcp.Description("Maximum number of categories to get"),
			mcp.DefaultNumber(5),
		),
	)

	s.AddTool(getTrendingTool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		limit := request.GetInt("limit", 5)
		r, err := appbsky.UnspeccedGetTrends(ctx, c, int64(limit))
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Error getting trends: %s", err)), nil
		}
		if len(r.Trends) == 0 {
			return mcp.NewToolResultText("No trends found."), nil
		}
		str := fmt.Sprintf("Top %d trending categories:\n", len(r.Trends))
		for index, trend := range r.Trends {
			splitstr := strings.Split(trend.Link, "/")
			processedUri := fmt.Sprintf("at://did:plc:qrz3lhbyuxbeilrc6nekdqme/app.bsky.feed.generator/%s", splitstr[len(splitstr)-1])
			str += fmt.Sprintf("Rank: %d, Name: %s, Category: %s, Posts: %d, Time Started: %s, Status: %s, URI: %s\n",
				index+1,
				trend.DisplayName,
				*trend.Category,
				trend.PostCount,
				trend.StartedAt,
				*trend.Status,
				processedUri)
		}
		return mcp.NewToolResultText(str), nil
	})

	searchPostsTool := mcp.NewTool("searchPosts",
		mcp.WithDescription("Searches for posts containing a specific keyword or hashtag."),
		mcp.WithString("q",
			mcp.Required(),
			mcp.Description("Search query string; syntax, phrase, boolean, and faceting is unspecified, but Lucene query syntax is recommended."),
		),
		mcp.WithString("sort",
			mcp.Description("Specifies the ranking order of results. Possible values: [top, latest]. Default is 'latest'."),
			mcp.DefaultString("latest"),
			mcp.Enum("top", "latest"),
		),
		mcp.WithString("since",
			mcp.Description("Filter results for posts after the indicated datetime (inclusive). Expected to use 'sortAt' timestamp, which may not match 'createdAt'. Can be a datetime, or just an ISO date (YYYY-MM-DD)."),
		),
		mcp.WithString("until",
			mcp.Description("Filter results for posts before the indicated datetime (not inclusive). Expected to use 'sortAt' timestamp, which may not match 'createdAt'. Can be a datetime, or just an ISO date (YYYY-MM-DD)."),
		),
		mcp.WithString("mentions",
			mcp.Description("Filter to posts which mention the given account. Handles are resolved to DID before query-time. Only matches rich-text facet mentions."),
		),
		mcp.WithString("author",
			mcp.Description("Filter to posts by the given account. Handles are resolved to DID before query-time."),
		),
		mcp.WithString("lang",
			mcp.Description("Filter to posts in the given language. Expected to be based on post language field, though server may override language detection."),
		),
		mcp.WithString("domain",
			mcp.Description("Filter to posts with URLs (facet links or embeds) linking to the given domain (hostname). Server may apply hostname normalization."),
		),
		mcp.WithString("url",
			mcp.Description("Filter to posts with links (facet links or embeds) pointing to this URL. Server may apply URL normalization or fuzzy matching."),
		),
		mcp.WithString("tag",
			mcp.Description(" "),
		),
		mcp.WithArray("tags",
			mcp.Description("Filter to posts with the given tag (hashtag), based on rich-text facet or tag field. Do not include the hash (#) prefix. Multiple tags can be specified, with 'AND' matching."),
		),
		mcp.WithNumber("limit",
			mcp.Description("Maximum number of results to return. Possible values: >= 1 and <= 100. Default is 25."),
			mcp.DefaultNumber(25),
		),
		mcp.WithString("cursor",
			mcp.Description("Optional pagination mechanism; may not necessarily allow scrolling through entire result set."),
		),
	)

	s.AddTool(searchPostsTool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		q, err := request.RequireString("q")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		sort := request.GetString("sort", "latest")
		since := request.GetString("since", "")
		until := request.GetString("until", "")
		mentions := request.GetString("mentions", "")
		author := request.GetString("author", "")
		lang := request.GetString("lang", "")
		domain := request.GetString("domain", "")
		url := request.GetString("url", "")
		tags := request.GetStringSlice("tags", []string{})
		limit := request.GetInt("limit", 25)
		cursor := request.GetString("cursor", "")

		r, err := appbsky.FeedSearchPosts(ctx, c, author, cursor, domain, lang, int64(limit), mentions, q, since, sort, tags, until, url)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Error searching posts: %s", err)), nil
		}

		resultStr := fmt.Sprintf("Search Results (cursor: %s):\n", *r.Cursor)
		resultStr += generateStringFromPostViews(&r.Posts)

		return mcp.NewToolResultText(resultStr), nil
	})

	searchUsersTool := mcp.NewTool("searchUsers",
		mcp.WithDescription("Searches for actors (users) by handle or display name."),
		mcp.WithString("q",
			mcp.Required(),
			mcp.Description("Search query string; can be a handle (e.g., username) or display name."),
		),
		mcp.WithString("cursor",
			mcp.Description("Optional pagination cursor to continue from a previous search."),
		),
		mcp.WithNumber("limit",
			mcp.Description("Maximum number of results to return. Possible values: >= 1 and <= 100. Default is 25."),
			mcp.DefaultNumber(25),
		),
	)

	s.AddTool(searchUsersTool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		q, err := request.RequireString("q")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		cursor := request.GetString("cursor", "")
		limit := request.GetInt("limit", 25)

		r, err := appbsky.ActorSearchActors(ctx, c, cursor, int64(limit), q, "")
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Error searching actors: %s", err)), nil
		}

		resultStr := fmt.Sprintf("Search Results (cursor: %s):\n", *r.Cursor)
		for _, actor := range r.Actors {
			resultStr += fmt.Sprintf("%s (%s) - %s\n", *actor.DisplayName, actor.Did, actor.Handle)
		}

		return mcp.NewToolResultText(resultStr), nil
	})

	fmt.Println("Starting server...")
	if err := server.ServeStdio(s); err != nil {
		fmt.Printf("Server error: %v\n", err)
	}
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

func makeRepost(c *xrpc.Client, subj string) (*comatproto.RepoCreateRecord_Input, error) {
	uri, err := parseURI(subj)
	if err != nil {
		fmt.Println("Error parsing URI:", err)
		return nil, err
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

	return r, nil
}

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
	if len(r.Preferences) == 0 {
		return nil, fmt.Errorf("no preferences found")
	}
	for _, pref := range r.Preferences {
		if pref.ActorDefs_SavedFeedsPrefV2 != nil {
			if len(pref.ActorDefs_SavedFeedsPrefV2.Items) == 0 {
				return nil, fmt.Errorf("no saved feeds found in preferences")
			}
			return pref.ActorDefs_SavedFeedsPrefV2, nil
		}
	}

	return nil, fmt.Errorf("no saved feeds found") // hopefully never
}

func generateStringFromPosts(posts []*appbsky.FeedDefs_FeedViewPost) string {
	str := ""
	for _, post := range posts {
		p := post.Post
		fp := p.Record.Val.(*appbsky.FeedPost)
		if post.Reason.FeedDefs_ReasonPin != nil {
			str += fmt.Sprintf("Pinned post by %s (%s)",
				*p.Author.DisplayName,
				p.Author.Did)
		} else if post.Reason.FeedDefs_ReasonRepost != nil {
			reposter := post.Reason.FeedDefs_ReasonRepost.By
			str += fmt.Sprintf("%s (%s) reposted a post by %s (%s)",
				*reposter.DisplayName,
				reposter.Did,
				*p.Author.DisplayName,
				p.Author.Did)
		} else {
			str += fmt.Sprintf("Post by %s (DID %s)",
				*p.Author.DisplayName,
				p.Author.Did)
		}
		str += fmt.Sprintf(" with %d likes, %d quotes, %d replies, a URI of %s, and a posting time of %s:\n",
			*p.LikeCount,
			*p.QuoteCount,
			*p.ReplyCount,
			p.Uri,
			fp.CreatedAt)
		str += fmt.Sprintf("Text: %s\n", fp.Text)
		if fp.Facets != nil {
			str += "Facets:\n"
			facets := generateFacetListFromPost(fp)
			for _, facet := range facets {
				str += fmt.Sprintf("- %s\n", facet)
			}
		}
	}
	return str
}

func generateStringFromPostViews(postViews *[]*appbsky.FeedDefs_PostView) string {
	str := ""
	for _, postView := range *postViews {
		p := postView
		fp := postView.Record.Val.(*appbsky.FeedPost)
		str += fmt.Sprintf("Post by %s (DID %s) with %d likes, %d quotes, %d replies, a URI of %s, and a posting time of %s:\n",
			*p.Author.DisplayName,
			p.Author.Did,
			*p.LikeCount,
			*p.QuoteCount,
			*p.ReplyCount,
			p.Uri,
			fp.CreatedAt)
		str += fmt.Sprintf("Text: %s\n", fp.Text)
		if fp.Facets != nil {
			str += "Facets:\n"
			facets := generateFacetListFromPost(fp)
			for _, facet := range facets {
				str += fmt.Sprintf("- %s\n", facet)
			}
		}
	}
	return str
}

func generateFacetListFromPost(post *appbsky.FeedPost) []string {
	var facets []string
	if post.Facets != nil {
		for _, facet := range post.Facets {
			if facet.Features != nil {
				for _, feature := range facet.Features {
					if feature.RichtextFacet_Link != nil {
						facets = append(facets, fmt.Sprintf("Link from byte %d to byte %d: %s", facet.Index.ByteStart, facet.Index.ByteEnd, feature.RichtextFacet_Link.Uri))
					}
					if feature.RichtextFacet_Mention != nil {
						facets = append(facets, fmt.Sprintf("Mention from byte %d to byte %d: %s", facet.Index.ByteStart, facet.Index.ByteEnd, feature.RichtextFacet_Mention.Did))
					}
					if feature.RichtextFacet_Tag != nil {
						facets = append(facets, fmt.Sprintf("Tag from byte %d to byte %d: %s", facet.Index.ByteStart, facet.Index.ByteEnd, feature.RichtextFacet_Tag.Tag))
					}
				}
			}
		}
	}
	return facets
}
