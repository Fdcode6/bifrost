"use client";

import {
	AlertDialog,
	AlertDialogAction,
	AlertDialogCancel,
	AlertDialogContent,
	AlertDialogDescription,
	AlertDialogFooter,
	AlertDialogHeader,
	AlertDialogTitle,
} from "@/components/ui/alertDialog";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { Textarea } from "@/components/ui/textarea";
import { getErrorMessage, useClearAllLogsMutation, useGetCoreConfigQuery, useUpdateCoreConfigMutation } from "@/lib/store";
import { CoreConfig, DefaultCoreConfig } from "@/lib/types/config";
import { parseArrayFromText } from "@/lib/utils/array";
import { RbacOperation, RbacResource, useRbac } from "@enterprise/lib";
import { useCallback, useEffect, useMemo, useState } from "react";
import { toast } from "sonner";

export default function LoggingView() {
	const hasSettingsUpdateAccess = useRbac(RbacResource.Settings, RbacOperation.Update);
	const { data: bifrostConfig } = useGetCoreConfigQuery({ fromDB: true });
	const config = bifrostConfig?.client_config;
	const [updateCoreConfig, { isLoading }] = useUpdateCoreConfigMutation();
	const [clearAllLogs, { isLoading: isClearingLogs }] = useClearAllLogsMutation();
	const [localConfig, setLocalConfig] = useState<CoreConfig>(DefaultCoreConfig);
	const [needsRestart, setNeedsRestart] = useState<boolean>(false);
	const [loggingHeadersText, setLoggingHeadersText] = useState<string>("");
	const [isClearDialogOpen, setIsClearDialogOpen] = useState(false);

	useEffect(() => {
		if (config) {
			setLocalConfig(config);
			setLoggingHeadersText(config.logging_headers?.join(", ") || "");
		}
	}, [config]);

	const hasChanges = useMemo(() => {
		if (!config) return false;
		return (
			localConfig.enable_logging !== config.enable_logging ||
			localConfig.disable_content_logging !== config.disable_content_logging ||
			localConfig.log_retention_days !== config.log_retention_days ||
			localConfig.hide_deleted_virtual_keys_in_filters !== config.hide_deleted_virtual_keys_in_filters ||
			JSON.stringify(localConfig.logging_headers || []) !== JSON.stringify(config.logging_headers || [])
		);
	}, [config, localConfig]);

	const handleConfigChange = useCallback((field: keyof CoreConfig, value: boolean | number | string[]) => {
		setLocalConfig((prev) => ({ ...prev, [field]: value }));
		if (field === "enable_logging" || field === "disable_content_logging") {
			setNeedsRestart(true);
		}
	}, []);

	const handleLoggingHeadersChange = useCallback((value: string) => {
		setLoggingHeadersText(value);
		setLocalConfig((prev) => ({ ...prev, logging_headers: parseArrayFromText(value) }));
	}, []);

	const handleSave = useCallback(async () => {
		if (!bifrostConfig) {
			toast.error("Configuration not loaded");
			return;
		}

		// Validate log retention days
		if (localConfig.log_retention_days < 1) {
			toast.error("Log retention days must be at least 1 day");
			return;
		}

		try {
			await updateCoreConfig({ ...bifrostConfig, client_config: localConfig }).unwrap();
			toast.success("Logging configuration updated successfully.");
		} catch (error) {
			toast.error(getErrorMessage(error));
		}
	}, [bifrostConfig, localConfig, updateCoreConfig]);

	const handleClearAllLogs = useCallback(async () => {
		try {
			await clearAllLogs().unwrap();
			setIsClearDialogOpen(false);
			toast.success("All request logs and MCP tool logs were cleared.");
		} catch (error) {
			toast.error(getErrorMessage(error));
		}
	}, [clearAllLogs]);

	return (
		<div className="mx-auto w-full max-w-4xl space-y-4">
			<div>
				<h2 className="text-lg font-semibold tracking-tight">Logs Settings</h2>
				<p className="text-muted-foreground text-sm">Configure logging settings for requests and responses.</p>
			</div>

			<div className="space-y-4">
				{/* Enable Logs */}
				<div>
					<div className="flex items-center justify-between space-x-2 rounded-lg border p-4">
						<div className="space-y-0.5">
							<label htmlFor="enable-logging" className="text-sm font-medium">
								Enable Logs
							</label>
							<p className="text-muted-foreground text-sm">
								Enable logging of requests and responses to a SQL database. This can add 40-60mb of overhead to the system memory.
								{!bifrostConfig?.is_logs_connected && (
									<span className="text-destructive font-medium"> Requires logs store to be configured and enabled in config.json.</span>
								)}
							</p>
						</div>
						<Switch
							id="enable-logging"
							size="md"
							checked={localConfig.enable_logging && bifrostConfig?.is_logs_connected}
							disabled={!bifrostConfig?.is_logs_connected}
							onCheckedChange={(checked) => {
								if (bifrostConfig?.is_logs_connected) {
									handleConfigChange("enable_logging", checked);
								}
							}}
						/>
					</div>
					{needsRestart && <RestartWarning />}
				</div>

				{/* Disable Content Logging - Only show when logging is enabled */}
				{localConfig.enable_logging && bifrostConfig?.is_logs_connected && (
					<div>
						<div className="flex items-center justify-between space-x-2 rounded-lg border p-4">
							<div className="space-y-0.5">
								<label htmlFor="disable-content-logging" className="text-sm font-medium">
									Disable Content Logging
								</label>
								<p className="text-muted-foreground text-sm">
									When enabled, only usage metadata (latency, cost, token count, etc.) will be logged. Request/response content will not be
									stored.
								</p>
							</div>
							<Switch
								id="disable-content-logging"
								size="md"
								checked={localConfig.disable_content_logging}
								onCheckedChange={(checked) => handleConfigChange("disable_content_logging", checked)}
							/>
						</div>
						{needsRestart && <RestartWarning />}
					</div>
				)}

				{/* Log Retention Days */}
				{localConfig.enable_logging && bifrostConfig?.is_logs_connected && (
					<div className="flex items-center justify-between space-x-2 rounded-lg border p-4">
						<div className="space-y-0.5">
							<Label htmlFor="log-retention-days" className="text-sm font-medium">
								Log Retention Days
							</Label>
							<p className="text-muted-foreground text-sm">
								Number of days to retain logs in the database. Minimum is 1 day. Older logs will be automatically deleted.
							</p>
						</div>
						<Input
							id="log-retention-days"
							type="number"
							min="1"
							value={localConfig.log_retention_days}
							onChange={(e) => {
								const value = parseInt(e.target.value) || 1;
								handleConfigChange("log_retention_days", Math.max(1, value));
							}}
							className="w-24"
						/>
					</div>
				)}

				<div className="flex items-center justify-between space-x-2 rounded-lg border p-4">
					<div className="space-y-0.5">
						<label htmlFor="hide-deleted-virtual-keys-in-filters" className="text-sm font-medium">
							Do Not Show Deleted VirtualKeys In Filters
						</label>
						<p className="text-muted-foreground text-sm">
							When enabled, deleted virtual keys are excluded from Virtual Keys filter options in Logs, Dashboard, and MCP Logs.
						</p>
					</div>
					<Switch
						id="hide-deleted-virtual-keys-in-filters"
						data-testid="hide-deleted-virtual-keys-in-filters-switch"
						size="md"
						checked={localConfig.hide_deleted_virtual_keys_in_filters}
						onCheckedChange={(checked) => handleConfigChange("hide_deleted_virtual_keys_in_filters", checked)}
					/>
				</div>

				{/* Logging Headers */}
				{localConfig.enable_logging && bifrostConfig?.is_logs_connected && (
					<div className="space-y-2 rounded-lg border p-4">
						<label htmlFor="logging-headers" className="text-sm font-medium">
							Logging Headers
						</label>
						<p className="text-muted-foreground text-sm">
							Comma-separated list of request headers to capture in log metadata. Values are extracted from incoming requests and stored in
							the metadata field of log entries. Headers with the <code className="text-xs">x-bf-lh-</code> prefix are always captured
							automatically.
						</p>
						<Textarea
							id="logging-headers"
							data-testid="workspace-logging-headers-textarea"
							className="h-24"
							placeholder="X-Tenant-ID, X-Request-Source, X-Correlation-ID"
							value={loggingHeadersText}
							onChange={(e) => handleLoggingHeadersChange(e.target.value)}
						/>
					</div>
				)}

				{bifrostConfig?.is_logs_connected && (
					<div className="border-destructive/30 bg-destructive/5 space-y-3 rounded-lg border p-4">
						<div className="space-y-0.5">
							<h3 className="text-sm font-medium">Danger Zone</h3>
							<p className="text-muted-foreground text-sm">
								Clear all stored request logs and MCP tool logs. Success rates, distributions, and related dashboard statistics will restart
								from new traffic after this action.
							</p>
						</div>
						<Button
							variant="destructive"
							data-testid="clear-all-logs-button"
							disabled={!hasSettingsUpdateAccess || isClearingLogs}
							onClick={() => setIsClearDialogOpen(true)}
						>
							{isClearingLogs ? "Clearing..." : "Clear All Logs"}
						</Button>
					</div>
				)}
			</div>

			<div className="flex justify-end pt-2">
				<Button onClick={handleSave} disabled={!hasChanges || isLoading || !hasSettingsUpdateAccess}>
					{isLoading ? "Saving..." : "Save Changes"}
				</Button>
			</div>

			<AlertDialog open={isClearDialogOpen} onOpenChange={setIsClearDialogOpen}>
				<AlertDialogContent>
					<AlertDialogHeader>
						<AlertDialogTitle>Clear all logs?</AlertDialogTitle>
						<AlertDialogDescription>
							This permanently removes all stored request logs and MCP tool logs. Existing success rates, distributions, and related
							statistics will be reset. This action cannot be undone.
						</AlertDialogDescription>
					</AlertDialogHeader>
					<AlertDialogFooter>
						<AlertDialogCancel data-testid="clear-all-logs-cancel" disabled={isClearingLogs}>
							Cancel
						</AlertDialogCancel>
						<AlertDialogAction
							data-testid="clear-all-logs-confirm"
							onClick={(event) => {
								event.preventDefault();
								void handleClearAllLogs();
							}}
							disabled={isClearingLogs}
						>
							{isClearingLogs ? "Clearing..." : "Clear All Logs"}
						</AlertDialogAction>
					</AlertDialogFooter>
				</AlertDialogContent>
			</AlertDialog>
		</div>
	);
}

const RestartWarning = () => {
	return <div className="text-muted-foreground mt-2 pl-4 text-xs font-semibold">Need to restart Bifrost to apply changes.</div>;
};
