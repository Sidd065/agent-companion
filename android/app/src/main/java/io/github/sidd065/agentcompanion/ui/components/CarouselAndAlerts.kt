package io.github.sidd065.agentcompanion.ui.components

import android.widget.MediaController
import android.widget.VideoView
import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.pager.HorizontalPager
import androidx.compose.foundation.pager.rememberPagerState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.outlined.KeyboardArrowLeft
import androidx.compose.material.icons.automirrored.outlined.KeyboardArrowRight
import androidx.compose.material.icons.automirrored.outlined.OpenInNew
import androidx.compose.material.icons.outlined.Download
import androidx.compose.material.icons.outlined.ErrorOutline
import androidx.compose.material.icons.outlined.Info
import androidx.compose.material.icons.outlined.Lightbulb
import androidx.compose.material.icons.outlined.PlayArrow
import androidx.compose.material.icons.outlined.PriorityHigh
import androidx.compose.material.icons.outlined.Slideshow
import androidx.compose.material.icons.outlined.WarningAmber
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.draw.drawBehind
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.viewinterop.AndroidView
import coil3.compose.AsyncImage
import io.github.sidd065.agentcompanion.graph
import io.github.sidd065.agentcompanion.net.RemoteImage
import io.github.sidd065.agentcompanion.ui.LocalHostId
import io.github.sidd065.agentcompanion.ui.activeConnection
import io.github.sidd065.agentcompanion.ui.files.FileTransfer
import io.github.sidd065.agentcompanion.ui.theme.LocalExtra
import kotlinx.coroutines.launch
import java.io.File

data class FenceHeader(val char: Char, val length: Int, val lang: String)

fun parseFenceOpen(trimmedStart: String): FenceHeader? {
    if (trimmedStart.length < 3) return null
    val ch = trimmedStart[0]
    if (ch != '`' && ch != '~') return null
    var n = 0
    while (n < trimmedStart.length && trimmedStart[n] == ch) n++
    if (n < 3) return null
    val rest = trimmedStart.substring(n).trim()
    // CommonMark: backtick info strings cannot contain backticks
    if (ch == '`' && rest.contains('`')) return null
    val lang = rest.split(Regex("\\s+")).firstOrNull().orEmpty().lowercase()
    return FenceHeader(ch, n, lang)
}

fun isFenceClose(trimmedStart: String, open: FenceHeader): Boolean {
    if (trimmedStart.length < open.length) return false
    var n = 0
    while (n < trimmedStart.length && trimmedStart[n] == open.char) n++
    if (n < open.length) return false
    return trimmedStart.substring(n).all { it == ' ' || it == '\t' }
}

fun extractFencedCodeContent(rawFence: String): Pair<String, String> {
    val lines = rawFence.replace("\r\n", "\n").lines()
    if (lines.isEmpty()) return "" to ""
    val open = parseFenceOpen(lines.first().trimStart()) ?: return "" to rawFence
    val endIdx = if (lines.size >= 2 && isFenceClose(lines.last().trimStart(), open)) lines.size - 1 else lines.size
    val inner = if (endIdx > 1) lines.subList(1, endIdx).joinToString("\n") else ""
    return open.lang to inner
}

private val slideSeparatorRegex = Regex("""^\s*<!--\s*slide\s*-->\s*$""", RegexOption.IGNORE_CASE)

/** Splits a ````carousel```` body into slides, ignoring `<!-- slide -->` inside nested code fences. */
fun splitCarouselSlides(body: String): List<String> {
    val lines = body.replace("\r\n", "\n").lines()
    val slides = ArrayList<String>()
    val cur = ArrayList<String>()
    var innerFence: FenceHeader? = null
    for (line in lines) {
        val t = line.trimStart()
        if (innerFence != null) {
            cur += line
            if (isFenceClose(t, innerFence)) innerFence = null
            continue
        }
        val f = parseFenceOpen(t)
        if (f != null) {
            innerFence = f
            cur += line
            continue
        }
        if (slideSeparatorRegex.matches(line)) {
            val slide = cur.joinToString("\n").trim()
            if (slide.isNotEmpty()) slides += slide
            cur.clear()
        } else {
            cur += line
        }
    }
    val tail = cur.joinToString("\n").trim()
    if (tail.isNotEmpty()) slides += tail
    return slides
}

data class GitHubAlertData(val kind: String, val body: String)

private val alertHeaderRegex = Regex("""^\[!(NOTE|TIP|IMPORTANT|WARNING|CAUTION)]\s*(.*)$""", RegexOption.IGNORE_CASE)

fun parseGitHubAlert(rawBlockquote: String): GitHubAlertData? {
    val lines = rawBlockquote.replace("\r\n", "\n").lines()
    if (lines.isEmpty()) return null
    val stripped = lines.map { l ->
        val t = l.trimStart()
        if (!t.startsWith(">")) return null
        t.removePrefix(">").let { if (it.startsWith(" ")) it.substring(1) else it }
    }
    val first = stripped.firstOrNull()?.trim() ?: return null
    val m = alertHeaderRegex.find(first) ?: return null
    val kind = m.groupValues[1].uppercase()
    val inlineRest = m.groupValues[2].trim()
    val restLines = buildList {
        if (inlineRest.isNotEmpty()) add(inlineRest)
        addAll(stripped.drop(1))
    }
    return GitHubAlertData(kind = kind, body = restLines.joinToString("\n").trim())
}

private val standaloneMediaRegex = Regex("""^!\[([^\]]*)]\(([^)\s]+)(?:\s+"[^"]*")?\)\s*$""")
private val videoExts = setOf("mp4", "webm", "mov", "mkv", "m4v")

fun isVideoPath(path: String): Boolean {
    val clean = path.substringBefore('#').substringBefore('?')
    return clean.substringAfterLast('.', "").lowercase() in videoExts
}

fun resolveArtifactMediaPath(src: String, baseDir: String = ""): String {
    val raw = when {
        src.startsWith("file://") -> android.net.Uri.decode(src.removePrefix("file://").substringBefore('#'))
        src.startsWith("http://") || src.startsWith("https://") -> return src
        src.startsWith("/") -> android.net.Uri.decode(src.substringBefore('#'))
        baseDir.isNotBlank() -> baseDir.trimEnd('/') + "/" + android.net.Uri.decode(src.substringBefore('#').removePrefix("./"))
        else -> android.net.Uri.decode(src)
    }
    return raw
}

@Composable
fun GitHubAlertCard(
    alert: GitHubAlertData,
    modifier: Modifier = Modifier,
    selectable: Boolean = true,
) {
    val ex = LocalExtra.current
    val cs = MaterialTheme.colorScheme
    val (accent, icon, label) = when (alert.kind) {
        "TIP" -> Triple(ex.success, Icons.Outlined.Lightbulb, "Tip")
        "IMPORTANT" -> Triple(cs.secondary, Icons.Outlined.PriorityHigh, "Important")
        "WARNING" -> Triple(ex.warning, Icons.Outlined.WarningAmber, "Warning")
        "CAUTION" -> Triple(ex.danger, Icons.Outlined.ErrorOutline, "Caution")
        else -> Triple(cs.primary, Icons.Outlined.Info, "Note")
    }
    Surface(
        modifier = modifier
            .fillMaxWidth()
            .padding(vertical = 4.dp),
        shape = RoundedCornerShape(10.dp),
        color = accent.copy(alpha = 0.09f),
        border = BorderStroke(1.dp, accent.copy(alpha = 0.32f)),
    ) {
        Row(
            Modifier
                .fillMaxWidth()
                .drawBehind {
                    drawRect(color = accent, topLeft = Offset.Zero, size = Size(4.dp.toPx(), size.height))
                }
                .padding(start = 14.dp, end = 12.dp, top = 10.dp, bottom = 10.dp),
        ) {
            Column(Modifier.fillMaxWidth(), verticalArrangement = Arrangement.spacedBy(6.dp)) {
                Row(verticalAlignment = Alignment.CenterVertically) {
                    Icon(icon, contentDescription = label, modifier = Modifier.size(16.dp), tint = accent)
                    Spacer(Modifier.width(6.dp))
                    Text(label, style = MaterialTheme.typography.labelLarge, color = accent, fontWeight = FontWeight.SemiBold)
                }
                if (alert.body.isNotBlank()) {
                    MarkdownText(alert.body, selectable = selectable, compact = true)
                }
            }
        }
    }
}

@Composable
fun RemoteVideoPlayerCard(
    hostId: String?,
    remotePath: String,
    caption: String = "",
    modifier: Modifier = Modifier,
) {
    val ctx = LocalContext.current
    val snack = LocalSnack.current
    val scope = rememberCoroutineScope()
    val ex = LocalExtra.current
    val conn = hostId?.let { ctx.graph.connection(it) } ?: activeConnection()
    var cachedFile by remember(hostId, remotePath) { mutableStateOf<File?>(null) }
    var loading by remember(hostId, remotePath) { mutableStateOf(false) }
    var error by remember(hostId, remotePath) { mutableStateOf<String?>(null) }
    val title = caption.ifBlank { remotePath.substringAfterLast('/') }

    Surface(
        modifier = modifier.fillMaxWidth().padding(vertical = 4.dp),
        shape = RoundedCornerShape(12.dp),
        color = MaterialTheme.colorScheme.surfaceContainer,
        border = BorderStroke(1.dp, MaterialTheme.colorScheme.outlineVariant.copy(alpha = 0.6f)),
    ) {
        Column(Modifier.fillMaxWidth().padding(12.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
            Text(title, style = MaterialTheme.typography.labelLarge, maxLines = 2, overflow = TextOverflow.Ellipsis)
            if (cachedFile != null) {
                val f = cachedFile!!
                AndroidView(
                    factory = { c ->
                        VideoView(c).apply {
                            val mc = MediaController(c)
                            mc.setAnchorView(this)
                            setMediaController(mc)
                            setVideoPath(f.absolutePath)
                            setOnPreparedListener { mp ->
                                mp.isLooping = true
                                start()
                            }
                        }
                    },
                    modifier = Modifier.fillMaxWidth().heightIn(min = 180.dp, max = 360.dp).clip(RoundedCornerShape(8.dp)),
                )
            } else {
                Box(
                    Modifier
                        .fillMaxWidth()
                        .height(160.dp)
                        .clip(RoundedCornerShape(8.dp))
                        .background(ex.codeBg),
                    contentAlignment = Alignment.Center,
                ) {
                    if (loading) {
                        Column(horizontalAlignment = Alignment.CenterHorizontally) {
                            CircularProgressIndicator(Modifier.size(28.dp), strokeWidth = 2.5.dp)
                            Spacer(Modifier.height(8.dp))
                            Text("Loading video…", style = MaterialTheme.typography.bodySmall, color = ex.muted)
                        }
                    } else if (conn != null) {
                        OutlinedButton(onClick = {
                            loading = true
                            error = null
                            scope.launch {
                                runCatching { FileTransfer.toCache(ctx, conn, remotePath) }
                                    .onSuccess { cachedFile = it }
                                    .onFailure { error = it.message ?: "Failed to load video" }
                                loading = false
                            }
                        }) {
                            Icon(Icons.Outlined.PlayArrow, null, Modifier.size(18.dp))
                            Spacer(Modifier.width(6.dp))
                            Text("Play video")
                        }
                    } else {
                        Text("Host disconnected", style = MaterialTheme.typography.bodySmall, color = ex.muted)
                    }
                }
            }
            error?.let { Text(it, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.error) }
            if (conn != null) {
                Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    TextButton(onClick = {
                        FileTransfer.enqueueDownload(ctx, conn, remotePath) { res ->
                            scope.launch {
                                res.onSuccess { snack.showSnackbar("Saved to ${it.displayPath}") }
                                    .onFailure { snack.showSnackbar("Download failed: ${it.message}") }
                            }
                        }
                    }) {
                        Icon(Icons.Outlined.Download, null, Modifier.size(16.dp))
                        Spacer(Modifier.width(4.dp))
                        Text("Save")
                    }
                    TextButton(onClick = {
                        scope.launch {
                            runCatching { FileTransfer.openWith(ctx, conn, remotePath) }
                                .onFailure { snack.showSnackbar(it.message ?: "Failed to open") }
                        }
                    }) {
                        Icon(Icons.AutoMirrored.Outlined.OpenInNew, null, Modifier.size(16.dp))
                        Spacer(Modifier.width(4.dp))
                        Text("Open externally")
                    }
                }
            }
        }
    }
}

@Composable
fun CarouselView(
    rawBody: String,
    baseDir: String = "",
    modifier: Modifier = Modifier,
    selectable: Boolean = true,
) {
    val slides = remember(rawBody) { splitCarouselSlides(rawBody) }
    if (slides.isEmpty()) return
    val pagerState = rememberPagerState(pageCount = { slides.size })
    val scope = rememberCoroutineScope()
    val ex = LocalExtra.current
    val hostId = LocalHostId.current ?: activeConnection()?.config?.id

    Surface(
        modifier = modifier.fillMaxWidth().padding(vertical = 4.dp),
        shape = RoundedCornerShape(12.dp),
        color = MaterialTheme.colorScheme.surfaceContainerLow,
        border = BorderStroke(1.dp, MaterialTheme.colorScheme.outlineVariant.copy(alpha = 0.6f)),
    ) {
        Column(Modifier.fillMaxWidth().padding(10.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
            Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
                Icon(Icons.Outlined.Slideshow, null, Modifier.size(16.dp), tint = MaterialTheme.colorScheme.primary)
                Spacer(Modifier.width(6.dp))
                Text(
                    "CAROUSEL · ${pagerState.currentPage + 1} / ${slides.size}",
                    style = MaterialTheme.typography.labelSmall,
                    color = ex.muted,
                    fontWeight = FontWeight.SemiBold,
                    modifier = Modifier.weight(1f),
                )
                if (slides.size > 1) {
                    IconButton(
                        enabled = pagerState.currentPage > 0,
                        onClick = { scope.launch { pagerState.animateScrollToPage(pagerState.currentPage - 1) } },
                        modifier = Modifier.size(28.dp),
                    ) {
                        Icon(Icons.AutoMirrored.Outlined.KeyboardArrowLeft, "Previous slide", Modifier.size(18.dp))
                    }
                    IconButton(
                        enabled = pagerState.currentPage < slides.size - 1,
                        onClick = { scope.launch { pagerState.animateScrollToPage(pagerState.currentPage + 1) } },
                        modifier = Modifier.size(28.dp),
                    ) {
                        Icon(Icons.AutoMirrored.Outlined.KeyboardArrowRight, "Next slide", Modifier.size(18.dp))
                    }
                }
            }

            HorizontalPager(state = pagerState, modifier = Modifier.fillMaxWidth()) { page ->
                CarouselSlideBody(slide = slides[page], hostId = hostId, baseDir = baseDir, selectable = selectable)
            }

            if (slides.size > 1) {
                Row(
                    Modifier.fillMaxWidth(),
                    horizontalArrangement = Arrangement.Center,
                    verticalAlignment = Alignment.CenterVertically,
                ) {
                    repeat(slides.size) { idx ->
                        val active = idx == pagerState.currentPage
                        Box(
                            Modifier
                                .padding(horizontal = 3.dp)
                                .size(if (active) 7.dp else 5.dp)
                                .clip(CircleShape)
                                .background(if (active) MaterialTheme.colorScheme.primary else ex.muted.copy(alpha = 0.35f)),
                        )
                    }
                }
            }
        }
    }
}

@Composable
private fun CarouselSlideBody(
    slide: String,
    hostId: String?,
    baseDir: String,
    selectable: Boolean,
) {
    val m = standaloneMediaRegex.find(slide.trim())
    if (m != null) {
        val caption = m.groupValues[1].trim()
        val resolved = resolveArtifactMediaPath(m.groupValues[2].trim(), baseDir)
        if (isVideoPath(resolved) && !resolved.startsWith("http")) {
            RemoteVideoPlayerCard(hostId = hostId, remotePath = resolved, caption = caption)
        } else {
            var zoomOpen by remember { mutableStateOf(false) }
            val imgModel: Any = if (resolved.startsWith("http") || hostId == null) resolved else RemoteImage(hostId, path = resolved)
            Column(Modifier.fillMaxWidth(), verticalArrangement = Arrangement.spacedBy(6.dp)) {
                coil3.compose.SubcomposeAsyncImage(
                    model = imgModel,
                    contentDescription = caption,
                    contentScale = ContentScale.FillWidth,
                    loading = {
                        Box(
                            Modifier.fillMaxWidth().height(160.dp).clip(RoundedCornerShape(8.dp)).background(LocalExtra.current.codeBg),
                            contentAlignment = Alignment.Center,
                        ) {
                            CircularProgressIndicator(Modifier.size(24.dp), strokeWidth = 2.dp)
                        }
                    },
                    error = {
                        Box(
                            Modifier.fillMaxWidth().height(96.dp).clip(RoundedCornerShape(8.dp)).background(LocalExtra.current.codeBg).padding(12.dp),
                            contentAlignment = Alignment.Center,
                        ) {
                            Text(
                                "Media unavailable: ${caption.ifBlank { resolved.substringAfterLast('/') }}",
                                style = MaterialTheme.typography.bodySmall,
                                color = LocalExtra.current.muted,
                                maxLines = 2,
                                overflow = TextOverflow.Ellipsis,
                            )
                        }
                    },
                    modifier = Modifier.fillMaxWidth().heightIn(max = 420.dp).clip(RoundedCornerShape(8.dp)).clickable { zoomOpen = true },
                )
                if (caption.isNotBlank()) {
                    Text(caption, style = MaterialTheme.typography.labelMedium, color = LocalExtra.current.muted)
                }
            }
            if (zoomOpen) {
                androidx.compose.ui.window.Dialog(
                    onDismissRequest = { zoomOpen = false },
                    properties = androidx.compose.ui.window.DialogProperties(usePlatformDefaultWidth = false),
                ) {
                    Surface(Modifier.fillMaxSize().clickable { zoomOpen = false }, color = androidx.compose.ui.graphics.Color.Black.copy(alpha = 0.92f)) {
                        Box(Modifier.fillMaxSize().padding(16.dp), contentAlignment = Alignment.Center) {
                            AsyncImage(
                                model = imgModel,
                                contentDescription = caption,
                                contentScale = ContentScale.Fit,
                                modifier = Modifier.fillMaxSize(),
                            )
                        }
                    }
                }
            }
        }
    } else {
        MarkdownText(slide, selectable = selectable)
    }
}

sealed interface RichMarkdownSegment {
    data class Standard(val markdown: String) : RichMarkdownSegment
    data class Mermaid(val source: String) : RichMarkdownSegment
    data class Carousel(val source: String) : RichMarkdownSegment
    data class Alert(val data: GitHubAlertData) : RichMarkdownSegment
    data class Media(val caption: String, val path: String) : RichMarkdownSegment
}

/**
 * Splits markdown into rich segments only when special blocks (`mermaid`, `carousel`,
 * GitHub `[!ALERT]`, or standalone local `/path` / `file://` media) are present.
 */
fun splitRichMarkdownSegments(src: String): List<RichMarkdownSegment> {
    if (
        !src.contains("```") &&
        !src.contains("~~~") &&
        !src.contains("[!") &&
        !src.contains("![")
    ) {
        return listOf(RichMarkdownSegment.Standard(src))
    }

    val lines = src.replace("\r\n", "\n").lines()
    val out = ArrayList<RichMarkdownSegment>()
    val mdBuf = ArrayList<String>()
    fun flushMd() {
        val text = mdBuf.joinToString("\n")
        if (text.isNotBlank()) out += RichMarkdownSegment.Standard(text)
        mdBuf.clear()
    }

    var i = 0
    while (i < lines.size) {
        val l = lines[i]
        val t = l.trimStart()
        val indent = l.length - t.length
        // Only intercept top-level fences/quotes/media (indent <= 3) so list-nested code stays in Standard
        if (indent <= 3) {
            val open = parseFenceOpen(t)
            if (open != null) {
                var j = i + 1
                while (j < lines.size && !isFenceClose(lines[j].trimStart(), open)) j++
                val body = if (j > i + 1) lines.subList(i + 1, minOf(j, lines.size)).joinToString("\n") else ""
                when (open.lang) {
                    "mermaid" -> {
                        flushMd()
                        out += RichMarkdownSegment.Mermaid(body)
                        i = j + 1
                        continue
                    }
                    "carousel" -> {
                        flushMd()
                        out += RichMarkdownSegment.Carousel(body)
                        i = j + 1
                        continue
                    }
                    else -> {
                        for (k in i..minOf(j, lines.size - 1)) mdBuf += lines[k]
                        i = j + 1
                        continue
                    }
                }
            }
            if (t.startsWith(">")) {
                var j = i
                while (j + 1 < lines.size && lines[j + 1].trimStart().startsWith(">")) j++
                val quoteRaw = lines.subList(i, j + 1).joinToString("\n")
                val alert = parseGitHubAlert(quoteRaw)
                if (alert != null) {
                    flushMd()
                    out += RichMarkdownSegment.Alert(alert)
                    i = j + 1
                    continue
                }
            }
            val mediaMatch = standaloneMediaRegex.find(t)
            if (mediaMatch != null) {
                val rawUrl = mediaMatch.groupValues[2]
                if (rawUrl.startsWith("/") || rawUrl.startsWith("file://")) {
                    flushMd()
                    out += RichMarkdownSegment.Media(mediaMatch.groupValues[1], rawUrl.removePrefix("file://"))
                    i++
                    continue
                }
            }
        }
        mdBuf += l
        i++
    }
    flushMd()
    return out.ifEmpty { listOf(RichMarkdownSegment.Standard(src)) }
}
