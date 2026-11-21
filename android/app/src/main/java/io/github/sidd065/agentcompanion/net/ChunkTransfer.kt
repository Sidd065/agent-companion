package io.github.sidd065.agentcompanion.net

import kotlinx.coroutines.currentCoroutineContext
import kotlinx.coroutines.ensureActive
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.booleanOrNull
import kotlinx.serialization.json.jsonPrimitive
import kotlinx.serialization.json.longOrNull
import java.io.OutputStream
import java.util.Base64

/**
 * Pure-Kotlin chunked transfer loop used by [HostConnection.download] and
 * [HostConnection.readAttachment]. Uses 512 KiB chunks for smooth progress
 * reporting and reliable delivery over mobile/Cloudflare WebSocket tunnels,
 * and uses [java.util.Base64] so it is 100% testable on the JVM.
 */
object ChunkTransfer {
    const val DEFAULT_CHUNK_BYTES: Long = 512L * 1024L

    suspend fun readChunks(
        out: OutputStream,
        chunkSize: Long = DEFAULT_CHUNK_BYTES,
        onProgress: (done: Long, total: Long) -> Unit = { _, _ -> },
        fetchChunk: suspend (offset: Long, length: Long) -> JsonObject,
    ): Long {
        var off = 0L
        var size = -1L
        val decoder = Base64.getMimeDecoder()
        while (true) {
            currentCoroutineContext().ensureActive()
            val r = fetchChunk(off, chunkSize)
            val b64 = r["data"]?.jsonPrimitive?.content.orEmpty()
            val data = if (b64.isEmpty()) ByteArray(0) else decoder.decode(b64)
            if (data.isNotEmpty()) {
                out.write(data)
                off += data.size
            }
            val reportedSize = r["size"]?.jsonPrimitive?.longOrNull
            if (reportedSize != null && reportedSize >= 0L) {
                size = reportedSize
            }
            val effectiveTotal = if (size >= 0L) size else off
            onProgress(off, effectiveTotal)
            val eofPrim = r["eof"]?.jsonPrimitive
            val isEof = eofPrim?.booleanOrNull == true || eofPrim?.content == "true"
            if (isEof || data.isEmpty() || (size >= 0L && off >= size)) {
                break
            }
        }
        out.flush()
        return if (size >= 0L) size else off
    }
}
