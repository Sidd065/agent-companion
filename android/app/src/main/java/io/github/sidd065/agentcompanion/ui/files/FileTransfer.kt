package io.github.sidd065.agentcompanion.ui.files

import android.content.ContentValues
import android.content.Context
import android.content.Intent
import android.net.Uri
import android.os.Environment
import android.provider.MediaStore
import android.provider.OpenableColumns
import android.webkit.MimeTypeMap
import androidx.core.content.FileProvider
import io.github.sidd065.agentcompanion.net.HostConnection
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import java.io.File
import java.io.IOException

object FileTransfer {
    private fun mimeOf(name: String): String =
        MimeTypeMap.getSingleton().getMimeTypeFromExtension(name.substringAfterLast('.', "").lowercase()) ?: "application/octet-stream"

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

    /** Downloads a host file into the app cache (for sharing / opening). */
    suspend fun toCache(ctx: Context, conn: HostConnection, path: String): File = withContext(Dispatchers.IO) {
        val dir = File(ctx.cacheDir, "shared").apply { mkdirs() }
        val out = File(dir, path.substringAfterLast('/'))
        out.outputStream().use { o -> conn.download(path, o) }
        out
    }

    /** Saves a host file into the phone's public Downloads folder. Returns its display name. */
    suspend fun toDownloads(ctx: Context, conn: HostConnection, path: String): String = withContext(Dispatchers.IO) {
        val name = path.substringAfterLast('/')
        val values = ContentValues().apply {
            put(MediaStore.Downloads.DISPLAY_NAME, name)
            put(MediaStore.Downloads.MIME_TYPE, mimeOf(name))
            put(MediaStore.Downloads.RELATIVE_PATH, Environment.DIRECTORY_DOWNLOADS + "/AgentCompanion")
            put(MediaStore.Downloads.IS_PENDING, 1)
        }
        val cr = ctx.contentResolver
        val item = cr.insert(MediaStore.Downloads.EXTERNAL_CONTENT_URI, values) ?: throw IOException("can't create download")
        try {
            cr.openOutputStream(item)?.use { o -> conn.download(path, o) } ?: throw IOException("can't write")
            values.clear()
            values.put(MediaStore.Downloads.IS_PENDING, 0)
            cr.update(item, values, null, null)
        } catch (e: Exception) {
            cr.delete(item, null, null)
            throw e
        }
        "Downloads/AgentCompanion/$name"
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
