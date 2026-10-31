<script lang="ts">
	import { Activity, Box, Rocket } from "@lucide/svelte";
	import * as Card from "$lib/components/ui/card/index.js";

	type Project = { container_status: string };
	let { projects, deploysToday }: { projects: Project[]; deploysToday: number } = $props();
	const running = $derived(projects.filter((project) => project.container_status === "running").length);
</script>

<div class="grid grid-cols-1 gap-4 px-4 sm:grid-cols-2 lg:px-6 xl:grid-cols-3">
	<Card.Root class="@container/card">
		<Card.Header class="flex-row items-center justify-between space-y-0 pb-2">
			<Card.Title class="text-sm font-medium">Total project</Card.Title><Box class="size-4 text-muted-foreground" />
		</Card.Header>
		<Card.Content><div class="text-2xl font-bold tabular-nums">{projects.length}</div><p class="text-xs text-muted-foreground">di workspace ini</p></Card.Content>
	</Card.Root>
	<Card.Root class="@container/card">
		<Card.Header class="flex-row items-center justify-between space-y-0 pb-2">
			<Card.Title class="text-sm font-medium">Aplikasi berjalan</Card.Title><Activity class="size-4 text-muted-foreground" />
		</Card.Header>
		<Card.Content><div class="text-2xl font-bold tabular-nums">{running}<span class="text-sm font-normal text-muted-foreground"> / {projects.length}</span></div><p class="text-xs text-muted-foreground">status container aktual</p></Card.Content>
	</Card.Root>
	<Card.Root class="@container/card">
		<Card.Header class="flex-row items-center justify-between space-y-0 pb-2">
			<Card.Title class="text-sm font-medium">Deployment hari ini</Card.Title><Rocket class="size-4 text-muted-foreground" />
		</Card.Header>
		<Card.Content><div class="text-2xl font-bold tabular-nums">{deploysToday}</div><p class="text-xs text-muted-foreground">permintaan tercatat hari ini</p></Card.Content>
	</Card.Root>
</div>
