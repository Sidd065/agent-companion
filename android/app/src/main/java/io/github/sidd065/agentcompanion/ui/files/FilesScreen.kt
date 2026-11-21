package io.github.sidd065.agentcompanion.ui.files

import androidx.activity.compose.BackHandler
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.ExperimentalFoundationApi
import androidx.compose.foundation.background
import androidx.compose.foundation.combinedClickable
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.automirrored.outlined.Chat
import androidx.compose.material.icons.automirrored.outlined.InsertDriveFile
import androidx.compose.material.icons.outlined.Close
import androidx.compose.material.icons.outlined.ContentCopy
import androidx.compose.material.icons.outlined.ContentCut
import androidx.compose.material.icons.outlined.CreateNewFolder
import androidx.compose.material.icons.outlined.Delete
import androidx.compose.material.icons.outlined.Description
import androidx.compose.material.icons.outlined.Difference
import androidx.compose.material.icons.outlined.Download
import androidx.compose.material.icons.outlined.DriveFileRenameOutline
import androidx.compose.material.icons.outlined.Folder
import androidx.compose.material.icons.outlined.Home
import androidx.compose.material.icons.outlined.Image
import androidx.compose.material.icons.outlined.Link
import androidx.compose.material.icons.outlined.MoreVert
import androidx.compose.material.icons.outlined.NoteAdd
import androidx.compose.material.icons.outlined.Refresh
import androidx.compose.material.icons.outlined.Search
import androidx.compose.material.icons.outlined.Share
import androidx.compose.material.icons.outlined.Terminal
import androidx.compose.material.icons.outlined.Upload
import androidx.compose.material.icons.outlined.Visibility
import androidx.compose.material.icons.outlined.VisibilityOff
import androidx.compose.material3.AssistChip
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.material3.pulltorefresh.PullToRefreshBox
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateListOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import io.github.sidd065.agentcompanion.core.FsEntry
import io.github.sidd065.agentcompanion.core.FsList
import io.github.sidd065.agentcompanion.core.Term
import io.github.sidd065.agentcompanion.net.HostConnection
import io.github.sidd065.agentcompanion.net.decode
import io.github.sidd065.agentcompanion.net.params
import io.github.sidd065.agentcompanion.ui.BottomTabs
import io.github.sidd065.agentcompanion.ui.LocalNav
import io.github.sidd065.agentcompanion.ui.Routes
import io.github.sidd065.agentcompanion.ui.activeConnection
import io.github.sidd065.agentcompanion.ui.components.ConfirmDialog
import io.github.sidd065.agentcompanion.ui.components.EmptyState
import io.github.sidd065.agentcompanion.ui.components.ErrorBox
import io.github.sidd065.agentcompanion.ui.components.LoadingBox
import io.github.sidd065.agentcompanion.ui.components.LocalSnack
import io.github.sidd065.agentcompanion.ui.components.TextPromptDialog
import io.github.sidd065.agentcompanion.ui.components.copyText
import io.github.sidd065.agentcompanion.ui.components.fmtBytes
import io.github.sidd065.agentcompanion.ui.components.relTime
import io.github.sidd065.agentcompanion.ui.components.shortPath
import io.github.sidd065.agentcompanion.ui.sessions.ConnectionBanner
import io.github.sidd065.agentcompanion.ui.sessions.homeDir
import io.github.sidd065.agentcompanion.ui.theme.LocalExtra
import kotlinx.coroutines.launch
import androidx.compose.foundation.clickable
import androidx.compose.runtime.collectAsState
import io.github.sidd065.agentcompanion.graph
import androidx.compose.material.icons.outlined.ArrowDropDown
import kotlinx.serialization.builtins.ListSerializer
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.put

fun entryIcon(e: FsEntry): ImageVector = when {
    e.isDir -> Icons.Outlined.Folder
    e.type == "symlink" -> Icons.Outlined.Link
    e.mime?.startsWith("image/") == true -> Icons.Outlined.Image
    e.mime?.startsWith("text/") == true || e.name.substringAfterLast('.', "") in codeExts -> Icons.Outlined.Description
    else -> Icons.AutoMirrored.Outlined.InsertDriveFile
}

val codeExts = setOf("kt", "kts", "java", "go", "py", "js", "ts", "tsx", "jsx", "rs", "c", "h", "cpp", "hpp", "cs", "rb", "php", "swift", "sh", "bash", "zsh",
    "json", "yaml", "yml", "toml", "xml", "html", "css", "scss", "md", "txt", "gradle", "sql", "lua", "dart", "vue", "svelte", "ini", "conf", "env", "mk", "dockerfile")

@OptIn(ExperimentalMaterial3Api::class, ExperimentalFoundationApi::class)
@Composable
fun FilesScreen(initialPath: String, standalone: Boolean = false) {
    val ctx = LocalContext.current
    val nav = LocalNav.current
    val snack = LocalSnack.current
    val scope = rememberCoroutineScope()
    val conn = activeConnection() ?: return
    val home = conn.homeDir()
    var path by rememberSaveable { mutableStateOf(initialPath) }
    var listing by remember { mutableStateOf<FsList?>(null) }
    var error by remember { mutableStateOf<String?>(null) }
    var hidden by rememberSaveable { mutableStateOf(false) }
    var reload by remember { mutableIntStateOf(0) }
    var refreshing by remember { mutableStateOf(false) }
    val selected = remember { mutableStateListOf<String>() }
    var clipboard by remember { mutableStateOf<Pair<String, List<String>>?>(null) } // op -> paths
    var menu by remember { mutableStateOf(false) }
    var dialog by remember { mutableStateOf<String?>(null) } // newfile|newfolder|rename|delete|permdelete
    var searching by remember { mutableStateOf(false) }
    var query by remember { mutableStateOf("") }
    var results by remember { mutableStateOf<List<FsEntry>?>(null) }

    LaunchedEffect(conn) {
        if (path.isBlank()) path = home ?: "~"
    }
    LaunchedEffect(path, hidden, reload) {
        if (path.isBlank()) return@LaunchedEffect
        error = null
        runCatching { listing = conn.rpc.call("fs.list", params { put("path", path); put("hidden", hidden) }).decode(FsList.serializer()) }
            .onFailure { error = it.message }
        refreshing = false
    }
    LaunchedEffect(query) {
        if (query.length < 2) { results = null; return@LaunchedEffect }
        kotlinx.coroutines.delay(350)
        results = runCatching {
            conn.rpc.call("fs.search", params { put("root", listing?.path ?: path); put("query", query); put("max", 200); put("hidden", hidden) })
                .decode(ListSerializer(FsEntry.serializer()))
        }.getOrNull()
    }
    BackHandler(enabled = selected.isNotEmpty() || searching || (!standalone && listing?.parent?.isNotBlank() == true && listing?.path != home)) {
        when {
            selected.isNotEmpty() -> selected.clear()
            searching -> { searching = false; query = "" }
            else -> listing?.parent?.let { path = it }
        }
    }
    fun rpc(label: String, block: suspend () -> Unit) = scope.launch {
        runCatching { block() }.onFailure { snack.showSnackbar("$label: ${it.message}") }
        reload++
    }
    val upload = rememberLauncherForActivityResult(ActivityResultContracts.OpenMultipleDocuments()) { uris ->
        if (uris.isNotEmpty()) scope.launch {
            snack.showSnackbar("Uploading ${uris.size} file(s)…")
            uris.forEach { u ->
                runCatching { FileTransfer.upload(ctx, conn, u, listing?.path ?: path) }.onFailure { snack.showSnackbar("Upload failed: ${it.message}") }
            }
            reload++
        }
    }
    val cur = listing?.path ?: path

    Scaffold(
        topBar = {
            if (selected.isNotEmpty()) {
                TopAppBar(
                    title = { Text("${selected.size} selected") },
                    navigationIcon = { IconButton(onClick = { selected.clear() }) { Icon(Icons.Outlined.Close, null) } },
                    actions = {
                        IconButton(onClick = { clipboard = "copy" to selected.toList(); selected.clear() }) { Icon(Icons.Outlined.ContentCopy, "Copy") }
                        IconButton(onClick = { clipboard = "move" to selected.toList(); selected.clear() }) { Icon(Icons.Outlined.ContentCut, "Cut") }
                        if (selected.size == 1) IconButton(onClick = { dialog = "rename" }) { Icon(Icons.Outlined.DriveFileRenameOutline, "Rename") }
                        IconButton(onClick = { dialog = "delete" }) { Icon(Icons.Outlined.Delete, "Delete") }
                        Box {
                            IconButton(onClick = { menu = true }) { Icon(Icons.Outlined.MoreVert, null) }
                            DropdownMenu(menu, { menu = false }) {
                                DropdownMenuItem(text = { Text("Download to phone") }, leadingIcon = { Icon(Icons.Outlined.Download, null) }, onClick = {
                                    menu = false
                                    val entriesByPath = (results ?: listing?.entries.orEmpty()).associateBy { it.path }
                                    val paths = selected.toList(); selected.clear()
                                    val filesOnly = paths.filter { entriesByPath[it]?.isDir != true }
                                    val skippedDirs = paths.size - filesOnly.size
                                    if (filesOnly.isEmpty()) {
                                        scope.launch { snack.showSnackbar("Folders cannot be downloaded directly — open a folder and pick files inside") }
                                    } else {
                                        if (skippedDirs > 0) {
                                            scope.launch { snack.showSnackbar("Skipped $skippedDirs folder(s); downloading ${filesOnly.size} file(s)") }
                                        }
                                        filesOnly.forEach { fp ->
                                            FileTransfer.enqueueDownload(ctx, conn, fp) { res ->
                                                scope.launch {
                                                    res.onSuccess { snack.showSnackbar("Saved to ${it.displayPath}") }
                                                        .onFailure {
                                                            if (it !is kotlinx.coroutines.CancellationException) {
                                                                snack.showSnackbar("Download failed: ${it.message}")
                                                            }
                                                        }
                                                }
                                            }
                                        }
                                    }
                                })
                                if (selected.size == 1) DropdownMenuItem(text = { Text("Share") }, leadingIcon = { Icon(Icons.Outlined.Share, null) }, onClick = {
                                    menu = false; val p = selected.first(); selected.clear()
                                    scope.launch { runCatching { FileTransfer.share(ctx, conn, p) }.onFailure { snack.showSnackbar("${it.message}") } }
                                })
                                DropdownMenuItem(text = { Text("Copy path") }, leadingIcon = { Icon(Icons.Outlined.Link, null) }, onClick = {
                                    menu = false; ctx.copyText(selected.joinToString("\n")); selected.clear()
                                })
                                DropdownMenuItem(text = { Text("Ask an agent about these") }, leadingIcon = { Icon(Icons.AutoMirrored.Outlined.Chat, null) }, onClick = {
                                    menu = false
                                    val mention = selected.joinToString(" ") { "@" + it.removePrefix("$cur/") }
                                    selected.clear()
                                    nav.navigate(Routes.newSession(cur, "$mention "))
                                })
                            }
                        }
                    },
                )
            } else {
                TopAppBar(
                    title = {
                        if (searching) {
                            OutlinedTextField(query, { query = it }, placeholder = { Text("Search in ${shortPath(cur, home)}") }, singleLine = true, modifier = Modifier.fillMaxWidth())
                        } else {
                            Column {
                                Text(cur.trimEnd('/').substringAfterLast('/').ifEmpty { "/" }, maxLines = 1, overflow = TextOverflow.Ellipsis)
                                val gctx = androidx.compose.ui.platform.LocalContext.current.graph
                                val hosts = gctx.prefs.flow.collectAsState().value.hosts
                                var hostMenu by remember { mutableStateOf(false) }
                                Row(verticalAlignment = Alignment.CenterVertically) {
                                    if (hosts.size > 1 && !standalone) Box {
                                        Row(Modifier.clickable { hostMenu = true }, verticalAlignment = Alignment.CenterVertically) {
                                            Text("on ${conn.config.label}", style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.primary)
                                            Icon(Icons.Outlined.ArrowDropDown, null, Modifier.size(16.dp), tint = MaterialTheme.colorScheme.primary)
                                        }
                                        DropdownMenu(hostMenu, { hostMenu = false }) {
                                            hosts.forEach { h ->
                                                DropdownMenuItem(text = { Text(h.label + if (h.id == conn.config.id) "  ✓" else "") }, onClick = {
                                                    hostMenu = false
                                                    scope.launch { gctx.prefs.update { it.copy(activeHostId = h.id) } }
                                                })
                                            }
                                        }
                                    }
                                    listing?.gitBranch?.takeIf { it.isNotBlank() }?.let {
                                        if (hosts.size > 1 && !standalone) Spacer(Modifier.width(8.dp))
                                        Text("git: $it", style = MaterialTheme.typography.labelSmall, color = LocalExtra.current.success)
                                    }
                                }
                            }
                        }
                    },
                    navigationIcon = {
                        if (standalone) IconButton(onClick = { nav.popBackStack() }) { Icon(Icons.AutoMirrored.Filled.ArrowBack, null) }
                    },
                    actions = {
                        IconButton(onClick = { searching = !searching; query = "" }) { Icon(if (searching) Icons.Outlined.Close else Icons.Outlined.Search, "Search") }
                        IconButton(onClick = { hidden = !hidden }) { Icon(if (hidden) Icons.Outlined.Visibility else Icons.Outlined.VisibilityOff, "Hidden files") }
                        Box {
                            IconButton(onClick = { menu = true }) { Icon(Icons.Outlined.MoreVert, null) }
                            DropdownMenu(menu, { menu = false }) {
                                DropdownMenuItem(text = { Text("Start a session here") }, leadingIcon = { Icon(Icons.AutoMirrored.Outlined.Chat, null) },
                                    onClick = { menu = false; nav.navigate(Routes.newSession(cur)) })
                                DropdownMenuItem(text = { Text("Open terminal here") }, leadingIcon = { Icon(Icons.Outlined.Terminal, null) }, onClick = {
                                    menu = false
                                    rpc("Terminal") {
                                        val t = conn.rpc.call("term.create", params { put("kind", "shell"); put("cwd", cur) }).decode(Term.serializer())
                                        nav.navigate(Routes.term(t.id))
                                    }
                                })
                                DropdownMenuItem(text = { Text("Run agy TUI here") }, leadingIcon = { Icon(Icons.Outlined.Terminal, null) }, onClick = {
                                    menu = false
                                    rpc("Terminal") {
                                        val fl = io.github.sidd065.agentcompanion.App.instance.graph.prefs.value.agyFlags
                                        val t = conn.rpc.call("term.create", params {
                                            put("kind", "agy"); put("cwd", cur); put("skipPermissions", fl.skipPermissions)
                                            put("args", JsonArray(fl.tuiArgs().map { JsonPrimitive(it) }))
                                        }).decode(Term.serializer())
                                        nav.navigate(Routes.term(t.id))
                                    }
                                })
                                if (conn.agents.value.any { it.id == "claude" && it.available }) {
                                    DropdownMenuItem(text = { Text("Run Claude Code here") }, leadingIcon = { Icon(Icons.Outlined.Terminal, null) }, onClick = {
                                        menu = false
                                        rpc("Terminal") {
                                            val fl = io.github.sidd065.agentcompanion.App.instance.graph.prefs.value.claudeFlags
                                            val t = conn.rpc.call("term.create", params {
                                                put("kind", "claude"); put("cwd", cur); put("skipPermissions", fl.skipPermissions)
                                                put("args", JsonArray(fl.tuiArgs().map { JsonPrimitive(it) }))
                                            }).decode(Term.serializer())
                                            nav.navigate(Routes.term(t.id))
                                        }
                                    })
                                }
                                if (listing?.gitBranch?.isNotBlank() == true) {
                                    DropdownMenuItem(text = { Text("Git changes") }, leadingIcon = { Icon(Icons.Outlined.Difference, null) }, onClick = { menu = false; nav.navigate(Routes.diff(path = cur)) })
                                }
                                HorizontalDivider()
                                DropdownMenuItem(text = { Text("New file") }, leadingIcon = { Icon(Icons.Outlined.NoteAdd, null) }, onClick = { menu = false; dialog = "newfile" })
                                DropdownMenuItem(text = { Text("New folder") }, leadingIcon = { Icon(Icons.Outlined.CreateNewFolder, null) }, onClick = { menu = false; dialog = "newfolder" })
                                DropdownMenuItem(text = { Text("Upload from phone") }, leadingIcon = { Icon(Icons.Outlined.Upload, null) }, onClick = { menu = false; upload.launch(arrayOf("*/*")) })
                                DropdownMenuItem(text = { Text("Copy path") }, leadingIcon = { Icon(Icons.Outlined.Link, null) }, onClick = { menu = false; ctx.copyText(cur) })
                                DropdownMenuItem(text = { Text("Refresh") }, leadingIcon = { Icon(Icons.Outlined.Refresh, null) }, onClick = { menu = false; reload++ })
                            }
                        }
                    },
                )
            }
        },
        bottomBar = { if (!standalone) BottomTabs(nav) },
    ) { pad ->
        Column(Modifier.padding(pad).fillMaxSize()) {
            ConnectionBanner(conn)
            val activeDownloads by FileTransfer.downloads.collectAsState()
            activeDownloads.firstOrNull { it.hostId == conn.config.id }?.let { dl ->
                DownloadStatusBanner(
                    task = dl,
                    onCancel = { FileTransfer.cancelDownload(dl.id) },
                    onOpen = {
                        if (!FileTransfer.openDownloaded(ctx, dl)) {
                            scope.launch { snack.showSnackbar("Saved to ${dl.displayPath ?: "Downloads"}") }
                        }
                    },
                    onDismiss = { FileTransfer.dismissDownload(dl.id) },
                )
            }
            Breadcrumbs(cur, home) { path = it }
            clipboard?.let { (op, paths) ->
                Surface(color = MaterialTheme.colorScheme.secondaryContainer) {
                    Row(Modifier.fillMaxWidth().padding(horizontal = 16.dp, vertical = 6.dp), verticalAlignment = Alignment.CenterVertically) {
                        Text("${paths.size} item(s) to ${if (op == "copy") "copy" else "move"}", style = MaterialTheme.typography.bodySmall, modifier = Modifier.weight(1f))
                        TextButton(onClick = {
                            val (o, ps) = clipboard!!
                            clipboard = null
                            rpc(if (o == "copy") "Copy" else "Move") {
                                ps.forEach { p ->
                                    conn.rpc.call(if (o == "copy") "fs.copy" else "fs.rename", params { put("from", p); put("to", cur) })
                                }
                            }
                        }) { Text("Paste here") }
                        TextButton(onClick = { clipboard = null }) { Text("Cancel") }
                    }
                }
            }
            error?.let { ErrorBox(it) { reload++ } }
            PullToRefreshBox(isRefreshing = refreshing, onRefresh = { refreshing = true; reload++ }, modifier = Modifier.fillMaxSize()) {
                val entries = results ?: listing?.entries
                when {
                    listing == null && error == null -> LoadingBox()
                    entries == null -> {}
                    entries.isEmpty() -> EmptyState(Icons.Outlined.Folder, if (results != null) "No matches" else "Empty folder", null)
                    else -> LazyColumn(Modifier.fillMaxSize(), contentPadding = PaddingValues(bottom = 24.dp)) {
                        items(entries, key = { it.path }) { e ->
                            val sel = e.path in selected
                            EntryRow(e, sel, showPath = results != null, home = home, modifier = Modifier.combinedClickable(
                                onClick = {
                                    when {
                                        selected.isNotEmpty() -> if (sel) selected.remove(e.path) else selected.add(e.path)
                                        e.isDir -> { path = e.path; searching = false; query = "" }
                                        else -> nav.navigate(Routes.file(e.path))
                                    }
                                },
                                onLongClick = { if (sel) selected.remove(e.path) else selected.add(e.path) },
                            ))
                        }
                    }
                }
            }
        }
    }

    when (dialog) {
        "newfile" -> TextPromptDialog("New file", "", "File name", "Create", onDismiss = { dialog = null }) { name ->
            rpc("Create") {
                conn.rpc.call("fs.create", params { put("path", "$cur/$name") })
                nav.navigate(Routes.file("$cur/$name", edit = true))
            }
        }
        "newfolder" -> TextPromptDialog("New folder", "", "Folder name", "Create", onDismiss = { dialog = null }) { name ->
            rpc("Create folder") { conn.rpc.call("fs.mkdir", params { put("path", "$cur/$name") }) }
        }
        "rename" -> {
            val p = selected.firstOrNull()
            if (p == null) dialog = null else TextPromptDialog("Rename", p.substringAfterLast('/'), "New name", "Rename", onDismiss = { dialog = null }) { name ->
                selected.clear()
                rpc("Rename") { conn.rpc.call("fs.rename", params { put("from", p); put("to", p.substringBeforeLast('/') + "/" + name) }) }
            }
        }
        "delete" -> {
            val paths = selected.toList()
            ConfirmDialog("Move ${paths.size} item(s) to trash?", paths.joinToString("\n") { shortPath(it, home) }.take(600), "Move to trash", destructive = true,
                onDismiss = { dialog = null }) {
                selected.clear()
                scope.launch {
                    runCatching { conn.rpc.call("fs.delete", params { put("paths", JsonArray(paths.map { JsonPrimitive(it) })) }) }
                        .onFailure { e ->
                            if (e.message?.contains("TRASH_UNSUPPORTED") == true) {
                                pendingPerm = paths; dialog = "permdelete"
                            } else snack.showSnackbar("Delete: ${e.message}")
                        }
                    reload++
                }
            }
        }
        "permdelete" -> {
            val paths = pendingPerm
            ConfirmDialog("Delete permanently?", "The trash isn't available for this location. Delete ${paths.size} item(s) permanently?", "Delete", destructive = true,
                onDismiss = { dialog = null }) {
                rpc("Delete") { conn.rpc.call("fs.delete", params { put("paths", JsonArray(paths.map { JsonPrimitive(it) })); put("permanent", true) }) }
            }
        }
    }
}

private var pendingPerm: List<String> = emptyList()

@Composable
private fun Breadcrumbs(path: String, home: String?, onGo: (String) -> Unit) {
    val parts = path.trim('/').split('/').filter { it.isNotEmpty() }
    Row(Modifier.fillMaxWidth().horizontalScroll(rememberScrollState()).padding(horizontal = 12.dp, vertical = 2.dp), verticalAlignment = Alignment.CenterVertically) {
        if (home != null) {
            IconButton(onClick = { onGo(home) }, modifier = Modifier.size(32.dp)) { Icon(Icons.Outlined.Home, "Home", Modifier.size(18.dp)) }
        }
        AssistChip(onClick = { onGo("/") }, label = { Text("/") })
        var acc = ""
        parts.forEach { p ->
            acc += "/$p"
            val target = acc
            Text(" › ", color = MaterialTheme.colorScheme.onSurfaceVariant)
            Text(p, style = MaterialTheme.typography.labelLarge, modifier = Modifier.clip(RoundedCornerShape(6.dp)).combinedClickableNoLong { onGo(target) }.padding(6.dp))
        }
    }
}

@OptIn(ExperimentalFoundationApi::class)
private fun Modifier.combinedClickableNoLong(onClick: () -> Unit) = this.combinedClickable(onClick = onClick)

@Composable
private fun EntryRow(e: FsEntry, selected: Boolean, showPath: Boolean, home: String?, modifier: Modifier) {
    val ex = LocalExtra.current
    Row(
        modifier.fillMaxWidth().background(if (selected) MaterialTheme.colorScheme.primaryContainer.copy(alpha = 0.45f) else MaterialTheme.colorScheme.surface)
            .padding(horizontal = 16.dp, vertical = 10.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Icon(entryIcon(e), null, tint = if (e.isDir) MaterialTheme.colorScheme.primary else MaterialTheme.colorScheme.onSurfaceVariant)
        Spacer(Modifier.width(14.dp))
        Column(Modifier.weight(1f)) {
            Text(e.name, style = MaterialTheme.typography.bodyLarge, maxLines = 1, overflow = TextOverflow.Ellipsis,
                color = if (e.hidden) ex.muted else MaterialTheme.colorScheme.onSurface)
            Text(
                if (showPath) shortPath(e.path, home) else listOfNotNull(
                    if (!e.isDir) fmtBytes(e.size) else null,
                    relTime(e.mtime),
                    e.target?.let { "→ $it" },
                ).joinToString(" · "),
                style = MaterialTheme.typography.bodySmall, color = ex.muted, maxLines = 1, overflow = TextOverflow.Ellipsis,
                fontFamily = if (showPath) FontFamily.Monospace else FontFamily.Default,
            )
        }
    }
}

@Suppress("unused")
private fun unused(c: HostConnection, a: Arrangement.Horizontal) = Unit
