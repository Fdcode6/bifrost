import type { RouteGroupFormData, RoutingTargetFormData } from "@/lib/types/routingRules";

export function updateRouteGroupTarget(
	group: RouteGroupFormData,
	index: number,
	patch: Partial<RoutingTargetFormData>,
): RouteGroupFormData {
	return {
		...group,
		targets: group.targets.map((target, targetIndex) => (
			targetIndex === index ? { ...target, ...patch } : target
		)),
	};
}
