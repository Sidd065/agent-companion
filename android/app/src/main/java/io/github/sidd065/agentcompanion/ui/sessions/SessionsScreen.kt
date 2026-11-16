package io.github.sidd065.agentcompanion.ui.sessions

import androidx.compose.foundation.ExperimentalFoundationApi
import androidx.compose.foundation.background
import androidx.compose.foundation.combinedClickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.outlined.CallSplit
import androidx.compose.material.icons.automirrored.outlined.Chat
import androidx.compose.material.icons.outlined.Add
import androidx.compose.material.icons.outlined.Archive
import androidx.compose.material.icons.outlined.ArrowDropDown
import androidx.compose.material.icons.outlined.Close
import androidx.compose.material.icons.outlined.ContentCopy
import androidx.compose.material.icons.outlined.Delete
import androidx.compose.material.icons.outlined.DriveFileRenameOutline
import androidx.compose.material.icons.outlined.FilterList
import androidx.compose.material.icons.outlined.PushPin
import androidx.compose.material.icons.outlined.Search
import androidx.compose.material.icons.outlined.Settings
import androidx.compose.material.icons.outlined.StopCircle
import androidx.compose.material.icons.outlined.Terminal
import androidx.compose.material.icons.outlined.Unarchive
import androidx.compose.material.icons.outlined.WarningAmber
import androidx.compose.material3.Button
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.ExtendedFloatingActionButton
import androidx.compose.material3.FilterChip
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.ListItem
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.material3.pulltorefresh.PullToRefreshBox
import androidx.compose.runtime.Composable
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import io.github.sidd065.agentcompanion.core.SessionSummary
import io.github.sidd065.agentcompanion.core.Term
import io.github.sidd065.agentcompanion.graph
import io.github.sidd065.agentcompanion.net.ConnState
import io.github.sidd065.agentcompanion.net.HostConnection
import io.github.sidd065.agentcompanion.net.decode
import io.github.sidd065.agentcompanion.net.params
import io.github.sidd065.agentcompanion.ui.BottomTabs
import io.github.sidd065.agentcompanion.ui.LocalNav
import io.github.sidd065.agentcompanion.ui.Routes
import io.github.sidd065.agentcompanion.ui.activeConnection
import io.github.sidd065.agentcompanion.ui.components.ConfirmDialog
import io.github.sidd065.agentcompanion.ui.components.EmptyState
import io.github.sidd065.agentcompanion.ui.components.LoadingBox
import io.github.sidd065.agentcompanion.ui.components.LocalSnack
import io.github.sidd065.agentcompanion.ui.components.StatusDot
import io.github.sidd065.agentcompanion.ui.components.TextPromptDialog
import io.github.sidd065.agentcompanion.ui.components.baseName
import io.github.sidd065.agentcompanion.ui.components.copyText
import io.github.sidd065.agentcompanion.ui.components.dateBucket
import io.github.sidd065.agentcompanion.ui.components.relTime
import io.github.sidd065.agentcompanion.ui.components.shortPath
import io.github.sidd065.agentcompanion.ui.theme.LocalExtra
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.launch
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import kotlinx.serialization.json.put

fun HostConnection.homeDir(): String? = hello.value?.get("home")?.jsonPrimitive?.content

@Composable
fun ConnectionBanner(conn: HostConnection) {
    val st by conn.rpc.state.collectAsState()
    val ex = LocalExtra.current
    val ctx = LocalContext.current
    val text = when (val s = st) {
        is ConnState.Connected -> return
        is ConnState.Connecting -> if (s.attempt <= 1 && s.lastError == null) "Connecting to ${conn.config.label}…" else "Reconnecting to ${conn.config.label}… ${s.lastError ?: ""}"
        is ConnState.Failed -> s.error
        ConnState.Idle -> "Not connected"
    }
    Surface(color = ex.warning.copy(alpha = 0.14f), modifier = Modifier.fillMaxWidth()) {
        Row(Modifier.padding(horizontal = 16.dp, vertical = 8.dp), verticalAlignment = Alignment.CenterVertically) {
            Icon(Icons.Outlined.WarningAmber, null, tint = ex.warning, modifier = Modifier.size(18.dp))
            Spacer(Modifier.width(8.dp))
            Text(text, style = MaterialTheme.typography.bodySmall, modifier = Modifier.weight(1f), maxLines = 3)
            TextButton(onClick = { conn.rpc.reconnectNow() }) { Text("Retry") }
        }
    }
}

@OptIn(ExperimentalMaterial3Api::class, ExperimentalFoundationApi::class)
@Composable
fun SessionsScreen() {
    val ctx = LocalContext.current
    val g = ctx.graph
    val nav = LocalNav.current
    val snack = LocalSnack.current
    val scope = rememberCoroutineScope()
    val settings by g.prefs.flow.collectAsState()
    val allConns by g.connections.collectAsState()
    var hostFilter by remember { mutableStateOf<String?>(null) } // null = all hosts
    val conns = allConns.filter { hostFilter == null || it.config.id == hostFilter }
    if (conns.isEmpty() && hostFilter != null) hostFilter = null
    val conn = conns.firstOrNull() ?: return
    val multi = allConns.size > 1
    val hosted by remember(conns) {
        kotlinx.coroutines.flow.combine(conns.map { c -> c.sessions.map { l -> l.map { c to it } } }) { a -> a.flatMap { it.toList() }.sortedByDescending { it.second.updatedAt } }
    }.collectAsState(emptyList())
    val hostedTerms by remember(conns) {
        kotlinx.coroutines.flow.combine(conns.map { c -> c.terms.map { l -> l.map { c to it } } }) { a -> a.flatMap { it.toList() } }
    }.collectAsState(emptyList())
    val loaded by remember(conns) { kotlinx.coroutines.flow.combine(conns.map { it.sessionsLoaded }) { a -> a.any { it } } }.collectAsState(false)
    val sessions = hosted.map { it.second }
    var filter by remember { mutableStateOf("all") }
    var query by remember { mutableStateOf("") }
    var searching by remember { mutableStateOf(false) }
    var refreshing by remember { mutableStateOf(false) }
    var menuFor by remember { mutableStateOf<Pair<HostConnection, SessionSummary>?>(null) }
    var hostMenu by remember { mutableStateOf(false) }
    var groupMenu by remember { mutableStateOf(false) }
    fun rpc(label: String, block: suspend () -> Unit) = scope.launch {
        runCatching { block() }.onFailure { snack.showSnackbar("$label failed: ${it.message}") }
    }

    val visible = hosted.filter { (_, s) ->
        val f = when (filter) {
            "working" -> s.busy && !s.needsInput
            "input" -> s.needsInput
            "idle" -> !s.busy && !s.needsInput && !s.archived
            "archived" -> s.archived
            else -> !s.archived
        }
        f && (query.isBlank() || s.title.contains(query, true) || s.cwd.contains(query, true) || (s.lastText?.contains(query, true) == true))
    }
    val counts = mapOf(
        "working" to sessions.count { it.busy && !it.needsInput },
        "input" to sessions.count { it.needsInput },
    )

    Scaffold(
        topBar = {
            TopAppBar(
                title = {
                    if (searching) {
                        OutlinedTextField(query, { query = it }, placeholder = { Text("Filter by title, folder or text") }, singleLine = true,
                            modifier = Modifier.fillMaxWidth().padding(end = 8.dp))
                    } else {
                        Box {
                            Row(Modifier.clip(RoundedCornerShape(8.dp)).combinedClickable(onClick = { hostMenu = true }).padding(4.dp), verticalAlignment = Alignment.CenterVertically) {
                                if (!multi || hostFilter != null) {
                                    val st by conn.rpc.state.collectAsState()
                                    StatusDot(if (st is ConnState.Connected) "idle" else "sleeping", null, size = 9)
                                    Spacer(Modifier.width(8.dp))
                                }
                                Text(if (multi && hostFilter == null) "All hosts" else conn.config.label, maxLines = 1, overflow = TextOverflow.Ellipsis)
                                Icon(Icons.Outlined.ArrowDropDown, null)
                            }
                            DropdownMenu(hostMenu, { hostMenu = false }) {
                                if (multi) DropdownMenuItem(text = { Text("All hosts" + if (hostFilter == null) "  ✓" else "") }, onClick = { hostMenu = false; hostFilter = null })
                                allConns.forEach { c ->
                                    val st by c.rpc.state.collectAsState()
                                    DropdownMenuItem(
                                        leadingIcon = { StatusDot(if (st is ConnState.Connected) "idle" else "sleeping", null, size = 8) },
                                        text = { Text(c.config.label + if (hostFilter == c.config.id || (!multi)) "  ✓" else "") },
                                        onClick = { hostMenu = false; hostFilter = if (multi) c.config.id else null },
                                    )
                                }
                                HorizontalDivider()
                                DropdownMenuItem(text = { Text("Hosts & pairing…") }, onClick = { hostMenu = false; nav.navigate("hosts") })
                            }
                        }
                    }
                },
                actions = {
                    IconButton(onClick = { searching = !searching; if (!searching) query = "" }) {
                        Icon(if (searching) Icons.Outlined.Close else Icons.Outlined.Search, "Search")
                    }
                    Box {
                        IconButton(onClick = { groupMenu = true }) { Icon(Icons.Outlined.FilterList, "Group by") }
                        DropdownMenu(groupMenu, { groupMenu = false }) {
                            Text("Group by", style = MaterialTheme.typography.labelMedium, modifier = Modifier.padding(horizontal = 12.dp, vertical = 6.dp))
                            listOf("date" to "Date", "folder" to "Folder", "status" to "Status", "host" to "Computer", "none" to "None").forEach { (k, l) ->
                                DropdownMenuItem(text = { Text(l + if (settings.groupBy == k) "  ✓" else "") }, onClick = {
                                    groupMenu = false
                                    scope.launch { g.prefs.update { it.copy(groupBy = k) } }
                                })
                            }
                        }
                    }
                    IconButton(onClick = { nav.navigate("settings") }) { Icon(Icons.Outlined.Settings, "Settings") }
                },
            )
        },
        bottomBar = { BottomTabs(nav) },
        floatingActionButton = {
            ExtendedFloatingActionButton(onClick = { nav.navigate(Routes.newSession(host = hostFilter)) }, icon = { Icon(Icons.Outlined.Add, null) }, text = { Text("New session") })
        },
    ) { pad ->
        Column(Modifier.padding(pad).fillMaxSize()) {
            conns.forEach { ConnectionBanner(it) }
            LazyRow(contentPadding = PaddingValues(horizontal = 12.dp, vertical = 6.dp), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                val chips = listOf("all" to "All", "working" to "Working", "input" to "Needs input", "idle" to "Idle", "archived" to "Archived")
                items(chips) { (k, l) ->
                    val c = counts[k]
                    FilterChip(selected = filter == k, onClick = { filter = k }, label = { Text(if (c != null && c > 0) "$l · $c" else l) })
                }
            }
            PullToRefreshBox(isRefreshing = refreshing, onRefresh = {
                refreshing = true
                scope.launch {
                    conns.forEach { c -> runCatching { c.refreshSessions(true) }.onFailure { snack.showSnackbar("${c.config.label}: refresh failed: ${it.message}") } }
                    refreshing = false
                }
            }, modifier = Modifier.fillMaxSize()) {
                when {
                    !loaded -> LoadingBox(text = "Loading sessions…")
                    visible.isEmpty() && filter == "all" && query.isBlank() -> EmptyState(
                        Icons.AutoMirrored.Outlined.Chat, "What are we building?",
                        if (multi && hostFilter == null) "Start an agent session in any folder on any of your computers." else "Start an agent session in any folder on ${conn.config.label}.",
                    ) { Button(onClick = { nav.navigate(Routes.newSession(host = hostFilter)) }) { Text("Start your first session") } }
                    visible.isEmpty() -> EmptyState(Icons.Outlined.Search, "Nothing here", null)
                    else -> SessionList(visible, hostedTerms, settings.groupBy, multi && hostFilter == null,
                        onOpen = { (c, s) -> nav.navigate(Routes.chat(s.id, host = c.config.id)) }, onLong = { menuFor = it })
                }
            }
        }
    }

    menuFor?.let { (c, s) -> SessionActionsSheet(c, s, onDismiss = { menuFor = null }, rpc = { l, b -> rpc(l, b) }) }
}

@OptIn(ExperimentalFoundationApi::class)
@Composable
private fun SessionList(
    list: List<Pair<HostConnection, SessionSummary>>,
    terms: List<Pair<HostConnection, Term>>,
    groupBy: String,
    showHost: Boolean,
    onOpen: (Pair<HostConnection, SessionSummary>) -> Unit,
    onLong: (Pair<HostConnection, SessionSummary>) -> Unit,
) {
    val pinned = list.filter { it.second.pinned }
    val rest = list.filterNot { it.second.pinned }
    val groups: List<Pair<String, List<Pair<HostConnection, SessionSummary>>>> = when (groupBy) {
        "folder" -> rest.groupBy { (c, s) ->
            (if (showHost) c.config.label + " · " else "") + shortPath(s.cwd, c.homeDir()).ifEmpty { "Unknown folder" }
        }.toList().sortedByDescending { (_, v) -> v.maxOf { it.second.updatedAt } }
        "status" -> rest.groupBy { (_, s) ->
            when {
                s.needsInput -> "Needs input"
                s.busy -> "Working"
                s.live -> "Idle"
                else -> "Not running"
            }
        }.toList().sortedBy { listOf("Needs input", "Working", "Idle", "Not running").indexOf(it.first) }
        "host" -> rest.groupBy { it.first.config.label }.toList()
        "none" -> listOf("" to rest)
        else -> rest.groupBy { dateBucket(it.second.updatedAt) }.toList()
    }
    val agyTerms = terms.filter { it.second.kind == "agy" }
    LazyColumn(Modifier.fillMaxSize(), contentPadding = PaddingValues(bottom = 96.dp)) {
        if (agyTerms.isNotEmpty()) {
            item { GroupHeader("Live in terminals") }
            items(agyTerms, key = { "t:" + it.first.config.id + it.second.id }) { (c, t) -> TermRow(c, t, if (showHost) c.config.label else null) }
        }
        if (pinned.isNotEmpty()) {
            item { GroupHeader("Pinned") }
            items(pinned, key = { "p:" + it.first.config.id + it.second.id }) { p ->
                SessionRow(p.second, p.first.homeDir(), Modifier.combinedClickable(onClick = { onOpen(p) }, onLongClick = { onLong(p) }),
                    host = if (showHost) p.first.config.label else null)
            }
        }
        groups.forEach { (g, items) ->
            if (g.isNotEmpty()) item(key = "h:$g") { GroupHeader(g) }
            items(items, key = { it.first.config.id + it.second.id }) { p ->
                SessionRow(p.second, p.first.homeDir(), Modifier.combinedClickable(onClick = { onOpen(p) }, onLongClick = { onLong(p) }).animateItem(),
                    host = if (showHost && groupBy != "host") p.first.config.label else null)
            }
        }
    }
}

@Composable
private fun GroupHeader(t: String) {
    Text(t, style = MaterialTheme.typography.labelLarge, color = MaterialTheme.colorScheme.primary, modifier = Modifier.padding(start = 20.dp, end = 16.dp, top = 14.dp, bottom = 4.dp))
}

@Composable
private fun TermRow(conn: HostConnection, t: Term, host: String?) {
    val nav = LocalNav.current
    val ex = LocalExtra.current
    val home = conn.homeDir()
    Row(
        Modifier.fillMaxWidth().combinedClickableCompat { nav.navigate(Routes.term(t.id, host = conn.config.id)) }.padding(horizontal = 16.dp, vertical = 10.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Icon(Icons.Outlined.Terminal, null, tint = MaterialTheme.colorScheme.primary, modifier = Modifier.size(20.dp))
        Spacer(Modifier.width(12.dp))
        Column(Modifier.weight(1f)) {
            Text(t.name, style = MaterialTheme.typography.bodyLarge, maxLines = 1, overflow = TextOverflow.Ellipsis)
            Text(
                listOfNotNull(host, shortPath(t.cwd, home), t.state?.let { termStateLabel(it) }, t.msg?.takeIf { it.isNotBlank() }).joinToString(" · "),
                style = MaterialTheme.typography.bodySmall, color = ex.muted, maxLines = 1, overflow = TextOverflow.Ellipsis,
            )
        }
    }
}

fun termStateLabel(s: String): String = when {
    s == "working" -> "working"
    s == "done" -> "done"
    s == "idle" -> "idle"
    s.startsWith("blocked") -> "waiting for you"
    s == "failed" -> "failed"
    else -> s
}

@OptIn(ExperimentalFoundationApi::class)
private fun Modifier.combinedClickableCompat(onClick: () -> Unit): Modifier = this.combinedClickable(onClick = onClick)

@Composable
fun SessionRow(s: SessionSummary, home: String?, modifier: Modifier = Modifier, host: String? = null) {
    val ex = LocalExtra.current
    Row(modifier.fillMaxWidth().padding(horizontal = 16.dp, vertical = 10.dp), verticalAlignment = Alignment.Top) {
        Box(Modifier.padding(top = 6.dp)) { StatusDot(s.state, s.attention) }
        Spacer(Modifier.width(12.dp))
        Column(Modifier.weight(1f)) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                if (s.forkedFrom != null) {
                    Icon(Icons.AutoMirrored.Outlined.CallSplit, "fork", Modifier.size(14.dp), tint = ex.muted)
                    Spacer(Modifier.width(4.dp))
                }
                Text(
                    s.displayTitle, style = MaterialTheme.typography.bodyLarge, fontWeight = if (s.needsInput) FontWeight.SemiBold else FontWeight.Normal,
                    maxLines = 1, overflow = TextOverflow.Ellipsis, modifier = Modifier.weight(1f),
                )
                Spacer(Modifier.width(8.dp))
                Text(relTime(s.updatedAt), style = MaterialTheme.typography.labelSmall, color = ex.muted)
            }
            val status = when {
                s.attention == "permission" || s.pendingPermissions > 0 -> "Needs permission"
                s.attention == "question" -> "Has a question"
                s.attention == "plan" -> "Plan ready for review"
                s.state == "running" -> "Working…"
                s.state == "starting" -> "Starting…"
                s.state == "error" -> "Error"
                else -> null
            }
            val sub = listOfNotNull(
                status,
                host,
                baseName(shortPath(s.cwd, home)).takeIf { it.isNotBlank() },
                s.modelName?.takeIf { it.isNotBlank() } ?: s.model,
                if (s.queued > 0) "${s.queued} queued" else null,
            ).joinToString(" · ")
            Text(
                sub, style = MaterialTheme.typography.bodySmall,
                color = if (status != null && s.needsInput) ex.warning else if (status != null) ex.running else ex.muted,
                maxLines = 1, overflow = TextOverflow.Ellipsis,
            )
            s.lastText?.takeIf { it.isNotBlank() }?.let {
                Text(
                    io.github.sidd065.agentcompanion.ui.chat.plainPreview(it).take(200), style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant, maxLines = 2, overflow = TextOverflow.Ellipsis,
                    modifier = Modifier.padding(top = 2.dp),
                )
            }
        }
    }
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun SessionActionsSheet(conn: HostConnection, s: SessionSummary, onDismiss: () -> Unit, rpc: (String, suspend () -> Unit) -> Unit) {
    val ctx = LocalContext.current
    val nav = LocalNav.current
    var renaming by remember { mutableStateOf(false) }
    var deleting by remember { mutableStateOf(false) }
    ModalBottomSheet(onDismissRequest = onDismiss) {
        Column(Modifier.padding(bottom = 24.dp)) {
            Text(s.displayTitle, style = MaterialTheme.typography.titleMedium, modifier = Modifier.padding(horizontal = 20.dp), maxLines = 2)
            Text(s.cwd, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant, modifier = Modifier.padding(horizontal = 20.dp, vertical = 4.dp))
            Spacer(Modifier.height(8.dp))
            SheetItem(Icons.AutoMirrored.Outlined.Chat, "Open") { onDismiss(); nav.navigate(Routes.chat(s.id, host = conn.config.id)) }
            SheetItem(Icons.Outlined.DriveFileRenameOutline, "Rename") { renaming = true }
            SheetItem(Icons.Outlined.PushPin, if (s.pinned) "Unpin" else "Pin") {
                onDismiss(); rpc("Pin") { conn.rpc.call("session.pin", params { put("id", s.id); put("pinned", !s.pinned) }) }
            }
            SheetItem(Icons.AutoMirrored.Outlined.CallSplit, "Fork") {
                onDismiss()
                rpc("Fork") {
                    val r = conn.rpc.call("session.fork", params { put("id", s.id) }).jsonObject
                    val id = r["session"]?.jsonObject?.get("id")?.jsonPrimitive?.content
                    if (id != null) nav.navigate(Routes.chat(id, host = conn.config.id))
                }
            }
            SheetItem(Icons.Outlined.Terminal, "Open in agy terminal") {
                onDismiss()
                rpc("Open in terminal") {
                    val fl = ctx.graph.prefs.value.agyFlags
                    val t = conn.rpc.call("session.openInTerminal", params {
                        put("id", s.id); put("skipPermissions", fl.skipPermissions)
                        put("args", kotlinx.serialization.json.JsonArray(fl.tuiResumeArgs().map { kotlinx.serialization.json.JsonPrimitive(it) }))
                    }).decode(Term.serializer())
                    nav.navigate(Routes.term(t.id, host = conn.config.id))
                }
            }
            if (s.live) {
                SheetItem(Icons.Outlined.StopCircle, "Stop agy process") {
                    onDismiss(); rpc("Stop") { conn.rpc.call("session.stop", params { put("id", s.id) }) }
                }
            }
            SheetItem(if (s.archived) Icons.Outlined.Unarchive else Icons.Outlined.Archive, if (s.archived) "Unarchive" else "Archive") {
                onDismiss(); rpc("Archive") { conn.rpc.call("session.archive", params { put("id", s.id); put("archived", !s.archived) }) }
            }
            SheetItem(Icons.Outlined.ContentCopy, "Copy session ID") { ctx.copyText(s.id, "session id"); onDismiss() }
            SheetItem(Icons.Outlined.Delete, "Delete", danger = true) { deleting = true }
        }
    }
    if (renaming) {
        TextPromptDialog("Rename session", s.title, "Title", "Rename", onDismiss = { renaming = false; onDismiss() }, onConfirm = { t ->
            rpc("Rename") { conn.rpc.call("session.rename", params { put("id", s.id); put("title", t) }) }
        })
    }
    if (deleting) {
        ConfirmDialog("Delete session?", "This permanently deletes the conversation from agy on ${conn.config.label}.", "Delete", destructive = true,
            onDismiss = { deleting = false; onDismiss() }, onConfirm = {
                rpc("Delete") { conn.rpc.call("session.delete", params { put("id", s.id) }) }
            })
    }
}

@Composable
fun SheetItem(icon: ImageVector, label: String, danger: Boolean = false, onClick: () -> Unit) {
    ListItem(
        headlineContent = { Text(label, color = if (danger) MaterialTheme.colorScheme.error else MaterialTheme.colorScheme.onSurface) },
        leadingContent = { Icon(icon, null, tint = if (danger) MaterialTheme.colorScheme.error else MaterialTheme.colorScheme.onSurfaceVariant) },
        modifier = Modifier.combinedClickableCompat(onClick),
    )
}

@Suppress("unused")
private fun Modifier.bg() = this.background(androidx.compose.ui.graphics.Color.Transparent)
