package io.github.sidd065.agentcompanion.ui.terminal

import io.github.sidd065.agentcompanion.ui.components.AppSheet
import android.graphics.Typeface
import android.util.TypedValue
import android.view.KeyEvent
import android.view.inputmethod.InputMethodManager
import androidx.compose.foundation.ExperimentalFoundationApi
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
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
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.navigationBarsPadding
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
import androidx.compose.material.icons.outlined.Add
import androidx.compose.material.icons.outlined.ContentPaste
import androidx.compose.material.icons.outlined.Delete
import androidx.compose.material.icons.outlined.DriveFileRenameOutline
import androidx.compose.material.icons.outlined.Keyboard
import androidx.compose.material.icons.outlined.MoreVert
import androidx.compose.material.icons.outlined.Refresh
import androidx.compose.material.icons.outlined.Terminal
import androidx.compose.material.icons.outlined.TextDecrease
import androidx.compose.material.icons.outlined.TextIncrease
import androidx.compose.material3.Button
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.ExtendedFloatingActionButton
import androidx.compose.material3.FilterChip
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
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
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.viewinterop.AndroidView
import com.termux.view.TerminalView
import io.github.sidd065.agentcompanion.core.Term
import io.github.sidd065.agentcompanion.graph
import io.github.sidd065.agentcompanion.net.decode
import io.github.sidd065.agentcompanion.net.params
import io.github.sidd065.agentcompanion.ui.BottomTabs
import io.github.sidd065.agentcompanion.ui.LocalNav
import io.github.sidd065.agentcompanion.ui.Routes
import io.github.sidd065.agentcompanion.ui.activeConnection
import io.github.sidd065.agentcompanion.ui.components.ConfirmDialog
import io.github.sidd065.agentcompanion.ui.components.EmptyState
import io.github.sidd065.agentcompanion.ui.components.LocalSnack
import io.github.sidd065.agentcompanion.ui.components.Pill
import io.github.sidd065.agentcompanion.ui.components.TextPromptDialog
import io.github.sidd065.agentcompanion.ui.components.relTime
import io.github.sidd065.agentcompanion.ui.components.shortPath
import io.github.sidd065.agentcompanion.ui.sessions.ConnectionBanner
import io.github.sidd065.agentcompanion.ui.sessions.DirectoryPicker
import io.github.sidd065.agentcompanion.ui.sessions.homeDir
import io.github.sidd065.agentcompanion.ui.sessions.termStateLabel
import io.github.sidd065.agentcompanion.ui.theme.LocalExtra
import kotlinx.coroutines.launch
import kotlinx.coroutines.flow.map
import io.github.sidd065.agentcompanion.net.HostConnection
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.put

@OptIn(ExperimentalMaterial3Api::class, ExperimentalFoundationApi::class)
@Composable
fun TerminalsScreen() {
    val nav = LocalNav.current
    val snack = LocalSnack.current
    val scope = rememberCoroutineScope()
    val g = LocalContext.current.graph
    val conns by g.connections.collectAsState()
    val conn = conns.firstOrNull() ?: return
    val multi = conns.size > 1
    val hosted by remember(conns) {
        kotlinx.coroutines.flow.combine(conns.map { c -> c.terms.map { l -> l.map { c to it } } }) { a -> a.flatMap { it.toList() }.sortedByDescending { it.second.created } }
    }.collectAsState(emptyList())
    val terms = hosted
    var creating by remember { mutableStateOf(false) }
    var menuFor by remember { mutableStateOf<Term?>(null) }
    var renaming by remember { mutableStateOf<Pair<HostConnection, Term>?>(null) }
    var killing by remember { mutableStateOf<Pair<HostConnection, Term>?>(null) }
    var refreshing by remember { mutableStateOf(false) }
    LaunchedEffect(conns) { conns.forEach { c -> runCatching { c.refreshTerms() } } }
    Scaffold(
        topBar = { TopAppBar(title = { Text("Terminals") }) },
        bottomBar = { BottomTabs(nav) },
        floatingActionButton = { ExtendedFloatingActionButton(onClick = { creating = true }, icon = { Icon(Icons.Outlined.Add, null) }, text = { Text("New terminal") }) },
    ) { pad ->
        Column(Modifier.padding(pad).fillMaxSize()) {
            conns.forEach { ConnectionBanner(it) }
            PullToRefreshBox(isRefreshing = refreshing, onRefresh = {
                refreshing = true
                scope.launch { conns.forEach { c -> runCatching { c.refreshTerms() } }; refreshing = false }
            }, modifier = Modifier.fillMaxSize()) {
                if (terms.isEmpty()) {
                    EmptyState(Icons.Outlined.Terminal, "No terminals", "Open a shell, agy or Claude Code on ${if (multi) "any of your computers" else conn.config.label}. On a computer, run `agyr` to start agy in a terminal you can pick up here.")
                } else {
                    LazyColumn(Modifier.fillMaxSize(), contentPadding = PaddingValues(bottom = 96.dp)) {
                        items(terms, key = { it.first.config.id + it.second.id }) { (tc, t) ->
                            val home = tc.homeDir()
                            Box {
                                Row(
                                    Modifier.fillMaxWidth().combinedClickable(onClick = { nav.navigate(Routes.term(t.id, host = tc.config.id)) }, onLongClick = { menuFor = t }).padding(horizontal = 16.dp, vertical = 12.dp),
                                    verticalAlignment = Alignment.CenterVertically,
                                ) {
                                    Icon(if (t.kind == "agy" || t.kind == "claude") Icons.AutoMirrored.Outlined.Chat else Icons.Outlined.Terminal, null, tint = MaterialTheme.colorScheme.primary)
                                    Spacer(Modifier.width(14.dp))
                                    Column(Modifier.weight(1f)) {
                                        Row(verticalAlignment = Alignment.CenterVertically) {
                                            Text(t.name, style = MaterialTheme.typography.bodyLarge, maxLines = 1, overflow = TextOverflow.Ellipsis, modifier = Modifier.weight(1f, fill = false))
                                            if (t.origin == "agyr") { Spacer(Modifier.width(6.dp)); Pill("agyr", LocalExtra.current.running) }
                                            t.state?.takeIf { it.isNotBlank() }?.let {
                                                Spacer(Modifier.width(6.dp))
                                                Pill(termStateLabel(it), if (it.startsWith("blocked")) LocalExtra.current.warning else if (it == "working") LocalExtra.current.running else LocalExtra.current.success)
                                            }
                                        }
                                        Text(
                                            listOfNotNull(if (multi) tc.config.label else null, shortPath(t.cwd, home), t.currentCommand, if (t.attached > 0) "${t.attached} attached" else null, relTime(t.created)).joinToString(" · "),
                                            style = MaterialTheme.typography.bodySmall, color = LocalExtra.current.muted, maxLines = 1, overflow = TextOverflow.Ellipsis,
                                        )
                                        t.msg?.takeIf { it.isNotBlank() }?.let { Text(it, style = MaterialTheme.typography.bodySmall, maxLines = 2, overflow = TextOverflow.Ellipsis) }
                                    }
                                }
                                DropdownMenu(menuFor?.id == t.id, { menuFor = null }) {
                                    DropdownMenuItem(text = { Text("Rename") }, leadingIcon = { Icon(Icons.Outlined.DriveFileRenameOutline, null) }, onClick = { menuFor = null; renaming = tc to t })
                                    DropdownMenuItem(text = { Text("Kill") }, leadingIcon = { Icon(Icons.Outlined.Delete, null) }, onClick = { menuFor = null; killing = tc to t })
                                }
                            }
                        }
                    }
                }
            }
        }
    }
    if (creating) {
        var hostId by remember { mutableStateOf(g.prefs.value.activeHost?.id) }
        io.github.sidd065.agentcompanion.ui.HostScope(hostId) {
            androidx.compose.runtime.key(hostId) {
                NewTerminalSheet(onDismiss = { creating = false }, hostPicker = {
                    if (multi) Row(Modifier.horizontalScroll(rememberScrollState()), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                        conns.forEach { c -> FilterChip(c.config.id == (hostId ?: conn.config.id), { hostId = c.config.id }, label = { Text(c.config.label) }) }
                    }
                }) { t -> creating = false; nav.navigate(Routes.term(t.id, host = hostId ?: conn.config.id)) }
            }
        }
    }
    renaming?.let { (tc, t) ->
        TextPromptDialog("Rename terminal", t.name, "Name", "Rename", onDismiss = { renaming = null }) { n ->
            scope.launch { runCatching { tc.rpc.call("term.rename", params { put("id", t.id); put("name", n) }) }.onFailure { snack.showSnackbar("${it.message}") } }
        }
    }
    killing?.let { (tc, t) ->
        ConfirmDialog("Kill ${t.name}?", "Ends the terminal and everything running in it.", "Kill", destructive = true, onDismiss = { killing = null }) {
            scope.launch { runCatching { tc.rpc.call("term.kill", params { put("id", t.id) }) }.onFailure { snack.showSnackbar("${it.message}") } }
        }
    }
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun NewTerminalSheet(onDismiss: () -> Unit, hostPicker: @Composable () -> Unit = {}, onCreated: (Term) -> Unit) {
    val conn = activeConnection() ?: return
    val snack = LocalSnack.current
    val scope = rememberCoroutineScope()
    val home = conn.homeDir() ?: "~"
    val st = LocalContext.current.graph.prefs.flow.collectAsState().value
    val agentsAvail by conn.agents.collectAsState()
    var kind by remember { mutableStateOf(if (st.defaultAgent == "claude") "claude" else "agy") }
    var cwd by remember { mutableStateOf(home) }
    var command by remember { mutableStateOf("") }
    var args by remember { mutableStateOf("") }
    var picking by remember { mutableStateOf(false) }
    var busy by remember { mutableStateOf(false) }
    var flagsOpen by remember { mutableStateOf(false) }
    val flags = st.agyFlags
    val cflags = st.claudeFlags
    AppSheet(onDismissRequest = onDismiss) {
        Column(Modifier.padding(horizontal = 20.dp).padding(bottom = 28.dp).imePadding(), verticalArrangement = Arrangement.spacedBy(12.dp)) {
            Text("New terminal", style = MaterialTheme.typography.titleMedium)
            hostPicker()
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                val kinds = buildList {
                    add("agy" to "agy")
                    if (agentsAvail.any { it.id == "claude" && it.available }) add("claude" to "Claude Code")
                    add("shell" to "Shell"); add("cmd" to "Command")
                }
                Row(Modifier.horizontalScroll(rememberScrollState()), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    kinds.forEach { (k, l) -> FilterChip(kind == k, { kind = k }, label = { Text(l) }) }
                }
            }
            OutlinedTextField(shortPath(cwd, home), {}, readOnly = true, label = { Text("Folder") }, modifier = Modifier.fillMaxWidth().clickable { picking = true },
                trailingIcon = { TextButton(onClick = { picking = true }) { Text("Change") } })
            when (kind) {
                "agy", "claude" -> {
                    io.github.sidd065.agentcompanion.ui.components.AgentFlagsCard(kind, kind = "tui", onEdit = { flagsOpen = true })
                    OutlinedTextField(args, { args = it }, label = { Text("More arguments for this terminal (optional)") },
                        placeholder = { Text(if (kind == "claude") "--resume <id>" else "--conversation <id>") },
                        singleLine = true, modifier = Modifier.fillMaxWidth())
                }
                "cmd" -> OutlinedTextField(command, { command = it }, label = { Text("Command") }, placeholder = { Text("npm run dev") }, singleLine = true, modifier = Modifier.fillMaxWidth())
            }
            Button(enabled = !busy && (kind != "cmd" || command.isNotBlank()), modifier = Modifier.fillMaxWidth(), onClick = {
                busy = true
                scope.launch {
                    runCatching {
                        conn.rpc.call("term.create", params {
                            put("kind", kind); put("cwd", cwd)
                            if (kind == "cmd") put("command", command)
                            if (kind == "agy") {
                                put("skipPermissions", flags.skipPermissions)
                                val all = flags.tuiArgs() + io.github.sidd065.agentcompanion.core.splitArgs(args)
                                if (all.isNotEmpty()) put("args", JsonArray(all.map { JsonPrimitive(it) }))
                            }
                            if (kind == "claude") {
                                put("skipPermissions", cflags.skipPermissions)
                                val all = cflags.tuiArgs() + io.github.sidd065.agentcompanion.core.splitArgs(args)
                                if (all.isNotEmpty()) put("args", JsonArray(all.map { JsonPrimitive(it) }))
                            }
                        }).decode(Term.serializer())
                    }.onSuccess { onCreated(it) }.onFailure { snack.showSnackbar("Couldn't start terminal: ${it.message}") }
                    busy = false
                }
            }) { Text("Start") }
        }
    }
    if (picking) DirectoryPicker(conn, cwd, onDismiss = { picking = false }) { cwd = it; picking = false }
    if (flagsOpen) io.github.sidd065.agentcompanion.ui.components.AgentFlagsSheet(kind, conn, onDismiss = { flagsOpen = false })
}

private data class XKey(val label: String, val keyCode: Int = 0, val text: String? = null, val mod: String? = null)

private val extraKeys = listOf(
    XKey("ESC", KeyEvent.KEYCODE_ESCAPE), XKey("TAB", KeyEvent.KEYCODE_TAB), XKey("CTRL", mod = "ctrl"), XKey("ALT", mod = "alt"),
    XKey("←", KeyEvent.KEYCODE_DPAD_LEFT), XKey("↓", KeyEvent.KEYCODE_DPAD_DOWN), XKey("↑", KeyEvent.KEYCODE_DPAD_UP), XKey("→", KeyEvent.KEYCODE_DPAD_RIGHT),
    XKey("⇧TAB", text = "\u001b[Z"), XKey("HOME", KeyEvent.KEYCODE_MOVE_HOME), XKey("END", KeyEvent.KEYCODE_MOVE_END),
    XKey("PGUP", KeyEvent.KEYCODE_PAGE_UP), XKey("PGDN", KeyEvent.KEYCODE_PAGE_DOWN),
    XKey("^C", text = "\u0003"), XKey("^D", text = "\u0004"), XKey("^Z", text = "\u001a"), XKey("^R", text = "\u0012"),
    XKey("/", text = "/"), XKey("-", text = "-"), XKey("|", text = "|"), XKey("~", text = "~"), XKey("@", text = "@"),
)

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun TerminalScreen(termId: String) {
    val ctx = LocalContext.current
    val g = ctx.graph
    val nav = LocalNav.current
    val scope = rememberCoroutineScope()
    val conn = activeConnection() ?: return
    val settings by g.prefs.flow.collectAsState()
    val terms by conn.terms.collectAsState()
    val term = terms.firstOrNull { it.id == termId }
    // The terminal leaves the list when its program exits; keep showing its name.
    var termName by remember(termId) { mutableStateOf("") }
    LaunchedEffect(term?.name) { term?.name?.takeIf { it.isNotBlank() }?.let { termName = it } }
    var generation by remember { mutableIntStateOf(0) }
    val remote = remember(conn, termId, generation) { RemoteTerminal(ctx, conn, termId) }
    val mods = remember { Modifiers() }
    val state by remote.state.collectAsState()
    val exited by remote.exited.collectAsState()
    // A finished program needs no keyboard.
    LaunchedEffect(exited) {
        if (exited) remote.view?.let { v -> ctx.getSystemService(InputMethodManager::class.java)?.hideSoftInputFromWindow(v.windowToken, 0) }
    }
    val ctrl by mods.ctrl.collectAsState()
    val alt by mods.alt.collectAsState()
    var fontSp by remember { mutableIntStateOf(settings.terminalFontSize) }
    var menu by remember { mutableStateOf(false) }
    var killing by remember { mutableStateOf(false) }
    val ex = LocalExtra.current
    val bg = Color(0xFF0C0D10)

    DisposableEffect(remote) {
        onDispose { remote.close() }
    }
    fun px(sp: Int) = TypedValue.applyDimension(TypedValue.COMPLEX_UNIT_SP, sp.toFloat(), ctx.resources.displayMetrics).toInt()

    Scaffold(
        containerColor = bg,
        contentWindowInsets = androidx.compose.foundation.layout.WindowInsets(0, 0, 0, 0),
        topBar = {
            TopAppBar(
                title = {
                    Column {
                        Text(term?.name ?: termName.ifBlank { "Terminal" }, maxLines = 1, overflow = TextOverflow.Ellipsis)
                        Text(
                            listOfNotNull(
                                when (state) { "connecting" -> "connecting…"; "connected" -> "live"; "closed" -> if (exited) "exited" else "detached"; else -> remote.error ?: "error" },
                                term?.state?.takeIf { it.isNotBlank() }?.let { termStateLabel(it) },
                                term?.cwd?.let { shortPath(it, conn.homeDir()) },
                            ).joinToString(" · "),
                            style = MaterialTheme.typography.labelSmall, color = ex.muted, maxLines = 1,
                        )
                    }
                },
                navigationIcon = { IconButton(onClick = { nav.popBackStack() }) { Icon(Icons.AutoMirrored.Filled.ArrowBack, null) } },
                actions = {
                    IconButton(onClick = {
                        remote.view?.let { v -> v.requestFocus(); ctx.getSystemService(InputMethodManager::class.java)?.showSoftInput(v, 0) }
                    }) { Icon(Icons.Outlined.Keyboard, "Keyboard") }
                    IconButton(onClick = { remote.paste() }) { Icon(Icons.Outlined.ContentPaste, "Paste") }
                    Box {
                        IconButton(onClick = { menu = true }) { Icon(Icons.Outlined.MoreVert, null) }
                        DropdownMenu(menu, { menu = false }) {
                            DropdownMenuItem(text = { Text("Larger text") }, leadingIcon = { Icon(Icons.Outlined.TextIncrease, null) }, onClick = {
                                fontSp = (fontSp + 1).coerceAtMost(28); remote.view?.setTextSize(px(fontSp))
                            })
                            DropdownMenuItem(text = { Text("Smaller text") }, leadingIcon = { Icon(Icons.Outlined.TextDecrease, null) }, onClick = {
                                fontSp = (fontSp - 1).coerceAtLeast(7); remote.view?.setTextSize(px(fontSp))
                            })
                            DropdownMenuItem(text = { Text("Reconnect") }, leadingIcon = { Icon(Icons.Outlined.Refresh, null) }, onClick = { menu = false; remote.close(); generation++ })
                            term?.conversationId?.let { cid ->
                                DropdownMenuItem(text = { Text("Open conversation in chat") }, leadingIcon = { Icon(Icons.AutoMirrored.Outlined.Chat, null) }, onClick = {
                                    menu = false; nav.navigate(Routes.chat(cid))
                                })
                            }
                            DropdownMenuItem(text = { Text("Kill terminal") }, leadingIcon = { Icon(Icons.Outlined.Delete, null) }, onClick = { menu = false; killing = true })
                        }
                    }
                },
            )
        },
    ) { pad ->
        Column(Modifier.padding(pad).fillMaxSize().imePadding().background(bg)) {
            Box(Modifier.weight(1f).fillMaxWidth()) {
                key(generation) {
                    AndroidView(
                        modifier = Modifier.fillMaxSize().padding(horizontal = 4.dp),
                        factory = { c ->
                            TerminalView(c, null).apply {
                                val client = ViewClient(c, mods) { scale ->
                                    val next = (fontSp * scale).toInt().coerceIn(7, 28)
                                    if (next != fontSp) { fontSp = next; setTextSize(px(next)); 1f } else scale
                                }
                                client.view = this
                                setTerminalViewClient(client)
                                // setTextSize creates the renderer that setTypeface needs.
                                setTextSize(px(fontSp))
                                setTypeface(Typeface.MONOSPACE)
                                isFocusable = true
                                isFocusableInTouchMode = true
                                keepScreenOn = true
                                remote.view = this
                                attachSession(remote.session)
                                setBackgroundColor(android.graphics.Color.rgb(12, 13, 16))
                                post { requestFocus() }
                            }
                        },
                    )
                }
                if (state == "closed" || state == "error") {
                    Surface(Modifier.align(Alignment.BottomCenter).padding(16.dp), shape = RoundedCornerShape(14.dp), color = MaterialTheme.colorScheme.surfaceContainerHigh) {
                        Row(Modifier.padding(horizontal = 14.dp, vertical = 8.dp), verticalAlignment = Alignment.CenterVertically) {
                            Text(
                                when {
                                    state == "error" -> remote.error ?: "Disconnected"
                                    exited -> "${termName.ifBlank { "The program" }} exited"
                                    else -> "Detached"
                                },
                                style = MaterialTheme.typography.bodySmall, modifier = Modifier.weight(1f, fill = false),
                            )
                            Spacer(Modifier.width(8.dp))
                            if (exited) TextButton(onClick = { nav.popBackStack() }) { Text("Close") }
                            else if (term != null) TextButton(onClick = { generation++ }) { Text("Reconnect") }
                        }
                    }
                }
            }
            // Extra keys
            Row(
                Modifier.fillMaxWidth().background(MaterialTheme.colorScheme.surfaceContainer).horizontalScroll(rememberScrollState()).navigationBarsPadding().padding(vertical = 4.dp),
            ) {
                extraKeys.forEach { k ->
                    val active = (k.mod == "ctrl" && ctrl) || (k.mod == "alt" && alt)
                    Box(
                        Modifier.padding(horizontal = 2.dp).height(38.dp).background(if (active) MaterialTheme.colorScheme.primary else Color.Transparent, RoundedCornerShape(8.dp))
                            .clickable {
                                when {
                                    k.mod == "ctrl" -> mods.ctrl.value = !mods.ctrl.value
                                    k.mod == "alt" -> mods.alt.value = !mods.alt.value
                                    k.text != null -> remote.sendText(k.text)
                                    else -> {
                                        remote.sendKey(k.keyCode, ctrl = mods.ctrl.value, alt = mods.alt.value)
                                        mods.ctrl.value = false; mods.alt.value = false
                                    }
                                }
                            }.padding(horizontal = 12.dp),
                        contentAlignment = Alignment.Center,
                    ) {
                        Text(k.label, style = MaterialTheme.typography.labelLarge, fontFamily = FontFamily.Monospace,
                            color = if (active) MaterialTheme.colorScheme.onPrimary else MaterialTheme.colorScheme.onSurface)
                    }
                }
            }
        }
    }
    if (killing) {
        ConfirmDialog("Kill terminal?", "Ends ${term?.name ?: "it"} and everything running in it.", "Kill", destructive = true, onDismiss = { killing = false }) {
            scope.launch {
                runCatching { conn.rpc.call("term.kill", params { put("id", termId) }) }
                nav.popBackStack()
            }
        }
    }
}

@Composable
private fun key(k: Any, content: @Composable () -> Unit) = androidx.compose.runtime.key(k) { content() }

@Suppress("unused")
private fun unusedSize() = Modifier.size(1.dp)
