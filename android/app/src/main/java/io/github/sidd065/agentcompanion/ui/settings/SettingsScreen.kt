package io.github.sidd065.agentcompanion.ui.settings

import android.content.Intent
import android.net.Uri
import android.os.Build
import android.os.PowerManager
import android.provider.Settings
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.automirrored.outlined.KeyboardArrowRight
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.FilterChip
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Slider
import androidx.compose.material3.Switch
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.unit.dp
import io.github.sidd065.agentcompanion.BuildConfig
import io.github.sidd065.agentcompanion.core.AppSettings
import io.github.sidd065.agentcompanion.graph
import io.github.sidd065.agentcompanion.ui.LocalNav
import io.github.sidd065.agentcompanion.ui.activeConnection
import io.github.sidd065.agentcompanion.ui.theme.LocalExtra
import kotlinx.coroutines.launch
import java.util.Locale

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun SettingsScreen() {
    val ctx = LocalContext.current
    val g = ctx.graph
    val nav = LocalNav.current
    val s by g.prefs.flow.collectAsState()
    val scope = rememberCoroutineScope()
    val conn = activeConnection()
    val token by g.push.token.collectAsState()
    val pushErr by g.push.lastError.collectAsState()
    var testMsg by remember { mutableStateOf<String?>(null) }
    var flagsOpen by remember { mutableStateOf(false) }
    var claudeFlagsOpen by remember { mutableStateOf(false) }
    var codexFlagsOpen by remember { mutableStateOf(false) }
    fun upd(f: (AppSettings) -> AppSettings) = scope.launch { g.prefs.update(f) }

    Scaffold(
        topBar = {
            TopAppBar(title = { Text("Settings") }, navigationIcon = { IconButton(onClick = { nav.popBackStack() }) { Icon(Icons.AutoMirrored.Filled.ArrowBack, null) } })
        },
    ) { pad ->
        Column(Modifier.padding(pad).fillMaxSize().verticalScroll(rememberScrollState()).padding(horizontal = 16.dp, vertical = 8.dp)) {
            Section("Hosts")
            NavRow("Hosts & pairing", "${s.hosts.size} paired · ${s.hosts.joinToString { it.label }}") { nav.navigate("hosts") }

            Section("Notifications")
            SwitchRow("Notifications", "Enable notifications from your paired hosts", s.notificationsEnabled) { v -> upd { it.copy(notificationsEnabled = v) } }
            if (s.notificationsEnabled) {
                SwitchRow("Needs input", "Permission requests, agent questions, and plan reviews", s.notifyOnInput) { v -> upd { it.copy(notifyOnInput = v) } }
                SwitchRow("Task done", "When a session finishes all work (including subagents & background shells)", s.notifyOnDone) { v -> upd { it.copy(notifyOnDone = v) } }
                SwitchRow("Halted or error", "When an agent stops with an error or permission failure", s.notifyOnHalted) { v -> upd { it.copy(notifyOnHalted = v) } }
                SwitchRow("Minor progress updates", "Intermediate turn messages while background tasks keep working", s.notifyOnProgress) { v -> upd { it.copy(notifyOnProgress = v) } }
            }
            val hostPush = conn?.hello?.collectAsState()?.value?.get("push")?.toString()
            Text(
                buildString {
                    append("Delivery: Firebase push")
                    append(if (token != null) " · this phone is registered" else " · no push token yet")
                    if (hostPush != null && hostPush.contains("\"configured\":false")) append("\n⚠ ${conn.config.label}: push isn't set up on the computer (missing Firebase service account key)")
                    pushErr?.let { append("\n$it") }
                },
                style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant,
                modifier = Modifier.padding(vertical = 4.dp),
            )
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp), modifier = Modifier.padding(vertical = 4.dp)) {
                OutlinedButton(enabled = conn != null, onClick = {
                    testMsg = "Sending…"
                    scope.launch {
                        val r = g.push.test(conn!!)
                        testMsg = r.fold({ "Test sent — it should appear in a few seconds" }, { "Failed: ${it.message}" })
                    }
                }) { Text("Send test") }
                OutlinedButton(onClick = {
                    ctx.startActivity(Intent(Settings.ACTION_APP_NOTIFICATION_SETTINGS).putExtra(Settings.EXTRA_APP_PACKAGE, ctx.packageName))
                }) { Text("Channels") }
            }
            testMsg?.let { Text(it, style = MaterialTheme.typography.bodySmall, color = LocalExtra.current.muted) }
            SwitchRow(
                "Persistent connection (fallback)",
                "Keep a live connection in the background instead of relying on push. Uses more battery.",
                s.backgroundConnection,
            ) { v -> upd { it.copy(backgroundConnection = v) } }
            val pm = ctx.getSystemService(PowerManager::class.java)
            val ignoring = pm?.isIgnoringBatteryOptimizations(ctx.packageName) == true
            NavRow("Battery optimization", if (ignoring) "Unrestricted — notifications won't be delayed" else "Restricted — tap to allow background delivery") {
                runCatching {
                    @Suppress("BatteryLife")
                    ctx.startActivity(Intent(Settings.ACTION_REQUEST_IGNORE_BATTERY_OPTIMIZATIONS, Uri.parse("package:${ctx.packageName}")))
                }
            }

            Section("Appearance")
            Text("Theme", style = MaterialTheme.typography.bodyMedium)
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                listOf("system" to "System", "dark" to "Dark", "light" to "Light").forEach { (k, l) ->
                    FilterChip(selected = s.theme == k, onClick = { upd { it.copy(theme = k) } }, label = { Text(l) })
                }
            }
            if (Build.VERSION.SDK_INT >= 31) SwitchRow("Dynamic color", "Use your wallpaper colors", s.dynamicColor) { v -> upd { it.copy(dynamicColor = v) } }
            Text("Transcript text size · ${String.format(Locale.US, "%.0f%%", s.textScale * 100)}", style = MaterialTheme.typography.bodyMedium, modifier = Modifier.padding(top = 8.dp))
            Slider(value = s.textScale, onValueChange = { v -> upd { it.copy(textScale = (v * 20).toInt() / 20f) } }, valueRange = 0.8f..1.4f)
            SwitchRow("Wrap long code lines", null, s.codeWrap) { v -> upd { it.copy(codeWrap = v) } }
            SwitchRow("Show thinking", "Show the agent's thought summaries in the transcript", s.showThoughts) { v -> upd { it.copy(showThoughts = v) } }
            SwitchRow("Expand tool calls by default", "Start tool call inputs, diffs, and outputs expanded", s.expandToolCalls) { v -> upd { it.copy(expandToolCalls = v) } }

            Section("Composer")
            SwitchRow("Enter sends", "Otherwise Enter inserts a new line", s.enterSends) { v -> upd { it.copy(enterSends = v) } }
            Text("Messages sent while the agent is working", style = MaterialTheme.typography.bodyMedium, modifier = Modifier.padding(top = 6.dp))
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                listOf("" to "Agent default", "queue" to "Queue", "steer" to "Send now").forEach { (k, l) ->
                    FilterChip(selected = s.delivery == k, onClick = { upd { it.copy(delivery = k) } }, label = { Text(l) })
                }
            }

            Section("New sessions")
            SwitchRow(
                "Auto-allow agy read requests",
                "Automatically approve file read & search requests outside the workspace in agy sessions",
                s.agyFlags.autoAllowReads,
            ) { v ->
                scope.launch {
                    g.prefs.update { it.copy(agyFlags = it.agyFlags.copy(autoAllowReads = v)) }
                    g.connections.value.forEach { c ->
                        if (c.rpc.isConnected) {
                            runCatching {
                                c.rpc.call("daemon.config.set", io.github.sidd065.agentcompanion.net.params {
                                    put("agyAutoAllowReads", kotlinx.serialization.json.JsonPrimitive(v))
                                })
                            }
                        }
                    }
                }
            }
            io.github.sidd065.agentcompanion.ui.components.AgyFlagsCard(kind = "tui", onEdit = { flagsOpen = true })
            Spacer(Modifier.height(8.dp))
            io.github.sidd065.agentcompanion.ui.components.AgentFlagsCard("claude", kind = "tui", onEdit = { claudeFlagsOpen = true })
            Spacer(Modifier.height(8.dp))
            io.github.sidd065.agentcompanion.ui.components.AgentFlagsCard("codex", kind = "tui", onEdit = { codexFlagsOpen = true })

            Section("Terminal")
            Text("Font size · ${s.terminalFontSize}", style = MaterialTheme.typography.bodyMedium)
            Slider(value = s.terminalFontSize.toFloat(), onValueChange = { v -> upd { it.copy(terminalFontSize = v.toInt()) } }, valueRange = 8f..22f)

            Section("About")
            Text("Agent Companion ${BuildConfig.VERSION_NAME}", style = MaterialTheme.typography.bodyMedium)
            Text(
                "Connects to agentc daemons over static or ephemeral Cloudflare tunnels and direct LAN/Tailscale links with end-to-end encryption (Noise IK, X25519 + ChaCha20-Poly1305): " +
                    "only phones paired with a one-time QR code can connect, and the tunnel only relays ciphertext. Notifications use Firebase Cloud Messaging.\n" +
                    "Terminal emulation: Termux terminal-emulator and terminal-view (Apache License 2.0).",
                style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            Spacer(Modifier.height(32.dp))
        }
    }
    if (flagsOpen) io.github.sidd065.agentcompanion.ui.components.AgyFlagsSheet(conn, onDismiss = { flagsOpen = false })
    if (claudeFlagsOpen) io.github.sidd065.agentcompanion.ui.components.ClaudeFlagsSheet(conn, onDismiss = { claudeFlagsOpen = false })
    if (codexFlagsOpen) io.github.sidd065.agentcompanion.ui.components.CodexFlagsSheet(conn, onDismiss = { codexFlagsOpen = false })
}

@Composable
fun Section(title: String) {
    Spacer(Modifier.height(18.dp))
    Text(title.uppercase(), style = MaterialTheme.typography.labelMedium, color = MaterialTheme.colorScheme.primary)
    Spacer(Modifier.height(4.dp))
    HorizontalDivider(color = MaterialTheme.colorScheme.outlineVariant)
    Spacer(Modifier.height(6.dp))
}

@Composable
fun SwitchRow(title: String, subtitle: String?, checked: Boolean, onChange: (Boolean) -> Unit) {
    Row(Modifier.fillMaxWidth().clickable { onChange(!checked) }.padding(vertical = 8.dp), verticalAlignment = Alignment.CenterVertically) {
        Column(Modifier.weight(1f)) {
            Text(title, style = MaterialTheme.typography.bodyLarge)
            if (subtitle != null) Text(subtitle, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
        }
        Spacer(Modifier.width(12.dp))
        Switch(checked = checked, onCheckedChange = onChange)
    }
}

@Composable
fun NavRow(title: String, subtitle: String?, onClick: () -> Unit) {
    Row(Modifier.fillMaxWidth().clickable(onClick = onClick).padding(vertical = 10.dp), verticalAlignment = Alignment.CenterVertically) {
        Column(Modifier.weight(1f)) {
            Text(title, style = MaterialTheme.typography.bodyLarge)
            if (subtitle != null) Text(subtitle, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
        }
        Icon(Icons.AutoMirrored.Outlined.KeyboardArrowRight, null, tint = MaterialTheme.colorScheme.onSurfaceVariant)
    }
}
