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

	postTool := mcp.NewTool("post",
		mcp.WithDescription("Make a Bluesky post"),
		mcp.WithString("message",
			mcp.Required(),
			mcp.Description("The text contents of the post. Maximum length is 300 characters. Mentions (@bsky.app), links (https://google.com), and tags (#example) will be automatically detected and added as facets."),
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

	notificationTool := mcp.NewTool("readNotifications",
		mcp.WithDescription("Reads 50 most recent notifications"),
	)

	s.AddTool(notificationTool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		r, err := appbsky.NotificationListNotifications(ctx, c, "", 50, false, nil, "")
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Error reading notifications: %s", err)), nil
		}

		str := fmt.Sprintf("%d notifications:\n", len(r.Notifications))

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

	createRecord(ctx, c, makePost(ctx, c, "test https://pdsls.dev @bsky.app #testtag !"))
}

type URI struct {
	repo string
	collection string
	rkey string
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
