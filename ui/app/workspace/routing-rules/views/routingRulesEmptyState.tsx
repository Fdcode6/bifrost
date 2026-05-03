"use client";

import { Button } from "@/components/ui/button";
import { Route } from "lucide-react";
import { ArrowUpRight } from "lucide-react";

const ROUTING_RULES_DOCS_URL = "https://docs.getbifrost.ai/providers/routing-rules";

interface RoutingRulesEmptyStateProps {
	onAddClick: () => void;
	canCreate?: boolean;
}

export function RoutingRulesEmptyState({ onAddClick, canCreate = true }: RoutingRulesEmptyStateProps) {
	return (
		<div className="flex min-h-[80vh] w-full flex-col items-center justify-center gap-4 py-16 text-center" data-testid="routing-rules-empty-state">
			<div className="text-muted-foreground">
				<Route className="h-[5.5rem] w-[5.5rem]" strokeWidth={1} />
			</div>
			<div className="flex flex-col gap-1">
				<h1 className="text-muted-foreground text-xl font-medium">通过 CEL 条件控制请求分流</h1>
				<div className="text-muted-foreground mx-auto mt-2 max-w-[600px] text-sm font-normal">
					创建基于 CEL 的规则，按 Model、Provider、预算或自定义属性分配请求。
				</div>
				<div className="mx-auto mt-6 flex flex-row flex-wrap items-center justify-center gap-2">
					<Button
						variant="outline"
						aria-label="查看路由规则文档（新窗口打开）"
						data-testid="routing-rules-empty-read-more"
						onClick={() => {
							window.open(`${ROUTING_RULES_DOCS_URL}?utm_source=bfd`, "_blank", "noopener,noreferrer");
						}}
					>
						查看文档 <ArrowUpRight className="text-muted-foreground h-3 w-3" />
					</Button>
					<Button
						aria-label="创建第一条路由规则"
						data-testid="create-routing-rule-btn"
						onClick={onAddClick}
						disabled={!canCreate}
					>
						新建规则
					</Button>
				</div>
			</div>
		</div>
	);
}
