package io.github.sidd065.agentcompanion.ui.chat

import io.github.sidd065.agentcompanion.ui.components.AppSheet
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.outlined.AddComment
import androidx.compose.material.icons.outlined.ChatBubbleOutline
import androidx.compose.material.icons.outlined.CheckCircle
import androidx.compose.material.icons.outlined.Close
import androidx.compose.material.icons.outlined.MoreVert
import androidx.compose.material.icons.outlined.OpenInNew
import androidx.compose.material.icons.outlined.Refresh
import androidx.compose.material.icons.outlined.Share
import androidx.compose.material3.Button
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.material3.rememberModalBottomSheetState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableLongStateOf
import androidx.compose.runtime.mutableStateListOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import coil3.compose.AsyncImage
import io.github.sidd065.agentcompanion.core.AppJson
import io.github.sidd065.agentcompanion.core.DraftComment
import io.github.sidd065.agentcompanion.core.LiveArtifact
import io.github.sidd065.agentcompanion.core.LiveState
import io.github.sidd065.agentcompanion.net.RemoteImage
import io.github.sidd065.agentcompanion.net.decode
import io.github.sidd065.agentcompanion.net.params
import io.github.sidd065.agentcompanion.ui.LocalNav
import io.github.sidd065.agentcompanion.ui.activeConnection
import io.github.sidd065.agentcompanion.ui.components.LoadingBox
import io.github.sidd065.agentcompanion.ui.components.LocalSnack
import io.github.sidd065.agentcompanion.ui.components.MarkdownText
import io.github.sidd065.agentcompanion.ui.components.relTime
import io.github.sidd065.agentcompanion.ui.files.FileTransfer
import io.github.sidd065.agentcompanion.ui.theme.LocalExtra
import kotlinx.coroutines.launch
import kotlinx.serialization.builtins.ListSerializer
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import kotlinx.serialization.json.longOrNull
import kotlinx.serialization.json.put

/** A commentable piece of an artifact (1-based source lines). */
data class ArtifactBlock(val start: Int, val end: Int, val text: String, val kind: String)

private val listItem = Regex("^\\s*([-*+]|\\d+[.)])\\s+")
private val imageOnly = Regex("^!\\[([^]]*)]\\(([^)\\s]+)(?:\\s+\"[^\"]*\")?\\)\\s*$")

/** Splits markdown into blocks: headings, paragraphs, list items, code fences, tables, quotes. */
fun parseMarkdownBlocks(src: String): List<ArtifactBlock> {
    val lines = src.replace("\r\n", "\n").split('\n')
    val out = mutableListOf<ArtifactBlock>()
    var i = 0
    fun add(s: Int, e: Int, kind: String) {
        val t = lines.subList(s, e + 1).joinToString("\n")
        if (t.isNotBlank()) out += ArtifactBlock(s + 1, e + 1, t, kind)
    }
    while (i < lines.size) {
        val l = lines[i]
        val t = l.trimStart()
        when {
            t.isEmpty() -> i++
            t.startsWith("```") || t.startsWith("~~~") -> {
                val fence = t.take(3)
                var j = i + 1
                while (j < lines.size && !lines[j].trimStart().startsWith(fence)) j++
                add(i, minOf(j, lines.size - 1), "code")
                i = j + 1
            }
            t.startsWith("#") -> { add(i, i, "heading"); i++ }
            t.startsWith("|") -> {
                var j = i
                while (j + 1 < lines.size && lines[j + 1].trimStart().startsWith("|")) j++
                add(i, j, "table"); i = j + 1
            }
            t.startsWith(">") -> {
                var j = i
                while (j + 1 < lines.size && lines[j + 1].trimStart().startsWith(">")) j++
                add(i, j, "quote"); i = j + 1
            }
            listItem.containsMatchIn(l) -> {
                // One block per item, with its indented continuation: nested items,
                // paragraphs after blank lines and fenced code (blank lines included).
                val indent = l.length - t.length
                var j = i
                var fence: String? = null
                while (j + 1 < lines.size) {
                    val n = lines[j + 1]
                    val nt = n.trimStart()
                    val nIndent = n.length - nt.length
                    if (fence != null) {
                        j++
                        if (nt.startsWith(fence)) fence = null
                        continue
                    }
                    if (n.isBlank()) {
                        var k = j + 2
                        while (k < lines.size && lines[k].isBlank()) k++
                        if (k < lines.size && lines[k].length - lines[k].trimStart().length > indent) { j++; continue }
                        break
                    }
                    if (nIndent <= indent) break // a sibling item or dedented text
                    if (nt.startsWith("```") || nt.startsWith("~~~")) fence = nt.take(3)
                    j++
                }
                add(i, j, "item"); i = j + 1
            }
            imageOnly.matches(t) -> { add(i, i, "image"); i++ }
            else -> {
                var j = i
                while (j + 1 < lines.size) {
                    val n = lines[j + 1].trimStart()
                    if (n.isEmpty() || n.startsWith("#") || n.startsWith("```") || n.startsWith("|") || n.startsWith(">") || listItem.containsMatchIn(lines[j + 1])) break
                    j++
                }
                add(i, j, "para"); i = j + 1
            }
        }
    }
    return out
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun ArtifactScreen(sessionId: String, path: String) {
    val conn = activeConnection() ?: return
    val agentName = conn.session(sessionId)?.agentLabel ?: "agy"
    val nav = LocalNav.current
    val snack = LocalSnack.current
    val ctx = LocalContext.current
    val scope = rememberCoroutineScope()
    val ex = LocalExtra.current
    var content by remember { mutableStateOf<String?>(null) }
    var loadedAt by remember { mutableLongStateOf(0L) }
    var error by remember { mutableStateOf<String?>(null) }
    var info by remember { mutableStateOf<LiveArtifact?>(null) }
    var reload by remember { mutableIntStateOf(0) }
    var commenting by remember { mutableStateOf(false) }
    val drafts = remember { mutableStateListOf<DraftComment>() }
    var editing by remember { mutableStateOf<ArtifactBlock?>(null) }
    var sending by remember { mutableStateOf(false) }
    var menu by remember { mutableStateOf(false) }
    val name = path.substringAfterLast('/')
    val kind = info?.kind ?: when (name.substringAfterLast('.', "").lowercase()) {
        "md", "markdown" -> "markdown"
        "png", "jpg", "jpeg", "gif", "webp" -> "image"
        "txt", "json", "log", "py", "go", "js", "ts", "sh", "yaml", "yml", "csv" -> "text"
        else -> "file"
    }
    val uri = info?.uri ?: "file://$path"

    LaunchedEffect(reload) {
        runCatching { conn.rpc.call("session.live", params { put("id", sessionId) }).decode(LiveState.serializer()) }
            .onSuccess { l -> info = l.artifacts.firstOrNull { it.path == path } }
        if (kind == "markdown" || kind == "text") {
            runCatching { conn.rpc.call("session.artifact", params { put("id", sessionId); put("path", path) }).jsonObject }
                .onSuccess { r -> content = r["content"]?.jsonPrimitive?.content; loadedAt = r["mtime"]?.jsonPrimitive?.longOrNull ?: System.currentTimeMillis(); error = null }
                .onFailure { error = it.message }
        }
    }
    // Follow agent edits while the screen is open.
    LaunchedEffect(conn, sessionId) {
        conn.rpc.events.collect { ev ->
            if (ev.name != "session.live") return@collect
            val o = ev.data.jsonObject
            if (o["id"]?.jsonPrimitive?.content != sessionId) return@collect
            runCatching { AppJson.decodeFromJsonElement(LiveState.serializer(), o["live"]!!) }.getOrNull()
                ?.artifacts?.firstOrNull { it.path == path }?.let { info = it }
        }
    }
    val blocks = remember(content, kind) {
        when (kind) {
            "markdown" -> parseMarkdownBlocks(content.orEmpty())
            "text" -> content.orEmpty().split('\n').mapIndexed { i, l -> ArtifactBlock(i + 1, i + 1, l, "line") }
            else -> emptyList()
        }
    }
    val changedSince = info?.updated?.let { it > loadedAt + 1000 && loadedAt > 0 } == true

    fun send(list: List<DraftComment>, text: String? = null) {
        sending = true
        scope.launch {
            runCatching {
                conn.rpc.call("session.comment", params {
                    put("id", sessionId)
                    put("comments", AppJson.encodeToJsonElement(ListSerializer(DraftComment.serializer()), list))
                    if (text != null) put("text", text)
                })
            }.onSuccess {
                drafts.clear()
                commenting = false
                snack.showSnackbar(if (list.any { it.approve }) "Sent: proceed" else "Comments sent to $agentName")
                nav.popBackStack()
            }.onFailure { snack.showSnackbar("Couldn't send: ${it.message}") }
            sending = false
        }
    }

    Scaffold(
        topBar = {
            TopAppBar(
                title = {
                    Column {
                        Text(name, maxLines = 1, overflow = TextOverflow.Ellipsis, style = MaterialTheme.typography.titleMedium)
                        Text(
                            listOfNotNull(info?.summary?.takeIf { it.isNotBlank() }, info?.version?.takeIf { it > 0 }?.let { "v$it" },
                                info?.updated?.takeIf { it > 0 }?.let { relTime(it) }).joinToString(" · "),
                            style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.onSurfaceVariant, maxLines = 1, overflow = TextOverflow.Ellipsis,
                        )
                    }
                },
                navigationIcon = { IconButton(onClick = { nav.popBackStack() }) { Icon(Icons.AutoMirrored.Filled.ArrowBack, null) } },
                actions = {
                    if (kind == "markdown" || kind == "text") {
                        IconButton(onClick = { commenting = !commenting }) {
                            Icon(if (commenting) Icons.Outlined.Close else Icons.Outlined.AddComment, if (commenting) "Stop commenting" else "Comment")
                        }
                    }
                    Box {
                        IconButton(onClick = { menu = true }) { Icon(Icons.Outlined.MoreVert, "More") }
                        DropdownMenu(menu, { menu = false }) {
                            DropdownMenuItem(text = { Text("Reload") }, leadingIcon = { Icon(Icons.Outlined.Refresh, null) }, onClick = { menu = false; reload++ })
                            DropdownMenuItem(text = { Text("Open with…") }, leadingIcon = { Icon(Icons.Outlined.OpenInNew, null) }, onClick = {
                                menu = false
                                scope.launch { runCatching { FileTransfer.openWith(ctx, conn, path) }.onFailure { snack.showSnackbar(it.message ?: "failed") } }
                            })
                            DropdownMenuItem(text = { Text("Share") }, leadingIcon = { Icon(Icons.Outlined.Share, null) }, onClick = {
                                menu = false
                                scope.launch { runCatching { FileTransfer.share(ctx, conn, path) }.onFailure { snack.showSnackbar(it.message ?: "failed") } }
                            })
                        }
                    }
                },
            )
        },
        bottomBar = {
            Column(Modifier.navigationBarsPadding()) {
                when {
                    commenting || drafts.isNotEmpty() -> Surface(tonalElevation = 3.dp) {
                        Row(Modifier.fillMaxWidth().padding(horizontal = 16.dp, vertical = 10.dp), verticalAlignment = Alignment.CenterVertically) {
                            Text(
                                if (drafts.isEmpty()) "Tap anywhere to comment" else "${drafts.size} comment${if (drafts.size > 1) "s" else ""}",
                                style = MaterialTheme.typography.bodyMedium, modifier = Modifier.weight(1f),
                            )
                            if (commenting) TextButton(onClick = { commenting = false }) { Text("Done commenting") }
                            if (drafts.isNotEmpty()) Button(enabled = !sending, onClick = { send(drafts.toList()) }) { Text("Send") }
                        }
                    }
                    info?.needsReview == true -> Surface(color = ex.warning.copy(alpha = 0.12f)) {
                        Row(Modifier.fillMaxWidth().padding(horizontal = 16.dp, vertical = 10.dp), verticalAlignment = Alignment.CenterVertically) {
                            Text("$agentName asked for your review", style = MaterialTheme.typography.bodyMedium, modifier = Modifier.weight(1f))
                            OutlinedButton(onClick = { commenting = true }) { Text("Comment") }
                            Spacer(Modifier.width(8.dp))
                            Button(enabled = !sending, onClick = {
                                send(listOf(DraftComment(kind = "artifact", uri = uri, approve = true)), "This looks good. Please proceed.")
                            }) {
                                Icon(Icons.Outlined.CheckCircle, null, Modifier.size(18.dp))
                                Spacer(Modifier.width(6.dp))
                                Text("Proceed")
                            }
                        }
                    }
                }
            }
        },
    ) { pad ->
        Box(Modifier.padding(pad).fillMaxSize()) {
            when {
                error != null -> Text(error!!, color = MaterialTheme.colorScheme.error, modifier = Modifier.padding(16.dp))
                kind == "image" -> AsyncImage(model = RemoteImage(conn.config.id, path = path), contentDescription = name,
                    contentScale = ContentScale.Fit, modifier = Modifier.fillMaxSize())
                kind == "markdown" || kind == "text" -> if (content == null) LoadingBox(text = "Loading…") else Column(Modifier.fillMaxSize()) {
                    if (changedSince) Surface(color = MaterialTheme.colorScheme.primaryContainer, modifier = Modifier.fillMaxWidth().clickable { reload++ }) {
                        Text("$agentName updated this artifact — tap to reload", style = MaterialTheme.typography.bodySmall, modifier = Modifier.padding(horizontal = 16.dp, vertical = 8.dp))
                    }
                    LazyColumn(Modifier.fillMaxSize(), contentPadding = PaddingValues(horizontal = 12.dp, vertical = 8.dp)) {
                        itemsIndexed(blocks, key = { _, b -> b.start }) { _, b ->
                            val draft = drafts.firstOrNull { it.startLine == b.start && it.endLine == b.end }
                            BlockView(conn.config.id, path, b, kind == "text", commenting, draft) { editing = b }
                        }
                    }
                }
                else -> Column(Modifier.padding(24.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
                    Text("This artifact can't be shown here.", style = MaterialTheme.typography.bodyMedium)
                    info?.url?.let { u -> Text(u, fontFamily = FontFamily.Monospace, style = MaterialTheme.typography.bodySmall) }
                    Button(onClick = { scope.launch { runCatching { FileTransfer.openWith(ctx, conn, path) }.onFailure { snack.showSnackbar(it.message ?: "failed") } } }) {
                        Text("Open with another app")
                    }
                }
            }
        }
    }
    editing?.let { b ->
        val existing = drafts.firstOrNull { it.startLine == b.start && it.endLine == b.end }
        CommentSheet(
            quote = b.text, initial = existing?.comment.orEmpty(), existing = existing != null,
            onDismiss = { editing = null },
            onDelete = { drafts.remove(existing); editing = null },
        ) { text ->
            drafts.remove(existing)
            drafts.add(DraftComment(kind = "artifact", uri = uri, startLine = b.start, endLine = b.end, quote = b.text, comment = text))
            editing = null
        }
    }
}

@Composable
private fun BlockView(hostId: String, artifactPath: String, b: ArtifactBlock, plain: Boolean, commenting: Boolean, draft: DraftComment?, onTap: () -> Unit) {
    val ex = LocalExtra.current
    val hl = draft != null
    Column(
        Modifier.fillMaxWidth()
            .then(if (commenting || hl) Modifier.clickable(onClick = onTap) else Modifier)
            .then(if (hl) Modifier.background(ex.warning.copy(alpha = 0.10f), RoundedCornerShape(6.dp)) else Modifier)
            .then(if (commenting && !hl) Modifier.border(1.dp, MaterialTheme.colorScheme.outlineVariant.copy(alpha = 0.5f), RoundedCornerShape(6.dp)) else Modifier)
            .padding(horizontal = 6.dp, vertical = if (plain) 1.dp else 4.dp),
    ) {
        when {
            plain -> Row {
                Text("${b.start}", style = MaterialTheme.typography.bodySmall, fontFamily = FontFamily.Monospace, color = ex.muted, modifier = Modifier.width(36.dp))
                Text(b.text, style = MaterialTheme.typography.bodySmall, fontFamily = FontFamily.Monospace)
            }
            b.kind == "image" -> {
                val m = imageOnly.find(b.text.trim())
                val src = m?.groupValues?.get(2).orEmpty()
                val resolved = when {
                    src.startsWith("file://") -> src.removePrefix("file://")
                    src.startsWith("/") -> src
                    src.startsWith("http") -> src
                    else -> artifactPath.substringBeforeLast('/') + "/" + src
                }
                AsyncImage(
                    model = if (resolved.startsWith("http")) resolved else RemoteImage(hostId, path = resolved),
                    contentDescription = m?.groupValues?.get(1), contentScale = ContentScale.FillWidth,
                    modifier = Modifier.fillMaxWidth().heightIn(max = 480.dp),
                )
            }
            else -> MarkdownText(b.text, selectable = !commenting)
        }
        draft?.let { d ->
            Row(Modifier.padding(top = 4.dp), verticalAlignment = Alignment.Top) {
                Icon(Icons.Outlined.ChatBubbleOutline, null, Modifier.size(16.dp).padding(top = 2.dp), tint = ex.warning)
                Spacer(Modifier.width(6.dp))
                Text(d.comment, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurface)
            }
        }
    }
    if (!plain) Spacer(Modifier.size(2.dp))
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun CommentSheet(
    quote: String,
    initial: String,
    existing: Boolean,
    title: String = "Comment",
    onDismiss: () -> Unit,
    onDelete: () -> Unit,
    onSave: (String) -> Unit,
) {
    val state = rememberModalBottomSheetState(skipPartiallyExpanded = true)
    var text by remember { mutableStateOf(initial) }
    val focus = remember { FocusRequester() }
    LaunchedEffect(Unit) { runCatching { focus.requestFocus() } }
    AppSheet(onDismissRequest = onDismiss, sheetState = state) {
        Column(Modifier.fillMaxWidth().imePadding().padding(horizontal = 16.dp).padding(bottom = 16.dp), verticalArrangement = Arrangement.spacedBy(10.dp)) {
            Text(title, style = MaterialTheme.typography.titleMedium)
            Surface(color = LocalExtra.current.codeBg, shape = RoundedCornerShape(8.dp)) {
                Text(quote.trim(), style = MaterialTheme.typography.bodySmall, fontFamily = FontFamily.Monospace, maxLines = 8,
                    overflow = TextOverflow.Ellipsis, modifier = Modifier.fillMaxWidth().padding(10.dp))
            }
            OutlinedTextField(text, { text = it }, Modifier.fillMaxWidth().focusRequester(focus), placeholder = { Text("What should change?") }, minLines = 3)
            Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.End) {
                if (existing) TextButton(onClick = onDelete) { Text("Remove") }
                Spacer(Modifier.weight(1f))
                TextButton(onClick = onDismiss) { Text("Cancel") }
                Button(enabled = text.isNotBlank(), onClick = { onSave(text.trim()) }) { Text("Save comment") }
            }
            HorizontalDivider(color = Color.Transparent)
        }
    }
}
