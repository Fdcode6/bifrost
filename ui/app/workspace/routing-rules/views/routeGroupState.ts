import type { RouteGroupFormData, RoutingTargetFormData } from "@/lib/types/routingRules";

export interface RouteGroupProviderKeyOption {
	id: string;
	name: string;
}

export interface RouteGroupProviderData {
	name: string;
	keys: RouteGroupProviderKeyOption[];
}

export function updateRouteGroupTarget(
	group: RouteGroupFormData,
	index: number,
	patch: Partial<RoutingTargetFormData>,
): RouteGroupFormData {
	return {
		...group,
		targets: group.targets.map((target, targetIndex) => (targetIndex === index ? { ...target, ...patch } : target)),
	};
}

export function getRouteGroupAvailableKeys(providersData: RouteGroupProviderData[], providerName: string): RouteGroupProviderKeyOption[] {
	if (!providerName) {
		return [];
	}
	return providersData.find((provider) => provider.name === providerName)?.keys ?? [];
}

export function shouldShowRouteGroupKeySelector(target: RoutingTargetFormData, availableKeys: RouteGroupProviderKeyOption[]): boolean {
	return !!target.provider && (availableKeys.length > 0 || !!target.key_id);
}
