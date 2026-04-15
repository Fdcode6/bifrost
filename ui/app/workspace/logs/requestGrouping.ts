import type { LogEntry, LogStats, Pagination } from "@/lib/types/logs";

export interface VisibleRequestGroup {
	groupId: string;
	attempts: LogEntry[];
	latestAttempt: LogEntry;
	finalAttempt: LogEntry | null;
	finalAttemptVisible: boolean;
}

export interface GroupDisplayTarget {
	provider: string;
	model: string;
	selectedKeyId: string | null;
	routeLayerIndex: number | null;
	layerLabel: string;
}

export interface RequestGroupDisplayState {
	groupId: string;
	attempts: LogEntry[];
	visibleAttemptCount: number;
	latestAttempt: LogEntry;
	finalAttempt: LogEntry | null;
	finalAttemptVisible: boolean;
	isPartial: boolean;
	finalStatus: LogEntry["status"] | null;
	finalTarget: GroupDisplayTarget | null;
}

export function canUseGroupedRequestView(pagination: Pick<Pagination, "sort_by" | "order"> | null | undefined): boolean {
	return pagination?.sort_by === "timestamp" && pagination.order === "desc";
}

export function buildVisibleRequestGroups(logs: LogEntry[]): VisibleRequestGroup[] {
	const groups = new Map<string, LogEntry[]>();

	for (const log of logs) {
		const groupId = getGroupId(log);
		const existing = groups.get(groupId);
		if (existing) {
			existing.push(log);
		} else {
			groups.set(groupId, [log]);
		}
	}

	return Array.from(groups.entries())
		.map(([groupId, attempts]) => {
			const sortedAttempts = [...attempts].sort(compareAttemptSequenceAsc);
			const latestAttempt = [...attempts].sort(compareVisibleActivityDesc)[0];
			const finalAttempt = [...attempts].filter((attempt) => attempt.is_final_attempt).sort(compareFinalAttemptDesc)[0] ?? null;

			return {
				groupId,
				attempts: sortedAttempts,
				latestAttempt,
				finalAttempt,
				finalAttemptVisible: finalAttempt !== null,
			};
		})
		.sort((left, right) => compareVisibleActivityDesc(left.latestAttempt, right.latestAttempt));
}

export function buildGroupDisplayState(group: VisibleRequestGroup): RequestGroupDisplayState {
	const finalTarget =
		group.finalAttemptVisible && group.finalAttempt
			? {
					provider: group.finalAttempt.provider,
					model: group.finalAttempt.model,
					selectedKeyId: group.finalAttempt.selected_key_id ?? null,
					routeLayerIndex: group.finalAttempt.route_layer_index ?? null,
					layerLabel: formatLayerLabel(group.finalAttempt.route_layer_index),
				}
			: null;

	return {
		groupId: group.groupId,
		attempts: group.attempts,
		visibleAttemptCount: group.attempts.length,
		latestAttempt: group.latestAttempt,
		finalAttempt: group.finalAttemptVisible ? group.finalAttempt : null,
		finalAttemptVisible: group.finalAttemptVisible,
		isPartial: !group.finalAttemptVisible,
		finalStatus: group.finalAttemptVisible ? (group.finalAttempt?.status ?? null) : null,
		finalTarget,
	};
}

export function formatAttemptRateSummary(
	stats: Pick<LogStats, "completed_attempts" | "successful_attempts" | "success_rate" | "total_requests">,
): string {
	const completedAttempts = normalizeCount(stats.completed_attempts ?? stats.total_requests);
	const successfulAttempts = normalizeCount(stats.successful_attempts ?? deriveSuccessfulCount(completedAttempts, stats.success_rate));

	return formatRateSummary(successfulAttempts, completedAttempts, stats.success_rate);
}

export function formatRequestRateSummary(
	stats: Pick<LogStats, "completed_request_groups" | "successful_request_groups" | "request_success_rate">,
): string {
	const completedRequests = normalizeCount(stats.completed_request_groups);
	const successfulRequests = normalizeCount(
		stats.successful_request_groups ?? deriveSuccessfulCount(completedRequests, stats.request_success_rate),
	);

	return formatRateSummary(successfulRequests, completedRequests, stats.request_success_rate);
}

export function formatLayerLabel(routeLayerIndex: number | null | undefined): string {
	return routeLayerIndex == null ? "Unlayered" : `Layer ${routeLayerIndex + 1}`;
}

function getGroupId(log: LogEntry): string {
	return log.group_id ?? log.parent_request_id ?? log.id;
}

function compareAttemptSequenceAsc(left: LogEntry, right: LogEntry): number {
	const leftSequence = left.attempt_sequence ?? Number.MAX_SAFE_INTEGER;
	const rightSequence = right.attempt_sequence ?? Number.MAX_SAFE_INTEGER;
	if (leftSequence !== rightSequence) {
		return leftSequence - rightSequence;
	}

	if (left.fallback_index !== right.fallback_index) {
		return left.fallback_index - right.fallback_index;
	}

	const timestampDelta = getTimestamp(left) - getTimestamp(right);
	if (timestampDelta !== 0) {
		return timestampDelta;
	}

	return left.id.localeCompare(right.id);
}

function compareVisibleActivityDesc(left: LogEntry, right: LogEntry): number {
	const timestampDelta = getTimestamp(right) - getTimestamp(left);
	if (timestampDelta !== 0) {
		return timestampDelta;
	}

	if (left.fallback_index !== right.fallback_index) {
		return right.fallback_index - left.fallback_index;
	}

	return right.id.localeCompare(left.id);
}

function compareFinalAttemptDesc(left: LogEntry, right: LogEntry): number {
	if (left.fallback_index !== right.fallback_index) {
		return right.fallback_index - left.fallback_index;
	}

	const timestampDelta = getTimestamp(right) - getTimestamp(left);
	if (timestampDelta !== 0) {
		return timestampDelta;
	}

	return right.id.localeCompare(left.id);
}

function getTimestamp(log: LogEntry): number {
	const timestamp = Date.parse(log.timestamp);
	return Number.isNaN(timestamp) ? 0 : timestamp;
}

function normalizeCount(value: number | null | undefined): number {
	return Number.isFinite(value) ? Math.max(0, value as number) : 0;
}

function deriveSuccessfulCount(total: number, rate: number | null | undefined): number {
	if (total <= 0 || !Number.isFinite(rate)) {
		return 0;
	}

	return Math.round((total * (rate as number)) / 100);
}

function formatRateSummary(successfulCount: number, completedCount: number, rate: number | null | undefined): string {
	const normalizedRate = Number.isFinite(rate) ? (rate as number) : completedCount > 0 ? (successfulCount / completedCount) * 100 : 0;

	return `${successfulCount.toLocaleString()} / ${completedCount.toLocaleString()} (${normalizedRate.toFixed(2)}%)`;
}
