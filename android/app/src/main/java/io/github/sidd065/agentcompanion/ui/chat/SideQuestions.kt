package io.github.sidd065.agentcompanion.ui.chat

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ExperimentalLayoutApi
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.WindowInsets
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.automirrored.filled.Send
import androidx.compose.material.icons.automirrored.outlined.Reply
import androidx.compose.material.icons.outlined.Close
import androidx.compose.material.icons.outlined.ContentCopy
import androidx.compose.material.icons.outlined.Delete
import androidx.compose.material.icons.outlined.DeleteSweep
import androidx.compose.material.icons.outlined.FormatQuote
import androidx.compose.material.icons.outlined.MoreVert
import androidx.compose.material.icons.outlined.QuestionAnswer
import androidx.compose.material.icons.outlined.Refresh
import androidx.compose.material.icons.outlined.SelectAll
import androidx.compose.material.icons.outlined.Share
import androidx.compose.material3.AssistChip
import androidx.compose.material3.AssistChipDefaults
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TextField
import androidx.compose.material3.TextFieldDefaults
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableLongStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.input.TextFieldValue
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import io.github.sidd065.agentcompanion.core.Aside
import io.github.sidd065.agentcompanion.graph
import io.github.sidd065.agentcompanion.ui.LocalNav
import io.github.sidd065.agentcompanion.ui.Routes
import io.github.sidd065.agentcompanion.ui.TextHolder
import io.github.sidd065.agentcompanion.ui.activeConnection
import io.github.sidd065.agentcompanion.ui.components.ConfirmDialog
import io.github.sidd065.agentcompanion.ui.components.EmptyState
import io.github.sidd065.agentcompanion.ui.components.LocalSnack
import io.github.sidd065.agentcompanion.ui.components.MarkdownText
import io.github.sidd065.agentcompanion.ui.components.copyText
import io.github.sidd065.agentcompanion.ui.components.fmtDuration
import io.github.sidd065.agentcompanion.ui.components.relTime
import io.github.sidd065.agentcompanion.ui.components.shareText
import io.github.sidd065.agentcompanion.ui.sessions.ConnectionBanner
import io.github.sidd065.agentcompanion.ui.theme.LocalExtra
import androidx.compose.runtime.withFrameNanos
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch

/**
 * Side questions (/btw) about a Claude conversation: answered from what the
 * conversation already holds, never added to it. agentc keeps them, so an
 * answer that arrives after the phone left this screen is still here.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun SideQuestionsScreen(sessionId: String) {
    val ctx = LocalContext.current
    val nav = LocalNav.current
    val snack = LocalSnack.current
    val scope = rememberCoroutineScope()
    val conn = activeConnection() ?: return
    val store = remember(conn, sessionId) { conn.store(sessionId) }
    val asides by store.asides.collectAsState()
    val summary by store.summary.collectAsState()
    val label = summary?.agentLabel ?: "Claude"
    var input by rememberSaveable(stateSaver = TextFieldValue.Saver) { mutableStateOf(TextFieldValue("")) }
    var followUpId by rememberSaveable { mutableStateOf<String?>(null) }
    var sending by remember { mutableStateOf(false) }
    var menu by remember { mutableStateOf(false) }
    var clearing by remember { mutableStateOf(false) }
    val listState = rememberLazyListState()
    val focus = remember { FocusRequester() }
    val pending = asides.any { it.state == "pending" }
    val followUp = followUpId?.let { id -> asides.firstOrNull { it.id == id && it.state == "done" } }

    val g = ctx.graph
    DisposableEffect(store) {
        store.asideViewers++
        store.unseenAside.value = null
        val hide = g.showSession(sessionId)
        g.notifier.cancelAside(conn.config.id, sessionId)
        onDispose {
            store.asideViewers--
            hide()
        }
    }
    LaunchedEffect(store) {
        runCatching { store.refreshAsides() }.onFailure { snack.showSnackbar("Couldn't load side questions: ${it.message}") }
    }
    // Show the newest question at the top, so its answer reads from the start.
    // Later arrivals only move the list if the reader is already near the end,
    // and coming back from another screen keeps the reading position.
    var placedFor by rememberSaveable { mutableStateOf<String?>(null) }
    val newest = asides.lastOrNull()
    LaunchedEffect(newest?.id, newest?.state) {
        if (newest == null) return@LaunchedEffect
        val key = newest.id + ":" + newest.state
        if (key == placedFor) return@LaunchedEffect
        val first = placedFor == null
        placedFor = key
        val last = asides.lastIndex
        if (first) {
            listState.scrollToItem(last)
            withFrameNanos { }
            listState.scrollToItem(last) // again, once everything above has its real height
        } else if ((listState.layoutInfo.visibleItemsInfo.lastOrNull()?.index ?: last) >= last - 1) listState.animateScrollToItem(last)
    }

    fun ask(question: String, follow: String?, onAsked: () -> Unit = {}) {
        val q = question.trim()
        if (q.isEmpty() || sending) return
        sending = true
        scope.launch {
            runCatching { store.askAside(q, follow) }
                .onSuccess { onAsked() }
                .onFailure { snack.showSnackbar(it.message ?: "Couldn't ask") }
            sending = false
        }
    }

    fun delete(id: String?) = scope.launch {
        runCatching { store.deleteAside(id) }.onFailure { snack.showSnackbar("Delete: ${it.message}") }
    }

    Scaffold(
        contentWindowInsets = WindowInsets(0, 0, 0, 0),
        topBar = {
            TopAppBar(
                title = {
                    Column {
                        Text("Side questions", style = MaterialTheme.typography.titleMedium)
                        Text(summary?.displayTitle ?: "", style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.onSurfaceVariant,
                            maxLines = 1, overflow = TextOverflow.Ellipsis)
                    }
                },
                navigationIcon = { IconButton(onClick = { nav.popBackStack() }) { Icon(Icons.AutoMirrored.Filled.ArrowBack, "Back") } },
                actions = {
                    if (asides.any { it.state == "done" }) {
                        Box {
                            IconButton(onClick = { menu = true }) { Icon(Icons.Outlined.MoreVert, "More") }
                            DropdownMenu(menu, { menu = false }) {
                                DropdownMenuItem(text = { Text("Copy all as Markdown") }, leadingIcon = { Icon(Icons.Outlined.ContentCopy, null) },
                                    onClick = { menu = false; ctx.copyText(asidesMarkdown(asides)) })
                                DropdownMenuItem(text = { Text("Clear answered") }, leadingIcon = { Icon(Icons.Outlined.DeleteSweep, null) },
                                    onClick = { menu = false; clearing = true })
                            }
                        }
                    }
                },
            )
        },
    ) { pad ->
        Column(Modifier.padding(pad).fillMaxSize().imePadding()) {
            ConnectionBanner(conn)
            Box(Modifier.weight(1f)) {
                if (asides.isEmpty()) {
                    EmptyState(
                        Icons.Outlined.QuestionAnswer, "Ask on the side",
                        "$label answers from this conversation without adding to it or changing any files, even while it's working. " +
                            "Answers are kept here.",
                    )
                } else {
                    LazyColumn(state = listState, modifier = Modifier.fillMaxSize(), contentPadding = PaddingValues(top = 4.dp, bottom = 16.dp)) {
                        items(asides, key = { it.id }) { a ->
                            AsideEntry(
                                a, parent = asides.firstOrNull { it.id == a.followUp && a.followUp.isNotEmpty() }, label = label,
                                onCopy = { ctx.copyText(a.answer, "Side answer") },
                                onSelect = { nav.navigate(Routes.select(TextHolder.put(a.question, a.answer))) },
                                onFollowUp = { followUpId = a.id; runCatching { focus.requestFocus() } },
                                onQuote = {
                                    Drafts.of(conn.config.id, sessionId).appendQuote(a.answer, heading = "Side answer to: ${a.question.lines().first().take(200)}")
                                    if (nav.previousBackStackEntry?.destination?.route?.startsWith("chat/") == true) nav.popBackStack()
                                    else nav.navigate(Routes.chat(sessionId))
                                },
                                onShare = { ctx.shareText(a.answer, a.question) },
                                onCopyQuestion = { ctx.copyText(a.question, "Side question") },
                                onRetry = { ask(a.question, a.followUp.ifEmpty { null }) { delete(a.id) } },
                                onDelete = { delete(a.id) },
                            )
                        }
                    }
                }
            }
            AsideInput(
                value = input, onChange = { input = it }, followUp = followUp, onClearFollowUp = { followUpId = null },
                pending = pending, sending = sending, label = label, focus = focus,
                onSend = { ask(input.text, followUp?.id) { input = TextFieldValue(""); followUpId = null } },
            )
        }
    }
    if (clearing) {
        ConfirmDialog("Clear side questions?", "Deletes every answered side question in this session.", "Clear", destructive = true,
            onDismiss = { clearing = false }) { delete(null) }
    }
}

/** Every answered side question as one Markdown document. */
private fun asidesMarkdown(list: List<Aside>): String =
    list.filter { it.state == "done" }.joinToString("\n\n---\n\n") { "**Side question:** ${it.question}\n\n${it.answer}" }

@Composable
private fun AsideEntry(
    a: Aside,
    parent: Aside?,
    label: String,
    onCopy: () -> Unit,
    onSelect: () -> Unit,
    onFollowUp: () -> Unit,
    onQuote: () -> Unit,
    onShare: () -> Unit,
    onCopyQuestion: () -> Unit,
    onRetry: () -> Unit,
    onDelete: () -> Unit,
) {
    val ex = LocalExtra.current
    Column(Modifier.fillMaxWidth().padding(top = 12.dp, bottom = 8.dp)) {
        // The question, styled like the user's own messages in the chat.
        Column(Modifier.fillMaxWidth().padding(start = 48.dp, end = 12.dp), horizontalAlignment = Alignment.End) {
            if (parent != null) {
                Row(verticalAlignment = Alignment.CenterVertically, modifier = Modifier.padding(bottom = 4.dp, end = 4.dp)) {
                    Icon(Icons.AutoMirrored.Outlined.Reply, null, Modifier.size(14.dp), tint = ex.muted)
                    Spacer(Modifier.width(4.dp))
                    Text("Follow-up to “${parent.question}”", style = MaterialTheme.typography.labelSmall, color = ex.muted,
                        maxLines = 1, overflow = TextOverflow.Ellipsis)
                }
            }
            Surface(color = ex.userBubble, shape = RoundedCornerShape(18.dp, 18.dp, 4.dp, 18.dp)) {
                SelectionContainer {
                    Text(a.question, Modifier.padding(horizontal = 14.dp, vertical = 10.dp), color = ex.onUserBubble,
                        style = MaterialTheme.typography.bodyMedium)
                }
            }
            Text(relTime(a.ts), style = MaterialTheme.typography.labelSmall, color = ex.muted, modifier = Modifier.padding(top = 3.dp, end = 4.dp))
        }
        Spacer(Modifier.height(4.dp))
        Column(Modifier.fillMaxWidth().padding(horizontal = 16.dp)) {
            when (a.state) {
                "pending" -> AsidePending(label, a.ts)
                "failed" -> {
                    Text(a.error.ifBlank { "No answer." }, color = MaterialTheme.colorScheme.error, style = MaterialTheme.typography.bodyMedium)
                    Row {
                        TextButton(onClick = onRetry) {
                            Icon(Icons.Outlined.Refresh, null, Modifier.size(18.dp)); Spacer(Modifier.width(6.dp)); Text("Ask again")
                        }
                        TextButton(onClick = onDelete) {
                            Icon(Icons.Outlined.Delete, null, Modifier.size(18.dp)); Spacer(Modifier.width(6.dp)); Text("Delete")
                        }
                    }
                }
                else -> {
                    // Long-press anywhere in the answer to select part of it.
                    MarkdownText(a.answer, Modifier.fillMaxWidth(), immediate = true)
                    AnswerActions(onCopy, onSelect, onFollowUp)
                    Row(verticalAlignment = Alignment.CenterVertically) {
                        Text(
                            (if (a.doneTs > a.ts) "Answered in ${fmtDuration(a.doneTs - a.ts)} · " else "") + "not part of the conversation",
                            style = MaterialTheme.typography.labelSmall, color = ex.muted, modifier = Modifier.weight(1f).padding(start = 2.dp),
                        )
                        AnswerMenu(onQuote, onShare, onCopyQuestion, onDelete)
                    }
                }
            }
        }
    }
}

@OptIn(ExperimentalLayoutApi::class)
@Composable
private fun AnswerActions(onCopy: () -> Unit, onSelect: () -> Unit, onFollowUp: () -> Unit) {
    FlowRow(Modifier.padding(top = 6.dp), horizontalArrangement = Arrangement.spacedBy(8.dp), itemVerticalAlignment = Alignment.CenterVertically) {
        AssistChip(onClick = onCopy, label = { Text("Copy") },
            leadingIcon = { Icon(Icons.Outlined.ContentCopy, null, Modifier.size(AssistChipDefaults.IconSize)) })
        AssistChip(onClick = onSelect, label = { Text("Select text") },
            leadingIcon = { Icon(Icons.Outlined.SelectAll, null, Modifier.size(AssistChipDefaults.IconSize)) })
        AssistChip(onClick = onFollowUp, label = { Text("Follow up") },
            leadingIcon = { Icon(Icons.AutoMirrored.Outlined.Reply, null, Modifier.size(AssistChipDefaults.IconSize)) })
    }
}

@Composable
private fun AnswerMenu(onQuote: () -> Unit, onShare: () -> Unit, onCopyQuestion: () -> Unit, onDelete: () -> Unit) {
    var more by remember { mutableStateOf(false) }
    Box {
        IconButton(onClick = { more = true }) { Icon(Icons.Outlined.MoreVert, "More actions") }
        DropdownMenu(more, { more = false }) {
            DropdownMenuItem(text = { Text("Quote in chat") }, leadingIcon = { Icon(Icons.Outlined.FormatQuote, null) },
                onClick = { more = false; onQuote() })
            DropdownMenuItem(text = { Text("Share") }, leadingIcon = { Icon(Icons.Outlined.Share, null) }, onClick = { more = false; onShare() })
            DropdownMenuItem(text = { Text("Copy question") }, leadingIcon = { Icon(Icons.Outlined.ContentCopy, null) },
                onClick = { more = false; onCopyQuestion() })
            DropdownMenuItem(text = { Text("Delete") }, leadingIcon = { Icon(Icons.Outlined.Delete, null) }, onClick = { more = false; onDelete() })
        }
    }
}

@Composable
private fun AsidePending(label: String, since: Long) {
    val ex = LocalExtra.current
    var now by remember { mutableLongStateOf(System.currentTimeMillis()) }
    LaunchedEffect(Unit) {
        while (true) {
            delay(1000)
            now = System.currentTimeMillis()
        }
    }
    Row(verticalAlignment = Alignment.CenterVertically, modifier = Modifier.padding(vertical = 6.dp)) {
        CircularProgressIndicator(Modifier.size(16.dp), strokeWidth = 2.dp)
        Spacer(Modifier.width(10.dp))
        Text("$label is answering… ${fmtDuration((now - since).coerceAtLeast(0))}", style = MaterialTheme.typography.bodyMedium, color = ex.muted)
    }
    Text("You can leave this screen; the answer will wait here.", style = MaterialTheme.typography.labelSmall, color = ex.muted)
}

@Composable
private fun AsideInput(
    value: TextFieldValue,
    onChange: (TextFieldValue) -> Unit,
    followUp: Aside?,
    onClearFollowUp: () -> Unit,
    pending: Boolean,
    sending: Boolean,
    label: String,
    focus: FocusRequester,
    onSend: () -> Unit,
) {
    Surface(color = MaterialTheme.colorScheme.surface, tonalElevation = 2.dp) {
        Column(Modifier.fillMaxWidth().navigationBarsPadding().padding(horizontal = 10.dp, vertical = 8.dp)) {
            if (followUp != null) {
                Row(verticalAlignment = Alignment.CenterVertically, modifier = Modifier.fillMaxWidth().padding(start = 8.dp, bottom = 4.dp)) {
                    Icon(Icons.AutoMirrored.Outlined.Reply, null, Modifier.size(16.dp), tint = MaterialTheme.colorScheme.primary)
                    Spacer(Modifier.width(6.dp))
                    Text("Following up on “${followUp.question}”", Modifier.weight(1f), style = MaterialTheme.typography.labelMedium,
                        maxLines = 1, overflow = TextOverflow.Ellipsis)
                    IconButton(onClick = onClearFollowUp) { Icon(Icons.Outlined.Close, "Cancel follow-up", Modifier.size(18.dp)) }
                }
            }
            Row(verticalAlignment = Alignment.Bottom) {
                TextField(
                    value = value, onValueChange = onChange,
                    modifier = Modifier.weight(1f).heightIn(min = 48.dp, max = 180.dp).focusRequester(focus),
                    placeholder = { Text(if (pending) "Waiting for the answer…" else if (followUp != null) "Ask a follow-up…" else "Ask $label on the side…") },
                    shape = RoundedCornerShape(22.dp),
                    colors = TextFieldDefaults.colors(
                        focusedIndicatorColor = Color.Transparent, unfocusedIndicatorColor = Color.Transparent,
                        focusedContainerColor = MaterialTheme.colorScheme.surfaceContainerHigh, unfocusedContainerColor = MaterialTheme.colorScheme.surfaceContainerHigh,
                    ),
                    maxLines = 8,
                )
                Spacer(Modifier.width(6.dp))
                val can = value.text.isNotBlank() && !pending && !sending
                Box(
                    Modifier.size(46.dp).clip(CircleShape)
                        .background(if (can) MaterialTheme.colorScheme.primary else MaterialTheme.colorScheme.surfaceContainerHighest)
                        .clickable(enabled = can, onClick = onSend),
                    contentAlignment = Alignment.Center,
                ) {
                    if (sending) CircularProgressIndicator(Modifier.size(18.dp), strokeWidth = 2.dp)
                    else Icon(Icons.AutoMirrored.Filled.Send, "Ask", tint = if (can) MaterialTheme.colorScheme.onPrimary else MaterialTheme.colorScheme.onSurfaceVariant)
                }
            }
        }
    }
}

/** Shown in the chat when a side answer arrives while its screen is closed. */
@Composable
fun SideAnswerBanner(a: Aside, onOpen: () -> Unit, onDismiss: () -> Unit) {
    val failed = a.state == "failed"
    Surface(
        color = if (failed) MaterialTheme.colorScheme.errorContainer else MaterialTheme.colorScheme.secondaryContainer,
        shape = RoundedCornerShape(14.dp),
        modifier = Modifier.fillMaxWidth().padding(horizontal = 12.dp, vertical = 4.dp).clip(RoundedCornerShape(14.dp)).clickable(onClick = onOpen),
    ) {
        Row(verticalAlignment = Alignment.CenterVertically, modifier = Modifier.padding(start = 12.dp)) {
            Icon(Icons.Outlined.QuestionAnswer, null, Modifier.size(18.dp))
            Spacer(Modifier.width(10.dp))
            Column(Modifier.weight(1f).padding(vertical = 8.dp)) {
                Text(if (failed) "Side question failed" else "Side answer ready", style = MaterialTheme.typography.labelLarge)
                Text(a.question, style = MaterialTheme.typography.bodySmall, maxLines = 1, overflow = TextOverflow.Ellipsis)
            }
            IconButton(onClick = onDismiss) { Icon(Icons.Outlined.Close, "Dismiss", Modifier.size(18.dp)) }
        }
    }
}
