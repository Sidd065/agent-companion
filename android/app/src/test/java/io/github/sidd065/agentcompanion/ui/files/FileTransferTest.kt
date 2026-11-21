package io.github.sidd065.agentcompanion.ui.files

import android.content.Context
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import org.junit.Assert.assertFalse

import android.content.ContentProvider
import android.content.ContentValues
import android.database.Cursor
import android.net.Uri
import android.os.Environment
import android.os.ParcelFileDescriptor
import android.provider.MediaStore
import androidx.test.core.app.ApplicationProvider
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.delay
import kotlinx.coroutines.runBlocking
import org.junit.After
import org.junit.Assert.assertArrayEquals
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Assert.fail
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.Robolectric
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import java.io.File
import java.io.IOException
import java.util.concurrent.CopyOnWriteArrayList

/**
 * In-memory fake MediaStore ContentProvider that enforces Android 10–16 IS_PENDING semantics
 * and can simulate OEM MediaProvider insert rejections (e.g. ColorOS / OneUI subfolder or MIME rejection).
 */
class FakeMediaStoreProvider : ContentProvider() {
    data class Record(
        val id: Int,
        val uri: Uri,
        val file: File,
        val values: ContentValues,
        var pending: Int,
        var deleted: Boolean = false,
    )

    companion object {
        val records = CopyOnWriteArrayList<Record>()
        var failIfSubfolderOrMime: Boolean = false
        var failAllInserts: Boolean = false
        private var nextId = 1

        fun reset() {
            records.forEach { runCatching { it.file.delete() } }
            records.clear()
            failIfSubfolderOrMime = false
            failAllInserts = false
            nextId = 1
        }
    }

    override fun onCreate(): Boolean = true

    override fun insert(uri: Uri, values: ContentValues?): Uri? {
        val v = ContentValues(values)
        val rel = v.getAsString(MediaStore.Downloads.RELATIVE_PATH)
        val mime = v.getAsString(MediaStore.Downloads.MIME_TYPE)
        if (failAllInserts) return null
        if (failIfSubfolderOrMime && (rel?.contains("/") == true || mime != null)) {
            // Simulates OEM MediaProvider rejecting subfolder or custom MIME type
            return null
        }
        val id = nextId++
        val f = File.createTempFile("fake-ms-$id-", ".bin")
        val itemUri = Uri.withAppendedPath(uri, id.toString())
        val pending = v.getAsInteger(MediaStore.Downloads.IS_PENDING) ?: 0
        records.add(Record(id, itemUri, f, v, pending))
        return itemUri
    }

    override fun openFile(uri: Uri, mode: String): ParcelFileDescriptor {
        val rec = records.firstOrNull { it.uri == uri && !it.deleted }
            ?: throw IOException("No MediaStore record for $uri")
        val pfdMode = if (mode.contains("w")) {
            ParcelFileDescriptor.MODE_WRITE_ONLY or ParcelFileDescriptor.MODE_CREATE or ParcelFileDescriptor.MODE_TRUNCATE
        } else {
            ParcelFileDescriptor.MODE_READ_ONLY
        }
        return ParcelFileDescriptor.open(rec.file, pfdMode)
    }

    override fun update(uri: Uri, values: ContentValues?, selection: String?, selectionArgs: Array<out String>?): Int {
        val rec = records.firstOrNull { it.uri == uri && !it.deleted } ?: return 0
        values?.getAsInteger(MediaStore.Downloads.IS_PENDING)?.let { rec.pending = it }
        return 1
    }

    override fun delete(uri: Uri, selection: String?, selectionArgs: Array<out String>?): Int {
        val rec = records.firstOrNull { it.uri == uri } ?: return 0
        rec.deleted = true
        rec.file.delete()
        return 1
    }

    override fun query(uri: Uri, projection: Array<out String>?, selection: String?, selectionArgs: Array<out String>?, sortOrder: String?): Cursor? = null
    override fun getType(uri: Uri): String? = null
}

@RunWith(RobolectricTestRunner::class)
class FileTransferTest {

    @Before
    fun setUp() {
        FakeMediaStoreProvider.reset()
        FileTransfer.resetForTests()
        Robolectric.buildContentProvider(FakeMediaStoreProvider::class.java)
            .create(MediaStore.AUTHORITY)
    }

    @After
    fun tearDown() {
        FileTransfer.resetForTests()
        FakeMediaStoreProvider.reset()
    }

    @Test
    @Config(sdk = [34])
    fun api34_mediaStoreDownloadsWritesBytesAndClearsPending() = runBlocking {
        val ctx = ApplicationProvider.getApplicationContext<android.content.Context>()
        val payload = ByteArray(1_200_000) { (it * 31 % 256).toByte() }
        val progressSteps = mutableListOf<Pair<Long, Long>>()

        val res = FileTransfer.saveToDownloads(
            ctx = ctx,
            remotePath = "/home/user/releases/agent-companion_0.4.0_amd64.deb",
            streamWriter = { out, onProgress ->
                val half = payload.size / 2
                out.write(payload, 0, half)
                onProgress(half.toLong(), payload.size.toLong())
                out.write(payload, half, payload.size - half)
                onProgress(payload.size.toLong(), payload.size.toLong())
                payload.size.toLong()
            },
            onProgress = { d, t -> progressSteps.add(d to t) },
        )

        assertEquals("agent-companion_0.4.0_amd64.deb", res.fileName)
        assertEquals("Downloads/AgentCompanion/agent-companion_0.4.0_amd64.deb", res.displayPath)
        assertEquals(payload.size.toLong(), res.bytes)
        assertNotNull(res.uri)
        assertEquals(2, progressSteps.size)

        val rec = FakeMediaStoreProvider.records.single()
        assertEquals(0, rec.pending) // IS_PENDING cleared!
        assertEquals(false, rec.deleted)
        assertArrayEquals(payload, rec.file.readBytes())
    }

    @Test
    @Config(sdk = [34])
    fun api34_oemMediaStoreRejectionFallsBackToRootDownloads() = runBlocking {
        FakeMediaStoreProvider.failIfSubfolderOrMime = true
        val ctx = ApplicationProvider.getApplicationContext<android.content.Context>()
        val payload = "appimage-binary-content".toByteArray()

        val res = FileTransfer.saveToDownloads(
            ctx = ctx,
            remotePath = "/opt/Agent-Companion-0.4.0-x86_64.AppImage",
            streamWriter = { out, onProgress ->
                out.write(payload)
                onProgress(payload.size.toLong(), payload.size.toLong())
                payload.size.toLong()
            },
        )

        assertEquals("Downloads/Agent-Companion-0.4.0-x86_64.AppImage", res.displayPath)
        val rec = FakeMediaStoreProvider.records.single()
        assertEquals(Environment.DIRECTORY_DOWNLOADS, rec.values.getAsString(MediaStore.Downloads.RELATIVE_PATH))
        assertNull(rec.values.getAsString(MediaStore.Downloads.MIME_TYPE))
        assertEquals(0, rec.pending)
        assertArrayEquals(payload, rec.file.readBytes())
    }

    @Test
    @Config(sdk = [34])
    fun api34_failedStreamDeletesPendingMediaStoreEntry() = runBlocking {
        val ctx = ApplicationProvider.getApplicationContext<android.content.Context>()
        try {
            FileTransfer.saveToDownloads(
                ctx = ctx,
                remotePath = "/home/user/broken.bin",
                streamWriter = { out, _ ->
                    out.write(byteArrayOf(1, 2, 3))
                    throw IOException("network dropped mid-transfer")
                },
            )
            fail("Expected IOException")
        } catch (e: IOException) {
            assertTrue(e.message!!.contains("network dropped"))
        }
        val rec = FakeMediaStoreProvider.records.single()
        assertTrue("Pending MediaStore entry must be deleted on error", rec.deleted)
    }

    @Test
    @Config(sdk = [28])
    fun api28_legacyFallbackWritesWithoutMediaStoreDownloadsCrash() = runBlocking {
        val ctx = ApplicationProvider.getApplicationContext<android.content.Context>()
        val payload = "legacy-android-9-download".toByteArray()

        val res = FileTransfer.saveToDownloads(
            ctx = ctx,
            remotePath = "/home/user/notes.md",
            streamWriter = { out, onProgress ->
                out.write(payload)
                onProgress(payload.size.toLong(), payload.size.toLong())
                payload.size.toLong()
            },
        )

        assertEquals("notes.md", res.fileName)
        assertEquals(payload.size.toLong(), res.bytes)
        assertTrue(res.displayPath.startsWith("Downloads/AgentCompanion/"))
    }

    @Test
    @Config(sdk = [34])
    fun enqueueDownload_deduplicatesTracksProgressAndCancelsCleanly() = runBlocking {
        val ctx = ApplicationProvider.getApplicationContext<android.content.Context>()
        val gate = CompletableDeferred<Unit>()
        var doneNotified = false
        val completed = CompletableDeferred<Result<DownloadResult>>()

        val t1 = FileTransfer.enqueueWithWriter(
            ctx = ctx,
            scope = this,
            hostId = "host-1",
            remotePath = "/home/user/report.pdf",
            streamWriter = { out, onProgress ->
                val data = "PDF-DATA".toByteArray()
                onProgress(4L, 8L)
                gate.await()
                out.write(data)
                onProgress(8L, 8L)
                8L
            },
            onDoneNotify = { _, _ -> doneNotified = true },
            onComplete = { completed.complete(it) },
        )

        // Duplicate tap while running returns the same task without spawning a second download
        val t2 = FileTransfer.enqueueWithWriter(
            ctx = ctx,
            scope = this,
            hostId = "host-1",
            remotePath = "/home/user/report.pdf",
            streamWriter = { _, _ -> fail("Should not run duplicate download"); 0L },
        )
        assertEquals(t1.id, t2.id)

        gate.complete(Unit)
        val res = completed.await()
        assertTrue(res.isSuccess)
        assertTrue(doneNotified)
        val finalTask = FileTransfer.downloads.value.first { it.id == t1.id }
        assertEquals(DownloadStatus.Done, finalTask.status)
        assertEquals(8L, finalTask.doneBytes)

        // Now test cancellation mid-download
        var cancelNotified = false
        val cancelDone = CompletableDeferred<Result<DownloadResult>>()
        val cTask = FileTransfer.enqueueWithWriter(
            ctx = ctx,
            scope = this,
            hostId = "host-1",
            remotePath = "/home/user/huge.iso",
            streamWriter = { _, _ ->
                delay(10_000)
                0L
            },
            onCancelNotify = { cancelNotified = true },
            onComplete = { cancelDone.complete(it) },
        )
        delay(50)
        FileTransfer.cancelDownload(cTask.id)
        cancelDone.await()
        assertTrue(cancelNotified)
        assertEquals(DownloadStatus.Cancelled, FileTransfer.downloads.value.first { it.id == cTask.id }.status)
    }

    @Test
    @Config(sdk = [34])
    fun sanitizeFileNameAndMimeInference() {
        assertEquals("file.deb", FileTransfer.sanitizeFileName("/a/b/file.deb"))
        assertEquals("folder", FileTransfer.sanitizeFileName("/a/b/folder/"))
        assertEquals("download", FileTransfer.sanitizeFileName("/"))
        assertEquals("application/vnd.android.package-archive", FileTransfer.mimeOf("app.apk"))
        assertEquals("application/vnd.debian.binary-package", FileTransfer.mimeOf("app.deb"))
        assertEquals("text/markdown", FileTransfer.mimeOf("README.md"))
        assertEquals("application/octet-stream", FileTransfer.mimeOf("noext"))
    }

    @Test
    @Config(sdk = [34])
    fun api34_allMediaStoreInsertAttemptsFailThrowsClearIOException() = runBlocking {
        FakeMediaStoreProvider.failAllInserts = true
        val ctx = ApplicationProvider.getApplicationContext<Context>()
        val err = runCatching {
            FileTransfer.saveToDownloads(ctx, "/tmp/broken.bin", { _, _ -> 0L })
        }.exceptionOrNull()
        assertTrue("expected IOException, got $err", err is java.io.IOException)
        assertTrue("expected filename in error message, got ${err?.message}", err?.message?.contains("broken.bin") == true)
    }

    @Test
    @Config(sdk = [34])
    fun openDownloaded_launchesChooserWithReadGrantAndHandlesNullUri() {
        val app = ApplicationProvider.getApplicationContext<android.app.Application>()
        val noUriTask = DownloadTask(id = 99, hostId = "h", remotePath = "/a.pdf", fileName = "a.pdf", status = DownloadStatus.Done, uri = null)
        assertFalse(FileTransfer.openDownloaded(app, noUriTask))

        val uri = Uri.parse("content://media/external/downloads/42")
        val task = DownloadTask(
            id = 100,
            hostId = "h",
            remotePath = "/report.pdf",
            fileName = "report.pdf",
            status = DownloadStatus.Done,
            uri = uri,
            mime = "application/pdf",
        )
        assertTrue(FileTransfer.openDownloaded(app, task))
        val started = org.robolectric.Shadows.shadowOf(app).nextStartedActivity
        assertNotNull("chooser intent should be started", started)
        assertEquals(android.content.Intent.ACTION_CHOOSER, started.action)
        @Suppress("DEPRECATION")
        val target = started.getParcelableExtra<android.content.Intent>(android.content.Intent.EXTRA_INTENT)
        assertNotNull("wrapped VIEW intent should be present", target)
        assertEquals(android.content.Intent.ACTION_VIEW, target!!.action)
        assertEquals(uri, target.data)
        assertEquals("application/pdf", target.type)
        assertTrue((target.flags and android.content.Intent.FLAG_GRANT_READ_URI_PERMISSION) != 0)
    }

    @Test
    @Config(sdk = [34])
    fun dismissDownload_removesFinishedTasksOnly() = runBlocking {
        val ctx = ApplicationProvider.getApplicationContext<Context>()
        val blocker = CompletableDeferred<Unit>()
        val scope = CoroutineScope(SupervisorJob() + Dispatchers.IO)
        try {
            val running = FileTransfer.enqueueWithWriter(
                ctx = ctx,
                scope = scope,
                hostId = "h1",
                remotePath = "/slow.bin",
                streamWriter = { out, _ ->
                    blocker.await()
                    out.write(byteArrayOf(1))
                    1L
                },
            )
            // Dismissing a running task must be a no-op
            FileTransfer.dismissDownload(running.id)
            assertTrue(FileTransfer.downloads.value.any { it.id == running.id })

            // Cancel and then dismiss -> removed
            FileTransfer.cancelDownload(running.id)
            val deadline = System.currentTimeMillis() + 3000
            while (System.currentTimeMillis() < deadline &&
                FileTransfer.downloads.value.firstOrNull { it.id == running.id }?.status == DownloadStatus.Running
            ) {
                kotlinx.coroutines.delay(15)
            }
            FileTransfer.dismissDownload(running.id)
            assertFalse(FileTransfer.downloads.value.any { it.id == running.id })
        } finally {
            blocker.complete(Unit)
            scope.cancel()
        }
    }
}
