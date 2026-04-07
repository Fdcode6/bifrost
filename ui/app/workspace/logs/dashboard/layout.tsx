import { createFileRoute } from "@tanstack/react-router";
import DashboardPage from "@/app/workspace/dashboard/page";
import { NoPermissionView } from "@/components/noPermissionView";
import { RbacOperation, RbacResource, useRbac } from "@enterprise/lib";

function LogsDashboardLayout({ children }: { children: React.ReactNode }) {
	const hasObservabilityAccess = useRbac(RbacResource.Observability, RbacOperation.View);
	if (!hasObservabilityAccess) {
		return <NoPermissionView entity="dashboard" />;
	}
	return <>{children}</>;
}

function RouteComponent() {
	return (
		<LogsDashboardLayout>
			<DashboardPage />
		</LogsDashboardLayout>
	);
}

export const Route = createFileRoute("/workspace/logs/dashboard")({
	component: RouteComponent,
});
