# Bluesky MCP Server

## Tools:
 - [x] createPost - Creates a post
 - [x] createRepost - Reposts a post
 - [x] deletePost - Deletes a post
 - [x] likePost - Likes a post
 - [x] unlikePost - Unlikes a post
 - [x] followUser - Follows a user
 - [x] unfollowUser - Unfollows a user
 - [x] readNotifications - Reads your notifications
 - [x] readFeed - Reads a feed given a URI
 - [x] readListFeed - Reads a feed given a list URI
 - [x] readAuthorFeed - Reads a feed given a DID
 - [x] readLikedPosts - Reads your liked posts
 - [x] readProfile - Reads a profile given a DID
 - [x] listSavedFeeds - Lists your saved feeds
 - [x] getFollowers - Gets the users following a user
 - [x] getFollowing - Gets the users that are followed by a user
 - [x] getTrending - Get trending topics
 - [x] searchPosts - Searches posts
 - [x] searchUsers - Searches users

## Installation
 Download the corresponding binary for your platform:
 - [x86 Linux](build/bsky-mcp-linux-amd64)
 - [Intel Mac](build/bsky-mcp-darwin-amd64)
 - [M-Series (ARM) Mac](build/bsky-mcp-darwin-arm64)
 - [x86 Windows](build/bsky-mcp-darwin-amd64)

 ### Building from source
 Requirements:
 - [Go](https://go.dev/doc/install)

 To build, run
  ```bash
  $ go build .
  ```
  Building for all platforms is currently done through a bash script (and also requires Go):
  ```bash
  $ ./build.sh
  ```
  This will create binaries for all platforms in the `build` directory.

## Usage

### Environmental Variables
  `ATPROTO_DID`: Your Bluesky DID (e.g., `did:plc:z72i7hd...` or `did:web:example.com`)
   - You can get this by going to [PDSls](https://pdsls.dev), typing in your handle, and clicking "Copy DID" (or going to the DID Doc tab).

  `ATPROTO_APP_PASSWORD`: Your Bluesky app password (e.g., `c7hp-xxxx-xxxx-xxxx`)
   - You can get this by going to the [Bluesky App Passwords page](https://bsky.app/settings/app-passwords) and creating a new app password.
   - Don't use your regular password—it'll work, but it's bad practice :(.

### Claude Desktop
  Add the following to your claude_desktop_config.json:
  ```json
  {
    "mcpServers": {
      "bsky-mcp": {
        "command": "/path/to/binary/bsky-mcp-<YOUR PLATFORM>",
        "env": {
          "ATPROTO_DID": "your_did_here"
          "ATPROTO_APP_PASSWORD": "your_app_password_here"
        }
      }
    }
  }
  ```
  <details>
  <summary>Where is claude_desktop_config.json?</summary>
  On MacOS:
    ```
    ~/Library/Application Support/Claude/claude_desktop_config.json
    ```
  On Windows:
    ```
    %APPDATA%\Claude\claude_desktop_config.json
    ```
  On Linux:
    ```
    ~/.config/Claude/claude_desktop_config.json
    ```

### Claude Code
  Run the following from the command line:
  ```bash
  $ claude mcp add bsky-mcp -e ATPROTO_DID <YOUR DID> -e ATPROTO_APP_PASSWORD <YOUR APP PASSWORD> -- /path/to/binary/bsky-mcp-<YOUR PLATFORM>
  ```

  If you have already added the server to your Claude Desktop config, you can run:
  ```bash
  $ claude mcp add-from-claude-desktop
  ```
  and select bsky-mcp from the list.

### Gemini CLI
  Add the following to your `settings.json`:
  ```json
  {
    "mcpServers": {
      "bsky-mcp": {
        "command": "/path/to/binary/bsky-mcp-<YOUR PLATFORM>",
        "env": {
          "ATPROTO_DID": "your_did_here",
          "ATPROTO_APP_PASSWORD": "your_app_password_here"
        }
      }
    }
  }
  ```
  <details>
  <summary>Where is settings.json?</summary>
  On MacOS/Linux:
    ```
    ~/.config/gemini/settings.json
    ```
  On Windows:
    ```
    %USERPROFILE%\.gemini\settings.json
    ```
  </details>
