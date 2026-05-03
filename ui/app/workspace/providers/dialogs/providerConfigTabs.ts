export type ProviderConfigTab = {
	id: string;
	label: string;
};

type ProviderConfigTabsOptions = {
	hasCustomProviderConfig: boolean;
	hasGovernanceAccess: boolean;
	isOpenAI: boolean;
	isAnthropicFamily: boolean;
};

export function availableProviderConfigTabs({
	hasCustomProviderConfig,
	hasGovernanceAccess,
	isOpenAI,
	isAnthropicFamily,
}: ProviderConfigTabsOptions): ProviderConfigTab[] {
	const tabs: ProviderConfigTab[] = [];
	if (hasCustomProviderConfig) {
		tabs.push({
			id: "api-structure",
			label: "API Structure",
		});
	}
	tabs.push({
		id: "network",
		label: "Network",
	});
	tabs.push({
		id: "proxy",
		label: "Proxy",
	});
	tabs.push({
		id: "performance",
		label: "Performance",
	});
	tabs.push({
		id: "pricing-overrides",
		label: "价格覆盖",
	});
	if (hasGovernanceAccess) {
		tabs.push({
			id: "governance",
			label: "Governance",
		});
	}
	if (isAnthropicFamily) {
		tabs.push({
			id: "beta-headers",
			label: "Beta Headers",
		});
	}
	tabs.push({
		id: "debugging",
		label: "Debugging",
	});
	if (isOpenAI) {
		tabs.push({
			id: "openai-config",
			label: "OpenAI Config",
		});
	}
	return tabs;
}
