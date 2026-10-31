<script lang="ts">
	import { cn } from "$lib/utils.js";

	let { class: className = "", size = "h-10" }: { class?: string; size?: string } = $props();
</script>

<img
	src="/iploy-logo.png"
	alt="Iploy"
	class={cn("block w-auto max-w-full object-contain", size, className)}
	width="1400"
	height="450"
/>
