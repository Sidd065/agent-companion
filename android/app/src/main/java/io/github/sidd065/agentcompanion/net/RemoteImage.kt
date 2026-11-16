package io.github.sidd065.agentcompanion.net

import coil3.ImageLoader
import coil3.decode.DataSource
import coil3.decode.ImageSource
import coil3.fetch.FetchResult
import coil3.fetch.Fetcher
import coil3.fetch.SourceFetchResult
import coil3.key.Keyer
import coil3.request.Options
import io.github.sidd065.agentcompanion.AppGraph
import okio.Buffer
import okio.FileSystem
import java.io.IOException

/**
 * An image on a host (a file path or a prompt attachment), loaded through the
 * encrypted RPC channel instead of plain HTTP. Use as Coil's `model`.
 */
data class RemoteImage(val hostId: String, val path: String? = null, val attachment: String? = null)

class RemoteImageFetcher(private val data: RemoteImage, private val graph: AppGraph) : Fetcher {
    override suspend fun fetch(): FetchResult {
        val conn = graph.connection(data.hostId) ?: throw IOException("host is not configured")
        val bytes = when {
            data.attachment != null -> conn.readAttachment(data.attachment)
            data.path != null -> conn.readFileBytes(data.path, 40L shl 20)
            else -> throw IOException("nothing to load")
        }
        return SourceFetchResult(
            source = ImageSource(Buffer().write(bytes), FileSystem.SYSTEM),
            mimeType = null,
            dataSource = DataSource.NETWORK,
        )
    }

    class Factory(private val graph: AppGraph) : Fetcher.Factory<RemoteImage> {
        override fun create(data: RemoteImage, options: Options, imageLoader: ImageLoader): Fetcher = RemoteImageFetcher(data, graph)
    }

    class Key : Keyer<RemoteImage> {
        override fun key(data: RemoteImage, options: Options): String = "${data.hostId}|${data.path}|${data.attachment}"
    }
}
