package io.github.sidd065.agentcompanion.ui.chat

import android.content.Context
import android.graphics.Bitmap
import android.graphics.BitmapFactory
import android.graphics.ImageDecoder
import android.net.Uri
import android.provider.OpenableColumns
import android.util.Base64
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.buildJsonArray
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.put
import java.io.ByteArrayOutputStream

/** A pending attachment in the composer. */
data class Pending(
    val type: String, // image|file|path
    val name: String,
    val mime: String? = null,
    val data: String? = null, // base64
    val path: String? = null, // host path for type=path
    val preview: Bitmap? = null,
    val size: Long = 0,
)

object AttachmentUtil {
    private const val MAX_SIDE = 2000
    private const val MAX_FILE = 25L * 1024 * 1024

    suspend fun fromUri(ctx: Context, uri: Uri): Pending = withContext(Dispatchers.IO) {
        val cr = ctx.contentResolver
        val mime = cr.getType(uri) ?: "application/octet-stream"
        var name = "attachment"
        var size = -1L
        cr.query(uri, arrayOf(OpenableColumns.DISPLAY_NAME, OpenableColumns.SIZE), null, null, null)?.use { c ->
            if (c.moveToFirst()) {
                name = c.getString(0) ?: name
                size = if (c.isNull(1)) -1 else c.getLong(1)
            }
        }
        if (mime.startsWith("image/") && mime != "image/gif" && mime != "image/svg+xml") {
            val src = ImageDecoder.createSource(cr, uri)
            val bmp = ImageDecoder.decodeBitmap(src) { dec, info, _ ->
                val w = info.size.width
                val h = info.size.height
                val scale = minOf(1f, MAX_SIDE.toFloat() / maxOf(w, h))
                if (scale < 1f) dec.setTargetSize((w * scale).toInt(), (h * scale).toInt())
                dec.allocator = ImageDecoder.ALLOCATOR_SOFTWARE
            }
            val out = ByteArrayOutputStream()
            val png = mime == "image/png" && bmp.width * bmp.height < 1_500_000
            bmp.compress(if (png) Bitmap.CompressFormat.PNG else Bitmap.CompressFormat.JPEG, 88, out)
            val bytes = out.toByteArray()
            val thumb = Bitmap.createScaledBitmap(bmp, 160, (160f * bmp.height / bmp.width).toInt().coerceAtLeast(1), true)
            Pending("image", name, if (png) "image/png" else "image/jpeg", Base64.encodeToString(bytes, Base64.NO_WRAP), preview = thumb, size = bytes.size.toLong())
        } else {
            if (size > MAX_FILE) error("$name is too large (max 25 MB)")
            val bytes = cr.openInputStream(uri)?.use { it.readBytes() } ?: error("can't read $name")
            if (bytes.size > MAX_FILE) error("$name is too large (max 25 MB)")
            Pending("file", name, mime, Base64.encodeToString(bytes, Base64.NO_WRAP), size = bytes.size.toLong())
        }
    }

    fun toJson(list: List<Pending>): JsonArray = buildJsonArray {
        list.forEach { p ->
            add(buildJsonObject {
                put("type", p.type)
                put("name", p.name)
                p.mime?.let { put("mime", it) }
                p.data?.let { put("data", it) }
                p.path?.let { put("path", it) }
            })
        }
    }

    @Suppress("unused")
    fun decodeThumb(bytes: ByteArray): Bitmap? = BitmapFactory.decodeByteArray(bytes, 0, bytes.size)
}
