import { getSiteTitle } from "./siteMetadata";

type TitleDocumentLike = {
	title: string;
};

const workspaceRouteTitles: Record<string, string> = {
	"/workspace/dashboard": "Dashboard",
	"/workspace/logs": "LLM Logs",
	"/workspace/mcp-logs": "MCP Logs",
	"/workspace/observability": "Connectors",
	"/workspace/logs/connectors": "Connectors",
	"/workspace/logs/dashboard": "Dashboard",
	"/workspace/logs/mcp-logs": "MCP Logs",
	"/workspace/providers": "Model Providers",
	"/workspace/model-catalog": "Model Catalog",
	"/workspace/model-limits": "Budgets & Limits",
	"/workspace/providers/model-limits": "Budgets & Limits",
	"/workspace/routing-rules": "Routing Rules",
	"/workspace/providers/routing-rules": "Routing Rules",
	"/workspace/custom-pricing": "Pricing Config",
	"/workspace/mcp-gateway": "MCP Gateway",
	"/workspace/mcp-registry": "MCP Catalog",
	"/workspace/mcp-tool-groups": "Tool Groups",
	"/workspace/mcp-auth-config": "Auth Config",
	"/workspace/mcp-settings": "MCP Settings",
	"/workspace/plugins": "Plugins",
	"/workspace/governance": "Governance",
	"/workspace/governance/virtual-keys": "Virtual Keys",
	"/workspace/governance/users": "Users",
	"/workspace/governance/teams": "Teams",
	"/workspace/governance/customers": "Customers",
	"/workspace/governance/rbac": "Roles & Permissions",
	"/workspace/virtual-keys": "Virtual Keys",
	"/workspace/rbac": "Roles & Permissions",
	"/workspace/scim": "User Provisioning",
	"/workspace/audit-logs": "Audit Logs",
	"/workspace/guardrails": "Guardrails",
	"/workspace/guardrails/configuration": "Rules",
	"/workspace/guardrails/providers": "Providers",
	"/workspace/cluster": "Cluster Config",
	"/workspace/adaptive-routing": "Adaptive Routing",
	"/workspace/prompt-repo": "Prompt Repository",
	"/workspace/prompt-repo/prompts": "Prompts",
	"/workspace/prompt-repo/deployments": "Deployments",
	"/workspace/config": "Settings",
	"/workspace/config/client-settings": "Client Settings",
	"/workspace/config/caching": "Caching",
	"/workspace/config/security": "Security",
	"/workspace/config/proxy": "Proxy",
	"/workspace/config/api-keys": "API Keys",
	"/workspace/config/performance-tuning": "Performance Tuning",
	"/workspace/config/logging": "Logs Settings",
	"/workspace/config/mcp-gateway": "MCP Gateway",
	"/workspace/config/observability": "Observability",
	"/workspace/config/pricing-config": "Pricing Config",
	"/workspace/config/large-payload": "Large Payload",
	"/workspace/docs": "Documentation",
	"/workspace/alert-channels": "Alert Channels",
	"/workspace/pii-redactor": "PII Redactor",
	"/workspace/pii-redactor/providers": "PII Redactor Providers",
	"/workspace/pii-redactor/rules": "PII Redactor Rules",
};

const wordOverrides: Record<string, string> = {
	api: "API",
	mcp: "MCP",
	pii: "PII",
	rbac: "RBAC",
	llm: "LLM",
};

const humanizeSegment = (segment: string) =>
	segment
		.split("-")
		.filter(Boolean)
		.map((part) => wordOverrides[part] ?? part.charAt(0).toUpperCase() + part.slice(1))
		.join(" ");

const fallbackWorkspaceTitle = (pathname: string) => {
	const segments = pathname.split("/").filter(Boolean);
	if (segments.length <= 1) {
		return undefined;
	}

	return humanizeSegment(segments[segments.length - 1]);
};

export const resolveRouteTitle = (pathname: string) => {
	if (!pathname.startsWith("/workspace")) {
		return undefined;
	}

	if (pathname === "/workspace") {
		return undefined;
	}

	return workspaceRouteTitles[pathname] ?? fallbackWorkspaceTitle(pathname);
};

export const getDocumentTitle = (pathname: string, siteTitle = getSiteTitle()) => {
	const routeTitle = resolveRouteTitle(pathname);
	if (!routeTitle) {
		return siteTitle;
	}

	return `${routeTitle} | ${siteTitle}`;
};

export const syncDocumentTitle = (title: string, doc?: TitleDocumentLike) => {
	if (!doc || doc.title === title) {
		return false;
	}

	doc.title = title;
	return true;
};
