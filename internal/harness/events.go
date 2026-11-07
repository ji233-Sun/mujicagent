package harness

import (
	"encoding/json"
	"strings"
)

// ToolEvent is a partial update. Nil payloads preserve earlier fields; a JSON
// null is an explicit empty value. Result snapshots replace, never append.
// Protocol adapters own classification; consumers never inspect wire records.
type ToolEvent struct {
	Name, Kind, Title, Server string
	ParentID                  string
	Arguments, Result         json.RawMessage
	Error                     *string
}

// NativeToolKind maps native aliases, never inferring an MCP server identity.
func NativeToolKind(name string) string {
	name = strings.ToLower(name)
	if i := strings.LastIndex(name, "."); i >= 0 {
		name = name[i+1:]
	}
	switch name {
	case "bash", "execute", "script", "waitforscript", "shell", "shell_command", "run_command":
		return "execute"
	case "edit", "multiedit", "write", "create", "applypatch", "apply_patch", "edit_file", "write_file", "create_file", "str_replace_editor":
		return "edit"
	case "read", "ls", "read_file", "list_directory":
		return "read"
	case "grep", "glob", "find", "websearch", "web_search":
		return "search"
	case "fetchurl", "webfetch":
		return "fetch"
	case "todowrite", "todo", "update_plan":
		return "plan"
	case "askuser", "userask", "ask_user", "request_user_input":
		return "ask"
	case "compact", "compactcontext", "compact_context":
		return "compaction"
	default:
		return "other"
	}
}
