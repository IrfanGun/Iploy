<script lang="ts">
	import { Activity, FolderGit2, LogOut, Plus } from "@lucide/svelte";
	import { Badge } from "$lib/components/ui/badge/index.js";
	import { Button } from "$lib/components/ui/button/index.js";
	import * as Sidebar from "$lib/components/ui/sidebar/index.js";
	import IployLogo from "$lib/components/iploy-logo.svelte";

	type Project = { id: string; name: string; repo_url: string; branch: string; domain: string; container_port: number; active_deployment_id?: string | null; container_status: string; deployment_status: string; created_at: string };
	let {
		projects,
		selectedId,
		username,
		onOpenProject,
		onHome,
		onCreateProject,
		onLogout,
	}: {
		projects: Project[];
		selectedId: string | null;
		username: string;
		onOpenProject: (project: Project) => void;
		onHome: () => void;
		onCreateProject: () => void;
		onLogout: () => void;
	} = $props();
</script>

<Sidebar.Root variant="inset" collapsible="icon">
	<Sidebar.Header class="px-3 py-4">
		<IployLogo size="h-9" class="group-data-[collapsible=icon]:hidden" />
		<span class="hidden size-8 place-items-center rounded-md bg-main text-main-foreground group-data-[collapsible=icon]:grid" aria-label="Iploy">I</span>
	</Sidebar.Header>
	<Sidebar.Content>
		<Sidebar.Group>
			<Sidebar.GroupLabel>Workspace</Sidebar.GroupLabel>
			<Sidebar.GroupContent>
				<Sidebar.Menu>
					<Sidebar.MenuItem>
						<Sidebar.MenuButton isActive={!selectedId} tooltipContent="Overview" onclick={onHome}>
							<Activity /><span>Overview</span>
						</Sidebar.MenuButton>
					</Sidebar.MenuItem>
				</Sidebar.Menu>
			</Sidebar.GroupContent>
		</Sidebar.Group>
		<Sidebar.Group>
			<Sidebar.GroupLabel class="flex items-center justify-between">
				<span>Projects</span><Badge variant="secondary">{projects.length}</Badge>
			</Sidebar.GroupLabel>
			<Sidebar.GroupContent>
				<Sidebar.Menu>
					{#each projects as project (project.id)}
						<Sidebar.MenuItem>
							<Sidebar.MenuButton isActive={selectedId === project.id} tooltipContent={project.name} onclick={() => onOpenProject(project)}>
								<FolderGit2 /><span class="truncate">{project.name}</span>
							</Sidebar.MenuButton>
						</Sidebar.MenuItem>
					{/each}
					<Sidebar.MenuItem>
						<Sidebar.MenuButton tooltipContent="Tambah project" onclick={onCreateProject}>
							<Plus /><span>Tambah project</span>
						</Sidebar.MenuButton>
					</Sidebar.MenuItem>
				</Sidebar.Menu>
			</Sidebar.GroupContent>
		</Sidebar.Group>
	</Sidebar.Content>
	<Sidebar.Footer>
		<Sidebar.Menu>
			<Sidebar.MenuItem>
				<div class="flex items-center gap-2 px-2 py-2 group-data-[collapsible=icon]:hidden">
					<div class="grid size-8 shrink-0 place-items-center rounded-full bg-sidebar-accent text-sm font-medium">{username.slice(0, 1).toUpperCase() || "A"}</div>
					<div class="min-w-0 flex-1"><p class="truncate text-sm font-medium">{username || "Administrator"}</p><p class="text-xs text-muted-foreground">Admin</p></div>
					<Button variant="ghost" size="icon" title="Keluar" aria-label="Keluar" onclick={onLogout}><LogOut class="size-4" /></Button>
				</div>
				<Sidebar.MenuButton class="hidden group-data-[collapsible=icon]:flex" tooltipContent="Keluar" onclick={onLogout}><LogOut /><span>Keluar</span></Sidebar.MenuButton>
			</Sidebar.MenuItem>
		</Sidebar.Menu>
	</Sidebar.Footer>
</Sidebar.Root>
