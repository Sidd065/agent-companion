package io.github.sidd065.agentcompanion.ui.settings

import android.content.ClipboardManager
import android.content.Context
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.outlined.CheckCircle
import androidx.compose.material.icons.outlined.Computer
import androidx.compose.material.icons.outlined.ContentPaste
import androidx.compose.material.icons.outlined.Delete
import androidx.compose.material.icons.outlined.Edit
import androidx.compose.material.icons.outlined.Lock
import androidx.compose.material.icons.outlined.MoreVert
import androidx.compose.material.icons.outlined.QrCodeScanner
import androidx.compose.material.icons.outlined.Sync
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.Card
import androidx.compose.material3.CardDefaults
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.unit.dp
import com.google.mlkit.vision.barcode.common.Barcode
import com.google.mlkit.vision.codescanner.GmsBarcodeScannerOptions
import com.google.mlkit.vision.codescanner.GmsBarcodeScanning
import io.github.sidd065.agentcompanion.core.HostConfig
import io.github.sidd065.agentcompanion.core.PairLink
import io.github.sidd065.agentcompanion.core.Pairing
import io.github.sidd065.agentcompanion.graph
import io.github.sidd065.agentcompanion.net.ConnState
import io.github.sidd065.agentcompanion.net.params
import io.github.sidd065.agentcompanion.ui.LocalNav
import io.github.sidd065.agentcompanion.ui.PendingNav
import io.github.sidd065.agentcompanion.ui.components.LocalSnack
import io.github.sidd065.agentcompanion.ui.components.StatusDot
import kotlinx.coroutines.launch
import kotlinx.coroutines.withTimeoutOrNull
import kotlinx.serialization.json.put

/** Opens Google's code scanner (no camera permission needed). */
fun scanQr(ctx: Context, onResult: (String) -> Unit, onError: (String) -> Unit) {
    val opts = GmsBarcodeScannerOptions.Builder().setBarcodeFormats(Barcode.FORMAT_QR_CODE).enableAutoZoom().build()
    GmsBarcodeScanning.getClient(ctx, opts).startScan()
        .addOnSuccessListener { b -> b.rawValue?.let(onResult) }
        .addOnFailureListener { e -> onError(e.message ?: "The QR scanner isn't available on this device") }
}

@Composable
fun SetupSteps() {
    Surface(color = MaterialTheme.colorScheme.surfaceContainer, shape = RoundedCornerShape(14.dp)) {
        Column(Modifier.fillMaxWidth().padding(14.dp), verticalArrangement = Arrangement.spacedBy(6.dp)) {
            Text("On each computer", style = MaterialTheme.typography.titleSmall)
            Text("1. Install the daemon:  agentc install", style = MaterialTheme.typography.bodySmall, fontFamily = FontFamily.Monospace)
            Text("2. Show a pairing code:  agentc pair", style = MaterialTheme.typography.bodySmall, fontFamily = FontFamily.Monospace)
            Text("3. Scan it here (or open the link on this phone).", style = MaterialTheme.typography.bodySmall)
            Row(verticalAlignment = Alignment.CenterVertically) {
                Icon(Icons.Outlined.Lock, null, Modifier.size(14.dp), tint = MaterialTheme.colorScheme.primary)
                Spacer(Modifier.width(6.dp))
                Text(
                    "The daemon is reached through a Cloudflare tunnel, but everything is end-to-end encrypted: only phones paired with a code can connect.",
                    style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
        }
    }
}

/** "Scan QR code" / "Paste link" buttons that lead into the pairing dialog. */
@Composable
fun AddHostButtons(onLink: (PairLink) -> Unit) {
    val ctx = LocalContext.current
    val snack = LocalSnack.current
    val scope = rememberCoroutineScope()
    var pasting by remember { mutableStateOf(false) }
    fun handle(text: String) {
        val link = Pairing.parse(text)
        if (link == null) scope.launch { snack.showSnackbar("That isn't an agentc pairing code") } else onLink(link)
    }
    Row(horizontalArrangement = Arrangement.spacedBy(10.dp)) {
        Button(onClick = { scanQr(ctx, ::handle) { scope.launch { snack.showSnackbar(it) } } }) {
            Icon(Icons.Outlined.QrCodeScanner, null)
            Spacer(Modifier.width(8.dp))
            Text("Scan QR code")
        }
        OutlinedButton(onClick = { pasting = true }) {
            Icon(Icons.Outlined.ContentPaste, null)
            Spacer(Modifier.width(8.dp))
            Text("Paste link")
        }
    }
    if (pasting) {
        var text by remember {
            val clip = ctx.getSystemService(ClipboardManager::class.java)?.primaryClip?.getItemAt(0)?.coerceToText(ctx)?.toString().orEmpty()
            mutableStateOf(if (Pairing.parse(clip) != null) clip else "")
        }
        AlertDialog(
            onDismissRequest = { pasting = false },
            title = { Text("Paste pairing link") },
            text = {
                OutlinedTextField(text, { text = it }, Modifier.fillMaxWidth(), placeholder = { Text("https://…trycloudflare.com/p/…") }, minLines = 3)
            },
            confirmButton = { TextButton(onClick = { pasting = false; handle(text) }, enabled = text.isNotBlank()) { Text("Continue") } },
            dismissButton = { TextButton(onClick = { pasting = false }) { Text("Cancel") } },
        )
    }
}

/** Confirms and performs pairing for [link]. */
@Composable
fun PairDialog(link: PairLink, onDone: () -> Unit) {
    val g = LocalContext.current.graph
    val snack = LocalSnack.current
    val scope = rememberCoroutineScope()
    var busy by remember { mutableStateOf(false) }
    var error by remember { mutableStateOf<String?>(null) }
    val known = g.prefs.value.hosts.firstOrNull { it.hostKey == link.hostKey }
    AlertDialog(
        onDismissRequest = { if (!busy) onDone() },
        icon = { Icon(Icons.Outlined.Computer, null) },
        title = { Text(if (known != null) "Update ${known.label}?" else "Pair with ${link.name.ifBlank { "this host" }}?") },
        text = {
            Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
                Text(
                    if (known != null) "This phone is already paired with this host; its new address will be saved."
                    else "This phone will be able to run agents, read and change files and open terminals on this computer.",
                    style = MaterialTheme.typography.bodyMedium,
                )
                Text("Fingerprint", style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                Text(link.fingerprint, style = MaterialTheme.typography.bodyMedium, fontFamily = FontFamily.Monospace)
                Text("Check it matches what `agentc pair` printed.", style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                Text(link.base.removePrefix("https://"), style = MaterialTheme.typography.labelSmall, fontFamily = FontFamily.Monospace, color = MaterialTheme.colorScheme.onSurfaceVariant)
                if (busy) Row(verticalAlignment = Alignment.CenterVertically) {
                    CircularProgressIndicator(Modifier.size(18.dp), strokeWidth = 2.dp)
                    Spacer(Modifier.width(10.dp))
                    Text("Pairing…")
                }
                error?.let { Text(it, color = MaterialTheme.colorScheme.error, style = MaterialTheme.typography.bodySmall) }
            }
        },
        confirmButton = {
            TextButton(enabled = !busy, onClick = {
                busy = true
                error = null
                scope.launch {
                    runCatching { Pairing.pair(g, link) }
                        .onSuccess { r ->
                            onDone()
                            snack.showSnackbar(
                                when (r) {
                                    is Pairing.Result.Paired -> "Paired with ${r.host.label}"
                                    is Pairing.Result.Updated -> "Updated the address of ${r.host.label}"
                                },
                            )
                        }
                        .onFailure { error = it.message ?: "Pairing failed"; busy = false }
                }
            }) { Text(if (known != null) "Update" else "Pair") }
        },
        dismissButton = { TextButton(enabled = !busy, onClick = onDone) { Text("Cancel") } },
    )
}

/** Shows the pairing dialog for links opened from outside the app. */
@Composable
fun PendingPairing() {
    val pending by PendingNav.pairLink.collectAsState()
    val snack = LocalSnack.current
    val link = remember(pending) { pending?.let { Pairing.parse(it) } }
    LaunchedEffect(pending) {
        if (pending != null && link == null) {
            PendingNav.pairLink.value = null
            snack.showSnackbar("That pairing link is incomplete")
        }
    }
    if (link != null) PairDialog(link) { PendingNav.pairLink.value = null }
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun HostsScreen(firstRun: Boolean) {
    val ctx = LocalContext.current
    val g = ctx.graph
    val settings by g.prefs.flow.collectAsState()
    val conns by g.connections.collectAsState()
    val scope = rememberCoroutineScope()
    val snack = LocalSnack.current
    var pairing by remember { mutableStateOf<PairLink?>(null) }
    var renaming by remember { mutableStateOf<HostConfig?>(null) }
    var removing by remember { mutableStateOf<HostConfig?>(null) }
    val nav = if (firstRun) null else LocalNav.current

    Scaffold(
        topBar = {
            if (!firstRun) TopAppBar(
                title = { Text("Hosts") },
                navigationIcon = { IconButton(onClick = { nav?.popBackStack() }) { Icon(Icons.AutoMirrored.Filled.ArrowBack, null) } },
            )
        },
    ) { pad ->
        Column(
            Modifier.padding(pad).fillMaxSize().imePadding().verticalScroll(rememberScrollState()).padding(horizontal = 16.dp, vertical = if (firstRun) 28.dp else 8.dp),
            verticalArrangement = Arrangement.spacedBy(12.dp),
        ) {
            if (firstRun) {
                Text("Agent Companion", style = MaterialTheme.typography.headlineSmall)
                Text(
                    "Run and follow coding agents (agy today; Claude Code and Codex next) on your computers from this phone.",
                    style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
            settings.hosts.forEach { h ->
                val conn = conns.firstOrNull { it.config.id == h.id }
                val st = conn?.rpc?.state?.collectAsState()?.value
                var menu by remember { mutableStateOf(false) }
                Card(Modifier.fillMaxWidth(), colors = CardDefaults.cardColors(containerColor = MaterialTheme.colorScheme.surfaceContainer)) {
                    Row(Modifier.padding(14.dp), verticalAlignment = Alignment.CenterVertically) {
                        Icon(Icons.Outlined.Computer, null)
                        Spacer(Modifier.width(12.dp))
                        Column(Modifier.weight(1f)) {
                            Row(verticalAlignment = Alignment.CenterVertically) {
                                Text(h.label, style = MaterialTheme.typography.titleMedium)
                                if (st is ConnState.Connected) {
                                    Spacer(Modifier.width(6.dp))
                                    Icon(Icons.Outlined.CheckCircle, "connected", Modifier.size(16.dp), tint = MaterialTheme.colorScheme.primary)
                                }
                            }
                            Text(h.url.removePrefix("https://"), style = MaterialTheme.typography.bodySmall, fontFamily = FontFamily.Monospace,
                                color = MaterialTheme.colorScheme.onSurfaceVariant, maxLines = 1)
                            if (h.fingerprint.isNotBlank()) Text(h.fingerprint, style = MaterialTheme.typography.labelSmall, fontFamily = FontFamily.Monospace,
                                color = MaterialTheme.colorScheme.onSurfaceVariant)
                            Row(verticalAlignment = Alignment.CenterVertically) {
                                StatusDot(if (st is ConnState.Connected) "idle" else if (st is ConnState.Failed) "error" else "sleeping", null, size = 8)
                                Spacer(Modifier.width(6.dp))
                                Text(
                                    when (st) {
                                        is ConnState.Connected -> "Connected"
                                        is ConnState.Connecting -> "Connecting… ${st.lastError ?: ""}"
                                        is ConnState.Failed -> st.error
                                        else -> "Idle"
                                    },
                                    style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.onSurfaceVariant, maxLines = 3,
                                )
                            }
                        }
                        IconButton(onClick = { menu = true }) { Icon(Icons.Outlined.MoreVert, "More") }
                        DropdownMenu(menu, { menu = false }) {
                            DropdownMenuItem(text = { Text("Rename") }, leadingIcon = { Icon(Icons.Outlined.Edit, null) }, onClick = { menu = false; renaming = h })
                            DropdownMenuItem(text = { Text("Update address (scan code)") }, leadingIcon = { Icon(Icons.Outlined.Sync, null) }, onClick = {
                                menu = false
                                scanQr(ctx, { t -> Pairing.parse(t)?.let { pairing = it } ?: scope.launch { snack.showSnackbar("Not a pairing code") } }) {
                                    scope.launch { snack.showSnackbar(it) }
                                }
                            })
                            DropdownMenuItem(text = { Text("Unpair") }, leadingIcon = { Icon(Icons.Outlined.Delete, null) }, onClick = { menu = false; removing = h })
                        }
                    }
                }
            }
            Text(if (settings.hosts.isEmpty()) "Pair with a computer" else "Add a host", style = MaterialTheme.typography.titleSmall,
                modifier = Modifier.padding(top = 8.dp))
            AddHostButtons { pairing = it }
            SetupSteps()
            Spacer(Modifier.height(24.dp))
        }
    }
    pairing?.let { PairDialog(it) { pairing = null } }
    renaming?.let { h ->
        var name by remember(h.id) { mutableStateOf(h.name) }
        AlertDialog(
            onDismissRequest = { renaming = null },
            title = { Text("Rename host") },
            text = { OutlinedTextField(name, { name = it }, singleLine = true, modifier = Modifier.fillMaxWidth()) },
            confirmButton = {
                TextButton(onClick = {
                    scope.launch { g.prefs.update { s -> s.copy(hosts = s.hosts.map { if (it.id == h.id) it.copy(name = name.trim()) else it }) } }
                    renaming = null
                }) { Text("Save") }
            },
            dismissButton = { TextButton(onClick = { renaming = null }) { Text("Cancel") } },
        )
    }
    removing?.let { h ->
        AlertDialog(
            onDismissRequest = { removing = null },
            title = { Text("Unpair ${h.label}?") },
            text = { Text("This phone forgets the host and is removed from its paired devices (if it's reachable now). You can pair again later with `agentc pair`.") },
            confirmButton = {
                TextButton(onClick = {
                    removing = null
                    scope.launch {
                        val conn = g.connection(h.id)
                        if (conn != null && conn.rpc.isConnected && h.deviceId.isNotBlank()) {
                            withTimeoutOrNull(5000) { runCatching { conn.rpc.call("devices.revoke", params { put("id", h.deviceId) }) } }
                        }
                        g.prefs.update { s ->
                            val rest = s.hosts.filterNot { it.id == h.id }
                            s.copy(hosts = rest, activeHostId = if (s.activeHostId == h.id) rest.firstOrNull()?.id else s.activeHostId)
                        }
                        snack.showSnackbar("Unpaired ${h.label}")
                    }
                }) { Text("Unpair") }
            },
            dismissButton = { TextButton(onClick = { removing = null }) { Text("Cancel") } },
        )
    }
}

@Suppress("unused")
private fun Modifier.clickableIf(on: Boolean, f: () -> Unit) = if (on) clickable(onClick = f) else this
