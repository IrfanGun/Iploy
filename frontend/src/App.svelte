<script lang="ts">
  import { tick } from 'svelte'
  import { Terminal } from '@xterm/xterm'
  import { FitAddon } from '@xterm/addon-fit'
  import '@xterm/xterm/css/xterm.css'
  import { ArrowLeft, ArrowUpRight, Box, GitBranch, KeyRound, Moon, Plus, RefreshCw, Rocket, Server, Sun, Trash2, X } from '@lucide/svelte'
  import { Badge } from '$lib/components/ui/badge/index.js'
  import { Button } from '$lib/components/ui/button/index.js'
  import * as Card from '$lib/components/ui/card/index.js'
  import { Input } from '$lib/components/ui/input/index.js'
  import { Label } from '$lib/components/ui/label/index.js'
  import { Separator } from '$lib/components/ui/separator/index.js'
  import * as Sidebar from '$lib/components/ui/sidebar/index.js'
  import * as Table from '$lib/components/ui/table/index.js'
  import * as Tabs from '$lib/components/ui/tabs/index.js'
  import AppSidebar from '$lib/components/app-sidebar.svelte'
  import SectionCards from '$lib/components/section-cards.svelte'
  import IployLogo from '$lib/components/iploy-logo.svelte'

  type Project = { id: string; vps_id: string; project_path: string; name: string; repo_url: string; branch: string; domain: string; container_port: number; active_deployment_id?: string | null; container_status: string; deployment_status: string; created_at: string }
  type VPS = { id: string; name: string; host: string; port: number; username: string; host_fingerprint: string; status: string; created_at: string }
  type Deployment = { id: string; kind: string; status: string; requested_ref: string; commit_sha?: string | null; created_at: string; error_message?: string | null }

  let csrf = ''
  let theme: 'light' | 'dark' = localStorage.getItem('deploy-panel-theme') === 'light' ? 'light' : 'dark'
  let signedIn = false
  let projects: Project[] = []
  let vpsList: VPS[] = []
  let deploysToday = 0
  let apiOnline = false
  let selected: Project | null = null
  let activeTab = 'Overview'
  let deployments: Deployment[] = []
  let envRows: { key: string; value: string; updated_at: string }[] = []
  let volumeRows: { docker_volume_name: string; mount_target: string }[] = []
  let envKey = ''
  let envValue = ''
  let volumeName = ''
  let volumeTarget = ''
  let loading = true
  let error = ''
  let username = ''
  let password = ''
  let showForm = false
  let showVPSPage = false
  let showVPSForm = false
  let busy = false
  let terminalSocket: WebSocket | null = null
  let terminalStatus: 'disconnected' | 'connecting' | 'connected' = 'disconnected'
  let terminal: Terminal | null = null
  let terminalHost: HTMLDivElement | undefined
  let terminalResizeObserver: ResizeObserver | null = null
  let vpsForm = { name: '', host: '', port: 22, username: 'ubuntu', password: '' }
  let form = { vps_id: '', name: '', project_path: '' }

  function applyTheme(value: 'light' | 'dark') {
    theme = value
    document.documentElement.dataset.theme = value
    document.querySelector('meta[name="theme-color"]')?.setAttribute('content', value === 'light' ? '#ffffff' : '#09090b')
    localStorage.setItem('deploy-panel-theme', value)
  }
  function toggleTheme() { applyTheme(theme === 'dark' ? 'light' : 'dark') }
  applyTheme(theme)

  async function request(path: string, method = 'GET', data?: unknown) {
    const res = await fetch(path, { method, credentials: 'same-origin', headers: { 'Content-Type': 'application/json', ...(csrf ? { 'X-CSRF-Token': csrf } : {}) }, body: data === undefined ? undefined : JSON.stringify(data) })
    const payload = await res.json().catch(() => ({}))
    if (!res.ok) throw new Error(payload?.error?.message || 'Terjadi kesalahan. Coba lagi.')
    return payload
  }
  async function refresh() {
    loading = true
    try {
      projects = await request('/api/projects')
      vpsList = await request('/api/vps')
      error = ''
      apiOnline = true
      const histories = await Promise.all(projects.map((p) => request('/api/projects/' + p.id + '/deployments').catch(() => [])))
      const today = new Date().toDateString()
      deploysToday = histories.flat().filter((d: Deployment) => new Date(d.created_at).toDateString() === today).length
    } catch (e) {
      error = e instanceof Error ? e.message : 'Gagal memuat project'
      apiOnline = false
    } finally { loading = false }
  }
  async function checkSession() {
    try {
      const me = await request('/api/auth/me')
      csrf = me.csrf_token
      username = me.admin.username
      signedIn = true
      await refresh()
    } catch { signedIn = false; loading = false }
  }
  async function login(e: SubmitEvent) {
    e.preventDefault(); busy = true; error = ''
    try {
      const result = await request('/api/auth/login', 'POST', { username, password })
      csrf = result.csrf_token; signedIn = true; password = ''
      await refresh()
    } catch (e) { error = e instanceof Error ? e.message : 'Tidak bisa masuk' }
    finally { busy = false }
  }
  async function logout() {
    closeTerminal()
    try { await request('/api/auth/logout', 'POST', {}) } catch {}
    signedIn = false; projects = []; selected = null; csrf = ''
  }
  async function createProject(e: SubmitEvent) {
    e.preventDefault(); busy = true; error = ''
    try {
      await request('/api/projects', 'POST', form)
      showForm = false; showVPSPage = false; form = { ...form, name: '', project_path: '' }
      await refresh()
    } catch (e) { error = e instanceof Error ? e.message : 'Project gagal disimpan' }
    finally { busy = false }
  }
  async function createVPS(e: SubmitEvent) {
    e.preventDefault(); busy = true; error = ''
    try {
      const created: VPS = await request('/api/vps', 'POST', vpsForm)
      vpsList = [created, ...vpsList]
      vpsForm = { name: '', host: '', port: 22, username: 'ubuntu', password: '' }
      showVPSForm = false; showVPSPage = false; showForm = true
      form = { ...form, vps_id: created.id }
    } catch (e) { error = e instanceof Error ? e.message : 'VPS gagal disimpan' }
    finally { busy = false }
  }
  async function testVPS(id: string) {
    busy = true
    try { await request('/api/vps/' + id + '/test', 'POST', {}); vpsList = vpsList.map(v => v.id === id ? { ...v, status: 'connected' } : v); error = '' }
    catch (e) { error = e instanceof Error ? e.message : 'Koneksi SSH gagal'; vpsList = vpsList.map(v => v.id === id ? { ...v, status: 'disconnected' } : v) }
    finally { busy = false }
  }
  function closeTerminal() {
    terminalSocket?.close()
    terminalSocket = null
    terminalStatus = 'disconnected'
    terminalResizeObserver?.disconnect()
    terminalResizeObserver = null
    terminal?.dispose()
    terminal = null
  }
  async function connectTerminal() {
    const project = selected
    if (!project || !project.project_path || terminalSocket) return
    terminalStatus = 'connecting'
    await tick()
    const host = terminalHost
    if (!host || selected?.id !== project.id) { terminalStatus = 'disconnected'; return }
    terminalResizeObserver?.disconnect()
    terminal?.dispose()
    const activeTerminal = new Terminal({ cursorBlink: true, convertEol: true, fontSize: 13, fontFamily: 'ui-monospace, SFMono-Regular, Menlo, monospace', theme: { background: '#09090b', foreground: '#d4d4d8', cursor: '#fafafa' } })
    const fit = new FitAddon()
    terminal = activeTerminal
    activeTerminal.loadAddon(fit)
    activeTerminal.open(host)
    fit.fit()
    const scheme = location.protocol === 'https:' ? 'wss' : 'ws'
    const socket = new WebSocket(`${scheme}://${location.host}/api/projects/${project.id}/terminal`, ['iploy-terminal', `csrf.${csrf}`])
    terminalSocket = socket
    terminalResizeObserver = new ResizeObserver(() => fit.fit())
    terminalResizeObserver.observe(host)
    activeTerminal.onData((data) => { if (socket.readyState === WebSocket.OPEN) socket.send(data) })
    activeTerminal.onResize(({ cols, rows }) => { if (socket.readyState === WebSocket.OPEN) socket.send(`\u0000resize:${cols}:${rows}`) })
    socket.onopen = () => { terminalStatus = 'connected'; activeTerminal.write(`\r\nConnected · ${project.project_path}\r\n`); socket.send(`\u0000resize:${activeTerminal.cols}:${activeTerminal.rows}`) }
    socket.onmessage = (event) => activeTerminal.write(String(event.data))
    socket.onerror = () => activeTerminal.write('\r\nTerminal connection error.\r\n')
    socket.onclose = (event) => { if (terminalSocket === socket) { terminalSocket = null; terminalStatus = 'disconnected'; activeTerminal.write(event.reason ? `\r\nSSH session closed: ${event.reason}\r\n` : '\r\nSSH session closed.\r\n') } }
  }
  async function deploy() {
    if (!selected) return
    busy = true
    try { await request('/api/projects/' + selected.id + '/deployments', 'POST', {}); await refresh() }
    catch (e) { error = e instanceof Error ? e.message : 'Deployment gagal diantrekan' }
    finally { busy = false }
  }
  async function openProject(project: Project) {
    closeTerminal()
    selected = project; activeTab = 'Overview'
    try {
      deployments = await request('/api/projects/' + project.id + '/deployments')
      envRows = await request('/api/projects/' + project.id + '/env')
      volumeRows = await request('/api/projects/' + project.id + '/volumes')
    } catch (e) { error = e instanceof Error ? e.message : 'Gagal memuat detail project' }
  }
  async function changeTab(tab: string) {
    activeTab = tab
    if (tab === 'Terminal') { connectTerminal(); return }
    closeTerminal()
    if (!selected) return
    try {
      if (tab === 'Deployments') deployments = await request('/api/projects/' + selected.id + '/deployments')
      if (tab === 'Environment') envRows = await request('/api/projects/' + selected.id + '/env')
      if (tab === 'Storage') volumeRows = await request('/api/projects/' + selected.id + '/volumes')
    } catch (e) { error = e instanceof Error ? e.message : 'Gagal memuat detail' }
  }
  async function saveEnv(e: SubmitEvent) {
    e.preventDefault()
    if (!selected || !envKey) return
    busy = true
    try {
      await request('/api/projects/' + selected.id + '/env', 'PUT', { [envKey]: envValue })
      envKey = ''; envValue = ''; envRows = await request('/api/projects/' + selected.id + '/env')
    } catch (e) { error = e instanceof Error ? e.message : 'Secret gagal disimpan' }
    finally { busy = false }
  }
  async function deleteEnv(key: string) {
    if (!selected || !confirm('Hapus environment key ' + key + '?')) return
    try {
      await request('/api/projects/' + selected.id + '/env/' + encodeURIComponent(key), 'DELETE', {})
      envRows = await request('/api/projects/' + selected.id + '/env')
    } catch (e) { error = e instanceof Error ? e.message : 'Secret gagal dihapus' }
  }
  async function addVolume(e: SubmitEvent) {
    e.preventDefault()
    if (!selected || !volumeName || !volumeTarget) return
    busy = true
    try {
      const next = [...volumeRows, { docker_volume_name: volumeName, mount_target: volumeTarget }]
      await request('/api/projects/' + selected.id + '/volumes', 'PUT', next)
      volumeRows = next; volumeName = ''; volumeTarget = ''
    } catch (e) { error = e instanceof Error ? e.message : 'Volume gagal disimpan' }
    finally { busy = false }
  }
  async function deleteVolume(name: string) {
    if (!selected || !confirm('Lepas konfigurasi mount ' + name + '? Volume dan datanya tidak dihapus.')) return
    try {
      const next = volumeRows.filter((v) => v.docker_volume_name !== name)
      await request('/api/projects/' + selected.id + '/volumes', 'PUT', next)
      volumeRows = next
    } catch (e) { error = e instanceof Error ? e.message : 'Konfigurasi volume gagal diubah' }
  }
  function statusLabel(project: Project) {
    if (project.container_status === 'connected') return 'Terhubung'
    if (project.container_status === 'running') return 'Berjalan'
    if (project.container_status === 'stopped') return 'Berhenti'
    if (project.container_status === 'unhealthy') return 'Tidak sehat'
    return 'Belum aktif'
  }
  const dateLabel = new Intl.DateTimeFormat('id-ID', { weekday: 'long', day: 'numeric', month: 'long', year: 'numeric' }).format(new Date())
  checkSession()
</script>

{#if !signedIn}
  <main class="relative grid min-h-svh place-items-center bg-background px-4 py-12 text-foreground">
    <Button variant="ghost" size="icon" class="absolute right-4 top-4" title={theme === 'dark' ? 'Aktifkan tema terang' : 'Aktifkan tema gelap'} aria-label={theme === 'dark' ? 'Aktifkan tema terang' : 'Aktifkan tema gelap'} onclick={toggleTheme}>
      {#if theme === 'dark'}<Sun class="size-4" />{:else}<Moon class="size-4" />{/if}
    </Button>
    <Card.Root class="w-full max-w-sm">
      <Card.Header class="space-y-2">
        <IployLogo size="h-16" class="mb-3 justify-self-center" />
        <Card.Title class="text-2xl">Selamat datang.</Card.Title>
        <Card.Description>Masuk untuk mengelola deployment Anda.</Card.Description>
      </Card.Header>
      <Card.Content>
        {#if error}
          <div role="alert" class="mb-4 flex items-start justify-between gap-3 rounded-md border border-destructive/50 bg-destructive/10 p-3 text-sm text-destructive">
            <span>{error}</span><Button variant="ghost" size="icon-xs" aria-label="Tutup pesan" onclick={() => error = ''}><X /></Button>
          </div>
        {/if}
        <form class="space-y-4" onsubmit={login}>
          <div class="space-y-2"><Label for="login-username">Username</Label><Input id="login-username" autocomplete="username" bind:value={username} required /></div>
          <div class="space-y-2"><Label for="login-password">Kata sandi</Label><Input id="login-password" type="password" autocomplete="current-password" bind:value={password} required /></div>
          <Button type="submit" class="w-full" disabled={busy}>{busy ? 'Memeriksaâ€¦' : 'Masuk ke panel'} <ArrowUpRight class="size-4" /></Button>
        </form>
      </Card.Content>
      <Card.Footer class="justify-center"><div class="flex items-center gap-2 text-xs text-muted-foreground"><KeyRound class="size-3.5" />Koneksi aman Â· Hanya admin</div></Card.Footer>
    </Card.Root>
  </main>
{:else}
  <Sidebar.Provider style="--sidebar-width: calc(var(--spacing) * 64); --header-height: calc(var(--spacing) * 14);">
    <AppSidebar
      projects={projects}
      selectedId={selected?.id ?? null}
      {username}
      onOpenProject={openProject}
      onHome={() => { closeTerminal(); selected = null; showForm = false; showVPSPage = false; showVPSForm = false }}
      onCreateProject={() => { closeTerminal(); selected = null; showVPSPage = false; showVPSForm = false; showForm = true }}
      onVPS={() => { closeTerminal(); selected = null; showForm = false; showVPSPage = true; showVPSForm = false }}
      onLogout={logout}
    />
    <Sidebar.Inset>
      <div class="min-w-0">
      <header class="sticky top-0 z-20 flex h-(--header-height) items-center justify-between gap-3 border-b bg-background/95 px-4 backdrop-blur supports-[backdrop-filter]:bg-background/80 sm:px-6">
        <div class="flex min-w-0 items-center gap-2 text-sm">
          <Sidebar.Trigger class="-ms-1" />
          <Separator orientation="vertical" class="mx-1 h-4" />
          <span class="hidden text-muted-foreground sm:inline">Workspace</span><span class="hidden text-muted-foreground sm:inline">/</span>
          <span class="truncate font-medium">{selected ? selected.name : showVPSPage ? 'VPS' : showForm ? 'Project baru' : 'Overview'}</span>
        </div>
        <div class="flex shrink-0 items-center gap-2">
          <Badge variant={apiOnline ? 'secondary' : 'outline'} class="gap-1.5">
            <span class="size-1.5 rounded-full" class:bg-green-500={apiOnline} class:bg-muted-foreground={!apiOnline}></span>
            <span class="hidden sm:inline">{apiOnline ? 'API terhubung' : 'API offline'}</span>
          </Badge>
          <Button variant="ghost" size="icon" title="Refresh" aria-label="Refresh" onclick={refresh}><RefreshCw class="size-4" /></Button>
          <Button variant="ghost" size="icon" title={theme === 'dark' ? 'Tema terang' : 'Tema gelap'} aria-label={theme === 'dark' ? 'Tema terang' : 'Tema gelap'} onclick={toggleTheme}>
            {#if theme === 'dark'}<Sun class="size-4" />{:else}<Moon class="size-4" />{/if}
          </Button>
        </div>
      </header>

      <main class="mx-auto w-full max-w-6xl space-y-6 p-4 sm:p-6 lg:p-8">
        {#if error}
          <div role="alert" class="flex items-start justify-between gap-3 rounded-md border border-destructive/50 bg-destructive/10 p-3 text-sm text-destructive">
            <span>{error}</span><Button variant="ghost" size="icon-xs" aria-label="Tutup pesan" onclick={() => error = ''}><X /></Button>
          </div>
        {/if}

        {#if selected}
          <div class="flex flex-col justify-between gap-4 sm:flex-row sm:items-center">
            <div class="min-w-0 space-y-1">
              <p class="text-xs font-medium uppercase tracking-wider text-muted-foreground">Project Â· {selected.id.slice(0, 8)}</p>
              <h1 class="truncate text-2xl font-semibold tracking-tight sm:text-3xl">{selected.name}</h1>
              <p class="truncate font-mono text-sm text-muted-foreground">{selected.project_path || `${selected.domain} · ${selected.repo_url}`}</p>
            </div>
            <div class="flex shrink-0 gap-2">
              <Button variant="outline" onclick={() => { closeTerminal(); selected = null; activeTab = 'Overview' }}><ArrowLeft class="size-4" /> Semua project</Button>
              {#if !selected.project_path}<Button disabled={busy} onclick={deploy}><Rocket class="size-4" /> Deploy</Button>{/if}
            </div>
          </div>
          <Tabs.Root bind:value={activeTab} class="w-full">
            <Tabs.List variant="line" class="w-full justify-start overflow-x-auto">
              {#if selected.project_path}{#each ['Overview', 'Terminal'] as tab}<Tabs.Trigger value={tab} onclick={() => changeTab(tab)}>{tab === 'Overview' ? 'Folder VPS' : tab}</Tabs.Trigger>{/each}{:else}{#each ['Overview', 'Deployments', 'Environment', 'Storage', 'Settings'] as tab}<Tabs.Trigger value={tab} onclick={() => changeTab(tab)}>{tab}</Tabs.Trigger>{/each}{/if}
            </Tabs.List>
          </Tabs.Root>

          {#if activeTab === 'Terminal' && selected.project_path}
            <Card.Root>
              <Card.Header class="flex-row items-start justify-between gap-3">
                <div class="space-y-1"><Card.Title>Terminal VPS</Card.Title><Card.Description>SSH sebagai {vpsList.find(v => v.id === selected?.vps_id)?.username || 'user VPS'} · mulai di {selected.project_path}</Card.Description></div>
                <div class="flex items-center gap-2"><Badge variant={terminalStatus === 'connected' ? 'default' : 'secondary'}>{terminalStatus === 'connected' ? 'Terhubung' : terminalStatus === 'connecting' ? 'Menghubungkan' : 'Terputus'}</Badge><Button variant="outline" size="sm" disabled={terminalStatus === 'connecting'} onclick={terminalStatus === 'connected' ? closeTerminal : connectTerminal}>{terminalStatus === 'connected' ? 'Putuskan' : 'Hubungkan'}</Button></div>
              </Card.Header>
              <Card.Content class="space-y-3">
                <div bind:this={terminalHost} class="terminal-host h-[55vh] min-h-72 overflow-hidden rounded-md border bg-[#09090b] p-2"></div>
                <p class="text-xs text-muted-foreground">Perintah dijalankan di VPS dengan izin akun SSH. Akses filesystem mengikuti izin akun tersebut.</p>
              </Card.Content>
            </Card.Root>
          {:else if activeTab === 'Environment'}
            <Card.Root>
              <Card.Header><Card.Title>Environment variables</Card.Title><Card.Description>Secret dienkripsi saat tersimpan. Nilainya tidak ditampilkan kembali.</Card.Description></Card.Header>
              <Card.Content class="space-y-5">
                <div class="divide-y divide-border rounded-md border">
                  {#if envRows.length === 0}<p class="p-4 text-sm text-muted-foreground">Belum ada environment key.</p>{/if}
                  {#each envRows as item}
                    <div class="flex flex-wrap items-center gap-3 p-3 text-sm">
                      <code class="min-w-32 font-mono text-xs">{item.key}</code><span class="flex-1 font-mono text-xs text-muted-foreground">{item.value}</span>
                      <span class="text-xs text-muted-foreground">{new Date(item.updated_at).toLocaleDateString('id-ID')}</span>
                      <Button variant="ghost" size="sm" class="text-destructive hover:text-destructive" onclick={() => deleteEnv(item.key)}><Trash2 class="size-4" /> Hapus</Button>
                    </div>
                  {/each}
                </div>
                <form class="grid gap-4 sm:grid-cols-[1fr_1fr_auto] sm:items-end" onsubmit={saveEnv}>
                  <div class="space-y-2"><Label for="env-key">Key</Label><Input id="env-key" bind:value={envKey} pattern="[A-Za-z_][A-Za-z0-9_]*" required placeholder="API_TOKEN" /></div>
                  <div class="space-y-2"><Label for="env-value">Value</Label><Input id="env-value" bind:value={envValue} placeholder="Nilai secret (boleh kosong)" /></div>
                  <Button type="submit" disabled={busy}>Simpan</Button>
                </form>
              </Card.Content>
            </Card.Root>
          {:else if activeTab === 'Storage'}
            <Card.Root>
              <Card.Header><Card.Title>Persistent volumes</Card.Title><Card.Description>Named Docker volume dimount ke direktori aplikasi.</Card.Description></Card.Header>
              <Card.Content class="space-y-5">
                <div class="divide-y divide-border rounded-md border">
                  {#if volumeRows.length === 0}<p class="p-4 text-sm text-muted-foreground">Belum ada volume terpasang.</p>{/if}
                  {#each volumeRows as volume}
                    <div class="flex flex-wrap items-center gap-3 p-3 text-sm">
                      <code class="min-w-40 font-mono text-xs">{volume.docker_volume_name}</code><span class="flex-1 font-mono text-xs text-muted-foreground">{volume.mount_target}</span>
                      <Badge variant="outline">Persistent</Badge>
                      <Button variant="ghost" size="sm" class="text-destructive hover:text-destructive" onclick={() => deleteVolume(volume.docker_volume_name)}><Trash2 class="size-4" /> Lepas</Button>
                    </div>
                  {/each}
                </div>
                <form class="grid gap-4 sm:grid-cols-[1fr_1fr_auto] sm:items-end" onsubmit={addVolume}>
                  <div class="space-y-2"><Label for="volume-name">Nama volume</Label><Input id="volume-name" bind:value={volumeName} pattern="[A-Za-z0-9][A-Za-z0-9_.-]&#123;0,62&#125;" required placeholder="uploads-data" /></div>
                  <div class="space-y-2"><Label for="volume-target">Mount target</Label><Input id="volume-target" bind:value={volumeTarget} required placeholder="/app/uploads" /></div>
                  <Button type="submit" disabled={busy}>Tambah</Button>
                </form>
                <p class="text-xs text-muted-foreground">Melepas konfigurasi tidak menghapus data volume.</p>
              </Card.Content>
            </Card.Root>
          {:else if activeTab === 'Deployments'}
            <Card.Root>
              <Card.Header class="flex-row items-center justify-between space-y-0">
                <div class="space-y-1"><Card.Title>Riwayat deployment</Card.Title><Card.Description>Build dan rilis project ini.</Card.Description></div>
                {#if !selected.project_path}<Button size="sm" disabled={busy} onclick={deploy}><Rocket class="size-4" /> Deploy</Button>{/if}
              </Card.Header>
              <Card.Content>
                {#if deployments.length === 0}
                  <div class="flex items-center gap-4 rounded-md border border-dashed p-6">
                    <div class="grid size-10 place-items-center rounded-full bg-muted"><Server class="size-5 text-muted-foreground" /></div>
                    <div><p class="text-sm font-medium">Belum ada deployment</p><p class="mt-1 text-sm text-muted-foreground">Jalankan deployment untuk mengisi riwayat.</p></div>
                  </div>
                {:else}
                  <div class="divide-y divide-border rounded-md border">
                    {#each deployments as deployment}
                      <div class="flex flex-wrap items-center gap-3 p-3 text-sm">
                        <Badge variant={deployment.kind === 'rollback' ? 'secondary' : 'outline'}>{deployment.kind === 'rollback' ? 'Rollback' : 'Deploy'}</Badge>
                        <code class="font-mono text-xs">{deployment.commit_sha?.slice(0, 8) || deployment.requested_ref}</code>
                        <Badge variant={deployment.status === 'ready' ? 'default' : deployment.status === 'failed' ? 'destructive' : 'secondary'}>{deployment.status}</Badge>
                        <span class="ml-auto text-xs text-muted-foreground">{new Date(deployment.created_at).toLocaleString('id-ID')}</span>
                        {#if deployment.error_message}<p class="basis-full text-sm text-destructive">{deployment.error_message}</p>{/if}
                      </div>
                    {/each}
                  </div>
                {/if}
              </Card.Content>
            </Card.Root>
          {:else if activeTab === 'Settings'}
            <Card.Root>
              <Card.Header><Card.Title>Project settings</Card.Title><Card.Description>Konfigurasi source dan runtime project.</Card.Description></Card.Header>
              <Card.Content class="space-y-4">
                {#each [['Repository', selected.repo_url], ['Branch', selected.branch], ['Domain', selected.domain], ['Container port', String(selected.container_port)]] as setting}
                  <div class="flex flex-col gap-1 border-b pb-3 last:border-0"><span class="text-xs text-muted-foreground">{setting[0]}</span><code class="break-all font-mono text-sm">{setting[1]}</code></div>
                {/each}
                <p class="text-xs text-muted-foreground">Pengeditan konfigurasi tersedia melalui API.</p>
              </Card.Content>
            </Card.Root>
          {:else}
            <div class="grid gap-4 lg:grid-cols-[minmax(0,1.5fr)_minmax(240px,1fr)]">
              <Card.Root>
                <Card.Header class="flex-row items-start justify-between space-y-0">
                  <div class="space-y-2">
                    <Card.Description class="font-medium uppercase tracking-wide">{selected.project_path ? 'Folder project' : 'Status aplikasi'}</Card.Description>
                    <Card.Title class="break-all font-mono text-xl">{selected.project_path || selected.domain}</Card.Title>
                    <Card.Description class="flex items-center gap-2">{#if selected.project_path}<Server class="size-4" />Folder tersedia melalui SSH{:else}<GitBranch class="size-4" /> {selected.branch} Â· Port {selected.container_port}{/if}</Card.Description>
                  </div>
                  <Badge variant={selected.container_status === 'running' ? 'default' : selected.container_status === 'unhealthy' ? 'destructive' : 'secondary'}>{statusLabel(selected)}</Badge>
                </Card.Header>
              </Card.Root>
              <Card.Root>
                <Card.Header><Card.Description>Deployment terakhir</Card.Description><Card.Title class="text-xl capitalize">{selected.deployment_status === 'never' ? 'Belum pernah deploy' : selected.deployment_status}</Card.Title><Card.Description>{selected.active_deployment_id ? 'Commit aktif tercatat' : 'Commit belum tersedia'}</Card.Description></Card.Header>
              </Card.Root>
            </div>
            <Card.Root>
              <Card.Header class="flex-row items-center justify-between space-y-0">
                <div class="space-y-1"><Card.Title>Deployment terbaru</Card.Title><Card.Description>Riwayat build dan rilis.</Card.Description></div>
                <Button variant="outline" size="sm" onclick={() => changeTab('Deployments')}>Lihat semua <ArrowUpRight class="size-4" /></Button>
              </Card.Header>
              <Card.Content>
                {#if deployments.length === 0}
                  <div class="flex items-center gap-4 rounded-md border border-dashed p-6">
                    <div class="grid size-10 place-items-center rounded-full bg-muted"><Box class="size-5 text-muted-foreground" /></div>
                    <div><p class="text-sm font-medium">Belum ada deployment</p><p class="mt-1 text-sm text-muted-foreground">Jalankan deployment pertama untuk melihat status di sini.</p></div>
                  </div>
                {:else}
                  <div class="divide-y divide-border rounded-md border">
                    {#each deployments.slice(0, 3) as deployment}
                      <div class="flex flex-wrap items-center gap-3 p-3 text-sm">
                        <Badge variant="outline">Deploy</Badge><code class="font-mono text-xs">{deployment.commit_sha?.slice(0, 8) || deployment.requested_ref}</code>
                        <Badge variant={deployment.status === 'ready' ? 'default' : deployment.status === 'failed' ? 'destructive' : 'secondary'}>{deployment.status}</Badge>
                        <span class="ml-auto text-xs text-muted-foreground">{new Date(deployment.created_at).toLocaleString('id-ID')}</span>
                      </div>
                    {/each}
                  </div>
                {/if}
              </Card.Content>
            </Card.Root>
          {/if}
        {:else if showVPSPage}
          <div class="flex flex-col justify-between gap-4 sm:flex-row sm:items-end">
            <div class="space-y-1"><p class="text-xs font-medium uppercase tracking-wider text-muted-foreground">Workspace · Infrastruktur</p><h1 class="text-2xl font-semibold tracking-tight sm:text-3xl">VPS</h1><p class="text-sm text-muted-foreground">Hubungkan server lewat SSH sebelum menyiapkan project.</p></div>
            <div class="flex gap-2"><Button variant="outline" onclick={() => { showVPSPage = false }}><ArrowLeft class="size-4" /> Overview</Button><Button onclick={() => showVPSForm = true}><Plus class="size-4" /> Tambah VPS</Button></div>
          </div>
          {#if showVPSForm}
            <Card.Root>
              <form onsubmit={createVPS}>
                <Card.Header><Card.Title>Koneksi SSH</Card.Title><Card.Description>Masuk menggunakan akun Ubuntu. Password dienkripsi; fingerprint server akan dipin otomatis saat koneksi pertama.</Card.Description></Card.Header>
                <Card.Content class="grid gap-5 sm:grid-cols-2">
                  <div class="space-y-2"><Label for="vps-name">Nama VPS</Label><Input id="vps-name" bind:value={vpsForm.name} placeholder="Production Jakarta" required /></div>
                  <div class="space-y-2"><Label for="vps-host">IP atau hostname</Label><Input id="vps-host" bind:value={vpsForm.host} placeholder="203.0.113.10" required /></div>
                  <div class="space-y-2"><Label for="vps-port">Port SSH</Label><Input id="vps-port" type="number" min="1" max="65535" bind:value={vpsForm.port} required /></div>
                  <div class="space-y-2"><Label for="vps-user">Username SSH</Label><Input id="vps-user" bind:value={vpsForm.username} autocomplete="username" placeholder="ubuntu" required /></div>
                  <div class="space-y-2"><Label for="vps-password">Password SSH</Label><Input id="vps-password" type="password" bind:value={vpsForm.password} autocomplete="new-password" required /></div>
                  <p class="text-xs text-muted-foreground sm:col-span-2">Saat koneksi pertama, panel menyimpan fingerprint server dan membandingkannya pada koneksi berikutnya. Simpan VPS akan mencoba login SSH lebih dulu.</p>
                </Card.Content>
                <Card.Footer class="justify-between"><Button type="button" variant="outline" onclick={() => showVPSForm = false}>Batal</Button><Button type="submit" disabled={busy}>{busy ? 'Menguji SSH…' : 'Uji koneksi & simpan'}<ArrowUpRight class="size-4" /></Button></Card.Footer>
              </form>
            </Card.Root>
          {/if}
          <section class="space-y-4">
            <div class="space-y-1"><h2 class="text-lg font-semibold">Server terdaftar</h2><p class="text-sm text-muted-foreground">Kredensial tidak pernah dikirim kembali ke browser.</p></div>
            {#if vpsList.length === 0}
              <Card.Root><Card.Content class="py-10 text-center text-sm text-muted-foreground">Belum ada VPS. Tambahkan server untuk memulai setup project.</Card.Content></Card.Root>
            {:else}
              <div class="grid gap-3 md:grid-cols-2">
                {#each vpsList as vps (vps.id)}
                  <Card.Root><Card.Header class="flex-row items-start justify-between space-y-0"><div class="space-y-1"><Card.Title>{vps.name}</Card.Title><Card.Description>{vps.username}@{vps.host}:{vps.port}</Card.Description></div><Badge variant={vps.status === 'connected' ? 'default' : 'destructive'}>{vps.status === 'connected' ? 'Terhubung' : 'Terputus'}</Badge></Card.Header><Card.Content class="flex flex-wrap items-center justify-between gap-3"><code class="break-all text-xs text-muted-foreground">{vps.host_fingerprint}</code><Button variant="outline" size="sm" disabled={busy} onclick={() => testVPS(vps.id)}><RefreshCw class="size-4" /> Tes koneksi</Button></Card.Content></Card.Root>
                {/each}
              </div>
            {/if}
          </section>
        {:else if showForm}
          <div class="flex flex-col justify-between gap-4 sm:flex-row sm:items-center">
            <div class="space-y-1"><p class="text-xs font-medium uppercase tracking-wider text-muted-foreground">Workspace Â· Project yang sudah ada</p><h1 class="text-2xl font-semibold tracking-tight sm:text-3xl">Hubungkan project</h1><p class="text-sm text-muted-foreground">Project sudah disiapkan di VPS; panel hanya memeriksa akses SSH ke foldernya.</p></div>
            <Button variant="outline" onclick={() => showForm = false}><X class="size-4" /> Batal</Button>
          </div>
          <Card.Root>
            <form onsubmit={createProject}>
              <Card.Header><Card.Title>Folder project di VPS</Card.Title><Card.Description>Nama project bebas kamu tentukan. Masukkan path folder yang sudah ada di VPS; panel akan memeriksanya lewat SSH.</Card.Description></Card.Header>
              <Card.Content class="grid gap-5 sm:grid-cols-2">
                <div class="space-y-2 sm:col-span-2"><Label for="project-vps">Target VPS</Label><select id="project-vps" class="h-9 w-full rounded-md border border-input bg-background px-3 text-sm" bind:value={form.vps_id} required><option value="" disabled>Pilih VPS yang sudah terhubung</option>{#each vpsList as vps}<option value={vps.id}>{vps.name} · {vps.host}</option>{/each}</select></div>
                <div class="space-y-2"><Label for="project-name">Nama project</Label><Input id="project-name" bind:value={form.name} placeholder="Nama project" required /></div>
                <div class="space-y-2 sm:col-span-2"><Label for="project-path">Path folder project di VPS</Label><Input id="project-path" bind:value={form.project_path} placeholder="/home/ubuntu/finance-app atau finance-app" required pattern="/?[A-Za-z0-9._/-]+" /></div>
              </Card.Content>
              <Card.Footer class="justify-between"><p class="text-xs text-muted-foreground">Tidak ada folder atau layanan yang diubah di VPS.</p><Button type="submit" disabled={busy}>{busy ? 'Mengecek folder…' : 'Cek folder & simpan'}<Plus class="size-4" /></Button></Card.Footer>
            </form>
          </Card.Root>
        {:else}
          <div class="flex flex-col justify-between gap-4 sm:flex-row sm:items-end">
            <div class="space-y-2">
              <p class="text-xs font-medium uppercase tracking-wider text-muted-foreground">{dateLabel} Â· Workspace pribadi</p>
              <h1 class="text-3xl font-semibold tracking-tight sm:text-4xl">Selamat datang.</h1>
              <p class="text-sm text-muted-foreground">Ringkasan aplikasi dan deployment Anda.</p>
            </div>
            <Button onclick={() => showForm = true}><Plus class="size-4" /> Tambah project</Button>
          </div>
          <SectionCards {projects} {deploysToday} />
          <section class="space-y-4">
            <div class="flex items-center justify-between gap-4">
              <div class="space-y-1"><h2 class="text-lg font-semibold tracking-tight">Projects</h2><p class="text-sm text-muted-foreground">Pantau status aplikasi dan deployment terbaru.</p></div>
              <Button variant="ghost" size="sm" onclick={refresh}><RefreshCw class="size-4" /> Refresh</Button>
            </div>
            {#if loading}
              <Card.Root><Card.Content class="py-10 text-center text-sm text-muted-foreground">Memuat workspaceâ€¦</Card.Content></Card.Root>
            {:else if projects.length === 0}
              <Card.Root><Card.Content class="flex flex-col items-center justify-center gap-3 py-12 text-center">
                <div class="grid size-12 place-items-center rounded-full bg-muted"><Server class="size-5 text-muted-foreground" /></div>
                <div class="space-y-1"><p class="font-medium">Workspace Anda masih kosong</p><p class="text-sm text-muted-foreground">Tambahkan project pertama untuk mulai melakukan deploy.</p></div>
                <Button class="mt-2" onclick={() => showForm = true}><Plus class="size-4" /> Tambah project</Button>
              </Card.Content></Card.Root>
            {:else}
              <Card.Root>
                <Card.Header class="flex-row items-center justify-between space-y-0">
                  <div class="space-y-1"><Card.Title>Project</Card.Title><Card.Description>Status layanan dan deployment terakhir.</Card.Description></div>
                  <Badge variant="outline">{projects.length} total</Badge>
                </Card.Header>
                <Card.Content class="px-0">
                  <div class="overflow-x-auto px-4">
                    <Table.Root>
                      <Table.Header><Table.Row><Table.Head>Nama</Table.Head><Table.Head>Folder VPS</Table.Head><Table.Head>Status</Table.Head><Table.Head class="text-right">Deployment</Table.Head></Table.Row></Table.Header>
                      <Table.Body>
                        {#each projects as project (project.id)}
                          <Table.Row>
                            <Table.Cell>
                              <Button variant="link" class="h-auto justify-start p-0 font-medium" onclick={() => openProject(project)}>{project.name}<ArrowUpRight class="size-3.5" /></Button>
                              <p class="mt-1 font-mono text-xs text-muted-foreground">{project.project_path || project.domain}</p>
                            </Table.Cell>
                            <Table.Cell><span class="inline-flex items-center gap-1.5 font-mono text-xs">{project.project_path || project.branch}</span></Table.Cell>
                            <Table.Cell><Badge variant={project.container_status === 'running' ? 'default' : project.container_status === 'unhealthy' ? 'destructive' : 'secondary'}>{statusLabel(project)}</Badge></Table.Cell>
                            <Table.Cell class="text-right"><Badge variant="outline">{project.deployment_status === 'never' ? 'Belum ada' : project.deployment_status === 'linked' ? 'Terhubung ke VPS' : project.deployment_status}</Badge></Table.Cell>
                          </Table.Row>
                        {/each}
                      </Table.Body>
                    </Table.Root>
                  </div>
                </Card.Content>
              </Card.Root>            {/if}
          </section>
        {/if}
        <footer class="flex items-center justify-between border-t pt-4 text-xs text-muted-foreground">
          <span>Deploy Panel Â· Private VPS</span>
          <span class="flex items-center gap-2"><span class="size-1.5 rounded-full" class:bg-green-500={apiOnline} class:bg-muted-foreground={!apiOnline}></span>{apiOnline ? 'API tersedia' : 'Menunggu API'}</span>
        </footer>
      </main>
      </div>
    </Sidebar.Inset>
  </Sidebar.Provider>
{/if}

