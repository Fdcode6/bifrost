"use client";

import { LogDetailSheet } from "@/app/workspace/logs/sheets/logDetailsSheet";
import { LOGS_ANALYTICS_DEFAULT_OPEN, LOGS_PAGE_CHART_GRID_CLASS, LOGS_PAGE_STATS_GRID_CLASS } from "@/app/workspace/logs/layoutConfig";
import {
	buildGroupDisplayState,
	buildVisibleRequestGroups,
	canUseGroupedRequestView,
	type RequestGroupDisplayState,
} from "@/app/workspace/logs/requestGrouping";
import { createColumns } from "@/app/workspace/logs/views/columns";
import { EmptyState } from "@/app/workspace/logs/views/emptyState";
import { FinalSuccessDistributionCard } from "@/app/workspace/logs/views/finalSuccessDistributionCard";
import { LogsDataTable } from "@/app/workspace/logs/views/logsTable";
import { LogsVolumeChart } from "@/app/workspace/logs/views/logsVolumeChart";
import { RequestGroupRows } from "@/app/workspace/logs/views/requestGroupRows";
import FullPageLoader from "@/components/fullPageLoader";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Card, CardContent } from "@/components/ui/card";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible";
import { Skeleton } from "@/components/ui/skeleton";
import { useWebSocket } from "@/hooks/useWebSocket";
import {
	getErrorMessage,
	useDeleteLogsMutation,
	useGetAvailableFilterDataQuery,
	useLazyGetFinalSuccessDistributionQuery,
	useLazyGetLogsHistogramQuery,
	useLazyGetLogsQuery,
	useLazyGetLogsStatsQuery,
} from "@/lib/store";
import { useLazyGetLogByIdQuery } from "@/lib/store/apis/logsApi";
import type {
	ChatMessage,
	ChatMessageContent,
	ContentBlock,
	FinalSuccessDistributionDimension,
	FinalSuccessDistributionResponse,
	LogEntry,
	LogFilters,
	LogsHistogramResponse,
	LogStats,
	Pagination,
} from "@/lib/types/logs";
import { cn } from "@/lib/utils";
import { dateUtils } from "@/lib/types/logs";
import { RbacOperation, RbacResource, useRbac } from "@enterprise/lib";
import { AlertCircle, BarChart, CheckCircle, ChevronDown, Clock, DollarSign, Hash } from "lucide-react";
import { parseAsArrayOf, parseAsBoolean, parseAsInteger, parseAsString, useQueryStates } from "nuqs";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";

function formatRatePercentage(value: number | null | undefined): string {
	return Number.isFinite(value) ? `${(value as number).toFixed(2)}%` : "-";
}

function formatCountSummary(successful: number | null | undefined, total: number | null | undefined, noun: string): string {
	const normalizedSuccessful = Number.isFinite(successful) ? Math.max(0, successful as number) : 0;
	const normalizedTotal = Number.isFinite(total) ? Math.max(0, total as number) : 0;
	return `${normalizedSuccessful.toLocaleString()} / ${normalizedTotal.toLocaleString()} ${noun}`;
}

function formatLatencyValue(value: number | null | undefined): string {
	if (!Number.isFinite(value)) {
		return "-";
	}

	const latencyMs = value as number;
	if (latencyMs >= 10000) {
		return `${(latencyMs / 1000).toFixed(1)}s`;
	}
	if (latencyMs >= 1000) {
		return `${(latencyMs / 1000).toFixed(2)}s`;
	}
	return `${latencyMs.toFixed(0)}ms`;
}

export default function LogsPage() {
	const [logs, setLogs] = useState<LogEntry[]>([]);
	const [totalItems, setTotalItems] = useState(0); // changes with filters
	const [stats, setStats] = useState<LogStats | null>(null);
	const [histogram, setHistogram] = useState<LogsHistogramResponse | null>(null);
	const [finalDistribution, setFinalDistribution] = useState<FinalSuccessDistributionResponse | null>(null);
	const [initialLoading, setInitialLoading] = useState(true); // on initial load
	const [fetchingLogs, setFetchingLogs] = useState(false); // on pagination/filters change
	const [fetchingStats, setFetchingStats] = useState(false); // on stats fetch
	const [fetchingHistogram, setFetchingHistogram] = useState(false); // on histogram fetch
	const [fetchingFinalDistribution, setFetchingFinalDistribution] = useState(false);
	const [error, setError] = useState<string | null>(null);
	const [showEmptyState, setShowEmptyState] = useState(false);
	const [finalDistributionDimension, setFinalDistributionDimension] = useState<FinalSuccessDistributionDimension>("model");
	const [expandedGroupIds, setExpandedGroupIds] = useState<string[]>([]);
	const [isAnalyticsOpen, setIsAnalyticsOpen] = useState(LOGS_ANALYTICS_DEFAULT_OPEN);
	const [selectedGroupId, setSelectedGroupId] = useState<string | null>(null);
	const [selectedAttemptId, setSelectedAttemptId] = useState<string | null>(null);
	const [sheetMode, setSheetMode] = useState<"request" | "attempt">("attempt");

	const hasDeleteAccess = useRbac(RbacResource.Logs, RbacOperation.Delete);

	// RTK Query lazy hooks for manual triggering
	const [triggerGetLogs] = useLazyGetLogsQuery();
	const [triggerGetStats] = useLazyGetLogsStatsQuery();
	const [triggerGetHistogram] = useLazyGetLogsHistogramQuery();
	const [triggerGetFinalDistribution] = useLazyGetFinalSuccessDistributionQuery();
	const [deleteLogs] = useDeleteLogsMutation();

	const [isChartOpen, setIsChartOpen] = useState(true);
	const [triggerGetLogById] = useLazyGetLogByIdQuery();
	const [fetchedLog, setFetchedLog] = useState<LogEntry | null>(null);

	// Debouncing for streaming updates (client-side)
	const streamingUpdateTimeouts = useRef<Map<string, ReturnType<typeof setTimeout>>>(new Map());
	const groupedRefreshTimeout = useRef<ReturnType<typeof setTimeout> | null>(null);

	// Track if user has manually modified the time range
	const userModifiedTimeRange = useRef<boolean>(false);

	// Capture initial defaults on mount to detect shared URLs with custom time ranges
	const initialDefaults = useRef(dateUtils.getDefaultTimeRange());

	// Memoize default time range to prevent recalculation on every render
	// This is crucial to avoid triggering refetches when the sheet opens/closes
	const defaultTimeRange = useMemo(() => dateUtils.getDefaultTimeRange(), []);

	// Get fresh default time range for refresh logic
	const getDefaultTimeRange = () => dateUtils.getDefaultTimeRange();

	// URL state management with nuqs - all filters and pagination in URL
	const [urlState, setUrlState] = useQueryStates(
		{
			providers: parseAsArrayOf(parseAsString).withDefault([]),
			models: parseAsArrayOf(parseAsString).withDefault([]),
			status: parseAsArrayOf(parseAsString).withDefault([]),
			objects: parseAsArrayOf(parseAsString).withDefault([]),
			selected_key_ids: parseAsArrayOf(parseAsString).withDefault([]),
			virtual_key_ids: parseAsArrayOf(parseAsString).withDefault([]),
			routing_rule_ids: parseAsArrayOf(parseAsString).withDefault([]),
			routing_engine_used: parseAsArrayOf(parseAsString).withDefault([]),
			content_search: parseAsString.withDefault(""),
			start_time: parseAsInteger.withDefault(defaultTimeRange.startTime),
			end_time: parseAsInteger.withDefault(defaultTimeRange.endTime),
			limit: parseAsInteger.withDefault(25), // Default fallback, actual value calculated based on table height
			offset: parseAsInteger.withDefault(0),
			sort_by: parseAsString.withDefault("timestamp"),
			order: parseAsString.withDefault("desc"),
			live_enabled: parseAsBoolean.withDefault(true),
			missing_cost_only: parseAsBoolean.withDefault(false),
			metadata_filters: parseAsString.withDefault(""),
			selected_log: parseAsString.withDefault(""),
		},
		{
			history: "push",
			shallow: false,
		},
	);

	// Derive selectedLog: find in current logs array, or fetch by ID from API
	const selectedLogId = urlState.selected_log || null;
	const selectedLogFromData = useMemo(
		() => (selectedLogId ? (logs.find((l) => l.id === selectedLogId) ?? null) : null),
		[selectedLogId, logs],
	);

	const activeLogFetchId = useRef<string | null>(null);
	useEffect(() => {
		if (!selectedLogId || selectedLogFromData) {
			setFetchedLog(null);
			activeLogFetchId.current = null;
			return;
		}
		// Track which log ID this fetch is for to prevent stale responses
		const fetchId = selectedLogId;
		activeLogFetchId.current = fetchId;
		triggerGetLogById(selectedLogId).then((result) => {
			if (activeLogFetchId.current === fetchId) {
				if (result.data) {
					setFetchedLog(result.data);
				} else if (result.error) {
					setError(getErrorMessage(result.error));
				}
			}
		});
	}, [selectedLogId, selectedLogFromData, triggerGetLogById]);

	const selectedLog = selectedLogFromData ?? fetchedLog;

	useEffect(() => {
		if (!selectedLog) {
			setSelectedAttemptId(null);
			setSelectedGroupId(null);
			return;
		}

		setSelectedAttemptId(selectedLog.id);
		setSelectedGroupId(selectedLog.group_id ?? selectedLog.parent_request_id ?? selectedLog.id);
	}, [selectedLog]);

	// Refresh time range defaults on page focus/visibility
	useEffect(() => {
		const refreshDefaultsIfStale = () => {
			// Skip refresh if user has manually modified the time range
			if (userModifiedTimeRange.current) {
				return;
			}

			// Check if current time range matches the initial defaults (within tolerance)
			const startTimeDiff = Math.abs(urlState.start_time - initialDefaults.current.startTime);
			const endTimeDiff = Math.abs(urlState.end_time - initialDefaults.current.endTime);
			const tolerance = 5; // 5 seconds tolerance for slight timing differences

			// Only refresh if current values match the initial defaults
			// This preserves shared URLs with custom time ranges
			if (startTimeDiff <= tolerance && endTimeDiff <= tolerance) {
				const defaults = getDefaultTimeRange();
				const currentEndDiff = Math.abs(urlState.end_time - defaults.endTime);
				// If end time is more than 5 minutes old, refresh both
				if (currentEndDiff > 300) {
					setUrlState({
						start_time: defaults.startTime,
						end_time: defaults.endTime,
					});
					// Update baseline so subsequent focus events compare against refreshed defaults
					initialDefaults.current.startTime = defaults.startTime;
					initialDefaults.current.endTime = defaults.endTime;
				}
			}
		};

		const handleVisibilityChange = () => {
			if (!document.hidden) {
				refreshDefaultsIfStale();
			}
		};

		const handleFocus = () => {
			refreshDefaultsIfStale();
		};

		document.addEventListener("visibilitychange", handleVisibilityChange);
		window.addEventListener("focus", handleFocus);
		return () => {
			document.removeEventListener("visibilitychange", handleVisibilityChange);
			window.removeEventListener("focus", handleFocus);
		};
	}, [urlState.start_time, urlState.end_time, setUrlState]);

	// Convert URL state to filters and pagination for API calls
	const filters: LogFilters = useMemo(
		() => ({
			providers: urlState.providers,
			models: urlState.models,
			status: urlState.status,
			objects: urlState.objects,
			selected_key_ids: urlState.selected_key_ids,
			virtual_key_ids: urlState.virtual_key_ids,
			routing_rule_ids: urlState.routing_rule_ids,
			routing_engine_used: urlState.routing_engine_used,
			content_search: urlState.content_search,
			start_time: dateUtils.toISOString(urlState.start_time),
			end_time: dateUtils.toISOString(urlState.end_time),
			missing_cost_only: urlState.missing_cost_only,
			metadata_filters: urlState.metadata_filters
				? (() => {
						try {
							return JSON.parse(urlState.metadata_filters);
						} catch {
							return undefined;
						}
					})()
				: undefined,
		}),
		// Only re-derive filters when filter-related URL params change (not pagination)
		[
			urlState.providers,
			urlState.models,
			urlState.status,
			urlState.objects,
			urlState.selected_key_ids,
			urlState.virtual_key_ids,
			urlState.routing_rule_ids,
			urlState.routing_engine_used,
			urlState.content_search,
			urlState.start_time,
			urlState.end_time,
			urlState.missing_cost_only,
			urlState.metadata_filters,
		],
	);

	const pagination: Pagination = useMemo(
		() => ({
			limit: urlState.limit,
			offset: urlState.offset,
			sort_by: urlState.sort_by as "timestamp" | "latency" | "tokens" | "cost",
			order: urlState.order as "asc" | "desc",
		}),
		[urlState.limit, urlState.offset, urlState.sort_by, urlState.order],
	);

	const liveEnabled = urlState.live_enabled;
	const groupedViewEnabled = useMemo(() => canUseGroupedRequestView(pagination), [pagination]);

	const requestGroups = useMemo(
		() => (groupedViewEnabled ? buildVisibleRequestGroups(logs).map(buildGroupDisplayState) : []),
		[groupedViewEnabled, logs],
	);

	const selectedGroupState = useMemo<RequestGroupDisplayState | null>(() => {
		if (!selectedLog) {
			return null;
		}

		const targetGroupID = selectedGroupId ?? selectedLog.group_id ?? selectedLog.parent_request_id ?? selectedLog.id;
		const visibleGroup = requestGroups.find((group) => group.groupId === targetGroupID);
		if (visibleGroup) {
			return visibleGroup;
		}

		return {
			groupId: targetGroupID,
			attempts: [selectedLog],
			visibleAttemptCount: 1,
			latestAttempt: selectedLog,
			finalAttempt: null,
			finalAttemptVisible: false,
			isPartial: true,
			finalStatus: null,
			finalTarget: null,
		};
	}, [requestGroups, selectedGroupId, selectedLog]);

	// Helper to update filters in URL
	const setFilters = useCallback(
		(newFilters: LogFilters) => {
			// Mark time range as user-modified only if start_time or end_time actually changed
			if (newFilters.start_time !== filters.start_time || newFilters.end_time !== filters.end_time) {
				userModifiedTimeRange.current = true;
			}

			setUrlState({
				providers: newFilters.providers || [],
				models: newFilters.models || [],
				status: newFilters.status || [],
				objects: newFilters.objects || [],
				selected_key_ids: newFilters.selected_key_ids || [],
				virtual_key_ids: newFilters.virtual_key_ids || [],
				routing_rule_ids: newFilters.routing_rule_ids || [],
				routing_engine_used: newFilters.routing_engine_used || [],
				content_search: newFilters.content_search || "",
				start_time: newFilters.start_time ? dateUtils.toUnixTimestamp(new Date(newFilters.start_time)) : undefined,
				end_time: newFilters.end_time ? dateUtils.toUnixTimestamp(new Date(newFilters.end_time)) : undefined,
				missing_cost_only: newFilters.missing_cost_only ?? filters.missing_cost_only ?? false,
				metadata_filters: newFilters.metadata_filters ? JSON.stringify(newFilters.metadata_filters) : "",
				offset: 0,
			});
		},
		[setUrlState, filters],
	);

	// Helper to update pagination in URL
	const setPagination = useCallback(
		(newPagination: Pagination) => {
			setUrlState({
				limit: newPagination.limit,
				offset: newPagination.offset,
				sort_by: newPagination.sort_by,
				order: newPagination.order,
			});
		},
		[setUrlState],
	);

	// Handler for time range changes from the volume chart
	const handleTimeRangeChange = useCallback(
		(startTime: number, endTime: number) => {
			setUrlState({
				start_time: startTime,
				end_time: endTime,
				offset: 0,
			});
		},
		[setUrlState],
	);

	// Handler for resetting zoom to default 24h view
	const handleResetZoom = useCallback(() => {
		const now = Math.floor(Date.now() / 1000);
		const twentyFourHoursAgo = now - 24 * 60 * 60;
		setUrlState({
			start_time: twentyFourHoursAgo,
			end_time: now,
			offset: 0,
		});
	}, [setUrlState]);

	// Check if user has zoomed (time range is different from default 24h)
	const isZoomed = useMemo(() => {
		const currentRange = urlState.end_time - urlState.start_time;
		const defaultRange = 24 * 60 * 60; // 24 hours in seconds
		// Consider zoomed if range is less than 90% of default (to account for minor differences)
		return currentRange < defaultRange * 0.9;
	}, [urlState.start_time, urlState.end_time]);

	const latest = useRef({ logs, filters, pagination, showEmptyState, liveEnabled });
	useEffect(() => {
		latest.current = { logs, filters, pagination, showEmptyState, liveEnabled };
	}, [logs, filters, pagination, showEmptyState, liveEnabled]);

	const handleDelete = useCallback(
		async (log: LogEntry) => {
			try {
				await deleteLogs({ ids: [log.id] }).unwrap();
				setLogs((prevLogs) => prevLogs.filter((l) => l.id !== log.id));
				setTotalItems((prev) => prev - 1);
				// Clear selected log if it was the deleted one
				if (urlState.selected_log === log.id) {
					setUrlState({ selected_log: "" });
				}
			} catch (error) {
				setError(getErrorMessage(error));
			}
		},
		[deleteLogs, urlState.selected_log, setUrlState],
	);

	const selectRequestGroup = useCallback(
		(group: RequestGroupDisplayState) => {
			const targetLog = group.finalAttemptVisible && group.finalAttempt ? group.finalAttempt : group.latestAttempt;
			setSheetMode("request");
			setSelectedGroupId(group.groupId);
			setSelectedAttemptId(targetLog.id);
			setUrlState({ selected_log: targetLog.id }, { history: "replace" });
		},
		[setUrlState],
	);

	const selectRequestAttempt = useCallback(
		(group: RequestGroupDisplayState, attempt: LogEntry) => {
			setSheetMode("attempt");
			setSelectedGroupId(group.groupId);
			setSelectedAttemptId(attempt.id);
			setUrlState({ selected_log: attempt.id }, { history: "replace" });
		},
		[setUrlState],
	);

	const toggleRequestGroup = useCallback((groupId: string) => {
		setExpandedGroupIds((prev) => (prev.includes(groupId) ? prev.filter((id) => id !== groupId) : [...prev, groupId]));
	}, []);

	const updateExistingLog = useCallback((updatedLog: LogEntry) => {
		setLogs((prevLogs: LogEntry[]) => {
			return prevLogs.map((existingLog) => (existingLog.id === updatedLog.id ? updatedLog : existingLog));
		});

		// Update fetchedLog if it matches the updated log (for real-time detail sheet updates when log is not on current page)
		setFetchedLog((prev) => {
			if (prev && prev.id === updatedLog.id) {
				return updatedLog;
			}
			return prev;
		});
	}, []);

	const { isConnected: isSocketConnected, subscribe } = useWebSocket();

	// Cleanup timeouts on unmount
	useEffect(() => {
		const streamingTimeoutEntries = streamingUpdateTimeouts.current;

		return () => {
			streamingTimeoutEntries.forEach((timeout) => clearTimeout(timeout));
			streamingTimeoutEntries.clear();
			if (groupedRefreshTimeout.current) {
				clearTimeout(groupedRefreshTimeout.current);
			}
		};
	}, []);

	const fetchLogs = useCallback(async () => {
		setFetchingLogs(true);
		setError(null);

		try {
			const result = await triggerGetLogs({ filters, pagination });

			if (result.error) {
				const errorMessage = getErrorMessage(result.error);
				setError(errorMessage);
				setLogs([]);
				setTotalItems(0);
			} else if (result.data) {
				setLogs(result.data.logs || []);
				setTotalItems(result.data.stats.total_requests);
			}

			// Only set showEmptyState on initial load and only based on total logs
			if (initialLoading) {
				// Check if there are any logs globally, not just in the current filter
				setShowEmptyState(result.data ? !result.data.has_logs : true);
			}
		} catch {
			setError("Cannot fetch logs. Please check if logs are enabled in your Bifrost config.");
			setLogs([]);
			setTotalItems(0);
			setShowEmptyState(true);
		} finally {
			setFetchingLogs(false);
		}

		// eslint-disable-next-line react-hooks/exhaustive-deps
	}, [filters, pagination]);

	const fetchStats = useCallback(async () => {
		setFetchingStats(true);

		try {
			const result = await triggerGetStats({ filters });

			if (result.error) {
				// Don't show error for stats failure, just log it
				console.error("Failed to fetch stats:", result.error);
			} else if (result.data) {
				setStats(result.data);
			}
		} catch (error) {
			console.error("Failed to fetch stats:", error);
		} finally {
			setFetchingStats(false);
		}
	}, [filters, triggerGetStats]);

	const fetchHistogram = useCallback(async () => {
		setFetchingHistogram(true);

		try {
			const result = await triggerGetHistogram({ filters });

			if (result.error) {
				// Don't show error for histogram failure, just log it
				console.error("Failed to fetch histogram:", result.error);
			} else if (result.data) {
				setHistogram(result.data);
			}
		} catch (error) {
			console.error("Failed to fetch histogram:", error);
		} finally {
			setFetchingHistogram(false);
		}
	}, [filters, triggerGetHistogram]);

	const fetchFinalDistribution = useCallback(async () => {
		setFetchingFinalDistribution(true);

		try {
			const result = await triggerGetFinalDistribution({
				filters,
				groupBy: finalDistributionDimension,
			});

			if (result.error) {
				console.error("Failed to fetch final success distribution:", result.error);
			} else if (result.data) {
				setFinalDistribution(result.data);
			}
		} catch (distributionError) {
			console.error("Failed to fetch final success distribution:", distributionError);
		} finally {
			setFetchingFinalDistribution(false);
		}
	}, [filters, finalDistributionDimension, triggerGetFinalDistribution]);

	const scheduleGroupedRefresh = useCallback(() => {
		if (groupedRefreshTimeout.current) {
			clearTimeout(groupedRefreshTimeout.current);
		}

		groupedRefreshTimeout.current = setTimeout(() => {
			fetchLogs();
			fetchStats();
			fetchHistogram();
			fetchFinalDistribution();
			groupedRefreshTimeout.current = null;
		}, 150);
	}, [fetchFinalDistribution, fetchHistogram, fetchLogs, fetchStats]);

	// Helper to toggle live updates
	const handleLiveToggle = useCallback(
		(enabled: boolean) => {
			setUrlState({ live_enabled: enabled });
			// When re-enabling, refetch logs to get latest data
			if (enabled) {
				fetchLogs();
				fetchStats();
				fetchHistogram();
				fetchFinalDistribution();
			}
		},
		[setUrlState, fetchFinalDistribution, fetchHistogram, fetchLogs, fetchStats],
	);

	// Fetch logs when filters or pagination change
	useEffect(() => {
		if (!initialLoading) {
			fetchLogs();
		}
		// eslint-disable-next-line react-hooks/exhaustive-deps
	}, [filters, pagination, initialLoading]);

	// Fetch stats and histogram when filters change (but not pagination)
	useEffect(() => {
		if (!initialLoading) {
			fetchStats();
			fetchHistogram();
			fetchFinalDistribution();
		}
		// eslint-disable-next-line react-hooks/exhaustive-deps
	}, [filters, finalDistributionDimension, initialLoading]);

	// Initial load
	useEffect(() => {
		const initialLoad = async () => {
			// Load logs and stats in parallel, don't wait for stats to show the page
			await fetchLogs();
			fetchStats(); // Don't await - let it load in background
			fetchHistogram(); // Don't await - let it load in background
			fetchFinalDistribution(); // Don't await - let it load in background
			setInitialLoading(false);
		};
		initialLoad();
		// eslint-disable-next-line react-hooks/exhaustive-deps
	}, []);

	const getMessageText = useCallback((content: ChatMessageContent): string => {
		if (typeof content === "string") {
			return content;
		}
		if (Array.isArray(content)) {
			return content.reduce((acc: string, block: ContentBlock) => {
				if (block.type === "text" && block.text) {
					return acc + block.text;
				}
				return acc;
			}, "");
		}
		return "";
	}, []);

	// Helper function to check if a log matches the current filters
	const matchesFilters = useCallback(
		(log: LogEntry, filters: LogFilters, applyTimeFilters = true): boolean => {
			if (filters.missing_cost_only && typeof log.cost === "number" && log.cost > 0) {
				return false;
			}
			if (filters.providers?.length && !filters.providers.includes(log.provider)) {
				return false;
			}
			if (filters.models?.length && !filters.models.includes(log.model)) {
				return false;
			}
			if (filters.status?.length && !filters.status.includes(log.status)) {
				return false;
			}
			if (filters.objects?.length && !filters.objects.includes(log.object)) {
				return false;
			}
			if (filters.selected_key_ids?.length && !filters.selected_key_ids.includes(log.selected_key_id)) {
				return false;
			}
			if (filters.virtual_key_ids?.length) {
				if (!log.virtual_key_id || !filters.virtual_key_ids.includes(log.virtual_key_id)) {
					return false;
				}
			}
			if (filters.routing_rule_ids?.length) {
				if (!log.routing_rule_id || !filters.routing_rule_ids.includes(log.routing_rule_id)) {
					return false;
				}
			}
			if (filters.routing_engine_used?.length) {
				if (!log.routing_engines_used || !log.routing_engines_used.some((engine) => filters.routing_engine_used!.includes(engine))) {
					return false;
				}
			}
			if (filters.start_time && new Date(log.timestamp) < new Date(filters.start_time)) {
				return false;
			}
			if (applyTimeFilters && filters.end_time && new Date(log.timestamp) > new Date(filters.end_time)) {
				return false;
			}
			if (filters.min_latency && (!log.latency || log.latency < filters.min_latency)) {
				return false;
			}
			if (filters.max_latency && (!log.latency || log.latency > filters.max_latency)) {
				return false;
			}
			if (filters.min_tokens && (!log.token_usage || log.token_usage.total_tokens < filters.min_tokens)) {
				return false;
			}
			if (filters.max_tokens && (!log.token_usage || log.token_usage.total_tokens > filters.max_tokens)) {
				return false;
			}
			if (filters.metadata_filters) {
				for (const [key, value] of Object.entries(filters.metadata_filters)) {
					const metadataValue = log.metadata?.[key];
					if (metadataValue === undefined || String(metadataValue) !== value) {
						return false;
					}
				}
			}
			if (filters.content_search) {
				const search = filters.content_search.toLowerCase();
				const content = [
					...(log.input_history || []).map((msg: ChatMessage) => getMessageText(msg.content)),
					log.output_message ? getMessageText(log.output_message.content) : "",
				]
					.join(" ")
					.toLowerCase();

				if (!content.includes(search)) {
					return false;
				}
			}
			return true;
		},
		[getMessageText],
	);

	const handleLogMessage = useCallback(
		(log: LogEntry, operation: "create" | "update") => {
			const { logs, filters, pagination, showEmptyState, liveEnabled } = latest.current;

			if (canUseGroupedRequestView(pagination)) {
				if (selectedAttemptId && selectedAttemptId === log.id) {
					setFetchedLog(log);
				}
				scheduleGroupedRefresh();
				return;
			}

			if (showEmptyState) {
				setShowEmptyState(false);
			}

			if (operation === "create") {
				if (pagination.offset === 0 && pagination.sort_by === "timestamp" && pagination.order === "desc") {
					if (!matchesFilters(log, filters, !liveEnabled)) {
						return;
					}

					setLogs((prevLogs: LogEntry[]) => {
						if (prevLogs.some((existingLog) => existingLog.id === log.id)) {
							return prevLogs;
						}

						const updatedLogs = [log, ...prevLogs];
						if (updatedLogs.length > pagination.limit) {
							updatedLogs.pop();
						}
						return updatedLogs;
					});

					setFetchedLog((prev) => {
						if (prev && prev.id === log.id) {
							return log;
						}
						return prev;
					});

					setTotalItems((prev: number) => prev + 1);
				}
			} else if (operation === "update") {
				const logExists = logs.some((existingLog) => existingLog.id === log.id);

				if (!logExists) {
					if (pagination.offset === 0 && pagination.sort_by === "timestamp" && pagination.order === "desc") {
						if (matchesFilters(log, filters, !liveEnabled)) {
							setLogs((prevLogs: LogEntry[]) => {
								if (prevLogs.some((existingLog) => existingLog.id === log.id)) {
									return prevLogs.map((existingLog) => (existingLog.id === log.id ? log : existingLog));
								}

								const updatedLogs = [log, ...prevLogs];
								if (updatedLogs.length > pagination.limit) {
									updatedLogs.pop();
								}
								return updatedLogs;
							});
						}
					}
				} else {
					if (log.stream) {
						const existingTimeout = streamingUpdateTimeouts.current.get(log.id);
						if (existingTimeout) {
							clearTimeout(existingTimeout);
						}

						const timeout = setTimeout(() => {
							updateExistingLog(log);
							streamingUpdateTimeouts.current.delete(log.id);
						}, 100);

						streamingUpdateTimeouts.current.set(log.id, timeout);
					} else {
						updateExistingLog(log);
					}

					if (log.status == "success" || log.status == "error") {
						setStats((prevStats) => {
							if (!prevStats) return prevStats;

							const newStats = { ...prevStats };
							newStats.total_requests += 1;

							const successCount = (prevStats.success_rate / 100) * prevStats.total_requests;
							const newSuccessCount = log.status === "success" ? successCount + 1 : successCount;
							newStats.success_rate = (newSuccessCount / newStats.total_requests) * 100;

							if (log.latency) {
								const totalLatency = prevStats.average_latency * prevStats.total_requests;
								newStats.average_latency = (totalLatency + log.latency) / newStats.total_requests;
							}

							if (log.token_usage) {
								newStats.total_tokens += log.token_usage.total_tokens;
							}

							if (log.cost) {
								newStats.total_cost += log.cost;
							}

							return newStats;
						});

						setHistogram((prevHistogram) => {
							if (!prevHistogram || typeof prevHistogram.bucket_size_seconds !== "number" || prevHistogram.bucket_size_seconds <= 0) {
								return prevHistogram;
							}

							const logTime = new Date(log.timestamp).getTime();
							const bucketSizeMs = prevHistogram.bucket_size_seconds * 1000;
							const bucketTime = Math.floor(logTime / bucketSizeMs) * bucketSizeMs;

							const updatedBuckets = [...prevHistogram.buckets];
							const bucketIndex = updatedBuckets.findIndex((b) => {
								const bTime = new Date(b.timestamp).getTime();
								return Math.floor(bTime / bucketSizeMs) * bucketSizeMs === bucketTime;
							});

							if (bucketIndex >= 0) {
								updatedBuckets[bucketIndex] = {
									...updatedBuckets[bucketIndex],
									count: updatedBuckets[bucketIndex].count + 1,
									success: updatedBuckets[bucketIndex].success + (log.status === "success" ? 1 : 0),
									error: updatedBuckets[bucketIndex].error + (log.status === "error" ? 1 : 0),
								};
							} else {
								const newBucket = {
									timestamp: new Date(bucketTime).toISOString(),
									count: 1,
									success: log.status === "success" ? 1 : 0,
									error: log.status === "error" ? 1 : 0,
								};
								const insertIndex = updatedBuckets.findIndex((b) => new Date(b.timestamp).getTime() > bucketTime);
								if (insertIndex === -1) {
									updatedBuckets.push(newBucket);
								} else {
									updatedBuckets.splice(insertIndex, 0, newBucket);
								}
							}

							return { ...prevHistogram, buckets: updatedBuckets };
						});
					}
				}
			}
		},
		[matchesFilters, scheduleGroupedRefresh, selectedAttemptId, updateExistingLog],
	);

	// Subscribe to log messages - only when live updates are enabled
	useEffect(() => {
		if (!liveEnabled) {
			return;
		}

		const unsubscribe = subscribe("log", (data) => {
			const { payload, operation } = data;
			handleLogMessage(payload, operation);
		});

		return unsubscribe;
	}, [handleLogMessage, subscribe, liveEnabled]);

	const statCards = useMemo(
		() => [
			{
				title: "Request Success Rate",
				value: fetchingStats ? <Skeleton className="h-8 w-24" /> : formatRatePercentage(stats?.request_success_rate),
				detail: fetchingStats ? (
					<Skeleton className="mt-2 h-4 w-32" />
				) : stats ? (
					formatCountSummary(stats.successful_request_groups, stats.completed_request_groups, "requests")
				) : (
					"No completed requests"
				),
				icon: <CheckCircle className="size-4" />,
			},
			{
				title: "Attempt Success Rate",
				value: fetchingStats ? <Skeleton className="h-8 w-24" /> : formatRatePercentage(stats?.success_rate),
				detail: fetchingStats ? (
					<Skeleton className="mt-2 h-4 w-32" />
				) : stats ? (
					formatCountSummary(stats.successful_attempts, stats.completed_attempts, "attempts")
				) : (
					"No completed attempts"
				),
				icon: <BarChart className="size-4" />,
			},
			{
				title: "Avg Final Latency",
				value: fetchingStats ? <Skeleton className="h-8 w-20" /> : formatLatencyValue(stats?.average_final_latency),
				detail: fetchingStats ? <Skeleton className="mt-2 h-4 w-24" /> : "Final attempt only",
				icon: <Clock className="size-4" />,
			},
			{
				title: "Total Attempts",
				value: fetchingStats ? <Skeleton className="h-8 w-24" /> : stats?.total_requests.toLocaleString() || "-",
				detail: fetchingStats ? <Skeleton className="mt-2 h-4 w-24" /> : "All log entries in range",
				icon: <Hash className="size-4" />,
			},
			{
				title: "Total Cost",
				value: fetchingStats ? <Skeleton className="h-8 w-20" /> : stats ? `$${(stats.total_cost ?? 0).toFixed(4)}` : "-",
				detail: fetchingStats ? <Skeleton className="mt-2 h-4 w-24" /> : "Matched completed attempts",
				icon: <DollarSign className="size-4" />,
			},
		],
		[stats, fetchingStats],
	);

	// Get metadata keys from filterdata API so columns always show even with no data on current page
	const { data: filterData } = useGetAvailableFilterDataQuery();
	const metadataKeys = useMemo(() => {
		if (!filterData?.metadata_keys) return [];
		return Object.keys(filterData.metadata_keys).sort();
	}, [filterData?.metadata_keys]);

	const columns = useMemo(() => createColumns(handleDelete, hasDeleteAccess, metadataKeys), [handleDelete, hasDeleteAccess, metadataKeys]);

	// Navigation for log detail sheet
	const selectedLogIndex = useMemo(() => (selectedLogId ? logs.findIndex((l) => l.id === selectedLogId) : -1), [selectedLogId, logs]);

	const handleLogNavigate = useCallback(
		(direction: "prev" | "next") => {
			const currentLogId = selectedLogId || "";
			if (direction === "prev") {
				if (selectedLogIndex > 0) {
					// Navigate to previous log on current page
					setUrlState({ selected_log: logs[selectedLogIndex - 1].id });
				} else if (pagination.offset > 0) {
					// Go to previous page and select the last item
					const newOffset = Math.max(0, pagination.offset - pagination.limit);
					setUrlState({ offset: newOffset, selected_log: "" });
					// Fetch previous page, then select last log
					triggerGetLogs({
						filters,
						pagination: { ...pagination, offset: newOffset },
					}).then((result) => {
						if (result.data?.logs?.length) {
							const lastLog = result.data.logs[result.data.logs.length - 1];
							setUrlState({ selected_log: lastLog.id });
						} else if (result.error) {
							setUrlState({ offset: pagination.offset, selected_log: currentLogId });
							setError(getErrorMessage(result.error));
						}
					});
				}
			} else {
				if (selectedLogIndex >= 0 && selectedLogIndex < logs.length - 1) {
					// Navigate to next log on current page
					setUrlState({ selected_log: logs[selectedLogIndex + 1].id });
				} else if (pagination.offset + pagination.limit < totalItems) {
					// Go to next page and select the first item
					const newOffset = pagination.offset + pagination.limit;
					setUrlState({ offset: newOffset, selected_log: "" });
					// Fetch next page, then select first log
					triggerGetLogs({
						filters,
						pagination: { ...pagination, offset: newOffset },
					}).then((result) => {
						if (result.data?.logs?.length) {
							const firstLog = result.data.logs[0];
							setUrlState({ selected_log: firstLog.id });
						} else if (result.error) {
							setUrlState({ offset: pagination.offset, selected_log: currentLogId });
							setError(getErrorMessage(result.error));
						}
					});
				}
			}
		},
		[selectedLogId, selectedLogIndex, logs, pagination, totalItems, filters, setUrlState, triggerGetLogs],
	);

	return (
		<div className="dark:bg-card h-[calc(100dvh-3.3rem)] max-h-[calc(100dvh-1.5rem)] bg-white">
			{initialLoading ? (
				<FullPageLoader />
			) : showEmptyState ? (
				<EmptyState isSocketConnected={isSocketConnected} error={error} />
			) : (
				<div className="mx-auto flex h-full w-full flex-col">
					<div className="flex flex-1 flex-col gap-3 overflow-hidden">
						{/* Quick Stats */}
						<div className={LOGS_PAGE_STATS_GRID_CLASS} data-testid="stats-cards">
							{statCards.map((card) => (
								<Card key={card.title} className="py-3 shadow-none">
									<CardContent className="px-4">
										<div className="flex items-start justify-between gap-3">
											<div className="min-w-0 flex-1">
												<div className="text-muted-foreground text-[11px] font-medium tracking-[0.08em]">{card.title}</div>
												<div className="mt-1.5 font-mono text-xl leading-none font-semibold tracking-tight break-words sm:text-2xl">
													{card.value}
												</div>
												<div className="text-muted-foreground mt-1.5 text-xs leading-5">{card.detail}</div>
											</div>
											<div className="text-muted-foreground mt-0.5 shrink-0">{card.icon}</div>
										</div>
									</CardContent>
								</Card>
							))}
						</div>

						<div className="shrink-0 rounded-sm border px-3 py-2">
							<Collapsible open={isAnalyticsOpen} onOpenChange={setIsAnalyticsOpen}>
								<div className="flex flex-wrap items-center justify-between gap-3">
									<CollapsibleTrigger
										className="flex items-center gap-2 text-sm font-medium hover:opacity-80"
										data-testid="logs-analytics-toggle"
									>
										<ChevronDown
											className={cn("text-muted-foreground h-4 w-4 transition-transform duration-200", !isAnalyticsOpen && "-rotate-90")}
										/>
										<span>Analytics</span>
									</CollapsibleTrigger>
									<div className="text-muted-foreground text-xs">
										{isAnalyticsOpen
											? "Hide charts to show more logs in the main list."
											: "Expand to inspect request volume and final success distribution."}
									</div>
								</div>
								<CollapsibleContent className="data-[state=closed]:animate-collapse-up data-[state=open]:animate-collapse-down overflow-hidden">
									<div className={`pt-3 ${LOGS_PAGE_CHART_GRID_CLASS}`}>
										<LogsVolumeChart
											data={histogram}
											loading={fetchingHistogram}
											onTimeRangeChange={handleTimeRangeChange}
											onResetZoom={handleResetZoom}
											isZoomed={isZoomed}
											startTime={urlState.start_time}
											endTime={urlState.end_time}
											isOpen={isChartOpen}
											onOpenChange={setIsChartOpen}
										/>
										<FinalSuccessDistributionCard
											data={finalDistribution}
											loading={fetchingFinalDistribution}
											dimension={finalDistributionDimension}
											onDimensionChange={setFinalDistributionDimension}
										/>
									</div>
								</CollapsibleContent>
							</Collapsible>
						</div>

						{/* Error Alert */}
						{error && (
							<Alert variant="destructive" className="shrink-0">
								<AlertCircle className="h-4 w-4" />
								<AlertDescription>{error}</AlertDescription>
							</Alert>
						)}

						<div className="min-h-0 flex-1">
							<LogsDataTable
								columns={columns}
								data={logs}
								totalItems={totalItems}
								loading={fetchingLogs}
								filters={filters}
								pagination={pagination}
								onFiltersChange={setFilters}
								onPaginationChange={setPagination}
								onRowClick={(row, columnId) => {
									if (columnId === "actions") return;
									setSheetMode("attempt");
									setSelectedGroupId(row.group_id ?? row.parent_request_id ?? row.id);
									setSelectedAttemptId(row.id);
									setUrlState({ selected_log: row.id }, { history: "replace" });
								}}
								isSocketConnected={isSocketConnected}
								liveEnabled={liveEnabled}
								onLiveToggle={handleLiveToggle}
								fetchLogs={fetchLogs}
								fetchStats={fetchStats}
								bodyRenderer={
									groupedViewEnabled
										? ({ table, pinOffsets, lastLeftPinId, firstRightPinId }) => (
												<RequestGroupRows
													table={table}
													groups={requestGroups}
													expandedGroupIds={expandedGroupIds}
													selectedGroupId={selectedGroupId}
													selectedAttemptId={selectedAttemptId}
													onToggleGroup={toggleRequestGroup}
													onSelectGroup={selectRequestGroup}
													onSelectAttempt={selectRequestAttempt}
													pinOffsets={pinOffsets}
													lastLeftPinId={lastLeftPinId}
													firstRightPinId={firstRightPinId}
												/>
											)
										: undefined
								}
								metadataKeys={metadataKeys}
							/>
						</div>
					</div>

					{/* Log Detail Sheet */}
					<LogDetailSheet
						log={selectedLog}
						groupState={selectedGroupState}
						sheetMode={sheetMode}
						open={selectedLog !== null}
						onOpenChange={(open) => !open && setUrlState({ selected_log: "" })}
						handleDelete={handleDelete}
						onNavigate={handleLogNavigate}
						hasPrev={selectedLogIndex > 0 || (selectedLogIndex !== -1 && pagination.offset > 0)}
						hasNext={selectedLogIndex !== -1 && (selectedLogIndex < logs.length - 1 || pagination.offset + pagination.limit < totalItems)}
					/>
				</div>
			)}
		</div>
	);
}
