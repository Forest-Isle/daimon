package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/Forest-Isle/IronClaw/internal/knowledge"
	"github.com/Forest-Isle/IronClaw/internal/memory"
	"github.com/Forest-Isle/IronClaw/internal/skill"
	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
)

// ServerDeps holds optional dependencies that MCP server tools use.
// Nil fields disable the corresponding tool.
type ServerDeps struct {
	MemoryStore memory.Store
	Knowledge   knowledge.Searcher
	SkillMgr    *skill.Manager
}

// RegisterDefaultTools registers all built-in IronClaw tools on the server.
// Tools with nil dependencies are silently skipped.
func RegisterDefaultTools(srv *Server, deps ServerDeps) {
	if deps.MemoryStore != nil {
		srv.RegisterTool(
			mcp.NewTool("ironclaw_memory_search",
				mcp.WithDescription("Search IronClaw's memory store for relevant entries"),
				mcp.WithString("query", mcp.Description("Search query text"), mcp.Required()),
				mcp.WithNumber("limit", mcp.Description("Maximum number of results (default 5)")),
			),
			makeMemorySearchHandler(deps.MemoryStore),
		)
		slog.Info("mcp server: registered ironclaw_memory_search")
	}

	if deps.Knowledge != nil {
		srv.RegisterTool(
			mcp.NewTool("ironclaw_knowledge_query",
				mcp.WithDescription("Query IronClaw's knowledge base for relevant information"),
				mcp.WithString("query", mcp.Description("Natural language query"), mcp.Required()),
				mcp.WithNumber("limit", mcp.Description("Maximum number of results (default 5)")),
			),
			makeKnowledgeQueryHandler(deps.Knowledge),
		)
		slog.Info("mcp server: registered ironclaw_knowledge_query")
	}

	if deps.SkillMgr != nil {
		srv.RegisterTool(
			mcp.NewTool("ironclaw_skill_list",
				mcp.WithDescription("List all available IronClaw skills"),
			),
			makeSkillListHandler(deps.SkillMgr),
		)
		slog.Info("mcp server: registered ironclaw_skill_list")
	}
}

// makeMemorySearchHandler returns a tool handler that searches the memory store.
func makeMemorySearchHandler(store memory.Store) mcpserver.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		query, _ := args["query"].(string)
		if query == "" {
			return mcp.NewToolResultError("query parameter is required"), nil
		}

		limit := 5
		if l, ok := args["limit"].(float64); ok && l > 0 {
			limit = int(l)
		}

		results, err := store.Search(ctx, memory.SearchQuery{
			Text:  query,
			Limit: limit,
		})
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("memory search failed: %v", err)), nil
		}

		if len(results) == 0 {
			return mcp.NewToolResultText("No matching memories found."), nil
		}

		type resultEntry struct {
			ID      string  `json:"id"`
			Content string  `json:"content"`
			Score   float64 `json:"score"`
		}
		entries := make([]resultEntry, len(results))
		for i, r := range results {
			entries[i] = resultEntry{
				ID:      r.Entry.ID,
				Content: r.Entry.Content,
				Score:   r.Score,
			}
		}
		data, _ := json.Marshal(entries)
		return mcp.NewToolResultText(string(data)), nil
	}
}

// makeKnowledgeQueryHandler returns a tool handler that queries the knowledge base.
func makeKnowledgeQueryHandler(searcher knowledge.Searcher) mcpserver.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		query, _ := args["query"].(string)
		if query == "" {
			return mcp.NewToolResultError("query parameter is required"), nil
		}

		limit := 5
		if l, ok := args["limit"].(float64); ok && l > 0 {
			limit = int(l)
		}

		results, err := searcher.Search(ctx, knowledge.KnowledgeQuery{
			Text:  query,
			Limit: limit,
		})
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("knowledge query failed: %v", err)), nil
		}

		if len(results) == 0 {
			return mcp.NewToolResultText("No matching knowledge found."), nil
		}

		type resultEntry struct {
			Source  string  `json:"source"`
			Content string  `json:"content"`
			Score   float64 `json:"score"`
		}
		entries := make([]resultEntry, len(results))
		for i, r := range results {
			entries[i] = resultEntry{
				Source:  r.Chunk.SourceURI,
				Content: r.Chunk.Content,
				Score:   r.Score,
			}
		}
		data, _ := json.Marshal(entries)
		return mcp.NewToolResultText(string(data)), nil
	}
}

// makeSkillListHandler returns a tool handler that lists available skills.
func makeSkillListHandler(mgr *skill.Manager) mcpserver.ToolHandlerFunc {
	return func(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		skills := mgr.All()
		if len(skills) == 0 {
			return mcp.NewToolResultText("No skills available."), nil
		}

		type skillEntry struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			Version     string `json:"version,omitempty"`
		}
		entries := make([]skillEntry, len(skills))
		for i, s := range skills {
			entries[i] = skillEntry{
				Name:        s.Name,
				Description: s.Description,
				Version:     s.Version,
			}
		}
		data, _ := json.Marshal(entries)
		return mcp.NewToolResultText(string(data)), nil
	}
}
