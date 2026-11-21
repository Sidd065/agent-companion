package io.github.sidd065.agentcompanion.service

import android.app.Application
import android.app.Notification
import android.app.NotificationManager
import android.content.Context
import android.content.Intent
import android.net.Uri
import androidx.test.core.app.ApplicationProvider
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.Shadows
import org.robolectric.annotation.Config

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34])
class NotifierDownloadTest {

    @Test
    fun downloadProgressCompleteErrorAndCancelLifecycle() {
        val app = ApplicationProvider.getApplicationContext<Application>()
        Shadows.shadowOf(app).grantPermissions(android.Manifest.permission.POST_NOTIFICATIONS)
        val nm = app.getSystemService(Context.NOTIFICATION_SERVICE) as NotificationManager
        val shadowNm = Shadows.shadowOf(nm)

        val notifier = Notifier(app)
        assertNotNull("downloads channel must exist", nm.getNotificationChannel(Notifier.CH_DOWNLOADS))

        // 1. Progress notification (ongoing, 50%)
        notifier.showDownloadProgress(7L, "release.apk", 500L, 1000L)
        val prog = shadowNm.allNotifications.firstOrNull { it.channelId == Notifier.CH_DOWNLOADS }
        assertNotNull("progress notification should be posted", prog)
        assertTrue((prog!!.flags and Notification.FLAG_ONGOING_EVENT) != 0)
        assertEquals("Downloading release.apk", prog.extras.getString(Notification.EXTRA_TITLE))
        assertEquals("50%", prog.extras.getString(Notification.EXTRA_TEXT))
        assertEquals(50, prog.extras.getInt(Notification.EXTRA_PROGRESS))

        // 2. Completion notification replaces progress with tap-to-open PendingIntent
        val uri = Uri.parse("content://media/external/downloads/77")
        notifier.showDownloadComplete(7L, "release.apk", "Downloads/AgentCompanion/release.apk", uri, "application/vnd.android.package-archive")
        val done = shadowNm.allNotifications.firstOrNull { it.channelId == Notifier.CH_DOWNLOADS }
        assertNotNull("complete notification should be posted", done)
        assertFalse((done!!.flags and Notification.FLAG_ONGOING_EVENT) != 0)
        assertEquals("Downloaded release.apk", done.extras.getString(Notification.EXTRA_TITLE))
        assertEquals("Saved to Downloads/AgentCompanion/release.apk", done.extras.getString(Notification.EXTRA_TEXT))
        assertNotNull("contentIntent should open the downloaded file", done.contentIntent)
        val openIntent = Shadows.shadowOf(done.contentIntent).savedIntent
        assertEquals(Intent.ACTION_VIEW, openIntent.action)
        assertEquals(uri, openIntent.data)
        assertEquals("application/vnd.android.package-archive", openIntent.type)
        assertTrue((openIntent.flags and Intent.FLAG_GRANT_READ_URI_PERMISSION) != 0)

        // 3. Error notification
        notifier.showDownloadError(8L, "bad.bin", "connection reset")
        val err = shadowNm.allNotifications.firstOrNull {
            it.extras.getString(Notification.EXTRA_TITLE) == "Download failed: bad.bin"
        }
        assertNotNull("error notification should be posted", err)
        assertEquals("connection reset", err!!.extras.getString(Notification.EXTRA_TEXT))

        // 4. Cancel removes notification
        notifier.cancelDownloadNotification(7L)
        notifier.cancelDownloadNotification(8L)
        assertNull(shadowNm.allNotifications.firstOrNull { it.channelId == Notifier.CH_DOWNLOADS })
    }
}
