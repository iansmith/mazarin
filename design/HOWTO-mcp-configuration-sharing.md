# HOWTO: Share MCP Configuration Between Claude Instances

## Current MCP Servers in This Environment

Two MCP servers are available:

| Server | Tools Prefix | Status |
|--------|-------------|--------|
| **Linear** | `mcp__linear-server__*` | Active — works out of the box |
| **Google Drive** | `mcp__claude_ai_Google_Drive__*` | Needs OAuth (`mcp__claude_ai_Google_Drive__authenticate`) |

## How MCP Is Configured

The Linear MCP server is a **built-in Claude Code integration** — it ships with Claude Code and does not require a separate `mcp.json` file. It is NOT configured via plugins, marketplace, or project-level settings. It appears automatically when your Claude account has Linear connected.

The Google Drive MCP server is also built-in but requires per-session OAuth authentication.

### What's NOT needed
- No `mcp.json` file in `~/.claude/` or `.claude/`
- No plugin installation
- No marketplace entry
- No project-level settings

### How authentication works
Claude Code's built-in MCP servers authenticate through your **claude.ai account** (the same account you're logged into Claude Code with). The Linear connection is account-level — once you've authorized Linear in Claude Code, it's available in all sessions across all projects.

### Files on disk (for reference)
```
~/.claude/settings.json          # User settings (no MCP config — MCP is built-in)
.claude/settings.local.json      # Project-local permissions (no MCP config)
~/.claude/mcp-needs-auth-cache.json  # Google Drive OAuth cache (ephemeral)
```

## How to Share With Another Claude Instance

### Scenario A: Same user, different machine/project

No setup needed. The built-in MCP servers follow your Claude account. Log into Claude Code with the same account, and Linear MCP tools will be available automatically.

### Scenario B: Different user / different Claude account

The other user needs to connect their own Linear account to Claude Code. Steps:

1. **Connect Linear to Claude Code**: In the other user's Claude Code session, they can use any Linear MCP tool — Claude Code will prompt them to authenticate Linear the first time. Alternatively, they can run:
   ```
   /mcp
   ```
   and follow the prompts to add the Linear integration.

2. **Verify**: The other user can verify by checking that `mcp__linear-server__list_issues` (or any Linear tool) is available in their tool list.

### Scenario C: Programmatic / CI / headless use

For non-interactive Claude instances (CI, scripts, API), use a Linear API key:

1. Generate a Linear API key from Linear Settings → API
2. Set it as `LINEAR_API_KEY` in the environment
3. The Linear MCP server uses this when available

## How to Add Custom MCP Servers

If you need to add a custom (non-built-in) MCP server, use the `/mcp` command:

```
/mcp add <name> <command> [args...]
```

This writes to `~/.claude/settings.json` under an `mcpServers` key. Example for a hypothetical local MCP server:

```json
{
  "mcpServers": {
    "my-server": {
      "command": "node",
      "args": ["/path/to/server.js"]
    }
  }
}
```

To share custom MCP servers with another Claude instance, share the `mcpServers` block from `~/.claude/settings.json`.

## Quick Reference: Available Linear MCP Tools

The key tools the other Claude will need:

```
mcp__linear-server__list_issues    — Search/filter issues
mcp__linear-server__get_issue      — Get issue details  
mcp__linear-server__save_issue     — Create/update issues
mcp__linear-server__save_comment   — Comment on issues
mcp__linear-server__list_comments  — List comments
mcp__linear-server__search_documentation — Search Linear docs
mcp__linear-server__get_team       — Get team info
mcp__linear-server__get_project    — Get project info
mcp__linear-server__get_user       — Get user info ("me" = current user)
mcp__linear-server__list_teams     — List teams
mcp__linear-server__list_projects  — List projects
mcp__linear-server__list_users     — List users
```

## Verifying MCP Is Working

In the target Claude session, ask:
```
list my open issues in Linear
```

If MCP is configured, Claude will call `mcp__linear-server__list_issues` with `assignee: "me"`. If not, Claude will say it can't access Linear.
