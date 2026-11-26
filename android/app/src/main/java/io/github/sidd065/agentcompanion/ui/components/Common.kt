package io.github.sidd065.agentcompanion.ui.components

import android.content.ClipData
import android.content.ClipboardManager
import android.content.Context
import android.widget.Toast
import androidx.compose.animation.core.RepeatMode
import androidx.compose.animation.core.animateFloat
import androidx.compose.animation.core.infiniteRepeatable
import androidx.compose.animation.core.rememberInfiniteTransition
import androidx.compose.animation.core.tween
import androidx.compose.foundation.background
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.ErrorOutline
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.FilterChip
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.SnackbarHostState
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.runtime.staticCompositionLocalOf
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.alpha
import androidx.compose.ui.draw.clip
import androidx.compose.ui.draw.drawBehind
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import io.github.sidd065.agentcompanion.net.ConnState
import io.github.sidd065.agentcompanion.net.HostConnection
import io.github.sidd065.agentcompanion.ui.theme.LocalExtra
import io.github.sidd065.agentcompanion.ui.theme.codeStyle
import kotlinx.serialization.json.contentOrNull
import kotlinx.serialization.json.jsonPrimitive
import java.text.SimpleDateFormat
import java.util.Calendar
import java.util.Date
import java.util.Locale
import kotlin.math.abs

val LocalSnack = staticCompositionLocalOf { SnackbarHostState() }

fun relTime(ms: Long): String {
    if (ms <= 0) return ""
    val now = System.currentTimeMillis()
    val d = (now - ms) / 1000
    if (d < 60) return "now"
    if (d < 3600) return "${d / 60}m"
    val cal = Calendar.getInstance().apply { timeInMillis = ms }
    val today = Calendar.getInstance()
    if (d < 6 * 3600 || cal.get(Calendar.DAY_OF_YEAR) == today.get(Calendar.DAY_OF_YEAR) && cal.get(Calendar.YEAR) == today.get(Calendar.YEAR)) {
        return if (d < 3600 * 12) "${d / 3600}h" else SimpleDateFormat("HH:mm", Locale.getDefault()).format(Date(ms))
    }
    if (d < 6 * 86400) return SimpleDateFormat("EEE", Locale.getDefault()).format(Date(ms))
    return SimpleDateFormat("d MMM", Locale.getDefault()).format(Date(ms))
}

fun dateBucket(ms: Long): String {
    val now = Calendar.getInstance()
    val c = Calendar.getInstance().apply { timeInMillis = ms }
    val days = ((startOfDay(now) - startOfDay(c)) / 86_400_000L).toInt()
    return when {
        ms <= 0 -> "Older"
        days <= 0 -> "Today"
        days == 1 -> "Yesterday"
        days < 7 -> "This week"
        days < 14 -> "Last week"
        days < 31 -> "This month"
        else -> "Older"
    }
}

private fun startOfDay(c: Calendar): Long {
    val x = c.clone() as Calendar
    x.set(Calendar.HOUR_OF_DAY, 0); x.set(Calendar.MINUTE, 0); x.set(Calendar.SECOND, 0); x.set(Calendar.MILLISECOND, 0)
    return x.timeInMillis
}

fun shortPath(p: String, home: String?): String {
    if (p.isBlank()) return ""
    val h = home?.trimEnd('/')
    return if (!h.isNullOrEmpty() && (p == h || p.startsWith("$h/"))) "~" + p.removePrefix(h) else p
}

fun baseName(p: String): String = p.trimEnd('/').substringAfterLast('/').ifEmpty { "/" }

fun fmtBytes(n: Long): String = when {
    n < 1024 -> "$n B"
    n < 1024 * 1024 -> String.format(Locale.US, "%.1f KB", n / 1024.0)
    n < 1024L * 1024 * 1024 -> String.format(Locale.US, "%.1f MB", n / 1048576.0)
    else -> String.format(Locale.US, "%.2f GB", n / 1073741824.0)
}

fun fmtTokens(n: Long): String = when {
    n < 1000 -> "$n"
    n < 1_000_000 -> String.format(Locale.US, "%.1fk", n / 1000.0)
    else -> String.format(Locale.US, "%.2fM", n / 1_000_000.0)
}

fun fmtDuration(ms: Long): String {
    val s = abs(ms) / 1000
    return when {
        s < 1 -> "${abs(ms)}ms"
        s < 60 -> "${s}s"
        s < 3600 -> "${s / 60}m ${s % 60}s"
        else -> "${s / 3600}h ${(s % 3600) / 60}m"
    }
}

/** Opens the share sheet for plain text. */
fun Context.shareText(text: String, subject: String? = null) {
    val i = android.content.Intent(android.content.Intent.ACTION_SEND).setType("text/plain").putExtra(android.content.Intent.EXTRA_TEXT, text)
    subject?.takeIf { it.isNotBlank() }?.let { i.putExtra(android.content.Intent.EXTRA_SUBJECT, it.take(120)) }
    startActivity(android.content.Intent.createChooser(i, "Share").addFlags(android.content.Intent.FLAG_ACTIVITY_NEW_TASK))
}

fun Context.copyText(text: String, label: String = "text") {
    val cm = getSystemService(ClipboardManager::class.java)
    cm.setPrimaryClip(ClipData.newPlainText(label, text))
    if (android.os.Build.VERSION.SDK_INT < 33) Toast.makeText(this, "Copied", Toast.LENGTH_SHORT).show()
}

@Composable
fun StatusDot(state: String, attention: String?, modifier: Modifier = Modifier, size: Int = 10) {
    val ex = LocalExtra.current
    val color = when {
        !attention.isNullOrEmpty() || state == "waiting" -> ex.warning
        state == "running" || state == "starting" -> ex.running
        state == "error" -> ex.danger
        state == "idle" -> ex.success
        else -> ex.muted.copy(alpha = 0.5f)
    }
    val pulsing = state == "running" || state == "starting"
    val alpha = if (pulsing) {
        val t = rememberInfiniteTransition(label = "pulse")
        t.animateFloat(0.35f, 1f, infiniteRepeatable(tween(800), RepeatMode.Reverse), label = "a").value
    } else 1f
    Box(modifier.size(size.dp).alpha(alpha).clip(CircleShape).background(color))
}

/**
 * A light checkerboard behind an image, like image editors show transparency, so a
 * dark logo on a transparent background stays visible on a dark bubble.
 */
fun Modifier.checkerboard(cell: androidx.compose.ui.unit.Dp = 6.dp): Modifier = drawBehind {
    val c = cell.toPx()
    drawRect(Color(0xFFF3F3F3))
    var y = 0f
    var row = 0
    while (y < size.height) {
        var x = if (row % 2 == 0) 0f else c
        while (x < size.width) {
            drawRect(Color(0xFFD9D9D9), androidx.compose.ui.geometry.Offset(x, y), androidx.compose.ui.geometry.Size(c, c))
            x += 2 * c
        }
        y += c
        row++
    }
}

private val backticked = Regex("`([^`]+)`")

/** UI copy with `commands` in backticks shown in monospace, without the backticks. */
fun codeSpans(s: String): androidx.compose.ui.text.AnnotatedString = androidx.compose.ui.text.buildAnnotatedString {
    var last = 0
    backticked.findAll(s).forEach { m ->
        append(s.substring(last, m.range.first))
        pushStyle(androidx.compose.ui.text.SpanStyle(fontFamily = androidx.compose.ui.text.font.FontFamily.Monospace))
        append(m.groupValues[1])
        pop()
        last = m.range.last + 1
    }
    append(s.substring(last))
}

@Composable
fun EmptyState(icon: ImageVector, title: String, body: String? = null, modifier: Modifier = Modifier, action: (@Composable () -> Unit)? = null) {
    Column(
        modifier.fillMaxSize().padding(32.dp),
        horizontalAlignment = Alignment.CenterHorizontally,
        verticalArrangement = Arrangement.Center,
    ) {
        Icon(icon, null, modifier = Modifier.size(48.dp), tint = MaterialTheme.colorScheme.primary.copy(alpha = 0.8f))
        Spacer(Modifier.height(16.dp))
        Text(title, style = MaterialTheme.typography.titleMedium, textAlign = TextAlign.Center)
        if (body != null) {
            Spacer(Modifier.height(6.dp))
            Text(codeSpans(body), style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onSurfaceVariant, textAlign = TextAlign.Center)
        }
        if (action != null) {
            Spacer(Modifier.height(20.dp))
            action()
        }
    }
}

@Composable
fun LoadingBox(modifier: Modifier = Modifier, text: String? = null) {
    Column(modifier.fillMaxWidth().padding(32.dp), horizontalAlignment = Alignment.CenterHorizontally) {
        CircularProgressIndicator(Modifier.size(28.dp), strokeWidth = 2.5.dp)
        if (text != null) {
            Spacer(Modifier.height(12.dp))
            Text(text, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
        }
    }
}

@Composable
fun ErrorBox(text: String, modifier: Modifier = Modifier, onRetry: (() -> Unit)? = null) {
    Surface(
        modifier.fillMaxWidth().padding(12.dp),
        shape = RoundedCornerShape(12.dp),
        color = MaterialTheme.colorScheme.errorContainer.copy(alpha = 0.5f),
    ) {
        Row(Modifier.padding(12.dp), verticalAlignment = Alignment.CenterVertically) {
            Icon(Icons.Outlined.ErrorOutline, null, tint = MaterialTheme.colorScheme.error)
            Text(text, Modifier.weight(1f).padding(horizontal = 10.dp), style = MaterialTheme.typography.bodySmall)
            if (onRetry != null) TextButton(onClick = onRetry) { Text("Retry") }
        }
    }
}

@Composable
fun ConfirmDialog(
    title: String,
    text: String,
    confirm: String,
    onDismiss: () -> Unit,
    destructive: Boolean = false,
    onConfirm: () -> Unit,
) {
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text(title) },
        text = { Text(text) },
        confirmButton = {
            TextButton(onClick = { onDismiss(); onConfirm() }) {
                Text(confirm, color = if (destructive) MaterialTheme.colorScheme.error else MaterialTheme.colorScheme.primary, fontWeight = FontWeight.SemiBold)
            }
        },
        dismissButton = { TextButton(onClick = onDismiss) { Text("Cancel") } },
    )
}

@Composable
fun TextPromptDialog(
    title: String,
    initial: String = "",
    label: String = "",
    confirm: String = "OK",
    singleLine: Boolean = true,
    onDismiss: () -> Unit,
    onConfirm: (String) -> Unit,
) {
    var value by remember { mutableStateOf(initial) }
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text(title) },
        text = {
            OutlinedTextField(value, { value = it }, label = if (label.isNotEmpty()) ({ Text(label) }) else null, singleLine = singleLine, modifier = Modifier.fillMaxWidth())
        },
        confirmButton = { Button(onClick = { onDismiss(); onConfirm(value) }, enabled = value.isNotBlank()) { Text(confirm) } },
        dismissButton = { TextButton(onClick = onDismiss) { Text("Cancel") } },
    )
}

@Composable
fun CodeBox(text: String, modifier: Modifier = Modifier, wrap: Boolean = false, color: Color = Color.Unspecified, maxChars: Int = 60_000) {
    val ex = LocalExtra.current
    val shown = if (text.length > maxChars) text.take(maxChars) + "\n…" else text
    val hs = rememberScrollState()
    Box(modifier.fillMaxWidth().clip(RoundedCornerShape(8.dp)).background(ex.codeBg)) {
        SelectionContainer {
            Text(
                shown,
                style = codeStyle,
                color = if (color == Color.Unspecified) MaterialTheme.colorScheme.onSurface else color,
                softWrap = wrap,
                modifier = Modifier.let { if (wrap) it else it.horizontalScroll(hs) }.padding(10.dp),
            )
        }
    }
}

@Composable
fun Pill(text: String, color: Color, modifier: Modifier = Modifier) {
    Box(modifier.clip(RoundedCornerShape(50)).background(color.copy(alpha = 0.16f)).padding(horizontal = 8.dp, vertical = 2.dp)) {
        Text(text, style = MaterialTheme.typography.labelSmall, color = color)
    }
}

@Composable
fun HostConnection.rememberHomeDir(): String? {
    val h by hello.collectAsState()
    return h?.get("home")?.jsonPrimitive?.contentOrNull
}

@Composable
fun HostSelectorBar(
    conns: List<HostConnection>,
    activeHostId: String?,
    onSelect: (String) -> Unit,
    modifier: Modifier = Modifier,
) {
    if (conns.size <= 1) return
    val ex = LocalExtra.current
    Row(
        modifier = modifier
            .fillMaxWidth()
            .horizontalScroll(rememberScrollState())
            .padding(horizontal = 12.dp, vertical = 4.dp),
        horizontalArrangement = Arrangement.spacedBy(8.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        conns.forEach { c ->
            val st by androidx.compose.runtime.key(c.config.id) { c.rpc.state.collectAsState() }
            val dotColor = when (st) {
                is ConnState.Connected -> ex.success
                is ConnState.Connecting -> ex.warning
                is ConnState.Failed, is ConnState.Idle -> ex.danger
            }
            FilterChip(
                selected = c.config.id == activeHostId,
                onClick = { onSelect(c.config.id) },
                leadingIcon = {
                    Box(
                        Modifier
                            .size(8.dp)
                            .clip(CircleShape)
                            .background(dotColor),
                    )
                },
                label = {
                    Text(
                        c.config.label,
                        maxLines = 1,
                        overflow = TextOverflow.Ellipsis,
                    )
                },
            )
        }
    }
}
