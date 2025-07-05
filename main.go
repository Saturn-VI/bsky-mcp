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

	ctx = context.WithValue(ctx, "client", c)

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

		r, err := makePost(ctx, c, m)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Error creating post: %s", err)), nil
		}

		return mcp.NewToolResultText(fmt.Sprintf("Successfully created post. CID: %s URI: %s", r.Cid, r.Uri)), nil
	})
	makePost(ctx, c, "test https://pdsls.dev @bsky.app #testtag")
}

func makePost(ctx context.Context, c *xrpc.Client, m string) (*comatproto.RepoCreateRecord_Output, error) {
	r, err := comatproto.RepoCreateRecord(ctx, c, &comatproto.RepoCreateRecord_Input{
		Collection: "app.bsky.feed.post",
		Record: &lexutil.LexiconTypeDecoder{
			Val: &appbsky.FeedPost{
				CreatedAt: syntax.DatetimeNow().String(),
				Text:      m,
				Facets: getFacetsFromString(ctx, c, m),
			},
		},
		Repo: c.Auth.Did,
	})

	if err != nil {
		return nil, fmt.Errorf("error creating post: %w", err)
	}

	return r, nil
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
					ByteEnd: int64(endIndex),
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
					ByteEnd: int64(endIndex),
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
				ByteEnd: int64(endIndex),
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
				ByteEnd: int64(endIndex),
				ByteStart: int64(startIndex),
			},
		}
		facets = append(facets, facet)
	}

	return facets
}
