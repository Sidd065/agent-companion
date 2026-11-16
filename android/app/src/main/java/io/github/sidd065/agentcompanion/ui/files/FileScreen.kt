package io.github.sidd065.agentcompanion.ui.files

import androidx.activity.compose.BackHandler
import androidx.compose.foundation.background
import androidx.compose.foundation.gestures.rememberTransformableState
import androidx.compose.foundation.gestures.transformable
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.BasicTextField
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.automirrored.outlined.Chat
import androidx.compose.material.icons.outlined.Code
import androidx.compose.material.icons.outlined.Delete
import androidx.compose.material.icons.outlined.Download
import androidx.compose.material.icons.outlined.DriveFileRenameOutline
import androidx.compose.material.icons.outlined.Edit
import androidx.compose.material.icons.outlined.FolderOpen
import androidx.compose.material.icons.outlined.Link
import androidx.compose.material.icons.outlined.MoreVert
import androidx.compose.material.icons.automirrored.outlined.OpenInNew
import androidx.compose.material.icons.outlined.Preview
import androidx.compose.material.icons.outlined.Refresh
import androidx.compose.material.icons.outlined.Save
import androidx.compose.material.icons.outlined.Share
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableFloatStateOf
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import coil3.compose.AsyncImage
import io.github.sidd065.agentcompanion.core.FsEntry
import io.github.sidd065.agentcompanion.core.ReadResult
import io.github.sidd065.agentcompanion.graph
import io.github.sidd065.agentcompanion.net.decode
import io.github.sidd065.agentcompanion.net.params
import io.github.sidd065.agentcompanion.ui.LocalNav
import io.github.sidd065.agentcompanion.ui.Routes
import io.github.sidd065.agentcompanion.ui.activeConnection
import io.github.sidd065.agentcompanion.ui.components.ConfirmDialog
import io.github.sidd065.agentcompanion.ui.components.ErrorBox
import io.github.sidd065.agentcompanion.ui.components.LoadingBox
import io.github.sidd065.agentcompanion.ui.components.LocalSnack
import io.github.sidd065.agentcompanion.ui.components.MarkdownText
import io.github.sidd065.agentcompanion.ui.components.TextPromptDialog
import io.github.sidd065.agentcompanion.ui.components.copyText
import io.github.sidd065.agentcompanion.ui.components.fmtBytes
import io.github.sidd065.agentcompanion.ui.components.relTime
import io.github.sidd065.agentcompanion.ui.theme.LocalExtra
import io.github.sidd065.agentcompanion.ui.theme.codeStyle
import kotlinx.coroutines.launch
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.put

private val langByExt = mapOf(
    "kt" to "kotlin", "kts" to "kotlin", "java" to "java", "go" to "go", "py" to "python", "js" to "javascript", "jsx" to "javascript",
    "ts" to "typescript", "tsx" to "typescript", "rs" to "rust", "c" to "c", "h" to "c", "cpp" to "cpp", "hpp" to "cpp", "cs" to "csharp",
    "rb" to "ruby", "php" to "php", "swift" to "swift", "sh" to "shell", "bash" to "shell", "zsh" to "shell", "json" to "json",
    "yaml" to "yaml", "yml" to "yaml", "toml" to "toml", "xml" to "xml", "html" to "html", "css" to "css", "sql" to "sql", "dart" to "dart",
)

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun FileScreen(path: String, startEditing: Boolean) {
    val ctx = LocalContext.current
    val nav = LocalNav.current
    val snack = LocalSnack.current
    val scope = rememberCoroutineScope()
    val conn = activeConnection() ?: return
    val settings by ctx.graph.prefs.flow.collectAsState()
    var stat by remember { mutableStateOf<FsEntry?>(null) }
    var content by remember { mutableStateOf<ReadResult?>(null) }
    var error by remember { mutableStateOf<String?>(null) }
    var editing by remember { mutableStateOf(startEditing) }
    var draft by remember { mutableStateOf("") }
    var preview by remember { mutableStateOf(true) }
    var saving by remember { mutableStateOf(false) }
    var conflict by remember { mutableStateOf(false) }
    var menu by remember { mutableStateOf(false) }
    var renaming by remember { mutableStateOf(false) }
    var deleting by remember { mutableStateOf(false) }
    var discard by remember { mutableStateOf(false) }
    var reload by remember { mutableIntStateOf(0) }
    val name = path.substringAfterLast('/')
    val ext = name.substringAfterLast('.', "").lowercase()
    val isImage = stat?.mime?.startsWith("image/") == true && ext != "svg"
    val isMarkdown = ext == "md" || ext == "markdown"

    LaunchedEffect(path, reload) {
        error = null
        runCatching {
            val st = conn.rpc.call("fs.stat", params { put("path", path) }).decode(FsEntry.serializer())
            stat = st
            if (!(st.mime?.startsWith("image/") == true && ext != "svg") && st.size <= 8L * 1024 * 1024) {
                val r = conn.rpc.call("fs.read", params { put("path", path); put("max", 2L * 1024 * 1024) }, timeoutMs = 120_000).decode(ReadResult.serializer())
                content = r
                if (r.text != null && (editing || draft.isEmpty())) draft = r.text
            }
        }.onFailure { error = it.message }
    }
    val dirty = editing && content?.text != null && draft != content?.text
    BackHandler(enabled = dirty) { discard = true }

    fun save(force: Boolean) {
        saving = true
        scope.launch {
            runCatching {
                val e = conn.rpc.call("fs.write", params {
                    put("path", path); put("text", draft)
                    if (!force) content?.mtime?.let { put("expectMtime", it) }
                }).decode(FsEntry.serializer())
                content = content?.copy(text = draft, mtime = e.mtime, size = e.size)
                stat = e
                snack.showSnackbar("Saved")
            }.onFailure {
                if (it.message?.contains("changed on disk") == true) conflict = true else snack.showSnackbar("Save failed: ${it.message}")
            }
            saving = false
        }
    }

    Scaffold(
        topBar = {
            TopAppBar(
                title = {
                    Column {
                        Text(name + if (dirty) " •" else "", maxLines = 1, overflow = TextOverflow.Ellipsis)
                        stat?.let { Text("${fmtBytes(it.size)} · ${relTime(it.mtime)}", style = MaterialTheme.typography.labelSmall, color = LocalExtra.current.muted) }
                    }
                },
                navigationIcon = { IconButton(onClick = { if (dirty) discard = true else nav.popBackStack() }) { Icon(Icons.AutoMirrored.Filled.ArrowBack, null) } },
                actions = {
                    if (content?.text != null && content?.truncated == false) {
                        if (editing) {
                            IconButton(onClick = { save(false) }, enabled = dirty && !saving) { Icon(Icons.Outlined.Save, "Save") }
                        } else {
                            if (isMarkdown) IconButton(onClick = { preview = !preview }) { Icon(if (preview) Icons.Outlined.Code else Icons.Outlined.Preview, "Toggle preview") }
                            IconButton(onClick = { draft = content?.text ?: ""; editing = true }) { Icon(Icons.Outlined.Edit, "Edit") }
                        }
                    }
                    Box {
                        IconButton(onClick = { menu = true }) { Icon(Icons.Outlined.MoreVert, null) }
                        DropdownMenu(menu, { menu = false }) {
                            DropdownMenuItem(text = { Text("Download to phone") }, leadingIcon = { Icon(Icons.Outlined.Download, null) }, onClick = {
                                menu = false
                                scope.launch { runCatching { FileTransfer.toDownloads(ctx, conn, path) }.onSuccess { snack.showSnackbar("Saved to $it") }.onFailure { snack.showSnackbar("${it.message}") } }
                            })
                            DropdownMenuItem(text = { Text("Share") }, leadingIcon = { Icon(Icons.Outlined.Share, null) }, onClick = {
                                menu = false; scope.launch { runCatching { FileTransfer.share(ctx, conn, path) }.onFailure { snack.showSnackbar("${it.message}") } }
                            })
                            DropdownMenuItem(text = { Text("Open with…") }, leadingIcon = { Icon(Icons.AutoMirrored.Outlined.OpenInNew, null) }, onClick = {
                                menu = false; scope.launch { runCatching { FileTransfer.openWith(ctx, conn, path) }.onFailure { snack.showSnackbar("${it.message}") } }
                            })
                            DropdownMenuItem(text = { Text("Ask agy about this file") }, leadingIcon = { Icon(Icons.AutoMirrored.Outlined.Chat, null) }, onClick = {
                                menu = false; nav.navigate(Routes.newSession(path.substringBeforeLast('/'), "@$name "))
                            })
                            DropdownMenuItem(text = { Text("Open folder") }, leadingIcon = { Icon(Icons.Outlined.FolderOpen, null) }, onClick = {
                                menu = false; nav.navigate(Routes.files(path.substringBeforeLast('/')))
                            })
                            DropdownMenuItem(text = { Text("Copy path") }, leadingIcon = { Icon(Icons.Outlined.Link, null) }, onClick = { menu = false; ctx.copyText(path) })
                            DropdownMenuItem(text = { Text("Rename") }, leadingIcon = { Icon(Icons.Outlined.DriveFileRenameOutline, null) }, onClick = { menu = false; renaming = true })
                            DropdownMenuItem(text = { Text("Reload") }, leadingIcon = { Icon(Icons.Outlined.Refresh, null) }, onClick = { menu = false; editing = false; draft = ""; reload++ })
                            DropdownMenuItem(text = { Text("Delete") }, leadingIcon = { Icon(Icons.Outlined.Delete, null) }, onClick = { menu = false; deleting = true })
                        }
                    }
                },
            )
        },
    ) { pad ->
        Box(Modifier.padding(pad).fillMaxSize().imePadding()) {
            when {
                error != null -> ErrorBox(error!!) { reload++ }
                stat == null -> LoadingBox()
                isImage -> ZoomableImage(io.github.sidd065.agentcompanion.net.RemoteImage(conn.config.id, path = path))
                content == null -> BinaryInfo(stat!!, onDownload = {
                    scope.launch { runCatching { FileTransfer.toDownloads(ctx, conn, path) }.onSuccess { snack.showSnackbar("Saved to $it") }.onFailure { snack.showSnackbar("${it.message}") } }
                }, onOpen = { scope.launch { runCatching { FileTransfer.openWith(ctx, conn, path) }.onFailure { snack.showSnackbar("${it.message}") } } })
                content!!.binary -> BinaryInfo(stat!!, onDownload = {
                    scope.launch { runCatching { FileTransfer.toDownloads(ctx, conn, path) }.onSuccess { snack.showSnackbar("Saved to $it") }.onFailure { snack.showSnackbar("${it.message}") } }
                }, onOpen = { scope.launch { runCatching { FileTransfer.openWith(ctx, conn, path) }.onFailure { snack.showSnackbar("${it.message}") } } })
                editing -> Editor(draft, onChange = { draft = it })
                isMarkdown && preview -> Column(Modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(16.dp)) {
                    MarkdownText(content!!.text.orEmpty())
                    Spacer(Modifier.height(40.dp))
                }
                else -> CodeView(content!!.text.orEmpty(), langByExt[ext], settings.codeWrap, truncated = content!!.truncated)
            }
        }
    }

    if (conflict) {
        AlertDialog(
            onDismissRequest = { conflict = false },
            title = { Text("File changed on disk") },
            text = { Text("Someone (probably the agent) changed $name after you opened it.") },
            confirmButton = { TextButton(onClick = { conflict = false; save(true) }) { Text("Overwrite") } },
            dismissButton = { TextButton(onClick = { conflict = false; editing = false; draft = ""; reload++ }) { Text("Discard mine & reload") } },
        )
    }
    if (discard) {
        ConfirmDialog("Discard changes?", "Your edits to $name haven't been saved.", "Discard", destructive = true, onDismiss = { discard = false }) {
            editing = false; nav.popBackStack()
        }
    }
    if (renaming) {
        TextPromptDialog("Rename", name, "New name", "Rename", onDismiss = { renaming = false }) { n ->
            scope.launch {
                runCatching { conn.rpc.call("fs.rename", params { put("from", path); put("to", path.substringBeforeLast('/') + "/" + n) }) }
                    .onSuccess { nav.popBackStack(); nav.navigate(Routes.file(path.substringBeforeLast('/') + "/" + n)) }
                    .onFailure { snack.showSnackbar("Rename failed: ${it.message}") }
            }
        }
    }
    if (deleting) {
        ConfirmDialog("Delete $name?", "Moves the file to the trash on ${conn.config.label} (or deletes it permanently where there's no trash).", "Delete", destructive = true,
            onDismiss = { deleting = false }) {
            scope.launch {
                val arr = JsonArray(listOf(JsonPrimitive(path)))
                val r = runCatching { conn.rpc.call("fs.delete", params { put("paths", arr) }) }
                if (r.exceptionOrNull()?.message?.contains("TRASH_UNSUPPORTED") == true) {
                    runCatching { conn.rpc.call("fs.delete", params { put("paths", arr); put("permanent", true) }) }
                }
                nav.popBackStack()
            }
        }
    }
}

@Composable
private fun Editor(text: String, onChange: (String) -> Unit) {
    val ex = LocalExtra.current
    val hs = rememberScrollState()
    val vs = rememberScrollState()
    Box(Modifier.fillMaxSize().background(ex.codeBg)) {
        BasicTextField(
            value = text,
            onValueChange = onChange,
            textStyle = codeStyle.copy(color = MaterialTheme.colorScheme.onSurface),
            cursorBrush = SolidColor(MaterialTheme.colorScheme.primary),
            modifier = Modifier.fillMaxSize().verticalScroll(vs).horizontalScroll(hs).padding(12.dp),
        )
    }
}

@Composable
private fun CodeView(text: String, lang: String?, wrap: Boolean, truncated: Boolean) {
    val ex = LocalExtra.current
    if (text.length <= 60_000 && lang != null) {
        Column(Modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(8.dp)) {
            if (truncated) Text("Showing the first 2 MB.", style = MaterialTheme.typography.labelSmall, color = ex.warning)
            MarkdownText("```$lang\n$text\n```")
            Spacer(Modifier.height(40.dp))
        }
        return
    }
    val lines = remember(text) { text.split('\n') }
    val hs = rememberScrollState()
    SelectionContainer {
        LazyColumn(Modifier.fillMaxSize().background(ex.codeBg).let { if (wrap) it else it.horizontalScroll(hs) }.padding(vertical = 8.dp)) {
            if (truncated) item { Text("Showing the first 2 MB.", style = MaterialTheme.typography.labelSmall, color = ex.warning, modifier = Modifier.padding(8.dp)) }
            itemsIndexed(lines) { i, l ->
                Row {
                    Text((i + 1).toString().padStart(5), style = codeStyle, color = ex.muted, modifier = Modifier.width(48.dp))
                    Text(l.replace("\t", "    "), style = codeStyle, softWrap = wrap, modifier = Modifier.padding(end = 16.dp))
                }
            }
        }
    }
}

@Composable
private fun ZoomableImage(url: Any) {
    var scale by remember { mutableFloatStateOf(1f) }
    var offset by remember { mutableStateOf(Offset.Zero) }
    val state = rememberTransformableState { zoom, pan, _ ->
        scale = (scale * zoom).coerceIn(1f, 8f)
        offset = if (scale == 1f) Offset.Zero else offset + pan
    }
    Box(Modifier.fillMaxSize().transformable(state), contentAlignment = Alignment.Center) {
        AsyncImage(
            model = url, contentDescription = null,
            modifier = Modifier.fillMaxWidth().graphicsLayer(scaleX = scale, scaleY = scale, translationX = offset.x, translationY = offset.y),
        )
    }
}

@Composable
private fun BinaryInfo(e: FsEntry, onDownload: () -> Unit, onOpen: () -> Unit) {
    Column(Modifier.fillMaxSize().padding(32.dp), horizontalAlignment = Alignment.CenterHorizontally, verticalArrangement = Arrangement.Center) {
        Icon(entryIcon(e), null, Modifier.padding(8.dp))
        Text(e.name, style = MaterialTheme.typography.titleMedium)
        Text("${fmtBytes(e.size)} · ${e.mime ?: "binary"} · ${e.mode}", style = MaterialTheme.typography.bodySmall, fontFamily = FontFamily.Monospace)
        Spacer(Modifier.height(20.dp))
        Row(horizontalArrangement = Arrangement.spacedBy(10.dp)) {
            Button(onClick = onDownload) { Text("Download") }
            OutlinedButton(onClick = onOpen) { Text("Open with…") }
        }
    }
}
