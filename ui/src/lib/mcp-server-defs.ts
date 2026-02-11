/** Pre-configured MCP server definitions for one-click install. */

export interface McpServerDef {
  id: string
  name: string
  description: string
  command: string
  args: string[]
  /** Credentials the user must supply before install. */
  credentials?: McpCredential[]
}

export interface McpCredential {
  envVar: string
  label: string
  type: "text" | "password"
  placeholder?: string
}

export const mcpServerDefs: McpServerDef[] = [
  {
    id: "github",
    name: "GitHub",
    description: "Repos, issues, PRs, search",
    command: "npx",
    args: ["-y", "@modelcontextprotocol/server-github"],
    credentials: [
      {
        envVar: "GITHUB_PERSONAL_ACCESS_TOKEN",
        label: "GitHub Personal Access Token",
        type: "password",
        placeholder: "ghp_...",
      },
    ],
  },
  {
    id: "filesystem",
    name: "Filesystem",
    description: "Read/write local files",
    command: "npx",
    args: ["-y", "@modelcontextprotocol/server-filesystem", "/tmp"],
  },
  {
    id: "postgres",
    name: "Postgres",
    description: "Query databases",
    command: "npx",
    args: ["-y", "@modelcontextprotocol/server-postgres"],
    credentials: [
      {
        envVar: "POSTGRES_CONNECTION_STRING",
        label: "Connection String",
        type: "password",
        placeholder: "postgresql://user:pass@host:5432/db",
      },
    ],
  },
  {
    id: "brave-search",
    name: "Brave Search",
    description: "Web search via Brave API",
    command: "npx",
    args: ["-y", "@modelcontextprotocol/server-brave-search"],
    credentials: [
      {
        envVar: "BRAVE_API_KEY",
        label: "Brave API Key",
        type: "password",
      },
    ],
  },
  {
    id: "slack",
    name: "Slack",
    description: "Channels, messages, search",
    command: "npx",
    args: ["-y", "@modelcontextprotocol/server-slack"],
    credentials: [
      {
        envVar: "SLACK_BOT_TOKEN",
        label: "Slack Bot Token",
        type: "password",
        placeholder: "xoxb-...",
      },
    ],
  },
  {
    id: "gdrive",
    name: "Google Drive",
    description: "Docs, sheets, files",
    command: "npx",
    args: ["-y", "@modelcontextprotocol/server-gdrive"],
    credentials: [
      {
        envVar: "GDRIVE_CREDENTIALS",
        label: "Service Account JSON Key",
        type: "password",
      },
    ],
  },
]
