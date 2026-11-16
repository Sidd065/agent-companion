package io.github.sidd065.agentcompanion.ui.sessions

import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.PickVisualMediaRequest
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.Image
import androidx.compose.foundation.clickable
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.outlined.AttachFile
import androidx.compose.material.icons.outlined.Close
import androidx.compose.material.icons.outlined.CreateNewFolder
import androidx.compose.material.icons.outlined.Folder
import androidx.compose.material.icons.outlined.FolderOpen
import androidx.compose.material.icons.outlined.History
import androidx.compose.material.icons.outlined.Image
import androidx.compose.material.icons.outlined.Home
import androidx.compose.material.icons.automirrored.outlined.InsertDriveFile
import androidx.compose.material3.AssistChip
import androidx.compose.material3.Button
import androidx.compose.material3.Card
import androidx.compose.material3.CardDefaults
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.FilterChip
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.ListItem
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Switch
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.material3.rememberModalBottomSheetState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateListOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import io.github.sidd065.agentcompanion.core.AgyModel
import io.github.sidd065.agentcompanion.core.FsList
import io.github.sidd065.agentcompanion.graph
import io.github.sidd065.agentcompanion.net.HostConnection
import io.github.sidd065.agentcompanion.net.decode
import io.github.sidd065.agentcompanion.net.params
import io.github.sidd065.agentcompanion.ui.LocalNav
import io.github.sidd065.agentcompanion.ui.Routes
import io.github.sidd065.agentcompanion.ui.ShareInbox
import io.github.sidd065.agentcompanion.ui.activeConnection
import io.github.sidd065.agentcompanion.ui.chat.AttachmentUtil
import io.github.sidd065.agentcompanion.ui.chat.Pending
import io.github.sidd065.agentcompanion.ui.components.AgyFlagsCard
import io.github.sidd065.agentcompanion.ui.components.AgyFlagsSheet
import io.github.sidd065.agentcompanion.ui.components.LocalSnack
import io.github.sidd065.agentcompanion.ui.components.TextPromptDialog
import io.github.sidd065.agentcompanion.ui.components.baseName
import io.github.sidd065.agentcompanion.ui.components.shortPath
import io.github.sidd065.agentcompanion.ui.theme.LocalExtra
import kotlinx.coroutines.launch
import kotlinx.serialization.builtins.ListSerializer
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.jsonArray
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import kotlinx.serialization.json.put

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun NewSessionScreen(initialCwd: String, initialText: String, initialHost: String = "") {
    val g = LocalContext.current.graph
    val settings by g.prefs.flow.collectAsState()
    var hostId by androidx.compose.runtime.saveable.rememberSaveable {
        mutableStateOf(initialHost.takeIf { h -> settings.hosts.any { it.id == h } } ?: settings.activeHost?.id ?: "")
    }
    val text = androidx.compose.runtime.saveable.rememberSaveable { mutableStateOf(initialText) }
    io.github.sidd065.agentcompanion.ui.HostScope(hostId) {
        androidx.compose.runtime.key(hostId) {
            NewSessionContent(
                initialCwd = if (hostId == initialHost || initialHost.isBlank()) initialCwd else "",
                textState = text,
                hostPicker = {
                    if (settings.hosts.size > 1) {
                        Text("Computer", style = MaterialTheme.typography.labelLarge)
                        Row(Modifier.horizontalScroll(rememberScrollState()), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                            settings.hosts.forEach { h ->
                                FilterChip(selected = h.id == hostId, onClick = { hostId = h.id }, label = { Text(h.label) })
                            }
                        }
                    }
                },
            )
        }
    }
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun NewSessionContent(initialCwd: String, textState: androidx.compose.runtime.MutableState<String>, hostPicker: @Composable () -> Unit) {
    val ctx = LocalContext.current
    val g = ctx.graph
    val nav = LocalNav.current
    val snack = LocalSnack.current
    val scope = rememberCoroutineScope()
    val conn = activeConnection() ?: return
    val settings by g.prefs.flow.collectAsState()
    val agents by conn.agents.collectAsState()
    val home = conn.homeDir()
    var cwd by remember { mutableStateOf(initialCwd) }
    var text by textState
    var agent by remember { mutableStateOf("agy") }
    val addDirs = remember { mutableStateListOf<String>() }
    var flagsOpen by remember { mutableStateOf(false) }
    val attachments = remember { mutableStateListOf<Pending>() }
    var picking by remember { mutableStateOf<String?>(null) } // "cwd" | "add"
    var creating by remember { mutableStateOf(false) }

    LaunchedEffect(conn) {
        if (cwd.isBlank()) {
            runCatching {
                val h = conn.rpc.call("fs.home").jsonObject
                cwd = h["recent"]?.jsonArray?.firstOrNull()?.jsonPrimitive?.content ?: h["home"]?.jsonPrimitive?.content ?: ""
            }
        }
    }
    LaunchedEffect(Unit) {
        ShareInbox.image?.let { uri ->
            ShareInbox.image = null
            runCatching { attachments.add(AttachmentUtil.fromUri(ctx, uri)) }
        }
    }
    val photo = rememberLauncherForActivityResult(ActivityResultContracts.PickMultipleVisualMedia(8)) { uris ->
        scope.launch { uris.forEach { u -> runCatching { attachments.add(AttachmentUtil.fromUri(ctx, u)) }.onFailure { snack.showSnackbar(it.message ?: "attach failed") } } }
    }
    val file = rememberLauncherForActivityResult(ActivityResultContracts.OpenMultipleDocuments()) { uris ->
        scope.launch { uris.forEach { u -> runCatching { attachments.add(AttachmentUtil.fromUri(ctx, u)) }.onFailure { snack.showSnackbar(it.message ?: "attach failed") } } }
    }

    fun create() {
        creating = true
        scope.launch {
            try {
                val flags = settings.agyFlags
                val res = conn.rpc.call("session.create", params {
                    flags.daemonJson().forEach { (k, v) -> put(k, v) }
                    put("agent", agent)
                    put("cwd", cwd)
                    val dirs = (flags.addDirs + addDirs).distinct()
                    if (dirs.isNotEmpty()) put("addDirs", JsonArray(dirs.map { JsonPrimitive(it) }))
                    if (text.isNotBlank() || attachments.isNotEmpty()) {
                        put("prompt", params {
                            put("text", text)
                            put("attachments", AttachmentUtil.toJson(attachments))
                        })
                    }
                }, timeoutMs = 180_000).jsonObject
                res["warning"]?.jsonPrimitive?.content?.let { snack.showSnackbar(it) }
                val id = res["session"]!!.jsonObject["id"]!!.jsonPrimitive.content
                nav.navigate(Routes.chat(id)) { popUpTo(Routes.SESSIONS) }
            } catch (e: Exception) {
                snack.showSnackbar("Couldn't start session: ${e.message}")
            } finally {
                creating = false
            }
        }
    }

    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text("New session") },
                navigationIcon = { IconButton(onClick = { nav.popBackStack() }) { Icon(Icons.AutoMirrored.Filled.ArrowBack, null) } },
            )
        },
    ) { pad ->
        Column(Modifier.padding(pad).fillMaxSize().imePadding().verticalScroll(rememberScrollState()).padding(16.dp), verticalArrangement = Arrangement.spacedBy(14.dp)) {
            // Agent
            hostPicker()
            Text("Agent", style = MaterialTheme.typography.labelLarge)
            Row(Modifier.horizontalScroll(rememberScrollState()), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                val list = agents.ifEmpty { listOf(io.github.sidd065.agentcompanion.core.AgentProfile("agy", "Antigravity (agy)", available = true)) }
                list.forEach { a ->
                    FilterChip(
                        selected = agent == a.id, enabled = a.available, onClick = { agent = a.id },
                        label = { Text(a.name + if (!a.available && a.planned) " · soon" else "") },
                    )
                }
            }
            // Folder
            Text("Folder on ${conn.config.label}", style = MaterialTheme.typography.labelLarge)
            Card(Modifier.fillMaxWidth().clickable { picking = "cwd" }, colors = CardDefaults.cardColors(containerColor = MaterialTheme.colorScheme.surfaceContainer)) {
                Row(Modifier.padding(14.dp), verticalAlignment = Alignment.CenterVertically) {
                    Icon(Icons.Outlined.FolderOpen, null, tint = MaterialTheme.colorScheme.primary)
                    Spacer(Modifier.width(12.dp))
                    Column(Modifier.weight(1f)) {
                        Text(if (cwd.isBlank()) "Choose a directory" else baseName(cwd), style = MaterialTheme.typography.titleSmall)
                        Text(shortPath(cwd, home), style = MaterialTheme.typography.bodySmall, fontFamily = FontFamily.Monospace,
                            color = MaterialTheme.colorScheme.onSurfaceVariant, maxLines = 1, overflow = TextOverflow.Ellipsis)
                    }
                    Text("Change", color = MaterialTheme.colorScheme.primary, style = MaterialTheme.typography.labelLarge)
                }
            }
            // Prompt
            OutlinedTextField(
                text, { text = it }, modifier = Modifier.fillMaxWidth().heightIn(min = 120.dp),
                placeholder = { Text("Describe a task or ask a question…") },
            )
            if (attachments.isNotEmpty()) {
                Row(Modifier.horizontalScroll(rememberScrollState()), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    attachments.forEachIndexed { i, a -> AttachmentChip(a) { attachments.removeAt(i) } }
                }
            }
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                OutlinedButton(onClick = { photo.launch(PickVisualMediaRequest(ActivityResultContracts.PickVisualMedia.ImageOnly)) }) {
                    Icon(Icons.Outlined.Image, null, Modifier.size(18.dp)); Spacer(Modifier.width(6.dp)); Text("Photo")
                }
                OutlinedButton(onClick = { file.launch(arrayOf("*/*")) }) {
                    Icon(Icons.Outlined.AttachFile, null, Modifier.size(18.dp)); Spacer(Modifier.width(6.dp)); Text("File")
                }
            }
            HorizontalDivider()
            // agy launch flags (saved on this phone, used for every session)
            AgyFlagsCard(kind = "acp", onEdit = { flagsOpen = true })
            // Extra dirs for this session only
            Text("Extra workspace folders (this session)", style = MaterialTheme.typography.labelLarge)
            FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                addDirs.forEachIndexed { i, d ->
                    AssistChip(onClick = { addDirs.removeAt(i) }, label = { Text(shortPath(d, home)) }, trailingIcon = { Icon(Icons.Outlined.Close, null, Modifier.size(16.dp)) })
                }
                AssistChip(onClick = { picking = "add" }, label = { Text("Add folder") }, leadingIcon = { Icon(Icons.Outlined.Folder, null, Modifier.size(16.dp)) })
            }
            Spacer(Modifier.height(4.dp))
            Button(onClick = { create() }, enabled = !creating && cwd.isNotBlank(), modifier = Modifier.fillMaxWidth().height(52.dp)) {
                if (creating) {
                    CircularProgressIndicator(Modifier.size(18.dp), strokeWidth = 2.dp, color = MaterialTheme.colorScheme.onPrimary)
                    Spacer(Modifier.width(10.dp))
                    Text("Starting agy…")
                } else Text(if (text.isBlank()) "Start session" else "Start session and send")
            }
            Spacer(Modifier.height(24.dp))
        }
    }
    if (flagsOpen) AgyFlagsSheet(conn, onDismiss = { flagsOpen = false })
    picking?.let { which ->
        DirectoryPicker(conn, start = if (which == "cwd") cwd else "", onDismiss = { picking = null }) { picked ->
            if (which == "cwd") cwd = picked else if (picked !in addDirs) addDirs.add(picked)
            picking = null
        }
    }
}

@Composable
fun AttachmentChip(a: Pending, onRemove: () -> Unit) {
    Card(colors = CardDefaults.cardColors(containerColor = MaterialTheme.colorScheme.surfaceContainerHigh), shape = RoundedCornerShape(10.dp)) {
        Row(Modifier.padding(6.dp), verticalAlignment = Alignment.CenterVertically) {
            if (a.preview != null) {
                Image(a.preview.asImageBitmap(), null, Modifier.size(40.dp).clip(RoundedCornerShape(6.dp)), contentScale = ContentScale.Crop)
            } else {
                Icon(Icons.AutoMirrored.Outlined.InsertDriveFile, null, Modifier.size(24.dp))
            }
            Spacer(Modifier.width(6.dp))
            Text(a.name, style = MaterialTheme.typography.labelMedium, maxLines = 1, overflow = TextOverflow.Ellipsis, modifier = Modifier.widthIn(max = 140.dp))
            IconButton(onClick = onRemove, modifier = Modifier.size(28.dp)) { Icon(Icons.Outlined.Close, "Remove", Modifier.size(16.dp)) }
        }
    }
}

/** Bottom-sheet directory browser on the host. */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun DirectoryPicker(conn: HostConnection, start: String, onDismiss: () -> Unit, onPick: (String) -> Unit) {
    val scope = rememberCoroutineScope()
    val sheet = rememberModalBottomSheetState(skipPartiallyExpanded = true)
    var path by remember { mutableStateOf(start) }
    var listing by remember { mutableStateOf<FsList?>(null) }
    var error by remember { mutableStateOf<String?>(null) }
    var recent by remember { mutableStateOf<List<String>>(emptyList()) }
    var home by remember { mutableStateOf("") }
    var newFolder by remember { mutableStateOf(false) }
    var showHidden by remember { mutableStateOf(false) }
    LaunchedEffect(Unit) {
        runCatching {
            val h = conn.rpc.call("fs.home").jsonObject
            home = h["home"]?.jsonPrimitive?.content ?: ""
            recent = h["recent"]?.jsonArray?.map { it.jsonPrimitive.content } ?: emptyList()
            if (path.isBlank()) path = home
        }
    }
    LaunchedEffect(path, showHidden) {
        if (path.isBlank()) return@LaunchedEffect
        error = null
        runCatching { listing = conn.rpc.call("fs.list", params { put("path", path); put("hidden", showHidden) }).decode(FsList.serializer()) }
            .onFailure { error = it.message }
    }
    ModalBottomSheet(onDismissRequest = onDismiss, sheetState = sheet) {
        Column(Modifier.fillMaxWidth().heightIn(max = 640.dp)) {
            Row(Modifier.padding(horizontal = 16.dp), verticalAlignment = Alignment.CenterVertically) {
                Text("Choose a directory", style = MaterialTheme.typography.titleMedium, modifier = Modifier.weight(1f))
                TextButton(onClick = { showHidden = !showHidden }) { Text(if (showHidden) "Hide hidden" else "Show hidden") }
            }
            Text(shortPath(listing?.path ?: path, home), style = MaterialTheme.typography.bodySmall, fontFamily = FontFamily.Monospace,
                color = MaterialTheme.colorScheme.onSurfaceVariant, modifier = Modifier.padding(horizontal = 16.dp, vertical = 4.dp))
            if (listing?.gitBranch?.isNotBlank() == true) {
                Text("git: ${listing!!.gitBranch}", style = MaterialTheme.typography.labelSmall, color = LocalExtra.current.success, modifier = Modifier.padding(horizontal = 16.dp))
            }
            Row(Modifier.padding(horizontal = 12.dp, vertical = 6.dp), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                Button(onClick = { onPick(listing?.path ?: path) }, enabled = listing != null) { Text("Use this folder") }
                OutlinedButton(onClick = { newFolder = true }) { Icon(Icons.Outlined.CreateNewFolder, null, Modifier.size(18.dp)); Spacer(Modifier.width(6.dp)); Text("New") }
                IconButton(onClick = { path = home }) { Icon(Icons.Outlined.Home, "Home") }
            }
            error?.let { Text(it, color = MaterialTheme.colorScheme.error, style = MaterialTheme.typography.bodySmall, modifier = Modifier.padding(16.dp)) }
            LazyColumn(Modifier.fillMaxWidth()) {
                if (recent.isNotEmpty() && path == home) {
                    item { Text("Recent", style = MaterialTheme.typography.labelMedium, color = MaterialTheme.colorScheme.primary, modifier = Modifier.padding(start = 16.dp, top = 8.dp)) }
                    items(recent) { r ->
                        ListItem(headlineContent = { Text(baseName(r)) }, supportingContent = { Text(shortPath(r, home), fontFamily = FontFamily.Monospace) },
                            leadingContent = { Icon(Icons.Outlined.History, null) }, modifier = Modifier.clickable { path = r })
                    }
                    item { HorizontalDivider() }
                }
                listing?.let { l ->
                    if (l.parent.isNotBlank()) {
                        item { ListItem(headlineContent = { Text("..") }, leadingContent = { Icon(Icons.Outlined.Folder, null) }, modifier = Modifier.clickable { path = l.parent }) }
                    }
                    items(l.entries.filter { it.isDir }, key = { it.path }) { e ->
                        ListItem(headlineContent = { Text(e.name) }, leadingContent = { Icon(Icons.Outlined.Folder, null, tint = MaterialTheme.colorScheme.primary) },
                            modifier = Modifier.clickable { path = e.path })
                    }
                }
            }
        }
    }
    if (newFolder) {
        TextPromptDialog("New folder", "", "Name", "Create", onDismiss = { newFolder = false }, onConfirm = { name ->
            scope.launch {
                val base = listing?.path ?: path
                runCatching { conn.rpc.call("fs.mkdir", params { put("path", "$base/$name"); put("parents", true) }) }
                    .onSuccess { path = "$base/$name" }
                    .onFailure { error = it.message }
            }
        })
    }
}
