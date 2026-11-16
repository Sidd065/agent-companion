package io.github.sidd065.agentcompanion.ui.chat

import android.content.Intent
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.PickVisualMediaRequest
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.WindowInsets
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.automirrored.outlined.CallSplit
import androidx.compose.material.icons.automirrored.outlined.Undo
import androidx.compose.material.icons.outlined.Archive
import androidx.compose.material.icons.outlined.ContentCopy
import androidx.compose.material.icons.outlined.Delete
import androidx.compose.material.icons.outlined.Difference
import androidx.compose.material.icons.outlined.DriveFileRenameOutline
import androidx.compose.material.icons.outlined.Edit
import androidx.compose.material.icons.outlined.FolderOpen
import androidx.compose.material.icons.outlined.Inventory2
import androidx.compose.material.icons.outlined.KeyboardArrowDown
import androidx.compose.material.icons.outlined.MoreVert
import androidx.compose.material.icons.outlined.Refresh
import androidx.compose.material.icons.outlined.Share
import androidx.compose.material.icons.outlined.StopCircle
import androidx.compose.material.icons.outlined.Terminal
import androidx.compose.material.icons.outlined.QuestionAnswer
import androidx.compose.material.icons.outlined.DataUsage
import androidx.compose.material.icons.outlined.Extension
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.ListItem
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Scaffold
import androidx.compose.material3.SmallFloatingActionButton
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.derivedStateOf
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.runtime.snapshotFlow
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import io.github.sidd065.agentcompanion.core.Artifact
import io.github.sidd065.agentcompanion.core.CompleteHit
import io.github.sidd065.agentcompanion.core.Item
import io.github.sidd065.agentcompanion.core.SessionSummary
import io.github.sidd065.agentcompanion.core.Term
import io.github.sidd065.agentcompanion.graph
import io.github.sidd065.agentcompanion.net.HostConnection
import io.github.sidd065.agentcompanion.net.decode
import io.github.sidd065.agentcompanion.net.params
import io.github.sidd065.agentcompanion.ui.LocalNav
import io.github.sidd065.agentcompanion.ui.Routes
import io.github.sidd065.agentcompanion.ui.activeConnection
import io.github.sidd065.agentcompanion.ui.components.ConfirmDialog
import io.github.sidd065.agentcompanion.ui.components.LoadingBox
import io.github.sidd065.agentcompanion.ui.components.LocalSnack
import io.github.sidd065.agentcompanion.ui.components.StatusDot
import io.github.sidd065.agentcompanion.ui.components.TextPromptDialog
import io.github.sidd065.agentcompanion.ui.components.baseName
import io.github.sidd065.agentcompanion.ui.components.copyText
import io.github.sidd065.agentcompanion.ui.components.fmtBytes
import io.github.sidd065.agentcompanion.ui.components.fmtTokens
import io.github.sidd065.agentcompanion.ui.components.relTime
import io.github.sidd065.agentcompanion.ui.components.shortPath
import io.github.sidd065.agentcompanion.ui.sessions.ConnectionBanner
import io.github.sidd065.agentcompanion.ui.sessions.DirectoryPicker
import io.github.sidd065.agentcompanion.ui.sessions.homeDir
import io.github.sidd065.agentcompanion.ui.theme.LocalExtra
import kotlinx.coroutines.flow.distinctUntilChanged
import kotlinx.coroutines.launch
import kotlinx.serialization.builtins.ListSerializer
import kotlinx.serialization.json.jsonArray
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import kotlinx.serialization.json.put
import java.io.File
import java.util.Locale

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun ChatScreen(sessionId: String, prefill: String) {
    val ctx = LocalContext.current
    val g = ctx.graph
    val nav = LocalNav.current
    val snack = LocalSnack.current
    val scope = rememberCoroutineScope()
    val conn = activeConnection() ?: return
    val settings by g.prefs.flow.collectAsState()
    val store = remember(conn, sessionId) { conn.store(sessionId) }
    val summary by store.summary.collectAsState()
    val extras by store.extras.collectAsState()
    val loaded by store.loaded.collectAsState()
    val items = store.items
    val composer = remember { ComposerState().also { if (prefill.isNotEmpty()) it.setText(prefill) } }
    val focus = remember { FocusRequester() }
    val listState = rememberLazyListState()
    val home = conn.homeDir()

    var sheet by remember { mutableStateOf<String?>(null) } // model|effort|mode|usage|artifacts|help|rewind
    var menu by remember { mutableStateOf(false) }
    var renaming by remember { mutableStateOf(false) }
    var deleting by remember { mutableStateOf(false) }
    var userAction by remember { mutableStateOf<Item?>(null) }
    var agentAction by remember { mutableStateOf<Item?>(null) }
    var rewindTarget by remember { mutableStateOf<Item?>(null) }
    var hostPick by remember { mutableStateOf(false) }

    DisposableEffect(store) {
        store.attach()
        g.visibleSession.value = sessionId
        g.notifier.cancelFor(conn.config.id, sessionId)
        onDispose {
            store.detach()
            if (g.visibleSession.value == sessionId) g.visibleSession.value = null
        }
    }

    fun rpc(label: String, block: suspend () -> Unit) = scope.launch {
        runCatching { block() }.onFailure { snack.showSnackbar("$label: ${it.message}") }
    }

    val s = summary
    val label = s?.agentLabel ?: "agy"
    val terms by conn.terms.collectAsState()
    val isClaude = s?.isClaude == true
    // Live state: background shells, subagents, artifacts, agent messages.
    var live by remember { mutableStateOf<io.github.sidd065.agentcompanion.core.LiveState?>(null) }
    var activityTab by remember { mutableStateOf<String?>(null) }
    var focusTask by remember { mutableStateOf<String?>(null) }
    LaunchedEffect(conn, sessionId, s?.turns, s?.state == "running") {
        runCatching { conn.rpc.call("session.live", params { put("id", sessionId) }).decode(io.github.sidd065.agentcompanion.core.LiveState.serializer()) }
            .onSuccess { live = it }
    }
    LaunchedEffect(conn, sessionId) {
        conn.rpc.events.collect { ev ->
            if (ev.name != "session.live") return@collect
            val o = ev.data.jsonObject
            if (o["id"]?.jsonPrimitive?.content == sessionId) {
                runCatching { io.github.sidd065.agentcompanion.core.AppJson.decodeFromJsonElement(io.github.sidd065.agentcompanion.core.LiveState.serializer(), o["live"]!!) }
                    .onSuccess { live = it }
            }
        }
    }
    val busy = s?.state == "running" || s?.state == "starting" || s?.state == "waiting"
    val rows by remember(settings.showThoughts) { derivedStateOf { buildRows(items.toList(), settings.showThoughts) } }
    val activeId by remember { derivedStateOf { activeAttentionId(items.toList(), store.summary.value?.attention) } }

    // Reversed list: index 0 is the bottom. Stay pinned there while new items
    // arrive unless the user has scrolled up to read.
    val atBottom by remember { derivedStateOf { listState.firstVisibleItemIndex <= 1 && listState.firstVisibleItemScrollOffset < 80 } }
    var follow by remember { mutableStateOf(true) }
    LaunchedEffect(atBottom) { follow = atBottom }
    LaunchedEffect(rows.size) {
        if (follow && rows.isNotEmpty()) listState.scrollToItem(0)
    }

    fun send(text: String, attachments: List<Pending>, delivery: String?) {
        val t = text.trim()
        if (t.startsWith("/") && !t.contains(' ')) {
            val name = t.drop(1)
            if (localCommands.any { it.name == name } && extras.commands.none { it.name == name }) {
                composer.clear(); runLocal(name, ctx = ctx, conn = conn, sessionId = sessionId, summary = s, items = items.toList(), setSheet = { sheet = it },
                    setRenaming = { renaming = true }, nav = { nav.navigate(it) }, rpc = ::rpc, snack = { m -> scope.launch { snack.showSnackbar(m) } })
                return
            }
        }
        val effDelivery = delivery ?: settings.delivery.ifBlank { null }
        composer.clear()
        follow = true
        rpc("Send failed") {
            conn.rpc.call("session.prompt", params {
                put("id", sessionId)
                put("text", text)
                if (attachments.isNotEmpty()) put("attachments", AttachmentUtil.toJson(attachments))
                if (effDelivery != null) put("delivery", effDelivery)
            })
        }
    }

    val actions = remember(conn, sessionId) {
        TranscriptActions(
            conn = conn, sessionId = sessionId,
            onUserLong = { userAction = it },
            onAgentLong = { agentAction = it },
            onSendText = { send(it, emptyList(), null) },
            onPrefill = { composer.setText(it); runCatching { focus.requestFocus() } },
            onOpenFile = {
                // Files in the conversation's brain folder (agy) or Claude's plans are artifacts: open the reviewer.
                if (it.contains("/.gemini/antigravity-cli/brain/$sessionId/") || it.contains("/.claude/plans/")) nav.navigate(Routes.artifact(sessionId, it))
                else nav.navigate(Routes.file(it))
            },
            onQueueAction = { it2, action ->
                if (action == "edit") {
                    composer.setText(it2.text.orEmpty())
                    rpc("Edit") { conn.rpc.call("session.queue", params { put("id", sessionId); put("itemId", it2.id); put("action", "remove") }) }
                } else rpc("Queue") { conn.rpc.call("session.queue", params { put("id", sessionId); put("itemId", it2.id); put("action", action) }) }
            },
            onLiveOutput = { cmd ->
                val t = live?.tasks?.firstOrNull { it.description.trim() == cmd.trim() } ?: live?.tasks?.singleOrNull()
                if (t != null) { focusTask = t.id; activityTab = "tasks" } else scope.launch { snack.showSnackbar("Live output appears once the command runs in the background") }
            },
            agent = summary?.agent ?: "agy",
            onApprovePlan = { switch ->
                rpc("Approve") {
                    if (switch) conn.rpc.call("session.setMode", params { put("id", sessionId); put("mode", "accept-edits") })
                    conn.rpc.call("session.prompt", params { put("id", sessionId); put("text", "The plan looks good. Proceed with the implementation.") })
                }
            },
        )
    }

    val photo = rememberLauncherForActivityResult(ActivityResultContracts.PickMultipleVisualMedia(8)) { uris ->
        scope.launch { uris.forEach { u -> runCatching { composer.attachments.add(AttachmentUtil.fromUri(ctx, u)) }.onFailure { snack.showSnackbar(it.message ?: "attach failed") } } }
    }
    val file = rememberLauncherForActivityResult(ActivityResultContracts.OpenMultipleDocuments()) { uris ->
        scope.launch { uris.forEach { u -> runCatching { composer.attachments.add(AttachmentUtil.fromUri(ctx, u)) }.onFailure { snack.showSnackbar(it.message ?: "attach failed") } } }
    }

    val modelLabel = s?.modelName?.takeIf { it.isNotBlank() } ?: extras.models?.availableModels?.firstOrNull { it.modelId == s?.model }?.name ?: s?.model
    val thoughtOpt = extras.configOptions.firstOrNull { it.id == "thought_level" }
    val modeLabel = extras.modes?.availableModes?.firstOrNull { it.id == s?.mode }?.name ?: s?.mode
    val cmds = remember(extras.commands) {
        localCommands + extras.commands.filter { c -> localCommands.none { it.name == c.name } }.map { CmdEntry(it.name, it.description, false) }
    }

    Scaffold(
        contentWindowInsets = WindowInsets(0, 0, 0, 0),
        topBar = {
            TopAppBar(
                title = {
                    Column(Modifier.clickable { renaming = true }) {
                        Text(s?.displayTitle ?: "Session", maxLines = 1, overflow = TextOverflow.Ellipsis, style = MaterialTheme.typography.titleMedium)
                        Row(verticalAlignment = Alignment.CenterVertically) {
                            StatusDot(s?.state ?: "", s?.attention, size = 7)
                            Spacer(Modifier.width(6.dp))
                            Text(
                                listOfNotNull(stateLabel(s), shortPath(s?.cwd ?: "", home).takeIf { it.isNotBlank() }).joinToString(" · "),
                                style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.onSurfaceVariant, maxLines = 1, overflow = TextOverflow.Ellipsis,
                            )
                        }
                    }
                },
                navigationIcon = { IconButton(onClick = { nav.popBackStack() }) { Icon(Icons.AutoMirrored.Filled.ArrowBack, null) } },
                actions = {
                    ContextRing(s) { sheet = "usage" }
                    IconButton(onClick = { nav.navigate(Routes.diff(sessionId)) }) { Icon(Icons.Outlined.Difference, "Changes") }
                    Box {
                        IconButton(onClick = { menu = true }) { Icon(Icons.Outlined.MoreVert, "More") }
                        DropdownMenu(menu, { menu = false }) {
                            MenuRow(Icons.Outlined.Inventory2, "Artifacts") { menu = false; activityTab = "artifacts" }
                            MenuRow(Icons.Outlined.Terminal, "Background shells & agents") { menu = false; activityTab = if (live?.subagents?.isNotEmpty() == true && live?.tasks.isNullOrEmpty()) "agents" else "tasks" }
                            MenuRow(Icons.Outlined.FolderOpen, "Browse folder") { menu = false; s?.cwd?.let { nav.navigate(Routes.files(it)) } }
                            if (isClaude) MenuRow(Icons.Outlined.QuestionAnswer, "Ask a side question") { menu = false; sheet = "side" }
                            if (isClaude) MenuRow(Icons.Outlined.DataUsage, "Context usage") { menu = false; sheet = "context" }
                            if (isClaude) MenuRow(Icons.Outlined.Extension, "MCP servers") { menu = false; sheet = "mcp" }
                            MenuRow(Icons.Outlined.Terminal, "Continue in $label terminal") { menu = false; openInTerminal(conn, sessionId, s?.agent, nav = { nav.navigate(it) }, rpc = ::rpc) }
                            MenuRow(Icons.AutoMirrored.Outlined.CallSplit, "Fork") { menu = false; fork(conn, sessionId, null, { nav.navigate(it) }, ::rpc, null) }
                            MenuRow(Icons.AutoMirrored.Outlined.Undo, "Rewind…") { menu = false; sheet = "rewind" }
                            MenuRow(Icons.Outlined.DriveFileRenameOutline, "Rename") { menu = false; renaming = true }
                            MenuRow(Icons.Outlined.Share, "Export as Markdown") { menu = false; export(ctx, conn, sessionId, s?.displayTitle ?: "conversation", ::rpc) }
                            MenuRow(Icons.Outlined.Refresh, if (isClaude) "Reload from disk" else "Reload from agy") { menu = false; rpc("Reload") { conn.rpc.call("session.reload", params { put("id", sessionId) }, timeoutMs = 300_000) } }
                            if (s?.live == true) MenuRow(Icons.Outlined.StopCircle, "Stop $label process") { menu = false; rpc("Stop") { conn.rpc.call("session.stop", params { put("id", sessionId) }) } }
                            MenuRow(Icons.Outlined.ContentCopy, "Copy session ID") { menu = false; ctx.copyText(sessionId) }
                            MenuRow(Icons.Outlined.Archive, if (s?.archived == true) "Unarchive" else "Archive") {
                                menu = false
                                rpc("Archive") { conn.rpc.call("session.archive", params { put("id", sessionId); put("archived", s?.archived != true) }) }
                            }
                            MenuRow(Icons.Outlined.Delete, "Delete") { menu = false; deleting = true }
                        }
                    }
                },
            )
        },
    ) { pad ->
        Column(Modifier.padding(pad).fillMaxSize().imePadding()) {
            ConnectionBanner(conn)
            StateBanner(s)
            val termHere = terms.firstOrNull { it.conversationId == sessionId && !it.dead }
            LiveElsewhereBanner(s, onOpenTerminal = termHere?.let { t -> { nav.navigate(Routes.term(t.id)) } }) {
                fork(conn, sessionId, null, { nav.navigate(it) }, ::rpc, null)
            }
            Box(Modifier.weight(1f)) {
                if (!loaded && items.isEmpty()) {
                    LoadingBox(text = if (s?.state == "history" || s?.state == "starting") "Loading conversation…" else "Loading…")
                } else {
                    LazyColumn(state = listState, reverseLayout = true, modifier = Modifier.fillMaxSize(), contentPadding = PaddingValues(top = 8.dp, bottom = 12.dp)) {
                        item(key = "end") { Spacer(Modifier.height(4.dp)) }
                        if (s?.state == "running" || s?.state == "starting") {
                            item(key = "working") { WorkingIndicator(items.lastOrNull { it.kind == "user" }?.ts ?: 0, s.status) }
                        }
                        items(rows.asReversed(), key = { it.key }) { row ->
                            TranscriptRow(row, actions, isLast = row === rows.lastOrNull(), activeId = activeId, wrap = settings.codeWrap, cwd = s?.cwd ?: "")
                        }
                    }
                    if (!follow) {
                        SmallFloatingActionButton(
                            onClick = { follow = true; scope.launch { listState.animateScrollToItem(0) } },
                            modifier = Modifier.align(Alignment.BottomEnd).padding(16.dp),
                        ) { Icon(Icons.Outlined.KeyboardArrowDown, "Scroll to bottom") }
                    }
                }
            }
            ActivityStrip(live, onOpen = { activityTab = it }, onReview = { nav.navigate(Routes.artifact(sessionId, it.path)) })
            val suggestion = s?.suggestion
            if (!suggestion.isNullOrBlank() && !busy && composer.value.text.isBlank()) {
                SuggestionChip(suggestion) { composer.setText(suggestion); runCatching { focus.requestFocus() } }
            }
            Composer(
                agentName = label,
                state = composer, busy = busy, delivery = settings.delivery.ifBlank { if (isClaude) "steer" else "queue" }, enterSends = settings.enterSends,
                modelLabel = modelLabel, thoughtLabel = thoughtOpt?.let { o -> o.options.firstOrNull { it.value == o.currentValue }?.name ?: o.currentValue },
                modeLabel = modeLabel, skipPerms = s?.skipPermissions == true, commands = cmds, focus = focus,
                onSend = ::send,
                onStop = { rpc("Stop") { conn.rpc.call("session.cancel", params { put("id", sessionId) }) } },
                onPickPhoto = { photo.launch(PickVisualMediaRequest(ActivityResultContracts.PickVisualMedia.ImageOnly)) },
                onPickFile = { file.launch(arrayOf("*/*")) },
                onBrowseHost = { hostPick = true },
                onModel = { sheet = "model" },
                onThinking = { sheet = "effort" },
                onMode = { sheet = "mode" },
                onLocalCommand = { name ->
                    runLocal(name, ctx, conn, sessionId, s, items.toList(), { sheet = it }, { renaming = true }, { nav.navigate(it) }, ::rpc) { m -> scope.launch { snack.showSnackbar(m) } }
                },
                complete = { q ->
                    conn.rpc.call("fs.complete", params { put("cwd", s?.cwd ?: ""); put("query", q); put("max", 30) }).decode(ListSerializer(CompleteHit.serializer()))
                },
            )
        }
    }

    // ---- sheets & dialogs ----
    when (sheet) {
        "model" -> PickerSheet(
            "Model",
            extras.models?.availableModels?.map { it.modelId to (it.name + (it.description?.takeIf { d -> d.isNotBlank() && isClaude }?.let { d -> " — $d" } ?: "")) }.orEmpty(),
            s?.model, onDismiss = { sheet = null }, agentName = label,
        ) { id -> rpc("Model") { conn.rpc.call("session.setModel", params { put("id", sessionId); put("model", id) }) } }
        "effort" -> PickerSheet(
            if (isClaude) "Effort" else "Thinking level", thoughtOpt?.options?.map { it.value to it.name }.orEmpty(), thoughtOpt?.currentValue, onDismiss = { sheet = null },
            agentName = label,
        ) { v -> rpc("Thinking level") { conn.rpc.call("session.setConfig", params { put("id", sessionId); put("configId", "thought_level"); put("value", v) }) } }
        "mode" -> PickerSheet(
            "Mode", extras.modes?.availableModes?.map { it.id to (it.name + (it.description?.let { d -> " — $d" } ?: "")) }.orEmpty(), s?.mode, onDismiss = { sheet = null },
            agentName = label,
        ) { v -> rpc("Mode") { conn.rpc.call("session.setMode", params { put("id", sessionId); put("mode", v) }) } }
        "usage" -> UsageSheet(s, onDismiss = { sheet = null }, onCompact = { send("/compact", emptyList(), null) }, onContext = if (isClaude) ({ sheet = "context" }) else null)
        "context" -> ContextUsageSheet(conn, sessionId, onDismiss = { sheet = null }, onCompact = { send("/compact", emptyList(), null) })
        "mcp" -> McpStatusSheet(conn, sessionId, onDismiss = { sheet = null })
        "artifacts" -> activityTab = "artifacts"
        "help" -> HelpSheet(cmds, label, onDismiss = { sheet = null }) { c -> sheet = null; composer.setText("/$c ") }
        "rewind" -> RewindPickerSheet(items.toList(), onDismiss = { sheet = null }) { rewindTarget = it; sheet = null }
    }
    activityTab?.let { tab ->
        ActivitySheet(conn, sessionId, live ?: io.github.sidd065.agentcompanion.core.LiveState(), tab,
            onDismiss = { activityTab = null; focusTask = null }, initialTaskId = focusTask) { a ->
            activityTab = null
            if (a.kind == "url" && a.url != null) runCatching { ctx.startActivity(android.content.Intent(android.content.Intent.ACTION_VIEW, android.net.Uri.parse(a.url))) }
            else nav.navigate(Routes.artifact(sessionId, a.path))
        }
    }
    userAction?.let { it ->
        MessageActionSheet(
            it, isUser = true, onDismiss = { userAction = null },
            onCopy = { ctx.copyText(it.text.orEmpty()) },
            onEdit = { rewindTarget = it },
            onFork = { fork(conn, sessionId, it.id, { r -> nav.navigate(r) }, ::rpc) { p -> composer.setText(p) } },
            onRewind = { rewindTarget = it },
        )
    }
    agentAction?.let { it ->
        MessageActionSheet(
            it, isUser = false, onDismiss = { agentAction = null },
            onCopy = { ctx.copyText(it.text.orEmpty()) },
            onEdit = null,
            onFork = { fork(conn, sessionId, it.id, { r -> nav.navigate(r) }, ::rpc, null) },
            onRewind = { rewindTarget = it },
            onShare = {
                ctx.startActivity(Intent.createChooser(Intent(Intent.ACTION_SEND).setType("text/plain").putExtra(Intent.EXTRA_TEXT, it.text.orEmpty()), "Share"))
            },
        )
    }
    rewindTarget?.let { target ->
        RewindDialog(conn, sessionId, target, onDismiss = { rewindTarget = null }) { prefillText ->
            if (!prefillText.isNullOrEmpty()) composer.setText(prefillText)
        }
    }
    if (renaming) {
        TextPromptDialog("Rename session", s?.title ?: "", "Title", "Rename", onDismiss = { renaming = false }) { t ->
            rpc("Rename") { conn.rpc.call("session.rename", params { put("id", sessionId); put("title", t) }) }
        }
    }
    if (deleting) {
        ConfirmDialog("Delete session?", "This permanently deletes the conversation from ${io.github.sidd065.agentcompanion.core.agentProduct(s?.agent)}.", "Delete", destructive = true, onDismiss = { deleting = false }) {
            rpc("Delete") {
                conn.rpc.call("session.delete", params { put("id", sessionId) })
                nav.popBackStack()
            }
        }
    }
    if (sheet == "side") SideQuestionDialog(conn, sessionId, label, onDismiss = { sheet = null })
    if (hostPick) {
        HostFilePicker(conn, s?.cwd ?: "", onDismiss = { hostPick = false }) { path ->
            hostPick = false
            val rel = path.removePrefix((s?.cwd ?: "") + "/")
            composer.attachments.add(Pending("path", rel, path = path))
        }
    }
}

private fun stateLabel(s: SessionSummary?): String? = when {
    s == null -> null
    s.attention == "question" -> "question for you"
    s.attention == "plan" -> "plan to review"
    s.attention == "permission" || s.pendingPermissions > 0 -> "needs permission"
    s.state == "running" -> "working"
    s.state == "starting" -> "starting ${s.agentLabel}"
    s.status == "compacting" -> "compacting"
    s.state == "idle" -> "ready"
    s.state == "sleeping" -> "sleeping"
    s.state == "history" -> "not loaded"
    s.state == "error" -> "error"
    else -> s.state
}

@Composable
private fun StateBanner(s: SessionSummary?) {
    val ex = LocalExtra.current
    val text = when {
        s == null -> null
        s.state == "sleeping" && s.error != null -> "${s.agentLabel} stopped: ${s.error}. Send a message to restart it."
        s.state == "error" -> "Couldn't start ${s.agentLabel}: ${s.error}"
        s.error != null && s.state != "running" -> s.error
        else -> null
    } ?: return
    Surface(color = ex.danger.copy(alpha = 0.12f), modifier = Modifier.fillMaxWidth()) {
        Text(text, style = MaterialTheme.typography.bodySmall, modifier = Modifier.padding(horizontal = 16.dp, vertical = 8.dp), maxLines = 4)
    }
}

@Composable
private fun ContextRing(s: SessionSummary?, onClick: () -> Unit) {
    if (s == null || s.contextSize <= 0) return
    val frac = (s.contextUsed.toFloat() / s.contextSize).coerceIn(0f, 1f)
    Box(Modifier.size(40.dp).clickable(onClick = onClick), contentAlignment = Alignment.Center) {
        CircularProgressIndicator(progress = { maxOf(frac, 0.02f) }, modifier = Modifier.size(22.dp), strokeWidth = 3.dp,
            color = if (frac > 0.8f) LocalExtra.current.warning else MaterialTheme.colorScheme.primary,
            trackColor = MaterialTheme.colorScheme.surfaceContainerHighest)
    }
}

@Composable
private fun MenuRow(icon: androidx.compose.ui.graphics.vector.ImageVector, label: String, onClick: () -> Unit) {
    DropdownMenuItem(text = { Text(label) }, leadingIcon = { Icon(icon, null) }, onClick = onClick)
}

private fun runLocal(
    name: String,
    ctx: android.content.Context,
    conn: HostConnection,
    sessionId: String,
    summary: SessionSummary?,
    items: List<Item>,
    setSheet: (String) -> Unit,
    setRenaming: () -> Unit,
    nav: (String) -> Unit,
    rpc: (String, suspend () -> Unit) -> Any,
    snack: (String) -> Unit,
) {
    when (name) {
        "model" -> setSheet("model")
        "effort" -> setSheet("effort")
        "mode" -> setSheet("mode")
        "context", "usage" -> setSheet("usage")
        "artifacts" -> setSheet("artifacts")
        "help" -> setSheet("help")
        "rewind" -> setSheet("rewind")
        "btw" -> if (summary?.isClaude == true) setSheet("side") else snack("Side questions are a Claude Code feature")
        "mcp" -> if (summary?.isClaude == true) setSheet("mcp") else snack("Open Host → MCP servers for agy")
        "rename" -> setRenaming()
        "new", "clear" -> nav(Routes.newSession(summary?.cwd ?: ""))
        "fork" -> fork(conn, sessionId, null, nav, rpc, null)
        "diff" -> nav(Routes.diff(sessionId))
        "files" -> summary?.cwd?.let { nav(Routes.files(it)) }
        "terminal" -> openInTerminal(conn, sessionId, summary?.agent, nav, rpc)
        "export" -> export(ctx, conn, sessionId, summary?.displayTitle ?: "conversation", rpc)
        "copy" -> {
            val last = items.lastOrNull { it.kind == "agent" && !it.text.isNullOrBlank() }
            if (last != null) { ctx.copyText(last.text!!); snack("Copied the latest response") } else snack("Nothing to copy yet")
        }
        "stop" -> rpc("Stop") { conn.rpc.call("session.stop", params { put("id", sessionId) }) }
        "reload" -> rpc("Reload") { conn.rpc.call("session.reload", params { put("id", sessionId) }, timeoutMs = 300_000) }
    }
}

private fun fork(conn: HostConnection, sessionId: String, itemId: String?, nav: (String) -> Unit, rpc: (String, suspend () -> Unit) -> Any, onPrefill: ((String) -> Unit)?) {
    rpc("Fork") {
        val r = conn.rpc.call("session.fork", params { put("id", sessionId); if (itemId != null) put("itemId", itemId) }, timeoutMs = 300_000).jsonObject
        val id = r["session"]?.jsonObject?.get("id")?.jsonPrimitive?.content ?: error("no session")
        val prefill = r["prefill"]?.jsonPrimitive?.content.orEmpty()
        nav(Routes.chat(id, prefill))
        if (prefill.isNotEmpty()) onPrefill?.invoke("")
    }
}

private fun openInTerminal(conn: HostConnection, sessionId: String, agent: String?, nav: (String) -> Unit, rpc: (String, suspend () -> Unit) -> Any) {
    rpc("Open in terminal") {
        val st = io.github.sidd065.agentcompanion.App.instance.graph.prefs.value
        val t = conn.rpc.call("session.openInTerminal", params {
            put("id", sessionId); put("skipPermissions", st.skipPermissions(agent))
            put("args", kotlinx.serialization.json.JsonArray(st.tuiResumeArgs(agent).map { kotlinx.serialization.json.JsonPrimitive(it) }))
        }).decode(Term.serializer())
        nav(Routes.term(t.id))
    }
}

private fun export(ctx: android.content.Context, conn: HostConnection, sessionId: String, title: String, rpc: (String, suspend () -> Unit) -> Any) {
    rpc("Export") {
        val md = conn.rpc.call("session.export", params { put("id", sessionId) }, timeoutMs = 120_000).jsonObject["markdown"]?.jsonPrimitive?.content.orEmpty()
        val dir = File(ctx.cacheDir, "shared").apply { mkdirs() }
        val f = File(dir, title.replace(Regex("[^A-Za-z0-9 ._-]"), "_").take(60) + ".md")
        f.writeText(md)
        val uri = androidx.core.content.FileProvider.getUriForFile(ctx, ctx.packageName + ".files", f)
        ctx.startActivity(
            Intent.createChooser(Intent(Intent.ACTION_SEND).setType("text/markdown").putExtra(Intent.EXTRA_STREAM, uri).addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION), "Export conversation")
                .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK),
        )
    }
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun PickerSheet(title: String, options: List<Pair<String, String>>, current: String?, onDismiss: () -> Unit, agentName: String = "agy", onPick: (String) -> Unit) {
    ModalBottomSheet(onDismissRequest = onDismiss) {
        Text(title, style = MaterialTheme.typography.titleMedium, modifier = Modifier.padding(horizontal = 20.dp, vertical = 8.dp))
        if (options.isEmpty()) {
            Text("Not available yet — send a message first so $agentName reports its options.", style = MaterialTheme.typography.bodySmall, modifier = Modifier.padding(20.dp))
        }
        LazyColumn(Modifier.padding(bottom = 24.dp)) {
            items(options) { (id, label) ->
                ListItem(
                    headlineContent = { Text(label) },
                    trailingContent = { if (id == current) Text("✓", color = MaterialTheme.colorScheme.primary) },
                    modifier = Modifier.clickable { onDismiss(); onPick(id) },
                )
            }
        }
    }
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun UsageSheet(s: SessionSummary?, onDismiss: () -> Unit, onCompact: () -> Unit, onContext: (() -> Unit)? = null) {
    ModalBottomSheet(onDismissRequest = onDismiss) {
        Column(Modifier.padding(horizontal = 20.dp).padding(bottom = 28.dp), verticalArrangement = Arrangement.spacedBy(10.dp)) {
            Text("Context & usage", style = MaterialTheme.typography.titleMedium)
            if (s != null && s.contextSize > 0) {
                val frac = s.contextUsed.toFloat() / s.contextSize
                Text("${fmtTokens(s.contextUsed)} of ${fmtTokens(s.contextSize)} tokens in context (${String.format(Locale.US, "%.1f", frac * 100)}%)")
                LinearProgressIndicator(progress = { frac.coerceIn(0f, 1f) }, modifier = Modifier.fillMaxWidth())
            }
            s?.usage?.let { u ->
                Text("This session (${s.turns} turns): ${fmtTokens(u.totalTokens)} tokens", style = MaterialTheme.typography.bodyMedium)
                Text("Input ${fmtTokens(u.inputTokens)} · output ${fmtTokens(u.outputTokens)} · cached ${fmtTokens(u.cachedReadTokens)} · thinking ${fmtTokens(u.thoughtTokens)}",
                    style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                if (u.costUsd > 0) Text("Cost so far: ${fmtUsd(u.costUsd)}", style = MaterialTheme.typography.bodyMedium)
            }
            s?.modelName?.let { Text("Model: $it", style = MaterialTheme.typography.bodySmall) }
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                OutlinedButton(onClick = { onDismiss(); onCompact() }) { Text("Compact now") }
                if (onContext != null) OutlinedButton(onClick = { onDismiss(); onContext() }) { Text("What's using context") }
            }
        }
    }
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun ArtifactsSheet(conn: HostConnection, sessionId: String, onDismiss: () -> Unit, onOpen: (String) -> Unit) {
    var list by remember { mutableStateOf<List<Artifact>?>(null) }
    var err by remember { mutableStateOf<String?>(null) }
    LaunchedEffect(Unit) {
        runCatching { list = conn.rpc.call("session.artifacts", params { put("id", sessionId) }).decode(ListSerializer(Artifact.serializer())) }
            .onFailure { err = it.message }
    }
    ModalBottomSheet(onDismissRequest = onDismiss) {
        Text("Artifacts", style = MaterialTheme.typography.titleMedium, modifier = Modifier.padding(horizontal = 20.dp, vertical = 8.dp))
        Text("Plans, task lists, walkthroughs and media the agent saved for this conversation.", style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant, modifier = Modifier.padding(horizontal = 20.dp))
        when {
            err != null -> Text(err!!, color = MaterialTheme.colorScheme.error, modifier = Modifier.padding(20.dp))
            list == null -> LoadingBox()
            list!!.isEmpty() -> Text("No artifacts yet.", modifier = Modifier.padding(20.dp))
            else -> LazyColumn(Modifier.padding(bottom = 24.dp)) {
                items(list!!) { a ->
                    ListItem(headlineContent = { Text(a.name) }, supportingContent = { Text("${fmtBytes(a.size)} · ${relTime(a.mtime)}") },
                        modifier = Modifier.clickable { onDismiss(); onOpen(a.path) })
                }
            }
        }
    }
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun HelpSheet(cmds: List<CmdEntry>, agentName: String, onDismiss: () -> Unit, onPick: (String) -> Unit) {
    ModalBottomSheet(onDismissRequest = onDismiss) {
        Text("Commands", style = MaterialTheme.typography.titleMedium, modifier = Modifier.padding(horizontal = 20.dp, vertical = 8.dp))
        LazyColumn(Modifier.padding(bottom = 24.dp)) {
            items(cmds) { c ->
                ListItem(headlineContent = { Text("/" + c.name, fontFamily = FontFamily.Monospace) }, supportingContent = { Text(c.description) },
                    overlineContent = { Text(if (c.local) "app" else agentName) }, modifier = Modifier.clickable { onPick(c.name) })
            }
        }
    }
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun RewindPickerSheet(items: List<Item>, onDismiss: () -> Unit, onPick: (Item) -> Unit) {
    val users = items.filter { it.kind == "user" && it.state.isNullOrEmpty() }.drop(1).reversed()
    ModalBottomSheet(onDismissRequest = onDismiss) {
        Text("Rewind to before…", style = MaterialTheme.typography.titleMedium, modifier = Modifier.padding(horizontal = 20.dp, vertical = 8.dp))
        if (users.isEmpty()) Text("Nothing to rewind yet.", modifier = Modifier.padding(20.dp))
        LazyColumn(Modifier.padding(bottom = 24.dp)) {
            items(users) { u ->
                ListItem(headlineContent = { Text(u.text.orEmpty(), maxLines = 2, overflow = TextOverflow.Ellipsis) }, supportingContent = { Text(relTime(u.ts)) },
                    modifier = Modifier.clickable { onPick(u) })
            }
        }
    }
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun MessageActionSheet(
    item: Item,
    isUser: Boolean,
    onDismiss: () -> Unit,
    onCopy: () -> Unit,
    onEdit: (() -> Unit)?,
    onFork: () -> Unit,
    onRewind: () -> Unit,
    onShare: (() -> Unit)? = null,
) {
    ModalBottomSheet(onDismissRequest = onDismiss) {
        Column(Modifier.padding(bottom = 24.dp)) {
            Text(item.text.orEmpty().take(160), style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant,
                maxLines = 3, overflow = TextOverflow.Ellipsis, modifier = Modifier.padding(horizontal = 20.dp, vertical = 8.dp))
            HorizontalDivider()
            ListItem(headlineContent = { Text("Copy message") }, leadingContent = { Icon(Icons.Outlined.ContentCopy, null) }, modifier = Modifier.clickable { onDismiss(); onCopy() })
            if (onEdit != null) ListItem(headlineContent = { Text("Edit & resend") }, supportingContent = { Text("Rewinds the conversation to before this message") },
                leadingContent = { Icon(Icons.Outlined.Edit, null) }, modifier = Modifier.clickable { onDismiss(); onEdit() })
            ListItem(headlineContent = { Text(if (isUser) "Fork from here" else "Fork after this response") },
                supportingContent = { Text(if (isUser) "New session ending before this message" else "New session that continues from here") },
                leadingContent = { Icon(Icons.AutoMirrored.Outlined.CallSplit, null) }, modifier = Modifier.clickable { onDismiss(); onFork() })
            ListItem(headlineContent = { Text(if (isUser) "Rewind to before this" else "Rewind to here") }, leadingContent = { Icon(Icons.AutoMirrored.Outlined.Undo, null) },
                modifier = Modifier.clickable { onDismiss(); onRewind() })
            if (onShare != null) ListItem(headlineContent = { Text("Share") }, leadingContent = { Icon(Icons.Outlined.Share, null) }, modifier = Modifier.clickable { onDismiss(); onShare() })
        }
    }
}

@Composable
private fun RewindDialog(conn: HostConnection, sessionId: String, target: Item, onDismiss: () -> Unit, onDone: (String?) -> Unit) {
    val scope = rememberCoroutineScope()
    val snack = LocalSnack.current
    var files by remember { mutableStateOf<Int?>(null) }
    var busy by remember { mutableStateOf(false) }
    LaunchedEffect(target.id) {
        runCatching {
            val r = conn.rpc.call("session.revertPreview", params { put("id", sessionId); put("itemId", target.id) }).jsonObject
            val pv = r["preview"]?.jsonObject
            files = pv?.get("codeEditPreviews")?.jsonArray?.size ?: pv?.get("filesChanged")?.jsonArray?.size ?: 0
        }.onFailure { files = -1 }
    }
    fun go(conversationOnly: Boolean) {
        busy = true
        scope.launch {
            runCatching {
                val r = conn.rpc.call("session.rewind", params { put("id", sessionId); put("itemId", target.id); put("conversationOnly", conversationOnly) }, timeoutMs = 300_000).jsonObject
                onDone(r["prefill"]?.jsonPrimitive?.content)
            }.onFailure { snack.showSnackbar("Rewind failed: ${it.message}") }
            busy = false
            onDismiss()
        }
    }
    AlertDialog(
        onDismissRequest = { if (!busy) onDismiss() },
        title = { Text(if (target.kind == "user") "Rewind to before this message?" else "Rewind to this point?") },
        text = {
            Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
                Text("Later messages are removed from the conversation." + if (target.kind == "user") " The message goes back into the composer so you can edit it." else "")
                when (files) {
                    null -> Text("Checking file changes…", style = MaterialTheme.typography.bodySmall)
                    -1 -> {}
                    0 -> Text("No file edits to undo.", style = MaterialTheme.typography.bodySmall)
                    else -> Text("$files file change(s) can be reverted too.", style = MaterialTheme.typography.bodySmall)
                }
                if (busy) LinearProgressIndicator(Modifier.fillMaxWidth())
            }
        },
        confirmButton = {
            Column(horizontalAlignment = Alignment.End) {
                Button(onClick = { go(true) }, enabled = !busy) { Text("Conversation only") }
                if ((files ?: 0) > 0) TextButton(onClick = { go(false) }, enabled = !busy) { Text("Conversation and files") }
            }
        },
        dismissButton = { TextButton(onClick = onDismiss, enabled = !busy) { Text("Cancel") } },
    )
}

/** Picks a file on the host (directories navigate; files are returned). */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun HostFilePicker(conn: HostConnection, start: String, onDismiss: () -> Unit, onPick: (String) -> Unit) {
    var path by remember { mutableStateOf(start) }
    var entries by remember { mutableStateOf<io.github.sidd065.agentcompanion.core.FsList?>(null) }
    LaunchedEffect(path) {
        runCatching { entries = conn.rpc.call("fs.list", params { put("path", path) }).decode(io.github.sidd065.agentcompanion.core.FsList.serializer()) }
    }
    ModalBottomSheet(onDismissRequest = onDismiss) {
        Text("Attach a file from ${conn.config.label}", style = MaterialTheme.typography.titleMedium, modifier = Modifier.padding(horizontal = 20.dp))
        Text(entries?.path ?: path, style = MaterialTheme.typography.bodySmall, fontFamily = FontFamily.Monospace, modifier = Modifier.padding(horizontal = 20.dp, vertical = 4.dp))
        LazyColumn(Modifier.padding(bottom = 24.dp)) {
            entries?.let { l ->
                if (l.parent.isNotBlank()) item { ListItem(headlineContent = { Text("..") }, modifier = Modifier.clickable { path = l.parent }) }
                items(l.entries, key = { it.path }) { e ->
                    ListItem(
                        headlineContent = { Text(e.name + if (e.isDir) "/" else "") },
                        supportingContent = { if (!e.isDir) Text(fmtBytes(e.size)) },
                        modifier = Modifier.clickable { if (e.isDir) path = e.path else onPick(e.path) },
                    )
                }
            }
        }
    }
}
