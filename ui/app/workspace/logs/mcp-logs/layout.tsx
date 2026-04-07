import { createFileRoute } from "@tanstack/react-router";
import MCPLogsPage from "@/app/workspace/mcp-logs/page";

export const Route = createFileRoute("/workspace/logs/mcp-logs")({
	component: MCPLogsPage,
});
