package io.github.sidd065.agentcompanion.ui.files

import android.content.ContentValues
import android.content.Context
import android.content.Intent
import android.net.Uri
import android.os.Build
import android.os.Environment
import android.provider.MediaStore
import android.provider.OpenableColumns
import android.webkit.MimeTypeMap
import androidx.core.content.FileProvider
import io.github.sidd065.agentcompanion.graph
import io.github.sidd065.agentcompanion.net.HostConnection
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import java.io.File
import java.io.IOException
import java.io.OutputStream
import java.security.MessageDigest
import java.util.concurrent.ConcurrentHashMap
import java.util.concurrent.atomic.AtomicLong

enum class DownloadStatus { Running, Done, Failed, Cancelled }

data class DownloadResult(
    val uri: Uri?,
    val displayPath: String,
    val fileName: String,
    val bytes: Long,
    val mime: String,
)

data class DownloadTask(
    val id: Long,
    val hostId: String,
    val remotePath: String,
    val fileName: String,
    val doneBytes: Long = 0L,
    val totalBytes: Long = 0L,
    val status: DownloadStatus = DownloadStatus.Running,
    val displayPath: String? = null,
    val uri: Uri? = null,
    val mime: String = "application/octet-stream",
    val error: String? = null,
) {
    val fraction: Float?
        get() = if (totalBytes > 0L) (doneBytes.toFloat() / totalBytes.toFloat()).coerceIn(0f, 1f) else null
}

object FileTransfer {
    private val nextId = AtomicLong(1)
    private val jobs = ConcurrentHashMap<Long, Job>()
    private val _downloads = MutableStateFlow<List<DownloadTask>>(emptyList())
    val downloads: StateFlow<List<DownloadTask>> = _downloads

    private val extraMimeByExt = mapOf(
        "apk" to "application/vnd.android.package-archive",
        "deb" to "application/vnd.debian.binary-package",
        "md" to "text/markdown",
        "markdown" to "text/markdown",
        "json" to "application/json",
        "yaml" to "text/yaml",
        "yml" to "text/yaml",
        "toml" to "text/plain",
        "log" to "text/plain",
        "txt" to "text/plain",
        "csv" to "text/csv",
        "pdf" to "application/pdf",
        "png" to "image/png",
        "jpg" to "image/jpeg",
        "jpeg" to "image/jpeg",
        "webp" to "image/webp",
        "gif" to "image/gif",
        "svg" to "image/svg+xml",
        "mp4" to "video/mp4",
        "mp3" to "audio/mpeg",
        "zip" to "application/zip",
        "gz" to "application/gzip",
        "tar" to "application/x-tar",
    )

    internal fun sanitizeFileName(rawPath: String): String {
        val cleaned = rawPath.trimEnd('/', '\\').substringAfterLast('/').substringAfterLast('\\')
            .replace(Regex("[\\u0000-\\u001F]"), "_")
            .trim()
        return cleaned.ifBlank { "download" }
    }

    fun mimeOf(name: String): String {
        val ext = name.substringAfterLast('.', "").lowercase()
        if (ext.isBlank() || ext == name.lowercase()) return "application/octet-stream"
        extraMimeByExt[ext]?.let { return it }
        return MimeTypeMap.getSingleton().getMimeTypeFromExtension(ext) ?: "application/octet-stream"
    }

    /** Uploads a phone document to a directory on the host. Returns the remote path. */
    suspend fun upload(ctx: Context, conn: HostConnection, uri: Uri, dir: String, overwrite: Boolean = false): String = withContext(Dispatchers.IO) {
        val cr = ctx.contentResolver
        var name = "upload"
        var size = -1L
        cr.query(uri, arrayOf(OpenableColumns.DISPLAY_NAME, OpenableColumns.SIZE), null, null, null)?.use { c ->
            if (c.moveToFirst()) {
                name = c.getString(0) ?: name
                size = if (c.isNull(1)) -1 else c.getLong(1)
            }
        }
        val target = dir.trimEnd('/') + "/" + name
        val input = cr.openInputStream(uri) ?: throw IOException("can't read $name")
        try {
            input.use { conn.upload(target, it, overwrite) }
        } catch (e: Exception) {
            if (e.message?.contains("file exists") == true) throw IOException("$name already exists")
            throw e
        }
        @Suppress("UNUSED_VARIABLE") val ignored = size
        target
    }

    internal fun cachePathHash(path: String): String =
        MessageDigest.getInstance("SHA-256")
            .digest(path.toByteArray(Charsets.UTF_8))
            .take(6)
            .joinToString("") { "%02x".format(it) }

    /**
     * Downloads a host file into the app cache (for sharing / opening), namespaced by host ID
     * and a 12-hex-char SHA-256 hash of [path] so same-named files in different folders never collide.
     */
    suspend fun toCache(ctx: Context, conn: HostConnection, path: String, onProgress: (Long, Long) -> Unit = { _, _ -> }): File = withContext(Dispatchers.IO) {
        val safeHost = sanitizeFileName(conn.config.id)
        val pathHash12 = cachePathHash(path)
        val dir = File(File(File(ctx.cacheDir, "shared"), safeHost), pathHash12).apply { mkdirs() }
        val out = File(dir, sanitizeFileName(path))
        out.outputStream().use { o -> conn.download(path, o, onProgress) }
        out
    }

    /**
     * Saves a host file into the phone's public Downloads folder (API 29+: MediaStore.Downloads with
     * OEM fallback; API 28: legacy public Downloads with app-external fallback).
     */
    suspend fun toDownloads(
        ctx: Context,
        conn: HostConnection,
        path: String,
        onProgress: (done: Long, total: Long) -> Unit = { _, _ -> },
    ): String = saveToDownloads(ctx, path, { out, progress -> conn.download(path, out, progress) }, onProgress).displayPath

    /**
     * Testable core download-to-device implementation.
     * [streamWriter] writes the remote bytes into the given [OutputStream], reporting progress and returning total bytes.
     */
    suspend fun saveToDownloads(
        ctx: Context,
        remotePath: String,
        streamWriter: suspend (OutputStream, (Long, Long) -> Unit) -> Long,
        onProgress: (done: Long, total: Long) -> Unit = { _, _ -> },
    ): DownloadResult = withContext(Dispatchers.IO) {
        val name = sanitizeFileName(remotePath)
        val mime = mimeOf(name)

        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
            saveViaMediaStore(ctx, name, mime, streamWriter, onProgress)
        } else {
            saveLegacyApi28(ctx, name, mime, streamWriter, onProgress)
        }
    }

    @androidx.annotation.RequiresApi(Build.VERSION_CODES.Q)
    private suspend fun saveViaMediaStore(
        ctx: Context,
        name: String,
        mime: String,
        streamWriter: suspend (OutputStream, (Long, Long) -> Unit) -> Long,
        onProgress: (done: Long, total: Long) -> Unit,
    ): DownloadResult {
        val cr = ctx.contentResolver
        val subRel = Environment.DIRECTORY_DOWNLOADS + "/AgentCompanion"
        val rootRel = Environment.DIRECTORY_DOWNLOADS

        // Some OEM MediaProviders (e.g. ColorOS/OneUI) reject insert() when MIME_TYPE is
        // "application/octet-stream" for unrecognized extensions (.AppImage, .deb, extensionless)
        // or when a custom subfolder fails validation. Try preferred subfolder first, then graceful fallbacks.
        val attempts = buildList {
            add(Triple(subRel, mime.takeIf { it != "application/octet-stream" }, "Downloads/AgentCompanion/$name"))
            if (mime != "application/octet-stream") {
                add(Triple(subRel, null, "Downloads/AgentCompanion/$name"))
            }
            add(Triple(rootRel, null, "Downloads/$name"))
        }

        var inserted: Uri? = null
        var displayPath = "Downloads/AgentCompanion/$name"
        var lastInsertError: Throwable? = null
        for ((relPath, tryMime, label) in attempts) {
            val values = ContentValues().apply {
                put(MediaStore.Downloads.DISPLAY_NAME, name)
                if (tryMime != null) put(MediaStore.Downloads.MIME_TYPE, tryMime)
                put(MediaStore.Downloads.RELATIVE_PATH, relPath)
                put(MediaStore.Downloads.IS_PENDING, 1)
            }
            val res = runCatching { cr.insert(MediaStore.Downloads.EXTERNAL_CONTENT_URI, values) }
            val uri = res.getOrNull()
            if (uri != null) {
                inserted = uri
                displayPath = label
                break
            }
            lastInsertError = res.exceptionOrNull()
        }

        val item = inserted
            ?: throw IOException("Can't create download entry for $name" + (lastInsertError?.message?.let { ": $it" } ?: ""))

        try {
            val stream = cr.openOutputStream(item, "w") ?: cr.openOutputStream(item)
                ?: throw IOException("Can't open download stream for $name")
            val total = stream.use { o ->
                val written = streamWriter(o, onProgress)
                o.flush()
                written
            }
            val doneValues = ContentValues().apply {
                put(MediaStore.Downloads.IS_PENDING, 0)
            }
            cr.update(item, doneValues, null, null)
            return DownloadResult(uri = item, displayPath = displayPath, fileName = name, bytes = total, mime = mime)
        } catch (t: Throwable) {
            runCatching { cr.delete(item, null, null) }
            throw t
        }
    }

    @Suppress("DEPRECATION")
    private suspend fun saveLegacyApi28(
        ctx: Context,
        name: String,
        mime: String,
        streamWriter: suspend (OutputStream, (Long, Long) -> Unit) -> Long,
        onProgress: (done: Long, total: Long) -> Unit,
    ): DownloadResult {
        val publicDir = File(Environment.getExternalStoragePublicDirectory(Environment.DIRECTORY_DOWNLOADS), "AgentCompanion")
        val targetDir = if ((publicDir.exists() || publicDir.mkdirs()) && publicDir.canWrite()) {
            publicDir
        } else {
            File(ctx.getExternalFilesDir(Environment.DIRECTORY_DOWNLOADS) ?: ctx.filesDir, "AgentCompanion").apply { mkdirs() }
        }
        val dest = uniqueFile(targetDir, name)
        val tmp = File(targetDir, ".${dest.name}.part")
        try {
            val total = tmp.outputStream().use { o ->
                val written = streamWriter(o, onProgress)
                o.flush()
                written
            }
            if (!tmp.renameTo(dest)) {
                tmp.copyTo(dest, overwrite = true)
                tmp.delete()
            }
            val uri = runCatching { FileProvider.getUriForFile(ctx, ctx.packageName + ".files", dest) }.getOrNull()
            return DownloadResult(
                uri = uri,
                displayPath = "Downloads/AgentCompanion/${dest.name}",
                fileName = dest.name,
                bytes = total,
                mime = mime,
            )
        } catch (t: Throwable) {
            runCatching { tmp.delete() }
            throw t
        }
    }

    private fun uniqueFile(dir: File, name: String): File {
        var candidate = File(dir, name)
        if (!candidate.exists()) return candidate
        val dot = name.lastIndexOf('.')
        val stem = if (dot > 0) name.substring(0, dot) else name
        val ext = if (dot > 0) name.substring(dot) else ""
        var i = 1
        while (candidate.exists()) {
            candidate = File(dir, "$stem ($i)$ext")
            i++
        }
        return candidate
    }

    /**
     * Starts an app-scoped download that survives screen navigation, shows live UI progress
     * via [downloads], and posts Android progress + completion notifications.
     */
    fun enqueueDownload(
        ctx: Context,
        conn: HostConnection,
        path: String,
        onComplete: ((Result<DownloadResult>) -> Unit)? = null,
    ): DownloadTask {
        val appCtx = ctx.applicationContext
        val existing = _downloads.value.firstOrNull {
            it.hostId == conn.config.id && it.remotePath == path && it.status == DownloadStatus.Running
        }
        if (existing != null) return existing

        val notifier = runCatching { appCtx.graph.notifier }.getOrNull()
        val scope: CoroutineScope = runCatching { appCtx.graph.scope }.getOrElse { conn.scope }
        return enqueueWithWriter(
            ctx = appCtx,
            scope = scope,
            hostId = conn.config.id,
            remotePath = path,
            streamWriter = { out, progress -> conn.download(path, out, progress) },
            onProgressNotify = { id, fileName, done, total -> notifier?.showDownloadProgress(id, fileName, done, total) },
            onDoneNotify = { id, res -> notifier?.showDownloadComplete(id, res.fileName, res.displayPath, res.uri, res.mime) },
            onErrorNotify = { id, fileName, err -> notifier?.showDownloadError(id, fileName, err) },
            onCancelNotify = { id -> notifier?.cancelDownloadNotification(id) },
            onComplete = onComplete,
        )
    }

    /** Testable app-scoped download queue entry point. */
    internal fun enqueueWithWriter(
        ctx: Context,
        scope: CoroutineScope,
        hostId: String,
        remotePath: String,
        streamWriter: suspend (OutputStream, (Long, Long) -> Unit) -> Long,
        onProgressNotify: (Long, String, Long, Long) -> Unit = { _, _, _, _ -> },
        onDoneNotify: (Long, DownloadResult) -> Unit = { _, _ -> },
        onErrorNotify: (Long, String, String) -> Unit = { _, _, _ -> },
        onCancelNotify: (Long) -> Unit = {},
        onComplete: ((Result<DownloadResult>) -> Unit)? = null,
    ): DownloadTask {
        val existing = _downloads.value.firstOrNull {
            it.hostId == hostId && it.remotePath == remotePath && it.status == DownloadStatus.Running
        }
        if (existing != null) return existing

        val id = nextId.getAndIncrement()
        val name = sanitizeFileName(remotePath)
        val initial = DownloadTask(id = id, hostId = hostId, remotePath = remotePath, fileName = name, mime = mimeOf(name))
        _downloads.update { (listOf(initial) + it).take(20) }

        val job = scope.launch {
            var lastNotifMs = 0L
            onProgressNotify(id, name, 0L, 0L)
            try {
                val res = saveToDownloads(ctx, remotePath, streamWriter) { done, total ->
                    updateTask(id) { it.copy(doneBytes = done, totalBytes = total) }
                    val now = System.currentTimeMillis()
                    if (now - lastNotifMs >= 400L || (total > 0L && done >= total)) {
                        lastNotifMs = now
                        onProgressNotify(id, name, done, total)
                    }
                }
                updateTask(id) {
                    it.copy(
                        doneBytes = res.bytes,
                        totalBytes = res.bytes,
                        status = DownloadStatus.Done,
                        displayPath = res.displayPath,
                        uri = res.uri,
                        mime = res.mime,
                        error = null,
                    )
                }
                onDoneNotify(id, res)
                onComplete?.invoke(Result.success(res))
            } catch (ce: CancellationException) {
                updateTask(id) { it.copy(status = DownloadStatus.Cancelled, error = null) }
                onCancelNotify(id)
                onComplete?.invoke(Result.failure(ce))
                throw ce
            } catch (t: Throwable) {
                val msg = t.message?.takeIf { it.isNotBlank() } ?: t.javaClass.simpleName
                updateTask(id) { it.copy(status = DownloadStatus.Failed, error = msg) }
                onErrorNotify(id, name, msg)
                onComplete?.invoke(Result.failure(t))
            } finally {
                jobs.remove(id)
            }
        }
        jobs[id] = job
        return initial
    }

    fun cancelDownload(id: Long) {
        jobs.remove(id)?.cancel()
        updateTask(id) {
            if (it.status == DownloadStatus.Running) it.copy(status = DownloadStatus.Cancelled, error = null) else it
        }
    }

    fun dismissDownload(id: Long) {
        _downloads.update { list -> list.filterNot { it.id == id && it.status != DownloadStatus.Running } }
    }

    internal fun resetForTests() {
        jobs.values.forEach { it.cancel() }
        jobs.clear()
        _downloads.value = emptyList()
    }

    private fun updateTask(id: Long, fn: (DownloadTask) -> DownloadTask) {
        _downloads.update { list -> list.map { if (it.id == id) fn(it) else it } }
    }

    fun openDownloaded(ctx: Context, task: DownloadTask): Boolean {
        val uri = task.uri ?: return false
        val i = Intent(Intent.ACTION_VIEW)
            .setDataAndType(uri, task.mime)
            .addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION or Intent.FLAG_ACTIVITY_NEW_TASK)
        return runCatching {
            ctx.startActivity(Intent.createChooser(i, "Open ${task.fileName}").addFlags(Intent.FLAG_ACTIVITY_NEW_TASK or Intent.FLAG_GRANT_READ_URI_PERMISSION))
            true
        }.getOrDefault(false)
    }

    suspend fun share(ctx: Context, conn: HostConnection, path: String) {
        val f = toCache(ctx, conn, path)
        val uri = FileProvider.getUriForFile(ctx, ctx.packageName + ".files", f)
        val i = Intent(Intent.ACTION_SEND).setType(mimeOf(f.name)).putExtra(Intent.EXTRA_STREAM, uri).addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION)
        withContext(Dispatchers.Main) { ctx.startActivity(Intent.createChooser(i, "Share ${f.name}").addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)) }
    }

    suspend fun openWith(ctx: Context, conn: HostConnection, path: String) {
        val f = toCache(ctx, conn, path)
        val uri = FileProvider.getUriForFile(ctx, ctx.packageName + ".files", f)
        val i = Intent(Intent.ACTION_VIEW).setDataAndType(uri, mimeOf(f.name)).addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION)
        withContext(Dispatchers.Main) { ctx.startActivity(Intent.createChooser(i, "Open ${f.name}").addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)) }
    }
}
