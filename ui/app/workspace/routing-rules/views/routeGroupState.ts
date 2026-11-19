import type { RouteGroup, RouteGroupFormData, RoutingTargetFormData } from "@/lib/types/routingRules";

export function toRouteGroupForm(group: RouteGroup): RouteGroupFormData {
	return {
		...group,
		fallback_only: group.fallback_only ?? false,
		targets: group.targets.map((target) => ({
			...target,
			provider: target.provider || "",
			model: target.model || "",
			key_id: target.key_id || "",
		})),
	};
}

export function toRouteGroupRequest(group: RouteGroupFormData): RouteGroup {
	return {
		...group,
		targets: group.targets.map(({ provider, model, key_id, weight, primary_attempt_timeout_ms }) => ({
			provider,
			model,
			weight,
			key_id: key_id || undefined,
			primary_attempt_timeout_ms,
		})),
	};
}

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
